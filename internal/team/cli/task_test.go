// Copyright 2026 Emiliano Spinella (eminwux)
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

package cli_test

import (
	"bytes"
	"slices"
	"strings"
	"testing"
	"time"

	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/config"
)

func TestTaskEnableOneHarness(t *testing.T) {
	h := kukeHarness(t)
	out, err := h.run(t, "task", "enable", "kuke", "health-sweep", "--role", "majordomo", "--harness", "claude")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	lines := strings.Split(out, "\n")
	if lines[0] != "Schedule of task health-sweep switched on for role majordomo on harness claude of team kuke." ||
		!strings.HasPrefix(lines[1], "Applied ") || !strings.Contains(lines[1], "stored team kuke") ||
		!strings.Contains(out, "No cells created or started.") {
		t.Fatalf("output:\n%s", out)
	}
	tasks := h.store.storedTeam(t, "kuke").Spec.Roles[0].Tasks
	if tasks == nil || len(*tasks) != 1 {
		t.Fatalf("stored tasks = %+v", tasks)
	}
	override, ok := (*tasks)[0].Harnesses["claude"]
	if !ok || override.Suspend == nil || *override.Suspend || (*tasks)[0].Suspend != nil {
		t.Fatalf("stored entry = %+v", (*tasks)[0])
	}
}

func TestTaskDisableEveryHarness(t *testing.T) {
	h := kukeHarness(t)
	if out, err := h.run(t, "task", "enable", "kuke", "health-sweep", "--role", "majordomo",
		"--harness", "claude", "--harness", "codex"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err := h.run(t, "task", "disable", "kuke", "health-sweep", "--role", "majordomo")
	if err != nil ||
		!strings.HasPrefix(out, "Schedule of task health-sweep switched off for role majordomo of team kuke.\n") {
		t.Fatalf("%v\n%s", err, out)
	}
	entry := (*h.store.storedTeam(t, "kuke").Spec.Roles[0].Tasks)[0]
	if entry.Suspend == nil || !*entry.Suspend || entry.Harnesses != nil {
		t.Fatalf("stored entry = %+v", entry)
	}
}

func TestTaskDryRun(t *testing.T) {
	h := kukeHarness(t)
	before := slices.Clone(h.store.teams["kuke"])
	out, err := h.run(t, "task", "enable", "kuke", "health-sweep", "--role", "majordomo", "--harness", "claude",
		"--dry-run")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.HasPrefix(out, "Dry run:") || strings.Contains(out, "switched on") {
		t.Fatalf("output:\n%s", out)
	}
	if h.store.writes != 0 || !bytes.Equal(before, h.store.teams["kuke"]) {
		t.Fatal("a dry run wrote")
	}
}

func TestTaskArguments(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"no role":          {[]string{"enable", "kuke", "health-sweep"}, `"role" not set`},
		"one argument":     {[]string{"enable", "kuke", "--role", "majordomo"}, "accepts 2 arg(s)"},
		"disable schedule": {[]string{"disable", "kuke", "x", "--role", "m", "--schedule", "@daily"}, "unknown flag"},
		"list arguments":   {[]string{"list"}, "accepts 1 arg(s)"},
		"unknown role":     {[]string{"enable", "kuke", "health-sweep", "--role", "dev"}, `no role "dev"`},
		"unknown task": {
			[]string{"enable", "kuke", "pick-bug", "--role", "majordomo"}, `renders no task "pick-bug"`,
		},
		"unknown harness": {
			[]string{"enable", "kuke", "health-sweep", "--role", "majordomo", "--harness", "gemini"},
			`no harness "gemini"`,
		},
		"not stored": {[]string{"enable", "other", "health-sweep", "--role", "m"}, "kuke create team other"},
	} {
		t.Run(name, func(t *testing.T) {
			h := kukeHarness(t)
			before := slices.Clone(h.store.teams["kuke"])
			out, err := h.run(t, append([]string{"task"}, tc.args...)...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q\n%s", err, tc.want, out)
			}
			if h.store.writes != 0 || !bytes.Equal(before, h.store.teams["kuke"]) {
				t.Fatal("a refused change wrote")
			}
		})
	}
}

func TestTaskList(t *testing.T) {
	h := kukeHarness(t)
	h.store.putTeam(t, config.Majordomo())
	next := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	doc := func(harness string, schedule *api.CellConfigSchedule) api.CellConfigDoc {
		labels := map[string]string{
			api.LabelTeam: "kuke", api.LabelRole: "majordomo", api.LabelTask: "health-sweep",
		}
		if harness != "" {
			labels[api.LabelHarness] = harness
		}
		name := "kuke-majordomo-" + harness + "-health-sweep"
		if harness == "" {
			name = "kuke-majordomo-opencode-health-sweep"
		}
		return api.CellConfigDoc{
			Metadata: api.CellConfigMetadata{Name: name, Labels: labels},
			Spec:     api.CellConfigSpec{Schedule: schedule},
			Status:   &api.CellConfigStatus{Schedule: &api.CellConfigScheduleStatus{NextScheduleTime: &next}},
		}
	}
	h.store.configs = []api.CellConfigDoc{
		doc("codex", &api.CellConfigSchedule{Cron: "@hourly", Suspend: true}),
		doc("claude", &api.CellConfigSchedule{Cron: "@hourly"}),
		doc("", nil),
		{Metadata: api.CellConfigMetadata{Name: "kuke-majordomo-claude", Labels: map[string]string{
			api.LabelTeam: "kuke", api.LabelRole: "majordomo", api.LabelHarness: "claude",
		}}},
	}
	out, err := h.run(t, "task", "list", "kuke")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"ROLE       TASK          HARNESS  CONFIG                                SCHEDULE  STATE      NEXT                ",
		"---------  ------------  -------  ------------------------------------  --------  ---------  --------------------",
		"majordomo  health-sweep  -        kuke-majordomo-opencode-health-sweep  -         manual     -                   ",
		"majordomo  health-sweep  claude   kuke-majordomo-claude-health-sweep    @hourly   on         2026-11-25T10:00:00Z",
		"majordomo  health-sweep  codex    kuke-majordomo-codex-health-sweep     @hourly   suspended  -                   ",
		"",
	}, "\n")
	if out != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out, want)
	}
	if h.store.writes != 0 {
		t.Fatal("list wrote")
	}
}
