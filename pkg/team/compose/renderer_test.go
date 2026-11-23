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
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/eminwux/kukeon/internal/cellblueprint"
	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/compose"
	"github.com/eminwux/kukeon/pkg/team/config"
	"github.com/eminwux/kukeon/pkg/team/harness"
	"gopkg.in/yaml.v3"
)

// The shared renderer must retain resource and binding behavior for both
// execution profiles, independently of each harness's startup implementation.
func TestSharedRendererProfiles(t *testing.T) {
	for _, root := range []bool{false, true} {
		for _, project := range []bool{false, true} {
			cfg, opts := fixture(t)
			allHarnesses(cfg)
			cfg.Spec.Secrets = []config.SecretBinding{routerBinding()}
			source := copiedSource(t)
			repos := ""
			if project {
				repos = "    repos: [project]\n"
				cfg.Spec.Defaults.Sources = append(
					cfg.Spec.Defaults.Sources,
					config.Source{
						Name:      "project",
						URL:       "https://example.test/work",
						Tag:       "v3",
						ClonePath: "/home/user/work",
					},
				)
			}
			source["team/roles/majordomo/role.yaml"].Data = bytes.ReplaceAll(
				source["team/roles/majordomo/role.yaml"].Data,
				[]byte("  needs:"),
				[]byte("  needs:\n"+repos+"    mounts: [ssh]"),
			)
			// The shipped role sets no Codex sandbox; select a narrower one so
			// the root profile's danger-full-access override stays exercised.
			editRole(source, "      approval: on-request", "      sandbox: workspace-write\n      approval: on-request")
			opts.Sources["kukeon"] = source
			if root {
				cfg.Spec.Defaults.Roles[0].Needs.Image = append(cfg.Spec.Defaults.Roles[0].Needs.Image, "root")
			}
			result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Blueprints) != len(result.Pairs) {
				t.Fatalf("blueprints = %d, want one per harness", len(result.Blueprints))
			}
			for _, pair := range result.Pairs {
				assertSharedProfile(t, pair, root, project)
			}
			// `needs.mounts: [ssh]` is still accepted, but mounts nothing: the
			// operator is told once, and no document names the host ~/.ssh.
			if !slices.ContainsFunc(result.Notes, func(note string) bool {
				return strings.Contains(note, "role majordomo") && strings.Contains(note, "no longer mounts")
			}) {
				t.Fatalf("missing ssh-mount note: %v", result.Notes)
			}
			raw, err := result.MarshalYAML()
			if err != nil {
				t.Fatal(err)
			}
			for _, absent := range []string{".ssh", "GIT_SSH_COMMAND", "/host/user"} {
				if bytes.Contains(raw, []byte(absent)) {
					t.Fatalf("rendered documents still contain %q", absent)
				}
			}
		}
	}
}

