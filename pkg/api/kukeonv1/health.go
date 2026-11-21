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

package kukeonv1

import "time"

// HealthSchemaVersion names the JSON shape of HealthDigest and HealthReport
// (issue #1509). It changes only when a field is removed or changes meaning;
// added fields keep the version.
const HealthSchemaVersion = "kukeon.io/health/v1"

// Health check statuses.
const (
	HealthStatusOK   = "ok"
	HealthStatusWarn = "warn"
	HealthStatusFail = "fail"
)

// Finding classifications. A digest only ever tags a cell kukeon or workload;
// unclear is a triage verdict that appears in reports.
const (
	HealthClassKukeon   = "kukeon"
	HealthClassWorkload = "workload"
	HealthClassUnclear  = "unclear"
)

// Finding severities, lowest to highest.
const (
	HealthSeverityInfo     = "info"
	HealthSeverityWarning  = "warning"
	HealthSeverityError    = "error"
	HealthSeverityCritical = "critical"
)

// DefaultHealthWindow is how far back a digest looks when the request names
// no start and no earlier report exists.
const DefaultHealthWindow = time.Hour

// HealthDigestRequest selects a digest's window. The window always ends when
// kukeond evaluates the request.
type HealthDigestRequest struct {
	// Since starts the window. Zero means DefaultHealthWindow before now,
	// unless SinceLastReport is set.
	Since time.Time `json:"since,omitempty"`
	// SinceLastReport starts the window at the newest stored report's Until,
	// falling back to DefaultHealthWindow before now when none is stored.
	SinceLastReport bool `json:"sinceLastReport,omitempty"`
}

// HealthDigest is kukeond's own, deterministic view of its health: no model
// is involved in producing it. Checks describe the host kukeond runs on, so
// the answer is the same whether the caller is on the host or inside a cell.
type HealthDigest struct {
	SchemaVersion string    `json:"schemaVersion"       yaml:"schemaVersion"`
	Since         time.Time `json:"since"               yaml:"since"`
	Until         time.Time `json:"until"               yaml:"until"`
	// Clean is true when no check is warn or fail, no error-level log entry
	// fell in the window, and no cell is Failed or Error.
	Clean  bool          `json:"clean"               yaml:"clean"`
	Checks []HealthCheck `json:"checks"              yaml:"checks"`
	// LogErrors groups kukeond's error-level log entries in the window by
	// normalized signature. LogSource is the file they were read from; it is
	// empty, with LogNote explaining why, when kukeond has no log file.
	LogErrors []HealthLogGroup `json:"logErrors"           yaml:"logErrors"`
	LogSource string           `json:"logSource,omitempty" yaml:"logSource,omitempty"`
	LogNote   string           `json:"logNote,omitempty"   yaml:"logNote,omitempty"`
	// Cells lists every cell currently Failed or Error, whatever the window.
	Cells []HealthCell `json:"cells"               yaml:"cells"`
}

// HealthCheck is one host or state check evaluated inside kukeond.
type HealthCheck struct {
	Section     string `json:"section"               yaml:"section"`
	Name        string `json:"name"                  yaml:"name"`
	Status      string `json:"status"                yaml:"status"`
	Detail      string `json:"detail,omitempty"      yaml:"detail,omitempty"`
	Remediation string `json:"remediation,omitempty" yaml:"remediation,omitempty"`
}

// HealthLogGroup is one group of error-level kukeond log entries that share a
// signature: the message and error text with timestamps, IDs and numbers
// masked.
type HealthLogGroup struct {
	Signature string    `json:"signature" yaml:"signature"`
	Count     int       `json:"count"     yaml:"count"`
	FirstSeen time.Time `json:"firstSeen" yaml:"firstSeen"`
	LastSeen  time.Time `json:"lastSeen"  yaml:"lastSeen"`
	// Sample is the newest entry of the group, verbatim.
	Sample string `json:"sample"    yaml:"sample"`
}

