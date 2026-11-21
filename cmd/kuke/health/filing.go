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
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

const (
	// TokenEnv holds the GitHub token filing uses. The github connector sets
	// it, and only on an install that opted in by binding OptInSecret.
	TokenEnv = "GH_TOKEN"
	// OptInSecret is the team-entry secret that enables the github connector
	// for majordomo: one binding serves the interactive cell and the sweep.
	OptInSecret = "gh-token"
	// APIEnv overrides the GitHub API base URL (GitHub Enterprise, tests).
	APIEnv = "KUKE_HEALTH_GITHUB_API"

	defaultGitHubAPI = "https://api.github.com"
	issuePageSize    = 100
	maxIssuePages    = 10
	maxTitle         = 120
	maxEvidence      = 4000
	filingTimeout    = 30 * time.Second
)

var repoName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// Filer opens and updates GitHub issues for the kukeon findings of a report.
// It is deterministic code run by `kuke health report submit --file-issues`:
// the sweep decides what is a kukeon defect, this decides nothing.
type Filer struct {
	Repo    string
	Token   string
	API     string
	Version string
	HTTP    *http.Client
}

type ghIssue struct {
	Number      int       `json:"number"`
	HTMLURL     string    `json:"html_url"`
	Body        string    `json:"body"`
	State       string    `json:"state"`
	PullRequest *struct{} `json:"pull_request,omitempty"`
}

// marker is the hidden line that ties an issue to a finding's signature.
func marker(signature string) string {
	sum := sha256.Sum256([]byte(signature))
	return "<!-- kukeon-health-signature: " + hex.EncodeToString(sum[:8]) + " -->"
}

// area names the part of kukeon a finding is about, from the signature forms
// the sweep uses: "cell <scope> <State>", "check <section>/<name>", else a
// kukeond log signature.
func area(signature string) string {
	switch {
	case strings.HasPrefix(signature, "cell "):
		return "cell"
	case strings.HasPrefix(signature, "check "):
		name := strings.TrimPrefix(signature, "check ")
		if i := strings.LastIndexByte(name, '/'); i >= 0 {
			name = name[i+1:]
		}
		return name
	default:
		return "kukeond"
	}
}

// Title renders `health: <area>: <symptom>`.
func (f *Filer) Title(finding kukeonv1.HealthFinding) string {
	symptom := finding.Title
	if strings.TrimSpace(symptom) == "" {
		symptom = finding.Signature
	}
	symptom = strings.Join(strings.Fields(Sanitize(symptom, f.Token)), " ")
	title := "health: " + area(finding.Signature) + ": " + symptom
	if len(title) > maxTitle {
		const ellipsis = "..."
		cut := maxTitle - len(ellipsis)
		for cut > 0 && !utf8.RuneStart(title[cut]) {
			cut-- // never split a multi-byte rune
		}
		title = title[:cut] + ellipsis
	}
	return title
}

func frequency(finding kukeonv1.HealthFinding, report kukeonv1.HealthReport, recurring bool, seenBefore string) string {
	window := fmt.Sprintf("%s to %s", stamp(report.Since), stamp(report.Until))
	seen := seenBefore
	if recurring {
		seen = "recurring: the previous health report also carried it"
	}
	if finding.Count > 0 {
		return fmt.Sprintf("%d occurrences in %s; %s", finding.Count, window, seen)
	}
	return fmt.Sprintf("present in %s; %s", window, seen)
}

// errNoToken is the error for filing without a token. It names the variable
// and the opt-in that sets it.
func errNoToken(what string) error {
	return fmt.Errorf(
		"%s needs a GitHub token in %s; this install has not opted in to filing. To opt in, create the %s secret "+
			"and add it to the majordomo role's needs.secrets in the team entry, then run kuke team init --reapply",
		what, TokenEnv, OptInSecret)
}

func (f *Filer) evidence(entries []string) string {
	var b strings.Builder
	for _, entry := range entries {
		clean := strings.TrimRight(Sanitize(entry, f.Token), "\n")
		if b.Len()+len(clean) > maxEvidence {
			b.WriteString("[further evidence omitted; see the stored health report]\n")
			break
		}
		// A fence inside evidence would end the block early.
		b.WriteString("```\n" + strings.ReplaceAll(clean, "```", "'''") + "\n```\n")
	}
	return b.String()
}

