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
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	healthcmd "github.com/eminwux/kukeon/cmd/kuke/health"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

func issueDraft(evidence ...string) healthcmd.IssueDraft {
	return healthcmd.IssueDraft{
		Signature: "cell default/default/default/api Failed", Classification: kukeonv1.HealthClassKukeon,
		Severity: kukeonv1.HealthSeverityError, Title: "api failed to start: CNI attach error",
		Evidence:     evidence,
		Reproduction: []string{"kuke start api --realm default --space default --stack default"},
	}
}

// An investigation-filed issue carries the same marker and title as a sweep's
// for the same signature, so the two paths dedupe against each other.
func TestInvestigationIssueMatchesTheSweepShape(t *testing.T) {
	gh := &github{}
	server := httptest.NewServer(gh.handler(t))
	defer server.Close()
	report := sweepReport()
	f := filer(server)
	if _, _, err := f.File(t.Context(), &report, nil); err != nil {
		t.Fatal(err)
	}
	sweepBody := gh.issues[0]["body"].(string)
	sweepTitle := gh.issues[0]["title"].(string)

	finding := report.Findings[0]
	draft := issueDraft("status.reason: CreateFailed")
	draft.Signature, draft.Title = finding.Signature, finding.Title
	rendered := f.Investigation(draft)
	if !strings.Contains(sweepBody, rendered.Marker) ||
		!strings.HasSuffix(strings.TrimSpace(rendered.Body), rendered.Marker) {
		t.Fatalf(
			"marker %q is not the sweep's:\nsweep:\n%s\ninvestigation:\n%s",
			rendered.Marker,
			sweepBody,
			rendered.Body,
		)
	}
	if rendered.Title != sweepTitle {
		t.Fatalf("title = %q, sweep filed %q", rendered.Title, sweepTitle)
	}
	for _, want := range []string{"## Symptom", "## Evidence", "## Reproduction", "kuke start api", "## kukeon version", "v0.11.0"} {
		if !strings.Contains(rendered.Body, want) {
			t.Errorf("body lacks %q:\n%s", want, rendered.Body)
		}
	}
}

func TestInvestigationIssueIsRedacted(t *testing.T) {
	f := &healthcmd.Filer{Token: "filing-token-0123456789", Version: "v0.11.0"}
	evidence := append([]string{"auth failed with filing-token-0123456789"}, tokenShaped...)
	draft := issueDraft(evidence...)
	draft.Reproduction = append(draft.Reproduction, "GITHUB_TOKEN="+tokenShaped[0]+" kuke start api")
	rendered := f.Investigation(draft)
	for _, text := range []string{rendered.Title, rendered.Body, rendered.Comment} {
		for _, secret := range append([]string{f.Token}, tokenShaped...) {
			if strings.Contains(text, secret) {
				t.Fatalf("%q survived redaction:\n%s", secret, text)
			}
		}
	}
	if !strings.Contains(rendered.Body, "[redacted]") || !strings.Contains(rendered.Comment, "[redacted]") {
		t.Fatalf("nothing was redacted:\n%s\n%s", rendered.Body, rendered.Comment)
	}
}