// HealthCell is a cell in a terminal failure state. Failed is kukeon's own
// bring-up failure and is classified kukeon; Error is a workload that exited
// non-zero and is classified workload.
type HealthCell struct {
	Realm          string `json:"realm"             yaml:"realm"`
	Space          string `json:"space"             yaml:"space"`
	Stack          string `json:"stack"             yaml:"stack"`
	Name           string `json:"name"              yaml:"name"`
	State          string `json:"state"             yaml:"state"`
	Classification string `json:"classification"    yaml:"classification"`
	// Since is when the cell entered the state, as closely as kukeond
	// records it: the newest container finish time for an Error cell, else
	// the cell's last status write.
	Since   time.Time `json:"since"             yaml:"since"`
	Reason  string    `json:"reason,omitempty"  yaml:"reason,omitempty"`
	Message string    `json:"message,omitempty" yaml:"message,omitempty"`
}

// HealthCellRef names the cell that submitted a report.
type HealthCellRef struct {
	Realm string `json:"realm" yaml:"realm"`
	Space string `json:"space" yaml:"space"`
	Stack string `json:"stack" yaml:"stack"`
	Name  string `json:"name"  yaml:"name"`
}

// HealthReport is one triaged health sweep. kukeond stores the newest reports
// as operational state; storing one files nothing anywhere.
type HealthReport struct {
	SchemaVersion string `json:"schemaVersion,omitempty" yaml:"schemaVersion,omitempty"`
	// ID identifies the report. kukeond assigns one when a submission leaves
	// it empty.
	ID string `json:"id"                      yaml:"id"`
	// CreatedAt is set by kukeond when a submission leaves it zero.
	CreatedAt time.Time `json:"createdAt"               yaml:"createdAt"`
	// Since and Until are the digest window the report covers.
	Since time.Time `json:"since"                   yaml:"since"`
	Until time.Time `json:"until"                   yaml:"until"`
	// Cell is the cell that ran the sweep.
	Cell     HealthCellRef   `json:"cell"                    yaml:"cell"`
	Summary  string          `json:"summary"                 yaml:"summary"`
	Findings []HealthFinding `json:"findings"                yaml:"findings"`
	// Body is the report as markdown, for people.
	Body string `json:"body,omitempty"          yaml:"body,omitempty"`
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

// HealthReportSummary is one row of ListHealthReports.
type HealthReportSummary struct {
	ID        string    `json:"id"        yaml:"id"`
	CreatedAt time.Time `json:"createdAt" yaml:"createdAt"`
	Since     time.Time `json:"since"     yaml:"since"`
	Until     time.Time `json:"until"     yaml:"until"`
	Summary   string    `json:"summary"   yaml:"summary"`
	Findings  int       `json:"findings"  yaml:"findings"`
	Recurring int       `json:"recurring" yaml:"recurring"`
	Filed     int       `json:"filed"     yaml:"filed"`
}

// HealthReportLatest is the id ReadHealthReport accepts for the newest report.
const HealthReportLatest = "latest"

// ReadHealthDigestArgs is the wire request for ReadHealthDigest.
type ReadHealthDigestArgs struct {
	Request HealthDigestRequest
}

// ReadHealthDigestReply is the wire reply for ReadHealthDigest.
type ReadHealthDigestReply struct {
	Result HealthDigest
	Err    *APIError
}

// SubmitHealthReportArgs is the wire request for SubmitHealthReport.
type SubmitHealthReportArgs struct {
	Report HealthReport
}

// SubmitHealthReportReply is the wire reply for SubmitHealthReport. Result is
// the report as stored, with the daemon-assigned fields filled in.
type SubmitHealthReportReply struct {
	Result HealthReport
	Err    *APIError
}

// ListHealthReportsArgs is the wire request for ListHealthReports.
type ListHealthReportsArgs struct{}

// ListHealthReportsReply is the wire reply for ListHealthReports, newest first.
type ListHealthReportsReply struct {
	Result []HealthReportSummary
	Err    *APIError
}

// ReadHealthReportArgs is the wire request for ReadHealthReport.
type ReadHealthReportArgs struct {
	ID string
}

// ReadHealthReportReply is the wire reply for ReadHealthReport.
type ReadHealthReportReply struct {
	Result HealthReport
	Err    *APIError
}
