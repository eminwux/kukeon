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

	finding := report.Spec.Findings[0]
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
