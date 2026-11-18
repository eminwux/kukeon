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

package health_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/health"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func TestNormalizeMasksWhatVaries(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"timestamp", "retry at 2026-11-17T10:04:05.123Z failed", "retry at <time> failed"},
		{"timestamp offset", "since 2026-11-17 10:04:05+01:00", "since <time>"},
		{"uuid", "task 3f2b8c1e-9d4a-4b6f-8a1c-0e9f7d6c5b4a gone", "task <id> gone"},
		{"digest", "pull sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08", "pull <id>"},
		{"container id", "container 4f1b2b0b822c exited", "container <id> exited"},
		{"generated name", "bridge k-1a2b3c4d missing", "bridge k-<id> missing"},
		{"number", "exit status 137 after 3 attempts", "exit status <n> after <n> attempts"},
		{"duration", "timed out after 1.5s", "timed out after <n>"},
		{"hex literal", "errno 0x1f", "errno <n>"},
		{"pid in path", "open /proc/4242/ns/net: no such file", "open /proc/<n>/ns/net: no such file"},
		{"whitespace", "  two   spaces\there ", "two spaces here"},
		{"words kept", "failed to start cell: deadline exceeded", "failed to start cell: deadline exceeded"},
	} {
		if got := health.Normalize(tc.in); got != tc.want {
			t.Errorf("%s: Normalize(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestSignatureJoinsMessageAndError(t *testing.T) {
	a := health.Signature("start cell failed", "task 4f1b2b0b822c: exit status 1")
	b := health.Signature("start cell failed", "task 0a1b2c3d4e5f: exit status 2")
	if a != b || a != "start cell failed: task <id>: exit status <n>" {
		t.Fatalf("signatures %q / %q", a, b)
	}
	if health.Signature("only message", "") != "only message" {
		t.Fatal("an entry without error text keeps its message alone")
	}
}

const logFixture = `time=2026-11-17T09:00:00.000Z level=ERROR msg="start cell failed" cell=a error="task 4f1b2b0b822c: exit status 1"
time=2026-11-17T10:10:00.000Z level=INFO msg="reconcile tick"
time=2026-11-17T10:20:00.000Z level=ERROR msg="start cell failed" cell=b error="task 0a1b2c3d4e5f: exit status 2"
panic: not a log line
time=2026-11-17T10:30:00.000Z level=ERROR msg="start cell failed" cell=c error="task aaaabbbbcccc: exit status 9"
time=2026-11-17T10:40:00.000Z level=WARN msg="disk pressure" pct=91
time=2026-11-17T10:45:00.000Z level=ERROR+2 msg="cni attach failed" err="netns /proc/77/ns/net missing"
time=2026-11-17T11:30:00.000Z level=ERROR msg="after the window"
level=ERROR msg="no time"
`

func TestGroupLogErrors(t *testing.T) {
	since := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	until := time.Date(2026, 11, 17, 11, 0, 0, 0, time.UTC)
	groups, err := health.GroupLogErrors(strings.NewReader(logFixture), since, until)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 {
		t.Fatalf("groups = %+v", groups)
	}
	first := groups[0]
	if first.Signature != "start cell failed: task <id>: exit status <n>" || first.Count != 2 ||
		!first.FirstSeen.Equal(since.Add(20*time.Minute)) || !first.LastSeen.Equal(since.Add(30*time.Minute)) ||
		!strings.Contains(first.Sample, "cell=c") {
		t.Fatalf("first group = %+v", first)
	}
	if groups[1].Signature != "cni attach failed: netns /proc/<n>/ns/net missing" || groups[1].Count != 1 {
		t.Fatalf("second group = %+v", groups[1])
	}
}

func TestGroupLogErrorsSkipsOverlongLine(t *testing.T) {
	long := "time=2026-11-17T10:05:00.000Z level=ERROR msg=\"" + strings.Repeat("x", 2<<20) + "\"\n"
	since := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	groups, err := health.GroupLogErrors(strings.NewReader(long+logFixture), since, since.Add(time.Hour))
	if err != nil || len(groups) != 2 {
		t.Fatalf("groups=%d err=%v", len(groups), err)
	}
}

func probe(t *testing.T) health.Probe {
	t.Helper()
	root := t.TempDir()
	cgroup, cni, run := filepath.Join(root, "cgroup"), filepath.Join(root, "cni"), filepath.Join(root, "run")
	for _, dir := range []string{cgroup, cni, run} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, data string) {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(cgroup, "cgroup.controllers"), "cpu memory io pids\n")
	write(filepath.Join(cni, "bridge"), "")
	write(filepath.Join(cni, "loopback"), "")
	write(filepath.Join(run, "kukeond.sock"), "")
	write(filepath.Join(run, "kukeond.pid"), "")
	return health.Probe{
		ContainerdSocket: "/run/containerd/containerd.sock", CgroupRoot: cgroup, CNIBinDir: cni, SocketDir: run,
		Namespaces: func() ([]string, error) {
			return []string{"default.kukeon.io", "kuke-system.kukeon.io", "moby"}, nil
		},
		RealmNamespaces: func() ([]string, error) {
			return []string{"default.kukeon.io", "kuke-system.kukeon.io"}, nil
		},
	}
}

func statuses(checks []kukeonv1.HealthCheck) map[string]string {
	out := map[string]string{}
	for _, check := range checks {
		out[check.Name] = check.Status
	}
	return out
}

func TestChecksCoverTheStatusHostAndStateSet(t *testing.T) {
	checks := probe(t).Checks()
	var names []string
	for _, check := range checks {
		names = append(names, check.Section+"/"+check.Name)
		if check.Status != kukeonv1.HealthStatusOK {
			t.Errorf("%s = %s (%s)", check.Name, check.Status, check.Detail)
		}
	}
	want := "host/containerd host/cgroup-v2 host/cni-plugins state/run-dir state/containerd-ns"
	if strings.Join(names, " ") != want {
		t.Fatalf("checks = %v", names)
	}
}

func TestChecksReportFailures(t *testing.T) {
	p := probe(t)
	if err := os.Remove(filepath.Join(p.CNIBinDir, "bridge")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(p.CgroupRoot, "cgroup.controllers")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.SocketDir, "stale.sock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	p.Namespaces = func() ([]string, error) {
		return []string{"default.kukeon.io", "gone.kukeon.io"}, nil
	}
	got := statuses(p.Checks())
	want := map[string]string{
		"containerd": kukeonv1.HealthStatusOK, "cgroup-v2": kukeonv1.HealthStatusFail,
		"cni-plugins": kukeonv1.HealthStatusFail, "run-dir": kukeonv1.HealthStatusWarn,
		"containerd-ns": kukeonv1.HealthStatusWarn,
	}
	for name, status := range want {
		if got[name] != status {
			t.Errorf("%s = %s, want %s", name, got[name], status)
		}
	}

	p.Namespaces = func() ([]string, error) { return nil, errors.New("dial failed") }
	got = statuses(p.Checks())
	if got["containerd"] != kukeonv1.HealthStatusFail || got["containerd-ns"] != kukeonv1.HealthStatusWarn {
		t.Fatalf("unreachable containerd: %v", got)
	}
}

func cell(name string, state v1beta1.CellState) v1beta1.CellDoc {
	doc := v1beta1.CellDoc{}
	doc.Metadata.Name = name
	doc.Spec.RealmID, doc.Spec.SpaceID, doc.Spec.StackID = "default", "team", "work"
	doc.Status.State = state
	doc.Status.UpdatedAt = time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	return doc
}

func TestFailedCellsAreTaggedByOwner(t *testing.T) {
	crashed := cell("job", v1beta1.CellStateError)
	finished := time.Date(2026, 11, 17, 8, 30, 0, 0, time.UTC)
	crashed.Status.Containers = []v1beta1.ContainerStatus{{FinishTime: finished.Add(-time.Minute)}, {FinishTime: finished}}
	broken := cell("api", v1beta1.CellStateFailed)
	broken.Status.Reason, broken.Status.Message = "CreateFailed", "cni attach failed"

	cells := health.FailedCells([]v1beta1.CellDoc{
		crashed, cell("fine", v1beta1.CellStateReady), broken, cell("done", v1beta1.CellStateExited),
	})
	if len(cells) != 2 {
		t.Fatalf("cells = %+v", cells)
	}
	api, job := cells[0], cells[1]
	if api.Name != "api" || api.State != "Failed" || api.Classification != kukeonv1.HealthClassKukeon ||
		api.Realm != "default" || api.Space != "team" || api.Stack != "work" ||
		!api.Since.Equal(broken.Status.UpdatedAt) || api.Reason != "CreateFailed" {
		t.Fatalf("failed cell = %+v", api)
	}
	if job.State != "Error" || job.Classification != kukeonv1.HealthClassWorkload || !job.Since.Equal(finished) {
		t.Fatalf("error cell = %+v", job)
	}
}

func TestClean(t *testing.T) {
	ok := []kukeonv1.HealthCheck{{Status: kukeonv1.HealthStatusOK}}
	if !health.Clean(&kukeonv1.HealthDigest{Checks: ok}) {
		t.Fatal("all-ok digest is clean")
	}
	for name, digest := range map[string]kukeonv1.HealthDigest{
		"warn check": {Checks: []kukeonv1.HealthCheck{{Status: kukeonv1.HealthStatusWarn}}},
		"log error":  {Checks: ok, LogErrors: []kukeonv1.HealthLogGroup{{Count: 1}}},
		"cell":       {Checks: ok, Cells: []kukeonv1.HealthCell{{Name: "x"}}},
	} {
		if health.Clean(&digest) {
			t.Errorf("%s: digest reported clean", name)
		}
	}
}

func validReport(at time.Time) kukeonv1.HealthReport {
	return kukeonv1.HealthReport{
		Since: at.Add(-time.Hour), Until: at, CreatedAt: at,
		Cell:    kukeonv1.HealthCellRef{Realm: "default", Space: "default", Stack: "default", Name: "sweep"},
		Summary: "one kukeon defect",
		Findings: []kukeonv1.HealthFinding{{
			Signature: "start cell failed: task <id>", Classification: kukeonv1.HealthClassKukeon,
			Severity: kukeonv1.HealthSeverityError, Evidence: []string{"kuke get cell api -o yaml"},
		}},
		Body: "# Sweep\n",
	}
}

func TestValidateRejectsSchemaViolations(t *testing.T) {
	at := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	ok := validReport(at)
	ok.ID = "r1"
	if err := health.Validate(&ok); err != nil {
		t.Fatalf("valid report rejected: %v", err)
	}
	for name, mutate := range map[string]func(*kukeonv1.HealthReport){
		"id":             func(r *kukeonv1.HealthReport) { r.ID = "../escape" },
		"reserved id":    func(r *kukeonv1.HealthReport) { r.ID = kukeonv1.HealthReportLatest },
		"created":        func(r *kukeonv1.HealthReport) { r.CreatedAt = time.Time{} },
		"window":         func(r *kukeonv1.HealthReport) { r.Since = time.Time{} },
		"window order":   func(r *kukeonv1.HealthReport) { r.Until = r.Since.Add(-time.Second) },
		"cell":           func(r *kukeonv1.HealthReport) { r.Cell.Name = "" },
		"summary":        func(r *kukeonv1.HealthReport) { r.Summary = " " },
		"findings":       func(r *kukeonv1.HealthReport) { r.Findings = nil },
		"signature":      func(r *kukeonv1.HealthReport) { r.Findings[0].Signature = "" },
		"classification": func(r *kukeonv1.HealthReport) { r.Findings[0].Classification = "ours" },
		"severity":       func(r *kukeonv1.HealthReport) { r.Findings[0].Severity = "bad" },
		"evidence":       func(r *kukeonv1.HealthReport) { r.Findings[0].Evidence = nil },
		"issue scheme":   func(r *kukeonv1.HealthReport) { r.Findings[0].IssueURL = "http://example.test/1" },
		"issue on workload": func(r *kukeonv1.HealthReport) {
			r.Findings[0].Classification = kukeonv1.HealthClassWorkload
			r.Findings[0].IssueURL = "https://github.com/o/r/issues/1"
		},
		"duplicate signature": func(r *kukeonv1.HealthReport) { r.Findings = append(r.Findings, r.Findings[0]) },
	} {
		bad := validReport(at)
		bad.ID = "r1"
		mutate(&bad)
		if err := health.Validate(&bad); !errors.Is(err, errdefs.ErrHealthReportInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	empty := validReport(at)
	empty.ID, empty.Findings = "r2", []kukeonv1.HealthFinding{}
	if err := health.Validate(&empty); err != nil {
		t.Fatalf("a report with no findings is valid: %v", err)
	}
}

func TestStoreSubmitListReadSurviveReopen(t *testing.T) {
	runPath := t.TempDir()
	at := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	store := health.NewStore(runPath)
	store.Now = func() time.Time { return at }

	if _, ok, err := store.LastUntil(); ok || err != nil {
		t.Fatalf("empty store: ok=%v err=%v", ok, err)
	}
	if _, err := store.Read(kukeonv1.HealthReportLatest); !errors.Is(err, errdefs.ErrHealthReportNotFound) {
		t.Fatalf("latest of empty store: %v", err)
	}

	first := validReport(at)
	first.CreatedAt = time.Time{} // assigned by the store
	stored, err := store.Submit(first)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ID != health.ReportID(at) || !stored.CreatedAt.Equal(at) ||
		stored.SchemaVersion != kukeonv1.HealthSchemaVersion || stored.Findings[0].Recurring {
		t.Fatalf("stored = %+v", stored)
	}

	second := validReport(at.Add(time.Hour))
	second.ID = "second"
	second.Findings = append(second.Findings, kukeonv1.HealthFinding{
		Signature: "cell default/default/default/job Error", Classification: kukeonv1.HealthClassWorkload,
		Severity: kukeonv1.HealthSeverityWarning, Evidence: []string{"exit code 3"},
	})
	second.Findings[0].Recurring = false
	second.Findings[1].Recurring = true // the store decides, not the submitter
	if stored, err = store.Submit(second); err != nil {
		t.Fatal(err)
	}
	if !stored.Findings[0].Recurring || stored.Findings[1].Recurring {
		t.Fatalf("recurrence = %+v", stored.Findings)
	}
	if _, err = store.Submit(second); !errors.Is(err, errdefs.ErrHealthReportInvalid) {
		t.Fatalf("duplicate id: %v", err)
	}

	// A new Store over the same run path is what a restarted kukeond sees.
	reopened := health.NewStore(runPath)
	list, err := reopened.List()
	if err != nil || len(list) != 2 || list[0].ID != "second" || list[0].Findings != 2 || list[0].Recurring != 1 {
		t.Fatalf("list = %+v err=%v", list, err)
	}
	latest, err := reopened.Read(kukeonv1.HealthReportLatest)
	if err != nil || latest.ID != "second" || latest.Body != "# Sweep\n" {
		t.Fatalf("latest = %+v err=%v", latest, err)
	}
	if until, ok, lastErr := reopened.LastUntil(); lastErr != nil || !ok || !until.Equal(at.Add(time.Hour)) {
		t.Fatalf("LastUntil = %v %v %v", until, ok, lastErr)
	}
	if _, err = reopened.Read("missing"); !errors.Is(err, errdefs.ErrHealthReportNotFound) {
		t.Fatalf("missing id: %v", err)
	}
}

func TestStoreKeepsOnlyTheNewestReports(t *testing.T) {
	store := health.NewStore(t.TempDir())
	if health.DefaultKeep != 50 {
		t.Fatalf("DefaultKeep = %d", health.DefaultKeep)
	}
	at := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	for i := range health.DefaultKeep + 3 {
		report := validReport(at.Add(time.Duration(i) * time.Minute))
		if _, err := store.Submit(report); err != nil {
			t.Fatal(err)
		}
	}
	list, err := store.List()
	if err != nil || len(list) != health.DefaultKeep {
		t.Fatalf("kept %d reports, err=%v", len(list), err)
	}
	// The three oldest were dropped: the oldest survivor is the fourth.
	if oldest := list[len(list)-1]; !oldest.CreatedAt.Equal(at.Add(3 * time.Minute)) {
		t.Fatalf("oldest kept = %v", oldest.CreatedAt)
	}
	files, err := os.ReadDir(store.Dir)
	if err != nil || len(files) != health.DefaultKeep {
		t.Fatalf("files on disk = %d, err=%v", len(files), err)
	}

	small := health.NewStore(t.TempDir())
	small.Keep = 2
	for i := range 4 {
		if _, err = small.Submit(validReport(at.Add(time.Duration(i) * time.Minute))); err != nil {
			t.Fatal(err)
		}
	}
	if list, err = small.List(); err != nil || len(list) != 2 {
		t.Fatalf("Keep=2 kept %d", len(list))
	}
}
