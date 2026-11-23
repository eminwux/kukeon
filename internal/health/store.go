// Copyright 2025 Emiliano Spinella (eminwux)
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

package health

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/util/fs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"gopkg.in/yaml.v3"
)

// DefaultKeep is how many reports a Store keeps.
const DefaultKeep = 50

const (
	tmpPrefix = ".healthreport-"
	dirMode   = 0o750
	fileMode  = 0o640
)

// validName is the pattern of a report's metadata.name. It admits the names
// ReportID assigns (uppercase letters and a dot), and no name can climb out of
// the reports directory: a name starts with a letter or digit and holds no
// separator.
var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// storeMu serializes submissions within the process. kukeond is the only
// writer of a run path's reports.
var storeMu sync.Mutex //nolint:gochecknoglobals // one writer per process

// Store keeps the newest kind: HealthReport documents, one YAML file each
// named by metadata.name, under the kuke-system realm's metadata tree
// (<RunPath>/data/kuke-system/healthreports, issue #1619). Files survive a
// kukeond restart; they are operational state, not a system of record.
type Store struct {
	Dir string
	// Keep is the number of reports retained; zero means DefaultKeep.
	Keep int
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// NewStore returns the Store of a run path.
func NewStore(runPath string) *Store {
	return &Store{Dir: fs.HealthReportsDir(runPath)}
}

// RemoveLegacyStore removes <RunPath>/health, where reports were kept before
// they became a kind (issue #1619). Reports stored there are not read or
// converted. An absent directory is not an error.
func RemoveLegacyStore(runPath string) error {
	if err := os.RemoveAll(filepath.Join(runPath, consts.KukeonLegacyHealthSubdir)); err != nil {
		return fmt.Errorf("remove legacy health report directory: %w", err)
	}
	return nil
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Store) keep() int {
	if s.Keep > 0 {
		return s.Keep
	}
	return DefaultKeep
}

// load reads every stored report, newest first. An unreadable or malformed
// file is skipped: one damaged report must not hide the others.
func (s *Store) load() ([]kukeonv1.HealthReport, error) {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read health reports: %w", err)
	}
	var reports []kukeonv1.HealthReport
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), tmpPrefix) {
			continue
		}
		raw, readErr := os.ReadFile(filepath.Join(s.Dir, entry.Name()))
		if readErr != nil {
			continue
		}
		var report kukeonv1.HealthReport
		if yaml.Unmarshal(raw, &report) != nil ||
			report.Kind != v1beta1.KindHealthReport || report.Metadata.Name != entry.Name() {
			continue
		}
		reports = append(reports, report)
	}
	sort.SliceStable(reports, func(i, j int) bool {
		if !reports[i].Spec.CreatedAt.Equal(reports[j].Spec.CreatedAt) {
			return reports[i].Spec.CreatedAt.After(reports[j].Spec.CreatedAt)
		}
		return reports[i].Metadata.Name > reports[j].Metadata.Name
	})
	return reports, nil
}

// Summarize returns the list row of a report.
func Summarize(report *kukeonv1.HealthReport) kukeonv1.HealthReportSummary {
	summary := kukeonv1.HealthReportSummary{
		ID: report.Metadata.Name, CreatedAt: report.Spec.CreatedAt,
		Since: report.Spec.Since, Until: report.Spec.Until,
		Summary: report.Spec.Summary, Findings: len(report.Spec.Findings),
	}
	for _, finding := range report.Spec.Findings {
		if finding.Recurring {
			summary.Recurring++
		}
		if finding.IssueURL != "" {
			summary.Filed++
		}
	}
	return summary
}

// List summarizes the stored reports, newest first.
func (s *Store) List() ([]kukeonv1.HealthReportSummary, error) {
	reports, err := s.load()
	if err != nil {
		return nil, err
	}
	out := make([]kukeonv1.HealthReportSummary, 0, len(reports))
	for i := range reports {
		out = append(out, Summarize(&reports[i]))
	}
	return out, nil
}

// Read returns the report named name, or the newest for HealthReportLatest.
func (s *Store) Read(name string) (kukeonv1.HealthReport, error) {
	reports, err := s.load()
	if err != nil {
		return kukeonv1.HealthReport{}, err
	}
	if name == kukeonv1.HealthReportLatest || name == "" {
		if len(reports) == 0 {
			return kukeonv1.HealthReport{}, fmt.Errorf("%w: no report is stored", errdefs.ErrHealthReportNotFound)
		}
		return reports[0], nil
	}
	for i := range reports {
		if reports[i].Metadata.Name == name {
			return reports[i], nil
		}
	}
	return kukeonv1.HealthReport{}, fmt.Errorf("%w: %q", errdefs.ErrHealthReportNotFound, name)
}

