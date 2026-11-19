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
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	healthcmd "github.com/eminwux/kukeon/cmd/kuke/health"
	"github.com/eminwux/kukeon/internal/health"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

// tokenShaped assembles each fixture from a prefix and a body at run time, so
// the repository holds no string a secret scanner would take for a credential.
var tokenShaped = []string{
	"ghp" + "_abcdefghijklmnopqrstuvwxyz0123456789",
	"github" + "_pat_11ABCDEFG0abcdefghijkl_mnopqrstuvwxyz0123456789ABCDEFGHIJ",
	"sk" + "-ant-api03-abcdefghijklmnopqrstuvwxyz012345",
	"AKIA" + "IOSFODNN7EXAMPLE",
	"eyJhbGciOiJIUzI1NiJ9" + ".eyJzdWIiOiIxMjM0NTY3ODkwIn0" + ".dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U",
	"xox" + "b-123456789012-abcdefghijklmnop",
}

func TestSanitizeRedactsSecrets(t *testing.T) {
	for _, secret := range tokenShaped {
		for _, line := range []string{
			"clone failed with " + secret + " in env",
			"GH_TOKEN=" + secret,
			`level=ERROR msg="auth" header="Authorization: Bearer ` + secret + `"`,
		} {
			if got := healthcmd.Sanitize(line); strings.Contains(got, secret) {
				t.Errorf("secret survived: %q -> %q", line, got)
			}
		}
	}
	for in, want := range map[string]string{
		"fetch https://x-access-token:hunter2secret@github.com/o/r.git failed": "fetch https://[redacted]@github.com/o/r.git failed",
		"ssh://deploy@git.example.test/r.git":                                  "ssh://[redacted]@git.example.test/r.git",
		"password=hunter2 user=bob":                                            "password=[redacted] user=bob",
		`api_key: "abc123" region=eu`:                                          "api_key: [redacted] region=eu",
		"OPENROUTER_API_KEY=or-v1-zzz":                                         "OPENROUTER_API_KEY=[redacted]",
		"exit status 1: cni attach failed for cell api":                        "exit status 1: cni attach failed for cell api",
		"image ghcr.io/eminwux/kukeon/codex:v0.10.0 pulled":                    "image ghcr.io/eminwux/kukeon/codex:v0.10.0 pulled",
	} {
		if got := healthcmd.Sanitize(in); got != want {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
		}
	}
	// Assembled so the repository holds no literal key header.
	header := "OPENSSH PRIVATE " + "KEY-----"
	key := "-----BEGIN " + header + "\nb3BlbnNzaC1rZXk\n-----END " + header
	if got := healthcmd.Sanitize("key:\n" + key + "\nafter"); strings.Contains(got, "b3BlbnNzaC1rZXk") ||
		!strings.Contains(got, "after") {
		t.Errorf("private key survived: %q", got)
	}
	if got := healthcmd.Sanitize("value opaque-literal-9 seen", "opaque-literal-9"); strings.Contains(got, "opaque-literal-9") {
		t.Errorf("explicit literal survived: %q", got)
	}
}

// github is a minimal stand-in for the issues API.
type github struct {
	mu       sync.Mutex
	issues   []map[string]any
	requests []string
	bodies   []string
	auth     []string
	fail     string
}