func TestExternalRendererContract(t *testing.T) {
	cfg, opts := fixture(t)
	source := copiedSource(t)
	opts.Sources["kukeon"] = source
	filename := "team/harnesses/codex/harness.yaml"
	var h harness.Harness
	if err := yaml.Unmarshal(source[filename].Data, &h); err != nil {
		t.Fatal(err)
	}
	// No Make dependency. Preserve order, shell state, and template-like text.
	// Dropping spec.modes makes this a harness with one startup, the form
	// external harness definitions written before modes still use.
	modes := h.Spec.Modes
	h.Spec.Modes = nil
	h.Spec.Shell = "/bin/sh"
	h.Spec.OnInit = []harness.Script{{Script: "set -eu\nvalue='{{ opaque }}'\n"}, {Script: "printf '%s' \"$value\"\n"}}
	raw, err := yaml.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	source[filename].Data = raw
	// External local files cannot override the release-owned template namespace.
	source["team/harnesses/codex/blueprint.tmpl.yaml"] = &fstest.MapFile{Data: []byte("{{ malformed startup")}
	result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
	if err != nil {
		t.Fatal(err)
	}
	// A harness without modes keeps the built-in PROMPT parameter and env.
	if got := cellblueprint.Modes(*result.Pairs[0].Blueprint); len(got) != 0 {
		t.Fatalf("modes = %v, want none", got)
	}
	c := workOf(t, renderCell(t, result.Pairs[0], "", map[string]string{"PROMPT": "legacy prompt"}))
	if c.Command != "/bin/sh" || len(c.Tty.OnInit) != 2 {
		t.Fatal("shell/order lost")
	}
	if envOf(c)["PROMPT"] != "legacy prompt" {
		t.Fatalf("PROMPT = %q", envOf(c)["PROMPT"])
	}
	var joined strings.Builder
	for i, stage := range c.Tty.OnInit {
		if stage.Script != h.Spec.OnInit[i].Script {
			t.Fatal("script was interpolated")
		}
		joined.WriteString(stage.Script)
	}
	cmd := exec.CommandContext(t.Context(), c.Command, "-c", joined.String())
	output, err := cmd.CombinedOutput()
	if err != nil || string(output) != "{{ opaque }}" {
		t.Fatalf("shell state lost: %q %v", output, err)
	}
	// With modes, spec.onInit runs first in every mode, followed by the mode's own.
	withModes := h
	withModes.Spec.Modes = map[string]harness.Mode{}
	for name, mode := range modes {
		mode.OnInit = []harness.Script{{Script: "printf ' %s' " + name + "\n"}}
		withModes.Spec.Modes[name] = mode
	}
	modeRaw, err := yaml.Marshal(withModes)
	if err != nil {
		t.Fatal(err)
	}
	source[filename].Data = modeRaw
	result, err = compose.Compose(cfg, selectedTeam(cfg), opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{interactive, headless} {
		staged := work(t, result.Pairs[0], mode)
		if len(staged.Tty.OnInit) != 3 || staged.Tty.OnInit[0].Script != h.Spec.OnInit[0].Script ||
			staged.Tty.OnInit[2].Script != "printf ' %s' "+mode+"\n" {
			t.Fatalf("%s stages = %+v", mode, staged.Tty.OnInit)
		}
		modeCmd := exec.CommandContext(t.Context(), staged.Command, "-c", script(staged))
		if out, runErr := modeCmd.CombinedOutput(); runErr != nil || string(out) != "{{ opaque }} "+mode {
			t.Fatalf("%s: shared prefix or mode stage lost: %q %v", mode, out, runErr)
		}
	}
	for _, test := range []struct {
		name   string
		mutate func(*harness.Harness)
		want   string
	}{
		{"unsafe-script-parameter", func(h *harness.Harness) { h.Spec.OnInit = []harness.Script{{Script: `echo "${PROMPT}"`}} }, "interpolate a blueprint parameter"},
		{"unknown-environment", func(h *harness.Harness) { h.Spec.Environment["EXTRA"] = "${UNKNOWN}" }, "unsupported environment substitution"},
		{"unsupported-expansion", func(h *harness.Harness) { h.Spec.Environment["EXTRA"] = "${HOME:-/tmp}" }, "unsupported environment substitution"},
		{"reserved-environment", func(h *harness.Harness) { h.Spec.Environment["ROLE_PATH"] = "other" }, "reserved input"},
		{"empty-stage", func(h *harness.Harness) { h.Spec.OnInit = []harness.Script{{}} }, "script must not be empty"},
		{"unsupported-shell", func(h *harness.Harness) { h.Spec.Shell = "/bin/fish" }, "shell"},
		{"unsafe-state", func(h *harness.Harness) { h.Spec.StatePath = "$HOME/../escape" }, "statePath"},
		{"unsafe-mount", func(h *harness.Harness) { h.Spec.Mounts = []harness.Mount{{Source: "relative", Target: "/safe"}} }, "spec.mounts"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var changed harness.Harness
			if decodeErr := yaml.Unmarshal(raw, &changed); decodeErr != nil {
				t.Fatal(decodeErr)
			}
			test.mutate(&changed)
			changedRaw, marshalErr := yaml.Marshal(changed)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			source[filename].Data = changedRaw
			_, composeErr := compose.Compose(cfg, selectedTeam(cfg), opts)
			if composeErr == nil || !strings.Contains(composeErr.Error(), test.want) {
				t.Fatalf("wanted %s: %v", test.want, composeErr)
			}
		})
	}
}

// Execute image-owned setup and the rendered scripts against a role-only source.
// Agent executables observe arguments; they never contact providers. No copy of
// the harness source is present in the role checkout used by setup.
func TestPackagedStartupContract(t *testing.T) {
	for _, name := range []string{"codex", "claude", "opencode"} {
		for _, prompt := range []string{"", "a 'quoted' \"prompt\"\n$(touch should-not-exist)"} {
			for _, conflict := range []bool{false, true} {
				t.Run(
					name+map[bool]string{false: "/clean", true: "/conflict"}[conflict]+map[bool]string{false: "/interactive", true: "/prompt"}[prompt != ""],
					func(t *testing.T) {
						runPackagedStartup(t, name, prompt, conflict)
					},
				)
			}
		}
	}
}