// Delete removes the report named name. HealthReportLatest is not resolved:
// a delete names the report it removes.
func (s *Store) Delete(name string) error {
	storeMu.Lock()
	defer storeMu.Unlock()

	if name == kukeonv1.HealthReportLatest {
		return fmt.Errorf("%w: %q names the newest report; delete a report by its name",
			errdefs.ErrHealthReportInvalid, name)
	}
	if !validName.MatchString(name) {
		return fmt.Errorf("%w: %q", errdefs.ErrHealthReportNotFound, name)
	}
	if _, err := s.Read(name); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(s.Dir, name)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %q", errdefs.ErrHealthReportNotFound, name)
		}
		return fmt.Errorf("delete health report: %w", err)
	}
	return nil
}

// LastUntil is the end of the newest report's window; ok is false when no
// report is stored.
func (s *Store) LastUntil() (time.Time, bool, error) {
	reports, err := s.load()
	if err != nil || len(reports) == 0 {
		return time.Time{}, false, err
	}
	return reports[0].Spec.Until, true, nil
}

// ReportID is the name Submit assigns to a report submitted without one.
func ReportID(createdAt time.Time) string {
	return createdAt.UTC().Format("20060102T150405.000000000Z")
}

// Defaults fills what kukeond fills on a submission that leaves it out:
// apiVersion, kind, metadata.realm, spec.createdAt (from now) and
// metadata.name (from spec.createdAt).
func Defaults(report *kukeonv1.HealthReport, now time.Time) {
	if report.APIVersion == "" {
		report.APIVersion = v1beta1.APIVersionV1Beta1
	}
	if report.Kind == "" {
		report.Kind = v1beta1.KindHealthReport
	}
	if report.Metadata.Realm == "" {
		report.Metadata.Realm = consts.KukeSystemRealmName
	}
	if report.Spec.CreatedAt.IsZero() {
		report.Spec.CreatedAt = now
	}
	report.Spec.CreatedAt = report.Spec.CreatedAt.UTC()
	if report.Metadata.Name == "" {
		report.Metadata.Name = ReportID(report.Spec.CreatedAt)
	}
}

// Submit fills the daemon-assigned fields, validates report, rejects a name
// already stored, marks findings the previous report already carried as
// recurring, stores it, and drops the reports beyond Keep. It returns the
// report as stored. `kuke apply` of a HealthReport and SubmitHealthReport
// both land here.
func (s *Store) Submit(report kukeonv1.HealthReport) (kukeonv1.HealthReport, error) {
	storeMu.Lock()
	defer storeMu.Unlock()

	Defaults(&report, s.now())
	if err := Validate(&report); err != nil {
		return kukeonv1.HealthReport{}, err
	}
	existing, err := s.load()
	if err != nil {
		return kukeonv1.HealthReport{}, err
	}
	for i := range existing {
		if existing[i].Metadata.Name == report.Metadata.Name {
			return kukeonv1.HealthReport{}, fmt.Errorf(
				"%w: a report named %q is already stored; a report cannot be changed",
				errdefs.ErrHealthReportExists, report.Metadata.Name)
		}
	}
	markRecurring(&report, existing)

	raw, err := yaml.Marshal(report)
	if err != nil {
		return kukeonv1.HealthReport{}, fmt.Errorf("encode health report: %w", err)
	}
	if err = os.MkdirAll(s.Dir, dirMode); err != nil {
		return kukeonv1.HealthReport{}, fmt.Errorf("create health report directory: %w", err)
	}
	if err = writeIfAbsent(filepath.Join(s.Dir, report.Metadata.Name), raw); err != nil {
		return kukeonv1.HealthReport{}, err
	}
	// The new report counts toward Keep wherever its CreatedAt sorts.
	all, err := s.load()
	if err != nil {
		return report, nil //nolint:nilerr // the report is stored; pruning retries on the next submit
	}
	for i := s.keep(); i < len(all); i++ {
		_ = os.Remove(filepath.Join(s.Dir, all[i].Metadata.Name))
	}
	return report, nil
}

// markRecurring flags each finding whose signature the newest earlier report
// already carried. A manual sweep that overlaps a scheduled one therefore
// reports the same finding as recurring, not as new.
func markRecurring(report *kukeonv1.HealthReport, existing []kukeonv1.HealthReport) {
	var previous *kukeonv1.HealthReport
	for i := range existing {
		if !existing[i].Spec.CreatedAt.After(report.Spec.CreatedAt) {
			previous = &existing[i]
			break
		}
	}
	seen := map[string]bool{}
	if previous != nil {
		for _, finding := range previous.Spec.Findings {
			seen[finding.Signature] = true
		}
	}
	for i := range report.Spec.Findings {
		report.Spec.Findings[i].Recurring = seen[report.Spec.Findings[i].Signature]
	}
}

