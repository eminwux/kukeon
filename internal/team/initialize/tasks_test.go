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

package initialize

import (
	"bytes"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/errdefs"
	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/config"
)

const (
	claudeSweep = "kuke-majordomo-claude-health-sweep"
	codexSweep  = "kuke-majordomo-codex-health-sweep"
)

// scheduleOf is the spec.schedule of the Config name in the last apply.
func scheduleOf(t *testing.T, raw []byte, name string) *api.CellConfigSchedule {
	t.Helper()
	for _, node := range decodeStream(raw) {
		var doc api.CellConfigDoc
		if kind, docName := docHeader(&node); kind != string(api.KindCellConfig) || docName != name {
			continue
		}
		if err := node.Decode(&doc); err != nil {
			t.Fatal(err)
		}
		return doc.Spec.Schedule
	}
	t.Fatalf("config %s is not in the apply:\n%s", name, raw)
	return nil
}

// suspended reports whether the Config name's schedule is switched off.
func suspended(t *testing.T, raw []byte, name string) bool {
	t.Helper()
	s := scheduleOf(t, raw, name)
	if s == nil {
		t.Fatalf("config %s has no schedule", name)
	}
	return s.Suspend
}

func sweepChange(enable bool, harnesses ...string) TaskChange {
	return TaskChange{
		Team: "kuke", Role: "majordomo", Task: "health-sweep", Harnesses: harnesses, Enable: enable,
	}
}

//nolint:gocyclo,cyclop // one subtest per AC5 case
func TestSetTaskSchedule(t *testing.T) {
	t.Run("enable on one harness", func(t *testing.T) {
		opts, fake := setupReapply(t)
		out, err := SetTaskSchedule(t.Context(), opts.Options, sweepChange(true, "claude"))
		if err != nil {
			t.Fatal(err)
		}
		if out.Result == nil ||
			out.Plan.Command != "kuke team task enable kuke health-sweep --role majordomo --harness claude" {
			t.Fatalf("outcome = %+v", out)
		}
		if suspended(t, fake.raw, claudeSweep) || !suspended(t, fake.raw, codexSweep) {
			t.Fatalf("claude on, codex suspended expected:\n%s", fake.raw)
		}
		// The role entry omitted tasks: it now lists the task, the entry's
		// own values untouched, and the override.
		tasks := storedTeam(t, fake, "kuke").Spec.Roles[0].Tasks
		off := false
		want := []config.TaskSelection{{
			Name: "health-sweep", Harnesses: map[string]config.TaskHarness{"claude": {Suspend: &off}},
		}}
		if tasks == nil || !reflect.DeepEqual(*tasks, want) {
			t.Fatalf("stored tasks = %+v", tasks)
		}
	})
	t.Run("disable without --harness clears every override's suspend", func(t *testing.T) {
		opts, fake := setupReapply(t)
		yes, no := true, false
		editTeam(t, fake, "kuke", func(team *config.Team) {
			team.Spec.Roles[0].Tasks = &[]config.TaskSelection{{
				Name: "health-sweep", Suspend: &no,
				Harnesses: map[string]config.TaskHarness{
					"claude": {Suspend: &no},
					"codex":  {Suspend: &yes, Schedule: "@daily"},
				},
			}}
		})
		if _, err := SetTaskSchedule(t.Context(), opts.Options, sweepChange(false)); err != nil {
			t.Fatal(err)
		}
		if !suspended(t, fake.raw, claudeSweep) || !suspended(t, fake.raw, codexSweep) {
			t.Fatalf("both sweeps must be suspended:\n%s", fake.raw)
		}
		if got := scheduleOf(t, fake.raw, codexSweep).Cron; got != "@daily" {
			t.Fatalf("codex schedule = %q, the override's schedule must stay", got)
		}
		entry := (*storedTeam(t, fake, "kuke").Spec.Roles[0].Tasks)[0]
		want := config.TaskSelection{
			Name: "health-sweep", Suspend: &yes,
			Harnesses: map[string]config.TaskHarness{"codex": {Schedule: "@daily"}},
		}
		if !reflect.DeepEqual(entry, want) {
			t.Fatalf("stored entry = %+v", entry)
		}
	})
	t.Run("enable without --harness with a schedule", func(t *testing.T) {
		opts, fake := setupReapply(t)
		change := sweepChange(true)
		change.Schedule = "@daily"
		if _, err := SetTaskSchedule(t.Context(), opts.Options, change); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{claudeSweep, codexSweep} {
			if got := scheduleOf(t, fake.raw, name); got == nil || got.Cron != "@daily" || got.Suspend {
				t.Errorf("%s schedule = %+v", name, got)
			}
		}
	})
	t.Run("disable one harness of an enabled entry", func(t *testing.T) {
		opts, fake := setupReapply(t)
		if _, err := SetTaskSchedule(t.Context(), opts.Options, sweepChange(true)); err != nil {
			t.Fatal(err)
		}
		if _, err := SetTaskSchedule(t.Context(), opts.Options, sweepChange(false, "codex")); err != nil {
			t.Fatal(err)
		}
		if suspended(t, fake.raw, claudeSweep) || !suspended(t, fake.raw, codexSweep) {
			t.Fatalf("claude on, codex suspended expected:\n%s", fake.raw)
		}
	})
	t.Run("a role with tasks omitted keeps every task selected", func(t *testing.T) {
		opts, fake := setupReapply(t)
		before := renderedConfigs(t, fake.byTeam["kuke"])
		if _, err := SetTaskSchedule(t.Context(), opts.Options, sweepChange(true, "claude")); err != nil {
			t.Fatal(err)
		}
		if after := renderedConfigs(t, fake.raw); !slices.Equal(before, after) {
			t.Fatalf("configs before %v, after %v", before, after)
		}
	})
	t.Run("dry run writes nothing", func(t *testing.T) {
		opts, fake := setupReapply(t)
		storedBefore, applies := slices.Clone(fake.teams["kuke"]), fake.applies
		opts.DryRun = true
		out, err := SetTaskSchedule(t.Context(), opts.Options, sweepChange(true, "claude"))
		if err != nil {
			t.Fatal(err)
		}
		if out.Result != nil || fake.applies != applies || !bytes.Equal(storedBefore, fake.teams["kuke"]) {
			t.Fatal("a dry run applied")
		}
		if !slices.Contains(out.Plan.Rerender, claudeSweep) {
			t.Fatalf("rerender = %v", out.Plan.Rerender)
		}
	})
	for name, tc := range map[string]struct {
		change TaskChange
		want   string
	}{
		"unknown role": {
			TaskChange{Team: "kuke", Role: "dev", Task: "health-sweep", Enable: true},
			`team kuke selects no role "dev"; its roles: majordomo`,
		},
		"unknown task": {
			TaskChange{Team: "kuke", Role: "majordomo", Task: "pick-bug", Enable: true},
			`role majordomo of team kuke renders no task "pick-bug"; its tasks: health-sweep`,
		},
		"unknown harness": {
			sweepChange(true, "gemini"),
			`the team has no harness "gemini"; its harnesses: codex, claude`,
		},
		"no role": {
			TaskChange{Team: "kuke", Task: "health-sweep", Enable: true},
			"name the role with --role",
		},
	} {
		t.Run(name, func(t *testing.T) {
			opts, fake := setupReapply(t)
			storedBefore, applies := slices.Clone(fake.teams["kuke"]), fake.applies
			_, err := SetTaskSchedule(t.Context(), opts.Options, tc.change)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if fake.applies != applies || !bytes.Equal(storedBefore, fake.teams["kuke"]) {
				t.Fatal("a refused change wrote something")
			}
		})
	}
	t.Run("not stored", func(t *testing.T) {
		opts, fake := setupInit(t)
		change := sweepChange(true)
		change.Team = "other"
		_, err := SetTaskSchedule(t.Context(), opts.Options, change)
		if !errors.Is(err, errdefs.ErrTeamNotFound) || len(fake.writes) != 0 {
			t.Fatalf("err = %v, writes = %v", err, fake.writes)
		}
	})
}

