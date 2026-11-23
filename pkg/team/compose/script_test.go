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

package compose_test

import (
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/compose"
	"github.com/eminwux/kukeon/pkg/team/config"
	"github.com/eminwux/kukeon/pkg/team/harness"
)

const (
	tickerRole     = "team/roles/ticker/role.yaml"
	tickerCommand  = `python3 "$ROLE_SOURCE_DIR/$ROLE_PATH/tick.py"`
	scriptHarness  = "script"
	scriptStateDir = "/home/user/.script-state"
)

// tickerYAML is a program role: a role.yaml and nothing else, no AGENTS.md
// and no skills.
const tickerYAML = `apiVersion: v1beta1
kind: Role
metadata: { name: ticker }
spec:
  harnesses:
    script:
      command: python3 "$ROLE_SOURCE_DIR/$ROLE_PATH/tick.py"
  connectors: [github]
  needs:
    mounts: [kuke-binary, kukeond-socket]
  tasks:
    - { name: tick, args: "--once", schedule: "*/5 * * * *", suspend: true }
`

func scriptSelection() config.Harness {
	return config.Harness{Name: scriptHarness, Source: "kukeon", Path: "team/harnesses/script"}
}

func claudeSelection() config.Harness {
	return config.Harness{Name: "claude", Source: "kukeon", Path: "team/harnesses/claude"}
}

func tickerSelection() config.Role {
	return config.Role{Name: "ticker", Source: "kukeon", Path: "team/roles/ticker"}
}

// scriptFixture is a team with the given harnesses and roles over a copied
// source that also holds the ticker role.
func scriptFixture(
	t *testing.T,
	harnesses []config.Harness,
	roles ...config.Role,
) (*config.TeamDefaults, compose.Options, fstest.MapFS) {
	t.Helper()
	cfg, opts := fixture(t)
	source := copiedSource(t)
	source[tickerRole] = &fstest.MapFile{Data: []byte(tickerYAML)}
	opts.Sources["kukeon"] = source
	cfg.Spec.Defaults.Harnesses = harnesses
	cfg.Spec.Defaults.Roles = roles
	return cfg, opts, source
}

// scriptTeam selects every default harness, including a single one, so the
// team does not fall back to the fixture's selection.
func scriptTeam(cfg *config.TeamDefaults) *config.Team {
	team := inheritingTeam()
	team.Spec.Harnesses = slices.Clone(cfg.Spec.Defaults.Harnesses)
	return team
}

func pairFor(t *testing.T, result *compose.Result, role, harnessName string) compose.Pair {
	t.Helper()
	for _, pair := range result.Pairs {
		if pair.Role == role && pair.Harness == harnessName {
			return pair
		}
	}
	t.Fatalf("no %s/%s pair in %v", role, harnessName, pairList(result))
	return compose.Pair{}
}

func pairList(result *compose.Result) []string {
	var out []string
	for _, pair := range result.Pairs {
		out = append(out, pair.Role+"/"+pair.Harness)
	}
	return out
}

// TestScriptHarnessRendersConfigs covers AC2: the pair config carries the
// role's command and the task config its args, headless and self-deleting on
// the task's schedule, with no prompt.
func TestScriptHarnessRendersConfigs(t *testing.T) {
	cfg, opts, _ := scriptFixture(t, []config.Harness{scriptSelection()}, tickerSelection())
	result, err := compose.Compose(cfg, scriptTeam(cfg), opts)
	if err != nil {
		t.Fatal(err)
	}
	pair := pairFor(t, result, "ticker", scriptHarness)
	if len(result.Pairs) != 1 {
		t.Fatalf("pairs = %v", pairList(result))
	}
	if got, want := pair.Config.Metadata.Name, "root-ticker-script"; got != want {
		t.Fatalf("config name = %q, want %q", got, want)
	}
	if got, want := pair.Blueprint.Metadata.Name, "script-host"; got != want {
		t.Fatalf("blueprint = %q, want %q", got, want)
	}
	if got := pair.Config.Spec.Values["COMMAND"]; got != tickerCommand {
		t.Fatalf("COMMAND = %q, want %q", got, tickerCommand)
	}
	if _, set := pair.Config.Spec.Values["PROMPT"]; set {
		t.Fatalf("pair config carries a PROMPT: %v", pair.Config.Spec.Values)
	}
	if len(pair.Tasks) != 1 {
		t.Fatalf("tasks = %v", taskNames(pair))
	}
	task := pair.Tasks[0]
	if got, want := task.Metadata.Name, "root-ticker-script-tick"; got != want {
		t.Fatalf("task config name = %q, want %q", got, want)
	}
	if got := task.Spec.Values["ARGS"]; got != "--once" {
		t.Fatalf("ARGS = %q, want --once", got)
	}
	if got := task.Spec.Values["COMMAND"]; got != tickerCommand {
		t.Fatalf("task COMMAND = %q", got)
	}
	if _, set := task.Spec.Values["PROMPT"]; set {
		t.Fatalf("script task carries a PROMPT: %v", task.Spec.Values)
	}
	if task.Spec.Modes == nil || !slices.Equal(task.Spec.Modes.Enabled, []string{headless}) ||
		task.Spec.Modes.Default != headless || !task.Spec.AutoDelete {
		t.Fatalf("task modes %+v autoDelete %v", task.Spec.Modes, task.Spec.AutoDelete)
	}
	if task.Spec.Schedule == nil || task.Spec.Schedule.Cron != "*/5 * * * *" || !task.Spec.Schedule.Suspend {
		t.Fatalf("task schedule = %+v", task.Spec.Schedule)
	}
	if task.Metadata.Labels[api.LabelTask] != "tick" {
		t.Fatalf("task labels = %v", task.Metadata.Labels)
	}
}