// writeIfAbsent stages data in a temp file and links it into place, so a
// reader never sees a partial report and a name another writer claimed first
// is never overwritten.
func writeIfAbsent(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), tmpPrefix+"*")
	if err != nil {
		return fmt.Errorf("stage health report: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), fileMode)
	}
	if err == nil {
		err = os.Link(tmp.Name(), path)
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%w: a report named %q is already stored; a report cannot be changed",
				errdefs.ErrHealthReportExists, filepath.Base(path))
		}
	}
	if err != nil {
		return fmt.Errorf("store health report: %w", err)
	}
	return nil
}

// Decode reads a HealthReport document strictly: an unknown field is an
// error, so a misspelled key is not silently dropped. JSON is a subset of
// YAML, so one decoder covers both.
func Decode(raw []byte) (kukeonv1.HealthReport, error) {
	var report kukeonv1.HealthReport
	decoder := yaml.NewDecoder(strings.NewReader(string(raw)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&report); err != nil {
		return kukeonv1.HealthReport{}, fmt.Errorf("decode HealthReport: %w", err)
	}
	return report, nil
}

// ValidateSubmission checks report as kukeond will store it: a copy is given
// the fields kukeond fills when they are left out (Defaults), then validated.
// The name is not checked against the stored reports.
func ValidateSubmission(report kukeonv1.HealthReport, now time.Time) error {
	candidate := report
	Defaults(&candidate, now)
	return Validate(&candidate)
}

// Validate checks a report document. Every problem is reported at once, so a
// submitter fixes the document in one pass.
func Validate(report *kukeonv1.HealthReport) error {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	if report.APIVersion != v1beta1.APIVersionV1Beta1 {
		add("apiVersion: must be %s", v1beta1.APIVersionV1Beta1)
	}
	if report.Kind != v1beta1.KindHealthReport {
		add("kind: must be %s", v1beta1.KindHealthReport)
	}
	switch {
	case !validName.MatchString(report.Metadata.Name):
		add("metadata.name: must be 1-128 characters of letters, digits, '.', '_' or '-', " +
			"starting with a letter or digit")
	case report.Metadata.Name == kukeonv1.HealthReportLatest:
		add("metadata.name: %q is reserved for the newest report", kukeonv1.HealthReportLatest)
	}
	if report.Metadata.Realm != consts.KukeSystemRealmName {
		add("metadata.realm: a HealthReport lives in the %s realm", consts.KukeSystemRealmName)
	}
	spec := &report.Spec
	if spec.CreatedAt.IsZero() {
		add("spec.createdAt: required")
	}
	switch {
	case spec.Since.IsZero() || spec.Until.IsZero():
		add("spec.since, spec.until: both are required")
	case spec.Until.Before(spec.Since):
		add("spec.until: must not be before since")
	}
	if spec.Cell.Realm == "" || spec.Cell.Space == "" || spec.Cell.Stack == "" || spec.Cell.Name == "" {
		add("spec.cell: realm, space, stack and name of the submitting cell are required")
	}
	if strings.TrimSpace(spec.Summary) == "" {
		add("spec.summary: required")
	}
	if spec.Findings == nil {
		add("spec.findings: required (an empty list is valid)")
	}
	signatures := map[string]bool{}
	for i, finding := range spec.Findings {
		at := fmt.Sprintf("spec.findings[%d]", i)
		switch {
		case strings.TrimSpace(finding.Signature) == "":
			add("%s.signature: required", at)
		case signatures[finding.Signature]:
			add("%s.signature: duplicates an earlier finding", at)
		}
		signatures[finding.Signature] = true
		switch finding.Classification {
		case kukeonv1.HealthClassKukeon, kukeonv1.HealthClassWorkload, kukeonv1.HealthClassUnclear:
		default:
			add("%s.classification: must be kukeon, workload or unclear", at)
		}
		switch finding.Severity {
		case kukeonv1.HealthSeverityInfo, kukeonv1.HealthSeverityWarning,
			kukeonv1.HealthSeverityError, kukeonv1.HealthSeverityCritical:
		default:
			add("%s.severity: must be info, warning, error or critical", at)
		}
		if len(finding.Evidence) == 0 {
			add("%s.evidence: at least one entry is required", at)
		}
		if finding.IssueURL != "" {
			if !strings.HasPrefix(finding.IssueURL, "https://") {
				add("%s.issueUrl: must be an https URL", at)
			}
			if finding.Classification != kukeonv1.HealthClassKukeon {
				add("%s.issueUrl: only a kukeon finding may carry an issue", at)
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w: %s", errdefs.ErrHealthReportInvalid, strings.Join(problems, "; "))
	}
	return nil
}
