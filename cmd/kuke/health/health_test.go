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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	healthcmd "github.com/eminwux/kukeon/cmd/kuke/health"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

type fakeClient struct {
	kukeonv1.FakeClient

	request   kukeonv1.HealthDigestRequest
	digest    kukeonv1.HealthDigest
	submitted kukeonv1.HealthReport
	reports   []kukeonv1.HealthReport
	err       error
	version   string
}

func (f *fakeClient) PingVersion(context.Context) (string, error) { return f.version, nil }

func (f *fakeClient) Close() error { return nil }

func (f *fakeClient) ReadHealthDigest(
	_ context.Context, req kukeonv1.HealthDigestRequest,
) (kukeonv1.HealthDigest, error) {
	f.request = req
	return f.digest, f.err
}

func (f *fakeClient) SubmitHealthReport(
	_ context.Context, report kukeonv1.HealthReport,
) (kukeonv1.HealthReport, error) {
	f.submitted = report
	if f.err != nil {
		return kukeonv1.HealthReport{}, f.err
	}
	report.ID = "assigned"
	if len(report.Findings) > 0 {
		report.Findings[0].Recurring = true
	}
	return report, nil
}

func (f *fakeClient) ListHealthReports(context.Context) ([]kukeonv1.HealthReportSummary, error) {
	out := []kukeonv1.HealthReportSummary{}
	for _, r := range f.reports {
		out = append(out, kukeonv1.HealthReportSummary{ID: r.ID, Summary: r.Summary, Findings: len(r.Findings)})
	}
	return out, f.err
}

func (f *fakeClient) ReadHealthReport(_ context.Context, id string) (kukeonv1.HealthReport, error) {
	for _, r := range f.reports {
		if r.ID == id || id == kukeonv1.HealthReportLatest {
			return r, nil
		}
	}
	return kukeonv1.HealthReport{}, errdefs.ErrHealthReportNotFound
}

func run(t *testing.T, client *fakeClient, stdin string, args ...string) (string, string, error) {
	t.Helper()
	cmd := healthcmd.NewHealthCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)
	cmd.SetContext(context.WithValue(context.Background(), healthcmd.MockClientKey{}, kukeonv1.Client(client)))
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

var window = time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)

func dirtyDigest() kukeonv1.HealthDigest {
	return kukeonv1.HealthDigest{
		SchemaVersion: kukeonv1.HealthSchemaVersion, Since: window, Until: window.Add(time.Hour),
		Checks: []kukeonv1.HealthCheck{
			{Section: "host", Name: "containerd", Status: kukeonv1.HealthStatusOK, Detail: "reachable"},
			{Section: "host", Name: "cni-plugins", Status: kukeonv1.HealthStatusFail, Detail: "missing: bridge",
				Remediation: "redeploy kukeond"},
		},
		LogErrors: []kukeonv1.HealthLogGroup{{
			Signature: "start cell failed: task <id>", Count: 3, FirstSeen: window, LastSeen: window,
			Sample: "time=... level=ERROR msg=\"start cell failed\"",
		}},
		LogSource: "/opt/kukeon/data/log",
		Cells: []kukeonv1.HealthCell{{
			Realm: "default", Space: "s", Stack: "k", Name: "api", State: "Failed",
			Classification: kukeonv1.HealthClassKukeon, Since: window, Reason: "CreateFailed",
		}},
	}
}

func TestDigestExitCodes(t *testing.T) {
	clean := &fakeClient{digest: kukeonv1.HealthDigest{Clean: true, Since: window, Until: window}}
	if out, _, err := run(t, clean, "", "--exit-code"); err != nil || !strings.Contains(out, ": clean") {
		t.Fatalf("clean digest: out=%q err=%v", out, err)
	}

	dirty := &fakeClient{digest: dirtyDigest()}
	out, errOut, err := run(t, dirty, "", "--exit-code")
	var coded interface{ ExitCode() int }
	if !errors.As(err, &coded) || coded.ExitCode() != healthcmd.ExitFindings || healthcmd.ExitFindings != 2 {
		t.Fatalf("findings exit: %v", err)
	}
	if errOut != "" {
		t.Fatalf("findings must not print an error: %q", errOut)
	}
	for _, want := range []string{
		": findings", "host/cni-plugins", "FAIL", "redeploy kukeond", "3x start cell failed: task <id>",
		"default/s/k/api", "kukeon", "CreateFailed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("text digest lacks %q:\n%s", want, out)
		}
	}
	// Without --exit-code, findings still exit 0.
	if _, _, err = run(t, dirty, ""); err != nil {
		t.Fatalf("findings without --exit-code: %v", err)
	}

	failing := &fakeClient{err: errors.New("dial unix: no such file")}
	_, errOut, err = run(t, failing, "", "--exit-code")
	if err == nil || errors.As(err, &coded) || !strings.Contains(errOut, "no such file") {
		t.Fatalf("command failure must be a plain error: err=%v stderr=%q", err, errOut)
	}
}

func TestDigestJSONSchema(t *testing.T) {
	out, _, err := run(t, &fakeClient{digest: dirtyDigest()}, "", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err = json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"schemaVersion", "since", "until", "clean", "checks", "logErrors", "cells"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("json digest lacks %q", key)
		}
	}
	check := decoded["checks"].([]any)[1].(map[string]any)
	group := decoded["logErrors"].([]any)[0].(map[string]any)
	cell := decoded["cells"].([]any)[0].(map[string]any)
	for object, keys := range map[string][]string{
		"check": {"section", "name", "status", "detail", "remediation"},
		"group": {"signature", "count", "firstSeen", "lastSeen", "sample"},
		"cell":  {"realm", "space", "stack", "name", "state", "classification", "since"},
	} {
		source := map[string]map[string]any{"check": check, "group": group, "cell": cell}[object]
		for _, key := range keys {
			if _, ok := source[key]; !ok {
				t.Errorf("%s lacks %q", object, key)
			}
		}
	}
	if out, _, err = run(t, &fakeClient{digest: dirtyDigest()}, "", "-o", "yaml"); err != nil ||
		!strings.Contains(out, "schemaVersion: kukeon.io/health/v1") {
		t.Fatalf("yaml digest: %q %v", out, err)
	}
	if _, _, err = run(t, &fakeClient{}, "", "-o", "table"); err == nil {
		t.Fatal("unsupported format accepted")
	}
}

