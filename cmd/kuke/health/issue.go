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
	"io"
	"os"
	"strings"

	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// IssueDraft is a kukeon defect found by an interactive investigation rather
// than a sweep. `kuke health issue` renders it in the sweep's issue shape, so
// both paths redact the same way and dedupe on the same signature marker.
type IssueDraft struct {
	// Signature is the sweep's signature for the defect when one applies
	// ("cell <realm/space/stack/name> <State>", "check <section>/<name>", a
	// kukeond log signature), so the marker matches a sweep-filed issue.
	Signature      string `json:"signature"              yaml:"signature"`
	Classification string `json:"classification"         yaml:"classification"`
	Severity       string `json:"severity"               yaml:"severity"`
	// Title is the symptom; the signature stands in when it is empty.
	Title    string   `json:"title,omitempty"        yaml:"title,omitempty"`
	Evidence []string `json:"evidence"               yaml:"evidence"`
	// Reproduction lists the steps or commands that reproduce the defect.
	Reproduction []string `json:"reproduction,omitempty" yaml:"reproduction,omitempty"`
	// Expected and Actual contrast the behavior the operator expected with
	// what kukeon did.
	Expected string `json:"expected,omitempty"     yaml:"expected,omitempty"`
	Actual   string `json:"actual,omitempty"       yaml:"actual,omitempty"`
	// RootCause names the code that emits the error (file:line and commit,
	// from the source checkout when one is available).
	RootCause    string `json:"rootCause,omitempty"    yaml:"rootCause,omitempty"`
	SuggestedFix string `json:"suggestedFix,omitempty" yaml:"suggestedFix,omitempty"`
	// Acceptance lists the criteria a fix must meet, one checkbox each.
	Acceptance []string `json:"acceptance,omitempty"   yaml:"acceptance,omitempty"`
}

// RenderedIssue is what `kuke health issue` prints. Everything in it is
// already redacted; the caller files it unchanged.
type RenderedIssue struct {
	Title string `json:"title"`
	// Body is a new issue's body, ending with the marker.
	Body string `json:"body"`
	// Comment is the note for an open issue that already carries Marker.
	Comment string `json:"comment"`
	// Marker is the hidden line an existing issue for the same signature
	// carries, whichever path filed it.
	Marker string `json:"marker"`
}