// Body renders a new issue: symptom, sanitized evidence, frequency and the
// kukeon version, followed by the signature marker.
func (f *Filer) Body(finding kukeonv1.HealthFinding, report kukeonv1.HealthReport, recurring bool) string {
	symptom := finding.Title
	if strings.TrimSpace(symptom) == "" {
		symptom = finding.Signature
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## Symptom\n\n%s\n\nSeverity: %s\n\n", Sanitize(symptom, f.Token), finding.Severity)
	fmt.Fprintf(&b, "## Evidence\n\n%s\n", f.evidence(finding.Evidence))
	fmt.Fprintf(&b, "## Frequency\n\n%s\n\n", frequency(finding, report, recurring, "first seen in this window"))
	fmt.Fprintf(&b, "## kukeon version\n\n%s\n\n", f.Version)
	fmt.Fprintf(&b, "Signature: `%s`\n\n", Sanitize(finding.Signature, f.Token))
	b.WriteString("Filed by a kukeon majordomo health sweep.\n\n" + marker(finding.Signature) + "\n")
	return b.String()
}

// comment renders the note added to an open issue that already tracks
// finding. recurring is whether the previous report carried it too; when not,
// the finding is back after at least one report without it.
func (f *Filer) comment(finding kukeonv1.HealthFinding, report kukeonv1.HealthReport, recurring bool) string {
	return fmt.Sprintf("Seen again by a health sweep.\n\n%s\n\nkukeon version: %s\n\n%s",
		frequency(finding, report, recurring, "back again: the previous health report did not carry it"),
		f.Version, f.evidence(finding.Evidence))
}

func (f *Filer) do(ctx context.Context, method, path string, payload, into any) error {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	ctx, cancel := context.WithTimeout(ctx, filingTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(f.API, "/")+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+f.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "kuke-health")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := f.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		// A transport error can quote the request; keep the token out of it.
		return errors.New(Sanitize(err.Error(), f.Token))
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var failure struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &failure)
		return fmt.Errorf("GitHub %s %s: %s %s", method, path, resp.Status, Sanitize(failure.Message, f.Token))
	}
	if into == nil {
		return nil
	}
	return json.Unmarshal(raw, into)
}

// openIssues lists the repository's open issues, up to maxIssuePages pages.
// Listing, not search: a just-filed issue is visible at once, so an
// overlapping sweep comments on it instead of filing a duplicate.
func (f *Filer) openIssues(ctx context.Context) ([]ghIssue, error) {
	var all []ghIssue
	for page := 1; page <= maxIssuePages; page++ {
		var batch []ghIssue
		path := fmt.Sprintf("/repos/%s/issues?state=open&per_page=%d&page=%d", f.Repo, issuePageSize, page)
		if err := f.do(ctx, http.MethodGet, path, nil, &batch); err != nil {
			return nil, err
		}
		for _, issue := range batch {
			if issue.PullRequest == nil {
				all = append(all, issue)
			}
		}
		if len(batch) < issuePageSize {
			break
		}
	}
	return all, nil
}

// File handles every kukeon finding of report: it comments on the open issue
// carrying the finding's signature, or opens one when there is none, and
// records the issue URL on the finding. workload and unclear findings are
// never filed. previous is the last stored report, or nil; it decides whether
// a finding is recurring. It returns how many issues were opened and how many
// commented on; findings handled before an error keep their URLs.
func (f *Filer) File(
	ctx context.Context, report *kukeonv1.HealthReport, previous *kukeonv1.HealthReport,
) (opened, commented int, err error) {
	if !repoName.MatchString(f.Repo) {
		return 0, 0, fmt.Errorf("--file-issues %q: expected <owner>/<repo>", f.Repo)
	}
	if f.Token == "" {
		return 0, 0, errNoToken("--file-issues")
	}
	if f.API == "" {
		f.API = defaultGitHubAPI
	}
	recurring := map[string]bool{}
	if previous != nil {
		for _, finding := range previous.Findings {
			recurring[finding.Signature] = true
		}
	}
	var open []ghIssue
	listed := false
	for i := range report.Findings {
		finding := &report.Findings[i]
		if finding.Classification != kukeonv1.HealthClassKukeon {
			finding.IssueURL = "" // never carried for workload or unclear
			continue
		}
		if !listed {
			if open, err = f.openIssues(ctx); err != nil {
				return opened, commented, err
			}
			listed = true
		}
		mark := marker(finding.Signature)
		existing := -1
		for j := range open {
			if strings.Contains(open[j].Body, mark) {
				existing = j
				break
			}
		}
		if existing >= 0 {
			path := fmt.Sprintf("/repos/%s/issues/%d/comments", f.Repo, open[existing].Number)
			payload := map[string]string{"body": f.comment(*finding, *report, recurring[finding.Signature])}
			if err = f.do(ctx, http.MethodPost, path, payload, nil); err != nil {
				return opened, commented, err
			}
			finding.IssueURL = open[existing].HTMLURL
			commented++
			continue
		}
		var created ghIssue
		payload := map[string]string{
			"title": f.Title(*finding), "body": f.Body(*finding, *report, recurring[finding.Signature]),
		}
		if err = f.do(ctx, http.MethodPost, "/repos/"+f.Repo+"/issues", payload, &created); err != nil {
			return opened, commented, err
		}
		finding.IssueURL = created.HTMLURL
		created.Body = payload["body"]
		// A later finding of this report with the same signature cannot
		// exist (signatures are unique), but keep the list current anyway.
		open = append(open, created)
		opened++
	}
	return opened, commented, nil
}
