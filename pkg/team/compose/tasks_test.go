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

package compose_test //nolint:cyclop // assertion-heavy tests raise the package average, not branching logic.

import (
	"bytes"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/eminwux/kukeon/internal/apply/parser"
	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/compose"
	"github.com/eminwux/kukeon/pkg/team/config"
	"gopkg.in/yaml.v3"
)

// roleTasks declares two tasks on the majordomo role: one bare, one with args.
const roleTasks = `  tasks:
    - { name: make-config, skill: create-cell-config }
    - { name: make-go, skill: create-cell-config, args: "--lang go" }
  needs:
`

func taskSource(t *testing.T, tasks string) fstest.MapFS {
	t.Helper()
	source := copiedSource(t)
	editRole(source, "  needs:\n", tasks)
	return source
}

func taskNames(pair compose.Pair) []string {
	var out []string
	for _, task := range pair.Tasks {
		out = append(out, task.Metadata.Name)
	}
	return out
}

func TestRoleTasksRenderHeadlessConfigs(t *testing.T) {
	cfg, opts := fixture(t)
	opts.Sources["kukeon"] = taskSource(t, roleTasks)
	result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
	if err != nil {
		t.Fatal(err)
	}
	pair := result.Pairs[0]
	base := pair.Config.Metadata.Name
	if got, want := taskNames(pair), []string{base + "-make-config", base + "-make-go"}; !slices.Equal(got, want) {
		t.Fatalf("task configs = %v, want %v", got, want)
	}
	// The pair's interactive config is untouched.
	if pair.Config.Spec.Modes != nil || pair.Config.Spec.AutoDelete || pair.Config.Spec.Values["PROMPT"] != "" ||
		pair.Config.Metadata.Labels[api.LabelTask] != "" {
		t.Fatalf("interactive config changed: %+v", pair.Config)
	}
	prompts := []string{
		"Use the create-cell-config skill",
		"Use the create-cell-config skill with arguments: --lang go",
	}
	for i, task := range pair.Tasks {
		want := *pair.Config
		want.Metadata.Name = task.Metadata.Name
		if task.Metadata.Labels[api.LabelTeam] != pair.Config.Metadata.Labels[api.LabelTeam] ||
			task.Metadata.Labels[api.LabelRole] != "majordomo" ||
			task.Metadata.Labels[api.LabelTask] != strings.TrimPrefix(task.Metadata.Name, base+"-") {
			t.Fatalf("%s labels = %v", task.Metadata.Name, task.Metadata.Labels)
		}
		want.Metadata.Labels = task.Metadata.Labels
		if task.Spec.Values["PROMPT"] != prompts[i] {
			t.Fatalf("%s PROMPT = %q, want %q", task.Metadata.Name, task.Spec.Values["PROMPT"], prompts[i])
		}
		if !task.Spec.AutoDelete || task.Spec.Modes == nil ||
			!slices.Equal(task.Spec.Modes.Enabled, []string{headless}) || task.Spec.Modes.Default != headless {
			t.Fatalf("%s modes %+v autoDelete %v", task.Metadata.Name, task.Spec.Modes, task.Spec.AutoDelete)
		}
		// Everything else is the pair's: blueprint, fills, components, requires.
		got := *task
		got.Spec.Modes, got.Spec.AutoDelete = nil, false
		got.Spec.Values = without(task.Spec.Values, "PROMPT")
		want.Spec.Values = without(pair.Config.Spec.Values, "")
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s diverges from the pair config:\n got %+v\nwant %+v", task.Metadata.Name, got, want)
		}
	}
	// Task configs are independent documents: editing one leaves the pair's.
	pair.Tasks[0].Spec.Values["IMAGE"] = "changed"
	if pair.Config.Spec.Values["IMAGE"] == "changed" {
		t.Fatal("task config shares the pair's values map")
	}

	raw, err := result.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	if n := bytes.Count(raw, []byte("kind: CellConfig")); n != 3 || result.Configs() != 3 {
		t.Fatalf("document stream has %d configs, Configs() = %d; want 3", n, result.Configs())
	}

	// A renamed blueprint repoints the task configs too.
	result.RenameBlueprint(pair.Blueprint, "renamed")
	for _, task := range result.Pairs[0].Tasks {
		if task.Spec.Blueprint.Name != "renamed" {
			t.Fatalf("%s still references %s", task.Metadata.Name, task.Spec.Blueprint.Name)
		}
	}
}

