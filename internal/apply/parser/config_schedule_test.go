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

// Apply-time validation of a CellConfig's spec.schedule (issue #1503).

package parser_test

import (
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/apply/parser"
	"github.com/eminwux/kukeon/internal/errdefs"
)

// scheduledConfigYAML is validConfig with extra spec lines appended.
func scheduledConfigYAML(specLines ...string) string {
	return validConfig + strings.Join(specLines, "\n") + "\n"
}

func TestValidateDocument_ConfigSchedule_Valid(t *testing.T) {
	for name, lines := range map[string][]string{
		"five fields": {"  schedule:", `    cron: "*/10 * * * *"`},
		"macro":       {"  schedule:", `    cron: "@hourly"`},
		"every field": {
			"  schedule:",
			`    cron: "30 6 * * mon-fri"`,
			"    timezone: Europe/Madrid",
			"    concurrencyPolicy: Forbid",
			"    startingDeadlineSeconds: 300",
			"    suspend: true",
		},
		"zero deadline": {"  schedule:", `    cron: "@daily"`, "    startingDeadlineSeconds: 0"},
		"headless default mode": {
			"  modes:", "    enabled: [headless]", "    default: headless",
			"  schedule:", `    cron: "@daily"`,
		},
		// With no default named, the blueprint decides; that is checked
		// against the stored blueprint at reconcile time, not here.
		"no default mode named": {
			"  modes:", "    enabled: [interactive, headless]",
			"  schedule:", `    cron: "@daily"`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			doc := parseConfig(t, scheduledConfigYAML(lines...))
			if err := parser.ValidateDocument(doc); err != nil {
				t.Fatalf("expected a valid scheduled config, got: %v", err)
			}
			if doc.CellConfigDoc.Spec.Schedule == nil {
				t.Fatal("spec.schedule was not parsed")
			}
		})
	}
}

func TestValidateDocument_ConfigSchedule_Rejections(t *testing.T) {
	for name, tc := range map[string]struct {
		lines []string
		want  string
	}{
		"missing cron":        {[]string{"  schedule:", "    timezone: UTC"}, "spec.schedule.cron"},
		"unparseable cron":    {[]string{"  schedule:", `    cron: "every ten minutes"`}, "spec.schedule.cron"},
		"seconds field":       {[]string{"  schedule:", `    cron: "0 */10 * * * *"`}, "want 5"},
		"every macro":         {[]string{"  schedule:", `    cron: "@every 10m"`}, "unsupported macro"},
		"yearly macro":        {[]string{"  schedule:", `    cron: "@yearly"`}, "unsupported macro"},
		"out of range minute": {[]string{"  schedule:", `    cron: "61 * * * *"`}, "minute"},
		"unknown timezone": {
			[]string{"  schedule:", `    cron: "@daily"`, "    timezone: Mars/Olympus"}, "unknown timezone",
		},
		"allow policy": {
			[]string{"  schedule:", `    cron: "@daily"`, "    concurrencyPolicy: Allow"}, "not supported",
		},
		"replace policy": {
			[]string{"  schedule:", `    cron: "@daily"`, "    concurrencyPolicy: Replace"}, "not supported",
		},
		"negative deadline": {
			[]string{"  schedule:", `    cron: "@daily"`, "    startingDeadlineSeconds: -1"}, "must not be negative",
		},
		"interactive default mode": {
			[]string{
				"  modes:", "    default: interactive",
				"  schedule:", `    cron: "@daily"`,
			},
			`default mode is "interactive"`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			doc := parseConfig(t, scheduledConfigYAML(tc.lines...))
			err := parser.ValidateDocument(doc)
			requireValidationErr(t, err, errdefs.ErrConfigSchedule)
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to mention %q", err.Error(), tc.want)
			}
		})
	}
}

// TestValidateDocument_ConfigSchedule_StatusIsAccepted: a document that
// carries a status block — `kuke get config -o yaml`, re-applied — parses and
// validates; the daemon drops the status when it stores the document.
func TestValidateDocument_ConfigSchedule_StatusIsAccepted(t *testing.T) {
	yaml := scheduledConfigYAML("  schedule:", `    cron: "@daily"`) + `status:
  schedule:
    nextScheduleTime: 2026-11-18T00:00:00Z
    skippedFires: 3
    lastError: ""
`
	doc := parseConfig(t, yaml)
	if err := parser.ValidateDocument(doc); err != nil {
		t.Fatalf("expected a config with status to validate, got: %v", err)
	}
}
