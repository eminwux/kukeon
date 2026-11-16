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

	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/compose"
)

// roleTasks declares two tasks on the operator role: one bare, one with args.
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
	selected := func(names ...string) *[]string { return &names }
	for _, tc := range []struct {
		name  string
		tasks *[]string
		want  []string
		err   string
	}{
		{name: "omitted selects every task", tasks: nil, want: []string{"make-config", "make-go"}},
		{name: "empty selects none", tasks: selected(), want: nil},
		{name: "a subset", tasks: selected("make-go"), want: []string{"make-go"}},
		{name: "unknown task", tasks: selected("make-rust"), err: `role operator declares no task "make-rust"`},
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
			err:   `role operator: task pick: skill "pick-issue" is not one of the role's spec.skills`,
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
