// Copyright 2025 Emiliano Spinella (eminwux)
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

package v1beta1

import "time"

// HealthReportDoc is one triaged health sweep (kind: HealthReport, issue
// #1619). kukeond stores the newest reports as operational state; storing one
// files nothing anywhere. A report is immutable once stored.
type HealthReportDoc struct {
	APIVersion Version              `json:"apiVersion" yaml:"apiVersion"`
	Kind       Kind                 `json:"kind"       yaml:"kind"`
	Metadata   HealthReportMetadata `json:"metadata"   yaml:"metadata"`
	Spec       HealthReportSpec     `json:"spec"       yaml:"spec"`
}

// HealthReportMetadata identifies a report. Realm is always kuke-system; an
// omitted realm defaults to it.
type HealthReportMetadata struct {
	// Name identifies the report. kukeond assigns one, derived from
	// spec.createdAt, when a submission leaves it empty.
	Name  string `json:"name"            yaml:"name"`
	Realm string `json:"realm,omitempty" yaml:"realm,omitempty"`
}

// HealthReportSpec is the content of a report.
type HealthReportSpec struct {
	// CreatedAt is set by kukeond when a submission leaves it zero.
	CreatedAt time.Time `json:"createdAt"      yaml:"createdAt"`
	// Since and Until are the digest window the report covers.
	Since time.Time `json:"since"          yaml:"since"`
	Until time.Time `json:"until"          yaml:"until"`
	// Cell is the cell that ran the sweep.
	Cell     HealthCellRef   `json:"cell"           yaml:"cell"`
	Summary  string          `json:"summary"        yaml:"summary"`
	Findings []HealthFinding `json:"findings"       yaml:"findings"`
	// Body is the report as markdown, for people.
	Body string `json:"body,omitempty" yaml:"body,omitempty"`
}

// HealthCellRef names the cell that submitted a report.
type HealthCellRef struct {
	Realm string `json:"realm" yaml:"realm"`
	Space string `json:"space" yaml:"space"`
	Stack string `json:"stack" yaml:"stack"`
	Name  string `json:"name"  yaml:"name"`
}

// HealthFinding is one triaged finding of a report.
type HealthFinding struct {
	// Signature identifies the finding across reports: the digest's log
	// signature, check name or cell identity it came from.
	Signature      string `json:"signature"                   yaml:"signature"`
	Classification string `json:"classification"              yaml:"classification"`
	Severity       string `json:"severity"                    yaml:"severity"`
	// Recurring is set by kukeond on submission: true when the previous
	// stored report already carried a finding with the same signature.
	Recurring bool   `json:"recurring"                   yaml:"recurring"`
	Title     string `json:"title,omitempty"             yaml:"title,omitempty"`
	// Count is how many times the finding occurred in the window, when the
	// digest counted it (a log group's count). Zero means not counted.
	Count int `json:"count,omitempty"             yaml:"count,omitempty"`
	// Evidence is what supports the finding: command output, log samples.
	Evidence []string `json:"evidence"                    yaml:"evidence"`
	// SuggestedCommands are commands the reader may run; nothing runs them.
	SuggestedCommands []string `json:"suggestedCommands,omitempty" yaml:"suggestedCommands,omitempty"`
	// IssueURL is the issue filed for the finding, when filing is enabled.
	IssueURL string `json:"issueUrl,omitempty"          yaml:"issueUrl,omitempty"`
}