func TestDecodeIssueDraftRefusesNonKukeonDefects(t *testing.T) {
	for name, doc := range map[string]string{
		"workload":      "signature: s\nclassification: workload\nseverity: error\nevidence: [x]\n",
		"unclear":       "signature: s\nclassification: unclear\nseverity: error\nevidence: [x]\n",
		"no signature":  "classification: kukeon\nseverity: error\nevidence: [x]\n",
		"bad severity":  "signature: s\nclassification: kukeon\nseverity: fatal\nevidence: [x]\n",
		"no evidence":   "signature: s\nclassification: kukeon\nseverity: error\n",
		"unknown field": "signature: s\nclassification: kukeon\nseverity: error\nevidence: [x]\nrepo: o/r\n",
	} {
		if _, err := healthcmd.DecodeIssueDraft([]byte(doc)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestIssueCommandRendersJSON(t *testing.T) {
	t.Setenv(healthcmd.TokenEnv, "filing-token-0123456789")
	doc := "signature: check host/cni-plugins\nclassification: kukeon\nseverity: error\n" +
		"title: cni plugins missing\nevidence:\n  - \"missing: bridge filing-token-0123456789\"\n"
	out, errOut, err := run(t, &fakeClient{version: "v0.11.0"}, doc, "issue", "-f", "-")
	if err != nil {
		t.Fatalf("err=%v stderr=%q", err, errOut)
	}
	var rendered healthcmd.RenderedIssue
	if err = json.Unmarshal([]byte(out), &rendered); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if rendered.Title != "health: cni-plugins: cni plugins missing" ||
		!strings.Contains(
			rendered.Body,
			"v0.11.0",
		) || !strings.Contains(rendered.Body, "<!-- kukeon-health-signature:") {
		t.Fatalf("rendered = %+v", rendered)
	}
	if strings.Contains(out, "filing-token-0123456789") {
		t.Fatalf("GH_TOKEN value survived:\n%s", out)
	}
	if _, errOut, err = run(t, &fakeClient{}, strings.Replace(doc, "kukeon", "workload", 1), "issue", "-f", "-"); err == nil ||
		!strings.Contains(errOut, "only kukeon defects") {
		t.Fatalf("workload draft rendered: err=%v stderr=%q", err, errOut)
	}
}

// fullDraft is a draft carrying every optional section (#1604).
func fullDraft() healthcmd.IssueDraft {
	draft := issueDraft("status.reason: CreateFailed")
	draft.Expected = "`kuke start api` brings the cell to Ready."
	draft.Actual = "The cell stays Failed with a CNI attach error."
	draft.RootCause = "`internal/cni/attach.go:42` (commit `abc1234`) drops the bridge error."
	draft.SuggestedFix = "Return the bridge error from `attach`."
	draft.Acceptance = []string{"`kuke start api` reaches Ready", "the CNI error is reported"}
	return draft
}

func TestInvestigationRendersTheOptionalSectionsInOrder(t *testing.T) {
	f := &healthcmd.Filer{Version: "v0.11.0"}
	rendered := f.Investigation(fullDraft())
	order := []string{
		"## Symptom", "## Expected", "## Actual", "## Evidence", "## Reproduction",
		"## Root cause", "## Suggested fix", "## Acceptance criteria", "## kukeon version", "Signature: ",
		"Filed by kukeon majordomo",
	}
	last := -1
	for _, heading := range order {
		at := strings.Index(rendered.Body, heading)
		if at < 0 || at < last {
			t.Fatalf("%q missing or out of order:\n%s", heading, rendered.Body)
		}
		last = at
	}
	for _, want := range []string{
		"- [ ] `kuke start api` reaches Ready\n", "- [ ] the CNI error is reported\n",
		"\n`internal/cni/attach.go:42` (commit `abc1234`) drops the bridge error.\n",
	} {
		if !strings.Contains(rendered.Body, want) {
			t.Errorf("body lacks %q:\n%s", want, rendered.Body)
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(rendered.Body), rendered.Marker) {
		t.Fatalf("body does not end with the marker:\n%s", rendered.Body)
	}
}

func TestInvestigationWithoutOptionalFieldsOmitsTheirSections(t *testing.T) {
	rendered := (&healthcmd.Filer{Version: "v0.11.0"}).Investigation(issueDraft("x"))
	for _, heading := range []string{
		"## Expected", "## Actual", "## Root cause", "## Suggested fix", "## Acceptance criteria",
	} {
		if strings.Contains(rendered.Body, heading) {
			t.Errorf("body has %q without the field:\n%s", heading, rendered.Body)
		}
	}
}

func TestInvestigationRendersReproductionAsOneBlock(t *testing.T) {
	draft := issueDraft("x")
	draft.Reproduction = []string{"kuke create cell api", "kuke start api", "kuke get cell api"}
	body := (&healthcmd.Filer{Version: "v0.11.0"}).Investigation(draft).Body
	start := strings.Index(body, "## Reproduction")
	end := strings.Index(body, "## kukeon version")
	if start < 0 || end < start {
		t.Fatalf("no Reproduction section:\n%s", body)
	}
	section := body[start:end]
	want := "```\nkuke create cell api\nkuke start api\nkuke get cell api\n```\n"
	if strings.Count(section, "```") != 2 || !strings.Contains(section, want) {
		t.Fatalf("Reproduction is not one fenced block of all three lines:\n%s", section)
	}
}

func TestInvestigationRedactsTheProseFields(t *testing.T) {
	draft := fullDraft()
	draft.RootCause = "the clone used " + tokenShaped[0]
	draft.SuggestedFix = "rotate " + tokenShaped[1] + " and retry"
	body := (&healthcmd.Filer{Version: "v0.11.0"}).Investigation(draft).Body
	for _, secret := range tokenShaped[:2] {
		if strings.Contains(body, secret) {
			t.Fatalf("%q survived redaction:\n%s", secret, body)
		}
	}
}

func TestDecodeIssueDraftAcceptsTheOptionalFields(t *testing.T) {
	doc := "signature: s\nclassification: kukeon\nseverity: error\nevidence: [x]\n" +
		"expected: e\nactual: a\nrootCause: r\nsuggestedFix: f\nacceptance: [one, two]\n"
	draft, err := healthcmd.DecodeIssueDraft([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if draft.Expected != "e" || draft.Actual != "a" || draft.RootCause != "r" || draft.SuggestedFix != "f" ||
		len(draft.Acceptance) != 2 {
		t.Fatalf("draft = %+v", draft)
	}
	if _, err = healthcmd.DecodeIssueDraft([]byte(doc + "repo: o/r\n")); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestIssueCommentPrintsRedactedMarkdown(t *testing.T) {
	t.Setenv(healthcmd.TokenEnv, "filing-token-0123456789")
	notes := filepath.Join(t.TempDir(), "notes.md")
	text := "## Analysis\n\nCloned from https://bot:s3cr3t@github.com/o/r.git with token=abc123def.\n" +
		"The filing token filing-token-0123456789 leaked too.\n"
	if err := os.WriteFile(notes, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errOut, err := run(t, &fakeClient{}, "", "issue", "--comment", "-f", notes)
	if err != nil {
		t.Fatalf("err=%v stderr=%q", err, errOut)
	}
	for _, secret := range []string{"s3cr3t", "abc123def", "filing-token-0123456789"} {
		if strings.Contains(out, secret) {
			t.Fatalf("%q survived redaction:\n%s", secret, out)
		}
	}
	if !strings.HasPrefix(out, "## Analysis\n") || json.Valid([]byte(out)) {
		t.Fatalf("output is not the plain markdown:\n%s", out)
	}
}

func TestIssueCommentReadsStdin(t *testing.T) {
	out, errOut, err := run(t, &fakeClient{}, "see token=abc123def\n", "issue", "--comment", "-f", "-")
	if err != nil {
		t.Fatalf("err=%v stderr=%q", err, errOut)
	}
	if !strings.HasPrefix(out, "see token=") || strings.Contains(out, "abc123def") {
		t.Fatalf("out = %q", out)
	}
}

func TestIssueCommentRefusesEmptyInput(t *testing.T) {
	_, errOut, err := run(t, &fakeClient{}, " \n\t\n", "issue", "--comment", "-f", "-")
	if err == nil || !strings.Contains(errOut, "comment text is empty") {
		t.Fatalf("err=%v stderr=%q", err, errOut)
	}
}