func TestSinceForms(t *testing.T) {
	client := &fakeClient{digest: kukeonv1.HealthDigest{Clean: true}}
	if _, _, err := run(t, client, ""); err != nil || !client.request.Since.IsZero() || client.request.SinceLastReport {
		t.Fatalf("default window must be left to the daemon: %+v", client.request)
	}
	if _, _, err := run(t, client, "", "--since", "last-report"); err != nil || !client.request.SinceLastReport {
		t.Fatalf("last-report: %+v %v", client.request, err)
	}
	before := time.Now().Add(-90 * time.Minute)
	if _, _, err := run(t, client, "", "--since", "90m"); err != nil ||
		client.request.Since.Before(before.Add(-time.Minute)) || client.request.Since.After(before.Add(time.Minute)) {
		t.Fatalf("duration: %+v %v", client.request, err)
	}
	if _, _, err := run(t, client, "", "--since", "2026-11-17T10:00:00Z"); err != nil ||
		!client.request.Since.Equal(window) {
		t.Fatalf("rfc3339: %+v %v", client.request, err)
	}
	for _, bad := range []string{"yesterday", "-5m", "2999-01-01T00:00:00Z"} {
		if _, _, err := run(t, client, "", "--since", bad); err == nil {
			t.Errorf("--since %s accepted", bad)
		}
	}
}

const reportYAML = `since: 2026-11-17T09:00:00Z
until: 2026-11-17T10:00:00Z
cell: {realm: default, space: default, stack: default, name: sweep}
summary: one kukeon defect
findings:
  - signature: "start cell failed: task <id>"
    classification: kukeon
    severity: error
    evidence: ["kuke get cell api -o yaml"]
    suggestedCommands: ["kuke daemon logs"]
body: |
  # Sweep
`

func TestSubmitFromFileAndStdin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.yaml")
	if err := os.WriteFile(path, []byte(reportYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	client := &fakeClient{}
	out, _, err := run(t, client, "", "report", "submit", "-f", path)
	if err != nil || !strings.Contains(out, "Stored health report assigned (1 findings, 1 recurring)") {
		t.Fatalf("submit: %q %v", out, err)
	}
	got := client.submitted
	if got.Cell.Name != "sweep" || !got.Until.Equal(window) || got.Findings[0].Severity != "error" ||
		got.Findings[0].SuggestedCommands[0] != "kuke daemon logs" || got.Body != "# Sweep\n" {
		t.Fatalf("submitted = %+v", got)
	}

	asJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	client = &fakeClient{}
	if _, _, err = run(t, client, string(asJSON), "report", "submit", "-f", "-"); err != nil ||
		client.submitted.Summary != "one kukeon defect" {
		t.Fatalf("stdin json: %+v %v", client.submitted, err)
	}

	if _, _, err = run(t, &fakeClient{}, "summary: x\nunknownField: 1\n", "report", "submit", "-f", "-"); err == nil {
		t.Fatal("unknown field accepted")
	}
	if _, _, err = run(t, &fakeClient{}, "", "report", "submit"); err == nil {
		t.Fatal("missing -f accepted")
	}
	rejected := &fakeClient{err: errdefs.ErrHealthReportInvalid}
	if _, errOut, submitErr := run(t, rejected, reportYAML, "report", "submit", "-f", "-"); submitErr == nil ||
		!strings.Contains(errOut, "invalid health report") {
		t.Fatalf("daemon rejection: %q %v", errOut, submitErr)
	}
}

func TestReportsAndReport(t *testing.T) {
	stored, err := healthcmd.DecodeReport([]byte(reportYAML))
	if err != nil {
		t.Fatal(err)
	}
	stored.ID, stored.CreatedAt = "r1", window
	stored.Findings[0].Recurring = true
	stored.Findings[0].IssueURL = "https://github.com/o/r/issues/7"
	client := &fakeClient{reports: []kukeonv1.HealthReport{stored}}

	out, _, err := run(t, client, "", "reports")
	if err != nil || !strings.Contains(out, "r1") || !strings.Contains(out, "one kukeon defect") {
		t.Fatalf("reports: %q %v", out, err)
	}
	if out, _, err = run(t, &fakeClient{}, "", "reports", "-o", "json"); err != nil || strings.TrimSpace(out) != "[]" {
		t.Fatalf("empty reports json: %q %v", out, err)
	}

	out, _, err = run(t, client, "", "report", "latest")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# Health report r1", "Classification: kukeon", "Recurrence: recurring",
		"Issue: https://github.com/o/r/issues/7", "kuke get cell api -o yaml", "- `kuke daemon logs`", "# Sweep",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown lacks %q:\n%s", want, out)
		}
	}
	if out, _, err = run(t, client, "", "report", "r1", "-o", "json"); err != nil || !strings.Contains(out, "\"issueUrl\"") {
		t.Fatalf("report json: %q %v", out, err)
	}
	if _, errOut, readErr := run(t, client, "", "report", "nope"); !errors.Is(readErr, errdefs.ErrHealthReportNotFound) ||
		!strings.Contains(errOut, "not found") {
		t.Fatalf("missing report: %q %v", errOut, readErr)
	}
}