// TestScriptTaskMaterializesCell covers AC3: the task config renders a cell
// the way the daemon does, running the role's command on base-user with the
// GitHub token, the host mounts and the program's state volume.
func TestScriptTaskMaterializesCell(t *testing.T) {
	cfg, opts, _ := scriptFixture(t, []config.Harness{scriptSelection()}, tickerSelection())
	result, err := compose.Compose(cfg, scriptTeam(cfg), opts)
	if err != nil {
		t.Fatal(err)
	}
	pair := pairFor(t, result, "ticker", scriptHarness)
	task := pair.Tasks[0]
	doc := renderCell(t, compose.Pair{Blueprint: pair.Blueprint, Config: task, Secrets: pair.Secrets},
		task.Spec.Modes.Default, nil)
	if !doc.Spec.AutoDelete {
		t.Fatal("task cell is not AutoDelete")
	}
	c := workOf(t, doc)
	env := envOf(c)
	want := map[string]string{
		"COMMAND":          tickerCommand,
		"ARGS":             "--once",
		"SCRIPT_STATE_DIR": scriptStateDir,
		"KUKEON_HOST":      "unix:///run/kukeon/kukeond/kukeond.sock",
	}
	for key, value := range want {
		if got, set := env[key]; !set || got != value {
			t.Fatalf("env %s = %q (set %v), want %q; env = %v", key, got, set, value, c.Env)
		}
	}
	if _, set := env["PROMPT"]; set {
		t.Fatalf("script cell carries a PROMPT: %v", c.Env)
	}
	if !slices.ContainsFunc(c.Secrets, func(s api.ContainerSecret) bool { return s.Name == "GH_TOKEN" }) {
		t.Fatalf("cell secrets = %+v, want a GH_TOKEN slot", c.Secrets)
	}
	if !slices.Contains(task.Spec.Components, "github") {
		t.Fatalf("components = %v, want github", task.Spec.Components)
	}
	got := map[string]hostMount{}
	for _, volume := range c.Volumes {
		if volume.Kind == api.VolumeKindVolume || volume.Target == "/opt/cni" {
			continue
		}
		got[volume.Target] = hostMount{volume.Source, volume.ReadOnly}
	}
	for target, mount := range mergeMounts(socketMounts, binaryMounts) {
		if got[target] != mount {
			t.Fatalf("host mount %s = %+v, want %+v; mounts = %v", target, got[target], mount, got)
		}
	}
	state, ok := stateVolume(c)
	if !ok || state.Target != scriptStateDir || state.VolumeRef == nil ||
		state.VolumeRef.Name != "root-ticker-script-state" {
		t.Fatalf("state volume = %+v (found %v)", state, ok)
	}
	catalog, err := harness.LoadCatalog(os.DirFS("../../../team/harnesses"), scriptHarness, scriptHarness)
	if err != nil {
		t.Fatal(err)
	}
	if image := catalog.Spec.Images[0].Image; c.Image != image ||
		!strings.HasPrefix(image, "ghcr.io/eminwux/kukeon/base-user:") {
		t.Fatalf("cell image = %q, want the base-user reference %q", c.Image, image)
	}
	if s := script(c); !strings.Contains(s, `exec /bin/bash -c "$COMMAND $ARGS"`) {
		t.Fatalf("onInit = %q", s)
	}
}

