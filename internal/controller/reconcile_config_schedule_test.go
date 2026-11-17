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

// Tests for the apply-time gate of a CellConfig's spec.schedule against the
// blueprint it references (issue #1503).

package controller_test

import (
	"errors"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	ext "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// TestApplyConfig_ScheduleAccepts: a schedule is accepted when the cells it
// fires are not created in the interactive mode — the Config defaults to (or
// only enables) another mode, or the blueprint declares no modes at all.
func TestApplyConfig_ScheduleAccepts(t *testing.T) {
	for name, tc := range map[string]struct {
		blueprint func() ext.CellBlueprintDoc
		mutate    func(*ext.CellConfigDoc)
	}{
		"default mode is headless": {
			blueprint: contractBlueprint,
			mutate: func(c *ext.CellConfigDoc) {
				c.Spec.Modes = &ext.CellConfigModes{Default: ext.ModeHeadless}
			},
		},
		"headless is the only enabled mode": {
			blueprint: contractBlueprint,
			mutate: func(c *ext.CellConfigDoc) {
				c.Spec.Modes = &ext.CellConfigModes{Enabled: []string{ext.ModeHeadless}}
			},
		},
		"blueprint declares no modes": {
			blueprint: sampleReferencedBlueprint,
			mutate:    func(*ext.CellConfigDoc) {},
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := sampleConfig()
			cfg.Spec.Schedule = &ext.CellConfigSchedule{Cron: "*/10 * * * *", Timezone: "Europe/Madrid"}
			tc.mutate(&cfg)
			for path, got := range applyContractConfig(t, cfg, tc.blueprint()) {
				if got.err != nil || !got.wrote {
					t.Errorf("%s: err = %v wrote = %v, want the Config stored", path, got.err, got.wrote)
				}
			}
		})
	}
}

// TestApplyConfig_ScheduleRejects: a schedule on a Config whose effective
// default mode is interactive is refused — nothing attaches to a cell the
// daemon starts — and so is a malformed schedule, on the create-only RPC path
// too, which reaches the daemon without the parser.
func TestApplyConfig_ScheduleRejects(t *testing.T) {
	negative := int64(-5)
	for name, tc := range map[string]struct {
		schedule ext.CellConfigSchedule
		mutate   func(*ext.CellConfigDoc)
	}{
		"blueprint's default mode is interactive": {
			schedule: ext.CellConfigSchedule{Cron: "@hourly"},
			mutate:   func(c *ext.CellConfigDoc) { c.Spec.Modes = nil },
		},
		"config defaults to interactive": {
			schedule: ext.CellConfigSchedule{Cron: "@hourly"},
			mutate: func(c *ext.CellConfigDoc) {
				c.Spec.Modes = &ext.CellConfigModes{Default: ext.ModeInteractive}
			},
		},
		"interactive is the only enabled mode": {
			schedule: ext.CellConfigSchedule{Cron: "@hourly"},
			mutate: func(c *ext.CellConfigDoc) {
				c.Spec.Modes = &ext.CellConfigModes{Enabled: []string{ext.ModeInteractive}}
			},
		},
		"unparseable cron":   {schedule: ext.CellConfigSchedule{Cron: "soon"}},
		"unknown timezone":   {schedule: ext.CellConfigSchedule{Cron: "@hourly", Timezone: "Mars/Olympus"}},
		"unsupported policy": {schedule: ext.CellConfigSchedule{Cron: "@hourly", ConcurrencyPolicy: "Allow"}},
		"negative deadline": {
			schedule: ext.CellConfigSchedule{Cron: "@hourly", StartingDeadlineSeconds: &negative},
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := sampleConfig()
			// Headless unless the case says otherwise, so only the case's own
			// fault can be what is refused.
			cfg.Spec.Modes = &ext.CellConfigModes{Default: ext.ModeHeadless}
			schedule := tc.schedule
			cfg.Spec.Schedule = &schedule
			if tc.mutate != nil {
				tc.mutate(&cfg)
			}
			for path, got := range applyContractConfig(t, cfg, contractBlueprint()) {
				if !errors.Is(got.err, errdefs.ErrConfigSchedule) {
					t.Errorf("%s: err = %v, want ErrConfigSchedule", path, got.err)
				}
				if got.wrote {
					t.Errorf("%s: a rejected Config reached the store", path)
				}
			}
		})
	}
}