// DecodeIssueDraft reads an issue draft document (JSON or YAML) and checks it
// describes a kukeon defect: workload and unclear problems are never filed.
func DecodeIssueDraft(raw []byte) (IssueDraft, error) {
	var draft IssueDraft
	decoder := yaml.NewDecoder(strings.NewReader(string(raw)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&draft); err != nil {
		return IssueDraft{}, fmt.Errorf("decode issue draft: %w", err)
	}
	if strings.TrimSpace(draft.Signature) == "" {
		return IssueDraft{}, errors.New("issue draft: signature is required")
	}
	if draft.Classification != kukeonv1.HealthClassKukeon {
		return IssueDraft{}, fmt.Errorf(
			"issue draft: classification %q is not filed; only %s defects are",
			draft.Classification, kukeonv1.HealthClassKukeon)
	}
	switch draft.Severity {
	case kukeonv1.HealthSeverityInfo, kukeonv1.HealthSeverityWarning,
		kukeonv1.HealthSeverityError, kukeonv1.HealthSeverityCritical:
	default:
		return IssueDraft{}, fmt.Errorf(
			"issue draft: severity %q: expected info, warning, error or critical",
			draft.Severity,
		)
	}
	if len(draft.Evidence) == 0 {
		return IssueDraft{}, errors.New("issue draft: evidence is required")
	}
	return draft, nil
}

// Investigation renders draft as an issue: the sweep's title, symptom,
// evidence, kukeon version and signature marker, with the reproduction steps
// in place of the sweep's frequency. The optional expected/actual, root cause,
// suggested fix and acceptance sections render only when the draft sets them,
// so a draft without them renders as the sweep shape.
func (f *Filer) Investigation(draft IssueDraft) RenderedIssue {
	finding := kukeonv1.HealthFinding{
		Signature: draft.Signature, Classification: draft.Classification,
		Severity: draft.Severity, Title: draft.Title, Evidence: draft.Evidence,
	}
	symptom := draft.Title
	if strings.TrimSpace(symptom) == "" {
		symptom = draft.Signature
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## Symptom\n\n%s\n\nSeverity: %s\n\n", Sanitize(symptom, f.Token), draft.Severity)
	f.prose(&b, "Expected", draft.Expected)
	f.prose(&b, "Actual", draft.Actual)
	fmt.Fprintf(&b, "## Evidence\n\n%s\n", f.evidence(draft.Evidence))
	if len(draft.Reproduction) > 0 {
		fmt.Fprintf(&b, "## Reproduction\n\n%s\n", f.block(draft.Reproduction))
	}
	f.prose(&b, "Root cause", draft.RootCause)
	f.prose(&b, "Suggested fix", draft.SuggestedFix)
	if len(draft.Acceptance) > 0 {
		b.WriteString("## Acceptance criteria\n\n")
		for _, item := range draft.Acceptance {
			fmt.Fprintf(&b, "- [ ] %s\n", strings.TrimSpace(Sanitize(item, f.Token)))
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "## kukeon version\n\n%s\n\n", f.Version)
	fmt.Fprintf(&b, "Signature: `%s`\n\n", Sanitize(draft.Signature, f.Token))
	b.WriteString("Filed by kukeon majordomo during an investigation.\n\n" + marker(draft.Signature) + "\n")
	return RenderedIssue{
		Title: f.Title(finding),
		Body:  b.String(),
		Comment: fmt.Sprintf("Seen again during a majordomo investigation.\n\nkukeon version: %s\n\n%s",
			f.Version, f.evidence(draft.Evidence)),
		Marker: marker(draft.Signature),
	}
}

// prose writes a sanitized markdown section for text, or nothing when text
// is empty. It is markdown, not a fence, so file:line references and inline
// code read normally.
func (f *Filer) prose(b *strings.Builder, heading, text string) {
	clean := strings.TrimSpace(Sanitize(text, f.Token))
	if clean == "" {
		return
	}
	fmt.Fprintf(b, "## %s\n\n%s\n\n", heading, clean)
}

// block renders lines as one fenced block.
func (f *Filer) block(lines []string) string {
	clean := strings.TrimRight(Sanitize(strings.Join(lines, "\n"), f.Token), "\n")
	if len(clean) > maxEvidence {
		clean = clean[:maxEvidence] + "\n[truncated]"
	}
	return "```\n" + strings.ReplaceAll(clean, "```", "'''") + "\n```\n"
}

func newIssueCmd() *cobra.Command {
	var (
		file    string
		comment bool
	)
	cmd := &cobra.Command{
		Use:   "issue [--comment] -f <file>",
		Short: "Render a diagnosed kukeon defect as a redacted GitHub issue",
		Long: "Render an issue draft (JSON or YAML; - reads stdin) as the title, body, comment and\n" +
			"signature marker a GitHub issue for it carries, redacted by the same sanitizer and in\n" +
			"the same shape as health sweep filing, so an issue filed from it and a sweep-filed\n" +
			"issue for the same signature dedupe against each other. It prints JSON and contacts\n" +
			"nothing but kukeond, for the kukeon version; the caller does the filing.\n\n" +
			"Only kukeon defects render: a draft classified workload or unclear is refused.\n" +
			"The value of " + TokenEnv + ", when set, is redacted as well.\n\n" +
			"With --comment, the file is a hand-written markdown follow-up instead of a draft:\n" +
			"it is printed redacted, as plain markdown, ready for gh issue comment --body-file.\n" +
			"No draft is decoded and nothing is contacted.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if file == "" {
				return report(cmd, errors.New("-f <file> is required"))
			}
			var raw []byte
			var err error
			if file == "-" {
				raw, err = io.ReadAll(cmd.InOrStdin())
			} else {
				raw, err = os.ReadFile(file)
			}
			if comment {
				if err != nil {
					return report(cmd, fmt.Errorf("read comment: %w", err))
				}
				if strings.TrimSpace(string(raw)) == "" {
					return report(cmd, errors.New("comment text is empty"))
				}
				_, err = io.WriteString(cmd.OutOrStdout(), Sanitize(string(raw), os.Getenv(TokenEnv)))
				return err
			}
			if err != nil {
				return report(cmd, fmt.Errorf("read issue draft: %w", err))
			}
			draft, err := DecodeIssueDraft(raw)
			if err != nil {
				return report(cmd, err)
			}
			filer := &Filer{Token: os.Getenv(TokenEnv), Version: "unknown"}
			// The version is context for the reader; rendering does not
			// depend on reaching kukeond.
			if client, clientErr := resolveClient(cmd); clientErr == nil {
				if version, pingErr := client.PingVersion(cmd.Context()); pingErr == nil && version != "" {
					filer.Version = version
				}
				_ = client.Close()
			}
			encoder := json.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent("", "  ")
			encoder.SetEscapeHTML(false)
			return encoder.Encode(filer.Investigation(draft))
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "",
		"Issue draft document (JSON or YAML), or with --comment a markdown file; - reads stdin")
	cmd.Flags().BoolVar(&comment, "comment", false,
		"Print the file as a redacted markdown follow-up comment instead of rendering a draft")
	return cmd
}