// without copies values without the dropped key.
func without(values map[string]string, drop string) map[string]string {
	out := map[string]string{}
	for k, v := range values {
		if k != drop {
			out[k] = v
		}
	}
	return out
}

func TestRoleTaskSelection(t *testing.T) {
	selected := func(names ...string) *[]config.TaskSelection {
		out := make([]config.TaskSelection, 0, len(names))
		for _, name := range names {
			out = append(out, config.TaskSelection{Name: name})
		}
		return &out
	}
	for _, tc := range []struct {
		name  string
		tasks *[]config.TaskSelection
		want  []string
		err   string
	}{
		{name: "omitted selects every task", tasks: nil, want: []string{"make-config", "make-go"}},
		{name: "empty selects none", tasks: selected(), want: nil},
		{name: "a subset", tasks: selected("make-go"), want: []string{"make-go"}},
		{name: "unknown task", tasks: selected("make-rust"), err: `role majordomo declares no task "make-rust"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, opts := fixture(t)
			opts.Sources["kukeon"] = taskSource(t, roleTasks)
			cfg.Spec.Defaults.Roles[0].Tasks = tc.tasks
			result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("err = %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			pair := result.Pairs[0]
			var got []string
			for _, task := range pair.Tasks {
				got = append(got, task.Metadata.Labels[api.LabelTask])
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("tasks = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRoleTaskHarnessPin(t *testing.T) {
	cfg, opts := fixture(t)
	allHarnesses(cfg)
	opts.Sources["kukeon"] = taskSource(t, roleTasks)
	cfg.Spec.Defaults.Roles[0].Harness = "claude"
	result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Pairs) != 1 || result.Pairs[0].Harness != "claude" || len(result.Pairs[0].Tasks) != 2 {
		t.Fatalf("pairs = %+v", result.Pairs)
	}
	for _, task := range result.Pairs[0].Tasks {
		if !strings.Contains(task.Metadata.Name, "-claude-") {
			t.Fatalf("task config %s is not the pinned harness's", task.Metadata.Name)
		}
	}
}

func TestRoleTaskErrors(t *testing.T) {
	for _, tc := range []struct{ name, tasks, err string }{
		{
			name:  "unknown skill",
			tasks: "  tasks:\n    - { name: pick, skill: pick-issue }\n  needs:\n",
			err:   `role majordomo: task pick: skill "pick-issue" is not one of the role's spec.skills`,
		},
		{
			name:  "duplicate name",
			tasks: "  tasks:\n    - { name: a, skill: create-cell-config }\n    - { name: a, skill: create-cell-config }\n  needs:\n",
			err:   `duplicate task name "a"`,
		},
		{
			name:  "invalid name",
			tasks: "  tasks:\n    - { name: Bad_Name, skill: create-cell-config }\n  needs:\n",
			err:   `invalid task name "Bad_Name"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, opts := fixture(t)
			opts.Sources["kukeon"] = taskSource(t, tc.tasks)
			_, err := compose.Compose(cfg, selectedTeam(cfg), opts)
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Fatalf("err = %v, want %q", err, tc.err)
			}
		})
	}
}

func TestRoleTasksSkipHarnessWithoutHeadless(t *testing.T) {
	cfg, opts := fixture(t)
	source := taskSource(t, roleTasks)
	editFile(source, "team/harnesses/codex/harness.yaml", "    headless:", "    batch:")
	opts.Sources["kukeon"] = source
	result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Pairs[0].Tasks) != 0 {
		t.Fatalf("tasks rendered for a harness with no headless mode: %v", taskNames(result.Pairs[0]))
	}
	if !slices.ContainsFunc(
		result.Notes,
		func(n string) bool { return strings.Contains(n, "declares no headless mode") },
	) {
		t.Fatalf("notes = %v", result.Notes)
	}
}

// TestTaskConfigMaterializesHeadlessCell renders a task config the way the
// daemon does and checks the cell runs headless with the task's prompt and
// deletes itself.
func TestTaskConfigMaterializesHeadlessCell(t *testing.T) {
	cfg, opts := fixture(t)
	opts.Sources["kukeon"] = taskSource(t, roleTasks)
	result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
	if err != nil {
		t.Fatal(err)
	}
	pair := result.Pairs[0]
	task := pair.Tasks[1]
	doc := renderCell(t, compose.Pair{Blueprint: pair.Blueprint, Config: task}, task.Spec.Modes.Default, nil)
	if !doc.Spec.AutoDelete {
		t.Fatal("task cell is not AutoDelete")
	}
	c := workOf(t, doc)
	if got := envOf(c)["PROMPT"]; got != "Use the create-cell-config skill with arguments: --lang go" {
		t.Fatalf("PROMPT = %q", got)
	}
	// The interactive config's cell keeps the blueprint's AutoDelete (unset).
	if renderCell(t, pair, interactive, nil).Spec.AutoDelete {
		t.Fatal("interactive cell became AutoDelete")
	}
}

// scheduledRoleTasks declares three tasks on the majordomo role: one with no
// schedule, one with a default schedule, and one whose default schedule ships
// switched off (issue #1503).
const scheduledRoleTasks = `  tasks:
    - { name: pick-issue, skill: create-cell-config }
    - { name: pick-bug, skill: create-cell-config, args: "--label bug", schedule: "*/30 * * * *" }
    - { name: health-sweep, skill: create-cell-config, schedule: "@hourly", suspend: true }
  needs:
`

// TestRoleTaskSchedules: a task's schedule and suspend come from the role's
// defaults, with each value the team's task entry sets overriding the
// corresponding default, and are rendered into the task config's
// spec.schedule. A task with no schedule renders none.
func TestRoleTaskSchedules(t *testing.T) {
	yes, no := true, false
	type schedule = api.CellConfigSchedule
	for _, tc := range []struct {
		name  string
		tasks *[]config.TaskSelection
		want  map[string]*schedule
	}{
		{
			name: "omitted selection takes the role defaults",
			want: map[string]*schedule{
				"pick-issue":   nil,
				"pick-bug":     {Cron: "*/30 * * * *"},
				"health-sweep": {Cron: "@hourly", Suspend: true},
			},
		},
		{
			name: "bare names take the role defaults",
			tasks: &[]config.TaskSelection{
				{Name: "pick-issue"}, {Name: "pick-bug"}, {Name: "health-sweep"},
			},
			want: map[string]*schedule{
				"pick-issue":   nil,
				"pick-bug":     {Cron: "*/30 * * * *"},
				"health-sweep": {Cron: "@hourly", Suspend: true},
			},
		},
		{
			name: "team schedule overrides the role default",
			tasks: &[]config.TaskSelection{
				{Name: "pick-bug", Schedule: "*/15 * * * *"},
			},
			want: map[string]*schedule{"pick-bug": {Cron: "*/15 * * * *"}},
		},
		{
			name: "team suspend false turns on a schedule the role ships off",
			tasks: &[]config.TaskSelection{
				{Name: "health-sweep", Suspend: &no},
			},
			want: map[string]*schedule{"health-sweep": {Cron: "@hourly"}},
		},
		{
			name: "team suspend true switches off a role default",
			tasks: &[]config.TaskSelection{
				{Name: "pick-bug", Suspend: &yes},
			},
			want: map[string]*schedule{"pick-bug": {Cron: "*/30 * * * *", Suspend: true}},
		},
		{
			name: "team schedules a task the role leaves unscheduled",
			tasks: &[]config.TaskSelection{
				{Name: "pick-issue", Schedule: "@daily", Suspend: &yes},
				{Name: "health-sweep", Schedule: "0 */6 * * *"},
			},
			want: map[string]*schedule{
				"pick-issue": {Cron: "@daily", Suspend: true},
				// Only the schedule is overridden; suspend stays the role's.
				"health-sweep": {Cron: "0 */6 * * *", Suspend: true},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, opts := fixture(t)
			opts.Sources["kukeon"] = taskSource(t, scheduledRoleTasks)
			cfg.Spec.Defaults.Roles[0].Tasks = tc.tasks
			result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
			if err != nil {
				t.Fatal(err)
			}
			pair := result.Pairs[0]
			if pair.Config.Spec.Schedule != nil {
				t.Fatalf("the interactive config got a schedule: %+v", pair.Config.Spec.Schedule)
			}
			got := map[string]*schedule{}
			for _, task := range pair.Tasks {
				got[task.Metadata.Labels[api.LabelTask]] = task.Spec.Schedule
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("task schedules:\n got %s\nwant %s", describeSchedules(got), describeSchedules(tc.want))
			}

			// What is rendered must pass the apply-time validation it is
			// about to meet: a parseable schedule on a headless-default Config.
			for _, task := range pair.Tasks {
				raw, marshalErr := yaml.Marshal(task)
				if marshalErr != nil {
					t.Fatal(marshalErr)
				}
				doc, parseErr := parser.ParseDocument(0, raw)
				if parseErr != nil {
					t.Fatalf("%s: %v", task.Metadata.Name, parseErr)
				}
				if validationErr := parser.ValidateDocument(doc); validationErr != nil {
					t.Fatalf("%s does not validate: %v", task.Metadata.Name, validationErr)
				}
			}
		})
	}
}

func describeSchedules(schedules map[string]*api.CellConfigSchedule) string {
	names := make([]string, 0, len(schedules))
	for name := range schedules {
		names = append(names, name)
	}
	slices.Sort(names)
	var b strings.Builder
	for _, name := range names {
		if s := schedules[name]; s == nil {
			b.WriteString(name + "=<none> ")
		} else {
			b.WriteString(name + "=" + s.Cron)
			if s.Suspend {
				b.WriteString("(suspended)")
			}
			b.WriteString(" ")
		}
	}
	return b.String()
}

// TestRoleTaskScheduleRejections: a schedule that does not parse is refused
// with the place it was written, and so is a suspend on a task that ends up
// with no schedule to switch.
func TestRoleTaskScheduleRejections(t *testing.T) {
	yes, no := true, false
	for _, tc := range []struct {
		name      string
		roleTasks string
		tasks     *[]config.TaskSelection
		err       string
	}{
		{
			name:      "team suspend true on an unscheduled task",
			roleTasks: scheduledRoleTasks,
			tasks:     &[]config.TaskSelection{{Name: "pick-issue", Suspend: &yes}},
			err:       "tasks: pick-issue: suspend is set but the task has no schedule",
		},
		{
			name:      "team suspend false on an unscheduled task",
			roleTasks: scheduledRoleTasks,
			tasks:     &[]config.TaskSelection{{Name: "pick-issue", Suspend: &no}},
			err:       "tasks: pick-issue: suspend is set but the task has no schedule",
		},
		{
			name:      "team schedule does not parse",
			roleTasks: scheduledRoleTasks,
			tasks:     &[]config.TaskSelection{{Name: "pick-bug", Schedule: "every day"}},
			err:       "tasks: pick-bug: schedule:",
		},
		{
			name:      "team schedule uses a seconds field",
			roleTasks: scheduledRoleTasks,
			tasks:     &[]config.TaskSelection{{Name: "pick-bug", Schedule: "0 */5 * * * *"}},
			err:       "tasks: pick-bug: schedule:",
		},
		{
			name: "role suspend with no schedule anywhere",
			roleTasks: `  tasks:
    - { name: pick-issue, skill: create-cell-config, suspend: true }
  needs:
`,
			err: "tasks: pick-issue: suspend is set but the task has no schedule",
		},
		{
			name: "role schedule does not parse",
			roleTasks: `  tasks:
    - { name: pick-issue, skill: create-cell-config, schedule: "@every 5m" }
  needs:
`,
			err: "task pick-issue: schedule:",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, opts := fixture(t)
			opts.Sources["kukeon"] = taskSource(t, tc.roleTasks)
			cfg.Spec.Defaults.Roles[0].Tasks = tc.tasks
			_, err := compose.Compose(cfg, selectedTeam(cfg), opts)
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Fatalf("err = %v, want %q", err, tc.err)
			}
		})
	}
}

// twoHarnessTasks is the fixture team on codex and claude, its role declaring
// health-sweep with the given role-side default (`schedule`/`suspend` keys of
// the role.yaml entry, or "").
func twoHarnessTasks(
	t *testing.T, roleDefault string, tasks *[]config.TaskSelection,
) (*config.TeamDefaults, *config.Team, compose.Options) {
	t.Helper()
	cfg, opts := fixture(t)
	cfg.Spec.Defaults.Harnesses = config.MajordomoHarnesses() // codex, claude
	entry := "{ name: health-sweep, skill: create-cell-config" + roleDefault + " }"
	opts.Sources["kukeon"] = taskSource(t, "  tasks:\n    - "+entry+"\n  needs:\n")
	cfg.Spec.Defaults.Roles[0].Tasks = tasks
	return cfg, selectedTeam(cfg), opts
}

// TestRoleTaskHarnessSchedules pins issue #1668 AC2: per role × harness pair, a
// value in the task entry's `harnesses.<harness>` replaces the entry's value,
// which replaces the role's default.
func TestRoleTaskHarnessSchedules(t *testing.T) {
	yes, no := true, false
	type schedule = api.CellConfigSchedule
	for _, tc := range []struct {
		name          string
		roleDefault   string
		entry         config.TaskSelection
		claude, codex *schedule
	}{
		{
			name:        "override turns one harness on",
			roleDefault: `, schedule: "@hourly", suspend: true`,
			entry: config.TaskSelection{
				Harnesses: map[string]config.TaskHarness{"claude": {Suspend: &no}},
			},
			claude: &schedule{Cron: "@hourly"},
			codex:  &schedule{Cron: "@hourly", Suspend: true},
		},
		{
			name:        "override wins over the entry",
			roleDefault: `, schedule: "@hourly"`,
			entry: config.TaskSelection{
				Suspend:   &yes,
				Harnesses: map[string]config.TaskHarness{"codex": {Suspend: &no}},
			},
			claude: &schedule{Cron: "@hourly", Suspend: true},
			codex:  &schedule{Cron: "@hourly"},
		},
		{
			name:        "override schedule for one harness",
			roleDefault: `, schedule: "@hourly"`,
			entry: config.TaskSelection{
				Harnesses: map[string]config.TaskHarness{"claude": {Schedule: "@daily"}},
			},
			claude: &schedule{Cron: "@daily"},
			codex:  &schedule{Cron: "@hourly"},
		},
		{
			name: "override schedules a task the role leaves unscheduled",
			entry: config.TaskSelection{
				Harnesses: map[string]config.TaskHarness{"claude": {Schedule: "@daily"}},
			},
			claude: &schedule{Cron: "@daily"},
			codex:  nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.entry.Name = "health-sweep"
			cfg, team, opts := twoHarnessTasks(t, tc.roleDefault, &[]config.TaskSelection{tc.entry})
			result, err := compose.Compose(cfg, team, opts)
			if err != nil {
				t.Fatal(err)
			}
			for harness, want := range map[string]*schedule{"claude": tc.claude, "codex": tc.codex} {
				pair := pairOf(t, result, harness)
				if len(pair.Tasks) != 1 {
					t.Fatalf("%s: task configs = %v", harness, taskNames(pair))
				}
				task := pair.Tasks[0]
				if got := task.Spec.Schedule; !reflect.DeepEqual(got, want) {
					t.Errorf("%s: schedule = %+v, want %+v", harness, got, want)
				}
				// AC4: both the pair Config and its task Config name the harness.
				if got := pair.Config.Metadata.Labels[api.LabelHarness]; got != harness {
					t.Errorf("%s: pair Config %s = %q", harness, api.LabelHarness, got)
				}
				if got := task.Metadata.Labels[api.LabelHarness]; got != harness {
					t.Errorf("%s: task Config %s = %q", harness, api.LabelHarness, got)
				}
			}
		})
	}
}

// TestRoleTaskHarnessRejections pins issue #1668 AC3: composition names the
// role, the task and the harness when an override cannot apply.
func TestRoleTaskHarnessRejections(t *testing.T) {
	yes := true
	for _, tc := range []struct {
		name        string
		roleDefault string
		pin         string
		entry       config.TaskSelection
		err         []string
	}{
		{
			name: "suspend set and no schedule on the other harness",
			entry: config.TaskSelection{
				Suspend:   &yes,
				Harnesses: map[string]config.TaskHarness{"claude": {Schedule: "@daily"}},
			},
			err: []string{
				"compose majordomo/codex:", "roles majordomo: tasks: health-sweep: harness codex: " +
					"suspend is set but the task has no schedule",
			},
		},
		{
			name: "key is not a harness of the team",
			entry: config.TaskSelection{
				Harnesses: map[string]config.TaskHarness{"gemini": {Suspend: &yes}},
			},
			roleDefault: `, schedule: "@hourly"`,
			err: []string{
				`roles majordomo: tasks: health-sweep: harnesses: the team has no harness "gemini"; ` +
					"its harnesses: codex, claude",
			},
		},
		{
			name:        "role is pinned to another harness",
			pin:         "claude",
			roleDefault: `, schedule: "@hourly"`,
			entry: config.TaskSelection{
				Harnesses: map[string]config.TaskHarness{"codex": {Suspend: &yes}},
			},
			err: []string{`roles majordomo: tasks: health-sweep: harnesses: codex: role is pinned to harness "claude"`},
		},
		{
			name:        "override schedule does not parse",
			roleDefault: `, schedule: "@hourly"`,
			entry: config.TaskSelection{
				Harnesses: map[string]config.TaskHarness{"claude": {Schedule: "nope"}},
			},
			err: []string{"roles majordomo: tasks: health-sweep: harnesses: claude: schedule:"},
		},
		{
			name:        "entry schedule does not parse though every harness overrides it",
			roleDefault: `, schedule: "@hourly"`,
			entry: config.TaskSelection{
				Schedule: "nope",
				Harnesses: map[string]config.TaskHarness{
					"claude": {Schedule: "@daily"},
					"codex":  {Schedule: "@daily"},
				},
			},
			err: []string{"roles majordomo: tasks: health-sweep: schedule:"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.entry.Name = "health-sweep"
			cfg, team, opts := twoHarnessTasks(t, tc.roleDefault, &[]config.TaskSelection{tc.entry})
			cfg.Spec.Defaults.Roles[0].Harness = tc.pin
			_, err := compose.Compose(cfg, team, opts)
			if err == nil {
				t.Fatal("Compose() succeeded")
			}
			for _, want := range tc.err {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("err = %v, want it to contain %q", err, want)
				}
			}
		})
	}
}