func (g *github) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		raw, _ := io.ReadAll(r.Body)
		g.requests = append(g.requests, r.Method+" "+r.URL.Path)
		g.bodies = append(g.bodies, string(raw))
		g.auth = append(g.auth, r.Header.Get("Authorization"))
		if g.fail != "" && strings.HasPrefix(r.Method+" "+r.URL.Path, g.fail) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"Resource not accessible"}`))
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/ops/issues":
			if r.URL.Query().Get("state") != "open" {
				t.Errorf("issue listing must be limited to open issues: %s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(g.issues)
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/ops/issues":
			var payload map[string]string
			_ = json.Unmarshal(raw, &payload)
			number := len(g.issues) + 1
			issue := map[string]any{
				"number": number, "state": "open", "body": payload["body"], "title": payload["title"],
				"html_url": "https://github.com/acme/ops/issues/" + string(rune('0'+number)),
			}
			g.issues = append(g.issues, issue)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(issue)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/comments"):
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":1}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func (g *github) count(prefix string) int {
	n := 0
	for _, request := range g.requests {
		if strings.HasPrefix(request, prefix) {
			n++
		}
	}
	return n
}

func sweepReport() kukeonv1.HealthReport {
	return kukeonv1.HealthReport{
		Since: window, Until: window.Add(3600e9),
		Cell:    kukeonv1.HealthCellRef{Realm: "default", Space: "default", Stack: "default", Name: "sweep"},
		Summary: "three findings",
		Findings: []kukeonv1.HealthFinding{
			{
				Signature: "start cell failed: task <id>: exit status <n>", Classification: kukeonv1.HealthClassKukeon,
				Severity: kukeonv1.HealthSeverityError, Title: "cells fail to start after CNI attach error", Count: 4,
				Evidence: []string{
					"time=... level=ERROR msg=\"start cell failed\" token=" + tokenShaped[0],
					"clone https://x-access-token:" + tokenShaped[2] + "@github.com/acme/app.git",
				},
			},
			{
				Signature: "cell default/default/default/job Error", Classification: kukeonv1.HealthClassWorkload,
				Severity: kukeonv1.HealthSeverityWarning, Evidence: []string{"exit code 3"},
			},
			{
				Signature: "check state/run-dir", Classification: kukeonv1.HealthClassUnclear,
				Severity: kukeonv1.HealthSeverityInfo, Evidence: []string{"orphan entries: x.sock"},
			},
		},
	}
}

func filer(server *httptest.Server) *healthcmd.Filer {
	return &healthcmd.Filer{
		Repo: "acme/ops", Token: "filing-token-0123456789", API: server.URL, Version: "v0.11.0", HTTP: server.Client(),
	}
}

func TestFilingOpensOneIssuePerNewKukeonFinding(t *testing.T) {
	gh := &github{}
	server := httptest.NewServer(gh.handler(t))
	defer server.Close()

	report := sweepReport()
	opened, commented, err := filer(server).File(t.Context(), &report, nil)
	if err != nil || opened != 1 || commented != 0 {
		t.Fatalf("opened=%d commented=%d err=%v", opened, commented, err)
	}
	if gh.count("POST /repos/acme/ops/issues") != 1 {
		t.Fatalf("requests = %v", gh.requests)
	}
	issue := gh.issues[0]
	if issue["title"] != "health: kukeond: cells fail to start after CNI attach error" {
		t.Fatalf("title = %q", issue["title"])
	}
	body := issue["body"].(string)
	for _, want := range []string{
		"## Symptom", "cells fail to start after CNI attach error", "## Evidence", "start cell failed",
		"## Frequency", "4 occurrences", "first seen in this window", "## kukeon version", "v0.11.0",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("issue body lacks %q:\n%s", want, body)
		}
	}
	// The URL is recorded on the kukeon finding only.
	if report.Findings[0].IssueURL != "https://github.com/acme/ops/issues/1" ||
		report.Findings[1].IssueURL != "" || report.Findings[2].IssueURL != "" {
		t.Fatalf("issue URLs = %q %q %q",
			report.Findings[0].IssueURL, report.Findings[1].IssueURL, report.Findings[2].IssueURL)
	}
	// Nothing sent to GitHub carries a secret, a credential URL, the
	// workload finding or the unclear one.
	sent := strings.Join(gh.bodies, "\n")
	for _, secret := range append([]string{"filing-token-0123456789", "x-access-token:"}, tokenShaped...) {
		if strings.Contains(sent, secret) {
			t.Errorf("request bodies leak %q", secret)
		}
	}
	for _, private := range []string{"default/default/default/job", "exit code 3", "run-dir", "x.sock"} {
		if strings.Contains(sent, private) {
			t.Errorf("a non-kukeon finding reached GitHub: %q", private)
		}
	}
	for _, header := range gh.auth {
		if header != "Bearer filing-token-0123456789" {
			t.Fatalf("authorization header = %q", header)
		}
	}
}

func TestFilingCommentsOnTheOpenIssueOfARecurringFinding(t *testing.T) {
	gh := &github{}
	server := httptest.NewServer(gh.handler(t))
	defer server.Close()

	first := sweepReport()
	if _, _, err := filer(server).File(t.Context(), &first, nil); err != nil {
		t.Fatal(err)
	}
	second := sweepReport()
	opened, commented, err := filer(server).File(t.Context(), &second, &first)
	if err != nil || opened != 0 || commented != 1 {
		t.Fatalf("opened=%d commented=%d err=%v", opened, commented, err)
	}
	if len(gh.issues) != 1 || gh.count("POST /repos/acme/ops/issues/1/comments") != 1 {
		t.Fatalf("a recurring finding must comment, not file again: %v", gh.requests)
	}
	if second.Findings[0].IssueURL != first.Findings[0].IssueURL {
		t.Fatalf("recurring finding lost its issue: %q", second.Findings[0].IssueURL)
	}
	comment := gh.bodies[len(gh.bodies)-1]
	if !strings.Contains(comment, "Seen again") ||
		!strings.Contains(comment, "recurring: the previous health report also carried it") {
		t.Fatalf("comment = %s", comment)
	}
	for _, secret := range tokenShaped {
		if strings.Contains(comment, secret) {
			t.Errorf("comment leaks %q", secret)
		}
	}

	// The issue is still open but the previous report did not carry the
	// finding: the comment must not claim it did.
	back := sweepReport()
	_, commented, err = filer(server).File(t.Context(), &back, &kukeonv1.HealthReport{})
	if err != nil || commented != 1 {
		t.Fatalf("back again: commented=%d err=%v", commented, err)
	}
	comment = gh.bodies[len(gh.bodies)-1]
	if strings.Contains(comment, "recurring") ||
		!strings.Contains(comment, "the previous health report did not carry it") {
		t.Fatalf("comment on a finding the previous report lacked = %s", comment)
	}

	// A recurring finding whose issue was closed gets a new one.
	gh.issues = nil
	third := sweepReport()
	if opened, commented, err = filer(server).File(t.Context(), &third, &second); err != nil || opened != 1 || commented != 0 {
		t.Fatalf("closed issue: opened=%d commented=%d err=%v", opened, commented, err)
	}
	if body := gh.issues[0]["body"].(string); !strings.Contains(body, "recurring: the previous health report") {
		t.Fatalf("reopened issue body lacks recurrence:\n%s", body)
	}
}

func TestFilingSkipsGitHubWhenNothingIsKukeon(t *testing.T) {
	gh := &github{}
	server := httptest.NewServer(gh.handler(t))
	defer server.Close()
	report := sweepReport()
	report.Findings = report.Findings[1:]
	report.Findings[0].IssueURL = "https://github.com/acme/ops/issues/9" // a submitter cannot smuggle one in
	if opened, commented, err := filer(server).File(t.Context(), &report, nil); err != nil || opened+commented != 0 {
		t.Fatalf("opened=%d commented=%d err=%v", opened, commented, err)
	}
	if len(gh.requests) != 0 || report.Findings[0].IssueURL != "" {
		t.Fatalf("requests=%v url=%q", gh.requests, report.Findings[0].IssueURL)
	}
}

func TestFilingRefusals(t *testing.T) {
	gh := &github{}
	server := httptest.NewServer(gh.handler(t))
	defer server.Close()
	report := sweepReport()

	noToken := filer(server)
	noToken.Token = ""
	_, _, err := noToken.File(t.Context(), &report, nil)
	if err == nil || !strings.Contains(err.Error(), "GH_TOKEN") || !strings.Contains(err.Error(), "gh-token") {
		t.Fatalf("missing token must name GH_TOKEN and the gh-token opt-in: %v", err)
	}
	badRepo := filer(server)
	badRepo.Repo = "not a repo"
	if _, _, err := badRepo.File(t.Context(), &report, nil); err == nil {
		t.Fatal("malformed repository accepted")
	}
	if len(gh.requests) != 0 {
		t.Fatalf("refused filing reached GitHub: %v", gh.requests)
	}

	gh.fail = "POST /repos/acme/ops/issues"
	_, _, err = filer(server).File(t.Context(), &report, nil)
	if err == nil || !strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "filing-token") {
		t.Fatalf("API failure: %v", err)
	}
}

func TestSubmitWithFileIssuesStoresTheIssueURL(t *testing.T) {
	gh := &github{}
	server := httptest.NewServer(gh.handler(t))
	defer server.Close()
	t.Setenv(healthcmd.TokenEnv, "filing-token-0123456789")
	t.Setenv(healthcmd.APIEnv, server.URL)

	raw, err := json.Marshal(sweepReport())
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeClient{version: "v0.11.0"}
	out, _, err := run(t, client, string(raw), "report", "submit", "-f", "-", "--file-issues", "acme/ops")
	if err != nil || !strings.Contains(out, "Filed in acme/ops: 1 issues opened, 0 commented") {
		t.Fatalf("out=%q err=%v", out, err)
	}
	if client.submitted.Findings[0].IssueURL != "https://github.com/acme/ops/issues/1" {
		t.Fatalf("stored report lacks the issue URL: %+v", client.submitted.Findings[0])
	}
	if !strings.Contains(gh.issues[0]["body"].(string), "v0.11.0") {
		t.Fatal("issue body lacks the daemon's kukeon version")
	}

	// Without the flag nothing reaches GitHub, token or not.
	before := len(gh.requests)
	if _, _, err = run(t, &fakeClient{}, string(raw), "report", "submit", "-f", "-"); err != nil || len(gh.requests) != before {
		t.Fatalf("default submit filed something: err=%v requests=%v", err, gh.requests[before:])
	}

	// Opted in by flag but no token: the report is still stored, the command fails.
	t.Setenv(healthcmd.TokenEnv, "")
	client = &fakeClient{}
	_, errOut, err := run(t, client, string(raw), "report", "submit", "-f", "-", "--file-issues", "acme/ops")
	if err == nil || client.submitted.Summary == "" || !strings.Contains(errOut, "the report is stored") {
		t.Fatalf("missing token: err=%v stderr=%q stored=%q", err, errOut, client.submitted.Summary)
	}
}

func TestSubmitWithFileIssuesFilesNothingForAnInvalidReport(t *testing.T) {
	gh := &github{}
	server := httptest.NewServer(gh.handler(t))
	defer server.Close()
	t.Setenv(healthcmd.TokenEnv, "filing-token-0123456789")
	t.Setenv(healthcmd.APIEnv, server.URL)

	duplicate := sweepReport()
	duplicate.Findings[1].Signature = duplicate.Findings[0].Signature
	badSeverity := sweepReport()
	badSeverity.Findings[0].Severity = "fatal"
	noSummary := sweepReport()
	noSummary.Summary = " "
	storedID := sweepReport()
	storedID.ID = "taken"
	// No id, but kukeond derives it from createdAt: resubmitting the same
	// document lands on the stored id.
	derivedID := sweepReport()
	derivedID.CreatedAt = window.Add(3600e9)
	reservedID := sweepReport()
	reservedID.ID = kukeonv1.HealthReportLatest

	for name, document := range map[string]kukeonv1.HealthReport{
		"duplicate signature": duplicate, "bad severity": badSeverity, "empty summary": noSummary,
		"id already stored": storedID, "id derived from createdAt already stored": derivedID,
		"reserved id": reservedID,
	} {
		raw, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		client := &fakeClient{version: "v0.11.0", reports: []kukeonv1.HealthReport{
			{ID: health.ReportID(derivedID.CreatedAt), Summary: "x"}, {ID: "taken", Summary: "x"},
		}}
		_, errOut, err := run(t, client, string(raw), "report", "submit", "-f", "-", "--file-issues", "acme/ops")
		if err == nil || !strings.Contains(errOut, "nothing was filed") {
			t.Fatalf("%s: err=%v stderr=%q", name, err, errOut)
		}
		if len(gh.requests) != 0 || client.submitted.Summary != "" {
			t.Fatalf("%s: rejected report reached GitHub or kukeond: requests=%v submitted=%q",
				name, gh.requests, client.submitted.Summary)
		}
	}
}

func TestSubmitWithFileIssuesReportsFilingWhenTheSubmissionFails(t *testing.T) {
	gh := &github{}
	server := httptest.NewServer(gh.handler(t))
	defer server.Close()
	t.Setenv(healthcmd.TokenEnv, "filing-token-0123456789")
	t.Setenv(healthcmd.APIEnv, server.URL)

	raw, err := json.Marshal(sweepReport())
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeClient{version: "v0.11.0", err: errors.New("kukeond unavailable")}
	_, errOut, err := run(t, client, string(raw), "report", "submit", "-f", "-", "--file-issues", "acme/ops")
	if err == nil {
		t.Fatal("a failed submission must fail the command")
	}
	for _, want := range []string{
		"The report was not stored, but filing in acme/ops already ran: 1 issues opened, 0 commented",
		"https://github.com/acme/ops/issues/1", "kukeond unavailable",
	} {
		if !strings.Contains(errOut, want) {
			t.Fatalf("stderr lacks %q:\n%s", want, errOut)
		}
	}
}

func TestSubmitWithFileIssuesComparesWithTheReportKukeondWill(t *testing.T) {
	gh := &github{}
	server := httptest.NewServer(gh.handler(t))
	defer server.Close()
	t.Setenv(healthcmd.TokenEnv, "filing-token-0123456789")
	t.Setenv(healthcmd.APIEnv, server.URL)

	document := sweepReport()
	document.CreatedAt = window.Add(3600e9)
	kukeonSignature := document.Findings[0].Signature
	client := &fakeClient{version: "v0.11.0", reports: []kukeonv1.HealthReport{
		// Newer than the back-dated document: kukeond does not compare with it.
		{ID: "newer", CreatedAt: window.Add(7200e9), Findings: []kukeonv1.HealthFinding{{Signature: kukeonSignature}}},
		{ID: "older", CreatedAt: window, Findings: []kukeonv1.HealthFinding{}},
	}}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = run(t, client, string(raw), "report", "submit", "-f", "-", "--file-issues", "acme/ops"); err != nil {
		t.Fatal(err)
	}
	if body := gh.issues[0]["body"].(string); !strings.Contains(body, "first seen in this window") {
		t.Fatalf("issue body compares with a report newer than the document:\n%s", body)
	}
}

func TestTitleTruncatesOnARuneBoundary(t *testing.T) {
	for _, lead := range []string{"", "x"} { // either byte parity at the cut
		finding := kukeonv1.HealthFinding{Signature: "check host/cni", Title: lead + strings.Repeat("é", 200)}
		title := (&healthcmd.Filer{}).Title(finding)
		if !utf8.ValidString(title) || !strings.HasSuffix(title, "...") || len(title) > 120 {
			t.Fatalf("title %q (%d bytes)", title, len(title))
		}
	}
}