// renderedConfigs are the names of the CellConfigs of the last apply.
func renderedConfigs(t *testing.T, raw []byte) []string {
	t.Helper()
	var out []string
	for _, node := range decodeStream(raw) {
		if kind, name := docHeader(&node); kind == string(api.KindCellConfig) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

func TestTeamTasksOf(t *testing.T) {
	next := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	doc := func(name, harness, task string, schedule *api.CellConfigSchedule) api.CellConfigDoc {
		labels := map[string]string{api.LabelTeam: "kuke", api.LabelRole: "majordomo"}
		if task != "" {
			labels[api.LabelTask] = task
		}
		if harness != "" {
			labels[api.LabelHarness] = harness
		}
		return api.CellConfigDoc{
			Metadata: api.CellConfigMetadata{Name: name, Labels: labels},
			Spec:     api.CellConfigSpec{Schedule: schedule},
			Status: &api.CellConfigStatus{
				Schedule: &api.CellConfigScheduleStatus{NextScheduleTime: &next},
			},
		}
	}
	configs := []api.CellConfigDoc{
		doc(codexSweep, "codex", "health-sweep", &api.CellConfigSchedule{Cron: "@hourly", Suspend: true}),
		doc(claudeSweep, "claude", "health-sweep", &api.CellConfigSchedule{Cron: "@hourly"}),
		doc("kuke-majordomo-claude", "claude", "", nil),
		// Rendered before the label existed (D7).
		doc("kuke-majordomo-claude-pick-issue", "", "pick-issue", nil),
		{Metadata: api.CellConfigMetadata{Name: "other-x-y", Labels: map[string]string{
			api.LabelTeam: "other", api.LabelTask: "y",
		}}},
	}
	got := TeamTasksOf("kuke", configs)
	want := []TeamTask{
		{
			Role: "majordomo", Task: "health-sweep", Harness: "claude", Config: claudeSweep, Schedule: "@hourly",
			State: TaskOn, Next: &next,
		},
		{
			Role: "majordomo", Task: "health-sweep", Harness: "codex", Config: codexSweep, Schedule: "@hourly",
			State: TaskSuspended,
		},
		{Role: "majordomo", Task: "pick-issue", Config: "kuke-majordomo-claude-pick-issue", State: TaskManual},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows:\n%+v\nwant:\n%+v", got, want)
	}
}

func TestTeamTasksNotStored(t *testing.T) {
	if _, err := TeamTasks(t.Context(), newFake(), "absent"); !errors.Is(err, errdefs.ErrTeamNotFound) {
		t.Fatalf("err = %v", err)
	}
}