func runPackagedStartup(t *testing.T, name, prompt string, conflict bool) {
	t.Helper()
	root := t.TempDir()
	sourceDir := filepath.Join(root, "role-source")
	if err := os.Mkdir(sourceDir, 0o700); err != nil {
		t.Fatal(err)
	}
	copyRoleSource(t, sourceDir)

	cfg, opts := fixture(t)
	allHarnesses(cfg)
	cfg.Spec.Defaults.Roles[0].Harness = name
	cfg.Spec.Defaults.Sources[0].ClonePath = sourceDir
	cfg.Spec.Secrets = []config.SecretBinding{routerBinding()}
	home := filepath.Join(root, "home")
	initial := filepath.Join(root, "outside-role-checkout")
	bin := filepath.Join(root, "bin")
	for _, dir := range []string{home, initial, bin} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	cfg.Spec.Defaults.Roles[0].InitialPath = initial
	result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
	if err != nil {
		t.Fatal(err)
	}
	// The mode is picked per cell: a prompt makes it a headless run.
	mode, params := interactive, map[string]string(nil)
	if prompt != "" {
		mode, params = headless, map[string]string{"PROMPT": prompt}
	}
	c := workOf(t, renderCell(t, result.Pairs[0], mode, params))
	if got, set := envOf(c)["PROMPT"]; set != (prompt != "") || got != prompt {
		t.Fatalf("%s cell PROMPT = %q (set=%v)", mode, got, set)
	}
	state := filepath.Join(root, "launch-args")
	observer := "#!/bin/sh\npwd > \"$LAUNCH_DIR\"\nprintf '%s\\0' \"$@\" > \"$LAUNCH_STATE\"\nprintf '%s\\n' '{\"type\":\"result\",\"is_error\":false}'\n"
	if err = os.WriteFile(filepath.Join(bin, name), []byte(observer), 0o700); err != nil {
		t.Fatal(err)
	}
	policy := map[string]string{"codex": ".codex/AGENTS.md", "claude": ".claude/CLAUDE.md", "opencode": ".config/opencode/AGENTS.md"}[name]
	credential := filepath.Join(home, "credential")
	if err = os.WriteFile(credential, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if conflict {
		destination := filepath.Join(home, policy)
		if err = os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(destination, []byte("user policy"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	adapter, err := filepath.Abs("../../../team/harnesses/" + name + "/context/Makefile.start")
	if err != nil {
		t.Fatal(err)
	}
	var scripts strings.Builder
	for _, stage := range c.Tty.OnInit {
		scripts.WriteString(
			strings.ReplaceAll(stage.Script, "/opt/kuketeams/harness/Makefile.start", adapter),
		)
		scripts.WriteByte('\n')
	}
	for range 2 {
		cmd := startupCommand(t, c, scripts.String(), home, initial, bin, state)
		output, launchErr := cmd.CombinedOutput()
		if conflict {
			if launchErr == nil || !bytes.Contains(output, []byte("Refusing to replace")) {
				t.Fatalf("conflict ignored: %v %s", launchErr, output)
			}
			if _, err = os.Stat(state); !os.IsNotExist(err) {
				t.Fatal("launched despite setup failure")
			}
			break
		}
		if launchErr != nil {
			t.Fatalf("startup: %v %s", launchErr, output)
		}
		assertObservedLaunch(t, state, prompt, initial)
		assertMaterializedRole(t, home, policy, name)
	}
	if data, readErr := os.ReadFile(credential); readErr != nil || string(data) != "preserve" {
		t.Fatal("credential modified")
	}
	if _, err = os.Stat(filepath.Join(initial, "should-not-exist")); !os.IsNotExist(err) {
		t.Fatal("prompt executed as shell code")
	}
}

func copyRoleSource(t *testing.T, sourceDir string) {
	t.Helper()
	for _, asset := range []string{"Makefile", "team"} {
		cmd := exec.CommandContext(
			t.Context(),
			"cp",
			"-a",
			filepath.Join("../../..", asset),
			sourceDir,
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("copy %s: %v", out, err)
		}
	}
}

func assertObservedLaunch(t *testing.T, state, prompt, initial string) {
	t.Helper()
	args, readErr := os.ReadFile(state)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if prompt != "" && !slices.Contains(strings.Split(string(args), "\x00"), prompt) {
		t.Fatalf("prompt quoting lost: %q", args)
	}
	cwd, readErr := os.ReadFile(state + "-dir")
	if readErr != nil || strings.TrimSpace(string(cwd)) != initial {
		t.Fatal("launch directory changed")
	}
}

func assertMaterializedRole(t *testing.T, home, policy, name string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(home, policy)); err != nil {
		t.Fatal("instructions missing", err)
	}
	skills := ".agents/skills"
	if name == "claude" {
		skills = ".claude/skills"
	}
	if _, err := os.Stat(filepath.Join(home, skills, "create-cell-config/SKILL.md")); err != nil {
		t.Fatal("skills missing", err)
	}
}

func startupCommand(
	t *testing.T,
	c api.ContainerSpec,
	script, home, initial, bin, state string,
) *exec.Cmd {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), c.Command, "-c", script)
	cmd.Dir = initial
	cmd.Env = os.Environ()
	for _, env := range c.Env {
		cmd.Env = append(cmd.Env, strings.ReplaceAll(env, "/home/user", home))
	}
	cmd.Env = append(
		cmd.Env,
		"HOME="+home,
		"PATH="+bin+":"+os.Getenv("PATH"),
		"OPENROUTER_MODEL=test",
		"LAUNCH_STATE="+state,
		"LAUNCH_DIR="+state+"-dir",
		"CLAUDE_CONFIG_DIR="+home+"/.claude",
		"CODEX_HOME="+home+"/.codex",
		"XDG_CONFIG_HOME="+home+"/.config",
	)
	return cmd
}

func assertSharedProfile(t *testing.T, pair compose.Pair, root, project bool) {
	t.Helper()
	// One blueprint per harness × security profile; the posture is fixed in it.
	wantName := pair.Harness
	if root {
		wantName += "-root"
	}
	bp := pair.Blueprint
	if bp.Metadata.Name != wantName || pair.Config.Spec.Blueprint.Name != wantName {
		t.Fatalf("blueprint = %q, want %q", bp.Metadata.Name, wantName)
	}
	if slices.Contains(bp.Spec.Requires, "root") != root || !slices.Contains(bp.Spec.Requires, "git") {
		t.Fatalf("blueprint requires = %v", bp.Spec.Requires)
	}
	template := bp.Spec.Cell.Containers[0]
	if template.Privileged != root || bp.Spec.Cell.NestedCgroupRuntime != root {
		t.Fatalf("posture not fixed in blueprint %s", wantName)
	}
	for _, mode := range []string{interactive, headless} {
		doc := renderCell(t, pair, mode, map[string]string{"PROMPT": "p"})
		c := workOf(t, doc)
		if c.Privileged != root || doc.Spec.NestedCgroupRuntime != root ||
			c.Command != "/bin/bash" ||
			c.WorkingDir != "/home/user" {
			t.Fatalf("profile lost for %s: %+v", pair.Harness, c)
		}
		if len(c.Tmpfs) != map[bool]int{true: 2, false: 0}[root] {
			t.Fatal("tmpfs profile lost")
		}
		if len(c.Repos) != map[bool]int{true: 2, false: 1}[project] {
			t.Fatalf("cloned repositories changed: %+v", c.Repos)
		}
		foundState := false
		for _, m := range c.Volumes {
			if strings.Contains(m.Source, ".ssh") || strings.Contains(m.Target, ".ssh") {
				t.Fatalf("host ssh directory mounted: %+v", m)
			}
			if m.Kind == api.VolumeKindVolume {
				foundState = true
				if !m.Ensure || m.Target != "/home/user/."+pair.Harness {
					t.Fatalf("state binding changed: %+v", m)
				}
			}
		}
		if foundState != (pair.Harness != "opencode") {
			t.Fatal("mounts lost")
		}
		env := envOf(c)
		if _, set := env["GIT_SSH_COMMAND"]; set {
			t.Fatal("GIT_SSH_COMMAND reached a cell")
		}
		if _, sandboxed := env["IS_SANDBOX"]; sandboxed != root {
			t.Fatalf("IS_SANDBOX set = %v, want %v", sandboxed, root)
		}
		if pair.Harness == "codex" {
			want := "workspace-write"
			if root {
				want = "danger-full-access"
			}
			if env["CODEX_SANDBOX"] != want {
				t.Fatalf("CODEX_SANDBOX = %q, want %s", env["CODEX_SANDBOX"], want)
			}
		}
	}
	if len(pair.Config.Spec.Repos) != map[bool]int{true: 2, false: 1}[project] {
		t.Fatal("repository bindings changed")
	}
	if project && pair.Config.Spec.Repos["project"].Ref != "v3" {
		t.Fatal("source revision lost")
	}
	wantRequires := []string{"go"}
	if !slices.Equal(pair.Config.Spec.Requires, wantRequires) {
		t.Fatalf("config requires = %v, want %v (root is the blueprint's)", pair.Config.Spec.Requires, wantRequires)
	}
	if root && !slices.Contains(pair.Image.Capabilities, "root") {
		t.Fatalf("root profile selected a non-root image: %+v", pair.Image)
	}
}
