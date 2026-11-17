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
	"encoding/json"
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
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

// DefaultKeep is how many reports a Store keeps.
const DefaultKeep = 50

const (
	reportsSubdir = "reports"
	reportSuffix  = ".json"
	dirMode       = 0o750
	fileMode      = 0o640
)

var validID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// storeMu serializes submissions within the process. kukeond is the only
// writer of a run path's reports.
var storeMu sync.Mutex //nolint:gochecknoglobals // one writer per process

// Store keeps the newest health reports as one JSON file each under
// <RunPath>/health/reports. Files survive a kukeond restart; they are
// operational state, not a system of record.
type Store struct {
	Dir string
	// Keep is the number of reports retained; zero means DefaultKeep.
	Keep int
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// NewStore returns the Store of a run path.
func NewStore(runPath string) *Store {
	return &Store{Dir: filepath.Join(runPath, consts.KukeonHealthSubdir, reportsSubdir)}
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
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), reportSuffix) {
			continue
		}
		raw, readErr := os.ReadFile(filepath.Join(s.Dir, entry.Name()))
		if readErr != nil {
			continue
		}
		var report kukeonv1.HealthReport
		if json.Unmarshal(raw, &report) != nil || report.ID+reportSuffix != entry.Name() {
			continue
		}
		reports = append(reports, report)
	}
	sort.SliceStable(reports, func(i, j int) bool {
		if !reports[i].CreatedAt.Equal(reports[j].CreatedAt) {
			return reports[i].CreatedAt.After(reports[j].CreatedAt)
		}
		return reports[i].ID > reports[j].ID
	})
	return reports, nil
}

// List summarizes the stored reports, newest first.
func (s *Store) List() ([]kukeonv1.HealthReportSummary, error) {
	reports, err := s.load()
	if err != nil {
		return nil, err
	}
	out := make([]kukeonv1.HealthReportSummary, 0, len(reports))
	for i := range reports {
		report := &reports[i]
		summary := kukeonv1.HealthReportSummary{
			ID: report.ID, CreatedAt: report.CreatedAt, Since: report.Since, Until: report.Until,
			Summary: report.Summary, Findings: len(report.Findings),
		}
		for _, finding := range report.Findings {
			if finding.Recurring {
				summary.Recurring++
			}
			if finding.IssueURL != "" {
				summary.Filed++
			}
		}
		out = append(out, summary)
	}
	return out, nil
}

// Read returns the report with id, or the newest for HealthReportLatest.
func (s *Store) Read(id string) (kukeonv1.HealthReport, error) {
	reports, err := s.load()
	if err != nil {
		return kukeonv1.HealthReport{}, err
	}
	if id == kukeonv1.HealthReportLatest || id == "" {
		if len(reports) == 0 {
			return kukeonv1.HealthReport{}, fmt.Errorf("%w: no report is stored", errdefs.ErrHealthReportNotFound)
		}
		return reports[0], nil
	}
	for i := range reports {
		if reports[i].ID == id {
			return reports[i], nil
		}
	}
	return kukeonv1.HealthReport{}, fmt.Errorf("%w: %q", errdefs.ErrHealthReportNotFound, id)
}

// LastUntil is the end of the newest report's window; ok is false when no
// report is stored.
func (s *Store) LastUntil() (time.Time, bool, error) {
	reports, err := s.load()
	if err != nil || len(reports) == 0 {
		return time.Time{}, false, err
	}
	return reports[0].Until, true, nil
}

// Submit validates report, fills the daemon-assigned fields, marks findings
// the previous report already carried as recurring, stores it, and drops the
// reports beyond Keep. It returns the report as stored.
func (s *Store) Submit(report kukeonv1.HealthReport) (kukeonv1.HealthReport, error) {
	storeMu.Lock()
	defer storeMu.Unlock()

	if report.CreatedAt.IsZero() {
		report.CreatedAt = s.now()
	}
	report.CreatedAt = report.CreatedAt.UTC()
	if report.ID == "" {
		report.ID = report.CreatedAt.Format("20060102T150405.000000000Z")
	}
	report.SchemaVersion = kukeonv1.HealthSchemaVersion
	if err := Validate(&report); err != nil {
		return kukeonv1.HealthReport{}, err
	}
	existing, err := s.load()
	if err != nil {
		return kukeonv1.HealthReport{}, err
	}
	for i := range existing {
		if existing[i].ID == report.ID {
			return kukeonv1.HealthReport{}, fmt.Errorf(
				"%w: a report with id %q is already stored", errdefs.ErrHealthReportInvalid, report.ID)
		}
	}
	markRecurring(&report, existing)

	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return kukeonv1.HealthReport{}, fmt.Errorf("encode health report: %w", err)
	}
	if err = os.MkdirAll(s.Dir, dirMode); err != nil {
		return kukeonv1.HealthReport{}, fmt.Errorf("create health report directory: %w", err)
	}
	if err = writeAtomic(filepath.Join(s.Dir, report.ID+reportSuffix), raw); err != nil {
		return kukeonv1.HealthReport{}, err
	}
	// The new report counts toward Keep wherever its CreatedAt sorts.
	all, err := s.load()
	if err != nil {
		return report, nil //nolint:nilerr // the report is stored; pruning retries on the next submit
	}
	for i := s.keep(); i < len(all); i++ {
		_ = os.Remove(filepath.Join(s.Dir, all[i].ID+reportSuffix))
	}
	return report, nil
}

// markRecurring flags each finding whose signature the newest earlier report
// already carried. A manual sweep that overlaps a scheduled one therefore
// reports the same finding as recurring, not as new.
func markRecurring(report *kukeonv1.HealthReport, existing []kukeonv1.HealthReport) {
	var previous *kukeonv1.HealthReport
	for i := range existing {
		if !existing[i].CreatedAt.After(report.CreatedAt) {
			previous = &existing[i]
			break
		}
	}
	seen := map[string]bool{}
	if previous != nil {
		for _, finding := range previous.Findings {
			seen[finding.Signature] = true
		}
	}
	for i := range report.Findings {
		report.Findings[i].Recurring = seen[report.Findings[i].Signature]
	}
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".report-*")
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
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		return fmt.Errorf("store health report: %w", err)
	}
	return nil
}

// Validate checks a report against the schema. Every problem is reported at
// once, so a submitter fixes the document in one pass.
func Validate(report *kukeonv1.HealthReport) error {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	if !validID.MatchString(report.ID) {
		add("id: must be 1-128 characters of letters, digits, '.', '_' or '-', starting with a letter or digit")
	}
	if report.CreatedAt.IsZero() {
		add("createdAt: required")
	}
	switch {
	case report.Since.IsZero() || report.Until.IsZero():
		add("since, until: both are required")
	case report.Until.Before(report.Since):
		add("until: must not be before since")
	}
	if report.Cell.Realm == "" || report.Cell.Space == "" || report.Cell.Stack == "" || report.Cell.Name == "" {
		add("cell: realm, space, stack and name of the submitting cell are required")
	}
	if strings.TrimSpace(report.Summary) == "" {
		add("summary: required")
	}
	if report.Findings == nil {
		add("findings: required (an empty list is valid)")
	}
	signatures := map[string]bool{}
	for i, finding := range report.Findings {
		at := fmt.Sprintf("findings[%d]", i)
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