// TestScriptTaskAndSettingChecks covers AC4.
func TestScriptTaskAndSettingChecks(t *testing.T) {
	for _, tc := range []struct {
		name, file, old, replacement, want string
		roles                              []config.Role
	}{
		{
			name: "script task names a skill", file: tickerRole,
			old: `args: "--once"`, replacement: `skill: tick, args: "--once"`,
			want: "role ticker: task tick: the script harness runs the role's command; a task names no skill",
		},
		{
			name: "script harness without a command", file: tickerRole,
			old: "    script:\n      command: " + tickerCommand + "\n", replacement: "    script: {}\n",
			want: "compose ticker/script: the script harness requires harnesses.script.command",
		},
		{
			name: "command with a braced reference", file: tickerRole,
			old: "$ROLE_SOURCE_DIR", replacement: "${ROLE_SOURCE_DIR}",
			want: "compose ticker/script: harnesses.script.command: write $VAR, not ${VAR}",
		},
		{
			name: "command on a model harness", file: majordomoRole,
			old: "      settings: config/claude.settings.json\n", replacement: "      settings: config/claude.settings.json\n      command: x\n",
			want:  "compose majordomo/claude: command is a script harness setting; harness claude does not run one",
			roles: []config.Role{config.Majordomo().Spec.Roles[0]},
		},
		{
			name: "model task names an unknown skill", file: majordomoRole,
			old: "  needs:\n", replacement: "  tasks:\n    - { name: pick, skill: pick-issue }\n  needs:\n",
			want:  `role majordomo: task pick: skill "pick-issue" is not one of the role's spec.skills`,
			roles: []config.Role{config.Majordomo().Spec.Roles[0]},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			roles := tc.roles
			if roles == nil {
				roles = []config.Role{tickerSelection()}
			}
			cfg, opts, source := scriptFixture(t, []config.Harness{claudeSelection(), scriptSelection()}, roles...)
			before := string(source[tc.file].Data)
			editFile(source, tc.file, tc.old, tc.replacement)
			if string(source[tc.file].Data) == before {
				t.Fatal("fixture was not edited")
			}
			_, err := compose.Compose(cfg, scriptTeam(cfg), opts)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// TestScriptCommandIsComposed: an operator's spec.parameters cannot replace a
// role's command.
func TestScriptCommandIsComposed(t *testing.T) {
	cfg, opts, _ := scriptFixture(t, []config.Harness{scriptSelection()}, tickerSelection())
	cfg.Spec.Parameters = map[string]string{"COMMAND": "rm -rf /"}
	result, err := compose.Compose(cfg, scriptTeam(cfg), opts)
	if err == nil || result != nil || !strings.Contains(err.Error(), "is set by composition") ||
		!strings.Contains(err.Error(), "COMMAND") {
		t.Fatalf("result=%v err=%v", result, err)
	}
}

// TestScriptHarnessPairing covers AC5: an unpinned role pairs only with the
// team harnesses it declares.
func TestScriptHarnessPairing(t *testing.T) {
	majordomo := config.Majordomo().Spec.Roles[0]
	harnesses := []config.Harness{claudeSelection(), scriptSelection()}

	cfg, opts, _ := scriptFixture(t, harnesses, majordomo, tickerSelection())
	result, err := compose.Compose(cfg, scriptTeam(cfg), opts)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := pairList(result), []string{"majordomo/claude", "ticker/script"}; !slices.Equal(got, want) {
		t.Fatalf("pairs = %v, want %v", got, want)
	}
	for _, note := range result.Notes {
		if strings.Contains(note, "script") && strings.Contains(note, "majordomo") {
			t.Fatalf("skipped pair produced a note: %q", note)
		}
	}

	// A pin on a harness the role does not declare still fails.
	pinned := majordomo
	pinned.Harness = scriptHarness
	cfg, opts, _ = scriptFixture(t, harnesses, pinned)
	if _, err = compose.Compose(cfg, scriptTeam(cfg), opts); err == nil ||
		!strings.Contains(err.Error(), "compose majordomo/script: role does not support selected harness") {
		t.Fatalf("pinned undeclared harness: err = %v", err)
	}

	// A role that declares none of the team's harnesses fails, naming it.
	cfg, opts, _ = scriptFixture(t, []config.Harness{scriptSelection()}, majordomo)
	if _, err = compose.Compose(cfg, scriptTeam(cfg), opts); err == nil ||
		!strings.Contains(err.Error(), `role "majordomo" declares none of the team's harnesses`) {
		t.Fatalf("role with no pair: err = %v", err)
	}
}

// TestScriptRoleNeedsNoAgentsFile covers AC6: a role directory with only
// role.yaml composes on the script harness, while a model harness still
// requires AGENTS.md and each skill's SKILL.md.
func TestScriptRoleNeedsNoAgentsFile(t *testing.T) {
	cfg, opts, source := scriptFixture(t, []config.Harness{scriptSelection()}, tickerSelection())
	for file := range source {
		if strings.HasPrefix(file, "team/roles/ticker/") && file != tickerRole {
			t.Fatalf("ticker fixture holds %s", file)
		}
	}
	if _, err := compose.Compose(cfg, scriptTeam(cfg), opts); err != nil {
		t.Fatalf("role.yaml-only role: %v", err)
	}

	for _, missing := range []string{
		"team/roles/majordomo/AGENTS.md",
		"team/roles/majordomo/skills/diagnose/SKILL.md",
	} {
		cfg, opts, source = scriptFixture(t, []config.Harness{claudeSelection()}, config.Majordomo().Spec.Roles[0])
		delete(source, missing)
		if _, err := compose.Compose(cfg, scriptTeam(cfg), opts); err == nil ||
			!strings.Contains(err.Error(), "missing role asset "+missing) {
			t.Fatalf("model harness without %s: err = %v", missing, err)
		}
	}
}

// agentRoleYAML is a model role on the team's two model harnesses, standing in
// for the agents repository's dev, pm and pr-reviewer roles.
const agentRoleYAML = `apiVersion: v1beta1
kind: Role
metadata: { name: %s }
spec:
  harnesses:
    claude:
      settings: config/claude.settings.json
    codex: {}
`

// pipelineYAML stands in for the agents repository's pipeline role: a program
// role on the script harness whose tick ships switched off.
const pipelineYAML = `apiVersion: v1beta1
kind: Role
metadata: { name: pipeline }
spec:
  harnesses:
    script:
      command: python3 "$ROLE_SOURCE_DIR/$ROLE_PATH/reactor.py"
  connectors: [github]
  needs:
    mounts: [kuke-binary, kukeond-socket]
  tasks:
    - { name: tick, schedule: "*/5 * * * *", suspend: true }
`

// TestKukeonTeamRunsThePipeline composes this repository's own kuketeam.yaml
// (issue #1542): the agent roles pair with claude and codex only, and the
// pipeline role, pinned to script, renders its tick switched on.
func TestKukeonTeamRunsThePipeline(t *testing.T) {
	f, err := os.Open("../../../kuketeam.yaml")
	if err != nil {
		t.Fatal(err)
	}
	team, err := config.DecodeTeam(f)
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	team.Spec.ProjectSource = &config.Source{
		Name: "project", URL: "git@github.com:eminwux/kukeon", Branch: "main", ClonePath: "/home/user/kukeon",
	}
	agents := fstest.MapFS{"pipeline/role.yaml": &fstest.MapFile{Data: []byte(pipelineYAML)}}
	for _, role := range []string{"dev", "pm", "pr-reviewer"} {
		agents[role+"/role.yaml"] = &fstest.MapFile{Data: []byte(fmt.Sprintf(agentRoleYAML, role))}
		agents[role+"/AGENTS.md"] = &fstest.MapFile{Data: []byte("# " + role + "\n")}
		agents[role+"/config/claude.settings.json"] = &fstest.MapFile{Data: []byte("{}\n")}
	}
	opts := compose.Options{Sources: map[string]fs.FS{"agents": agents, "project": copiedSource(t)}}

	result, err := compose.Compose(emptyConfig(), team, opts)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"dev/claude", "dev/codex", "pm/claude", "pm/codex",
		"pr-reviewer/claude", "pr-reviewer/codex", "pipeline/script",
	}
	if got := pairList(result); !slices.Equal(got, want) {
		t.Fatalf("pairs = %v, want %v", got, want)
	}
	var configs []string
	for _, pair := range result.Pairs {
		configs = append(configs, pair.Config.Metadata.Name)
		for _, task := range pair.Tasks {
			configs = append(configs, task.Metadata.Name)
		}
	}
	for _, absent := range []string{
		"kukeon-pipeline-claude", "kukeon-pipeline-codex",
		"kukeon-dev-script", "kukeon-pm-script", "kukeon-pr-reviewer-script",
	} {
		if slices.Contains(configs, absent) {
			t.Fatalf("configs %v include %s", configs, absent)
		}
	}

	pipeline := pairFor(t, result, "pipeline", scriptHarness)
	if got, want := pipeline.Config.Metadata.Name, "kukeon-pipeline-script"; got != want {
		t.Fatalf("pipeline config = %q, want %q", got, want)
	}
	if len(pipeline.Tasks) != 1 {
		t.Fatalf("pipeline tasks = %v", taskNames(pipeline))
	}
	tick := pipeline.Tasks[0]
	if got, want := tick.Metadata.Name, "kukeon-pipeline-script-tick"; got != want {
		t.Fatalf("tick config = %q, want %q", got, want)
	}
	if tick.Spec.Schedule == nil || tick.Spec.Schedule.Cron != "*/5 * * * *" || tick.Spec.Schedule.Suspend {
		t.Fatalf("tick schedule = %+v, want the role's cadence, unsuspended", tick.Spec.Schedule)
	}
}
