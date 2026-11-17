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

// Package health builds kukeond's health digest and stores health reports
// (issue #1509). Everything here is plain code: no model is involved, and the
// package neither files nor remediates anything.
package health

import (
	"regexp"
	"strings"
)

// maskRules run in order: the more specific shapes first, so a UUID is not
// first broken up by the hex or number rules.
var maskRules = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:?\d{2})?`), "<time>"},
	{regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`), "<id>"},
	{regexp.MustCompile(`(?i)\b(sha256:)?[0-9a-f]{12,}\b`), "<id>"},
	{regexp.MustCompile(`(?i)\b0x[0-9a-f]+\b`), "<n>"},
	// A token mixing letters and digits that is at least 8 long and has a
	// digit run reads as a generated identifier (k-1a2b3c4d, cell-17000001).
	{regexp.MustCompile(`(?i)\b[a-z0-9]*\d[a-z0-9]*[a-f][a-z0-9]*\d[a-z0-9]{4,}\b`), "<id>"},
	{regexp.MustCompile(`\d+(\.\d+)?(ns|µs|us|ms|s|m|h)\b`), "<n>"},
	{regexp.MustCompile(`\d+`), "<n>"},
}

var spaceRun = regexp.MustCompile(`\s+`)

// Normalize masks the parts of a log message that vary between occurrences
// of one failure: timestamps, identifiers and numbers. Two entries with the
// same result are the same finding.
func Normalize(text string) string {
	out := text
	for _, rule := range maskRules {
		out = rule.re.ReplaceAllString(out, rule.repl)
	}
	return strings.TrimSpace(spaceRun.ReplaceAllString(out, " "))
}

// Signature is the grouping key of one log entry: its message and, when
// present, its error text, normalized.
func Signature(msg, errText string) string {
	if errText == "" {
		return Normalize(msg)
	}
	return Normalize(msg) + ": " + Normalize(errText)
}
