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
			opts.HomeDir = "/host/user"
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
			source["team/roles/operator/role.yaml"].Data = bytes.ReplaceAll(
				source["team/roles/operator/role.yaml"].Data,
				[]byte("  needs:"),
				[]byte("  needs:\n"+repos+"    mounts: [ssh]"),
			)
			opts.Sources["kukeon"] = source
			if root {
				cfg.Spec.Defaults.Roles[0].Needs.Image = append(cfg.Spec.Defaults.Roles[0].Needs.Image, "root")
			}
			result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
			if err != nil {
				t.Fatal(err)
			}
			for _, pair := range result.Pairs {
				assertSharedProfile(t, pair, root, project)
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
	c := result.Pairs[0].Blueprint.Spec.Cell.Containers[0]
	if c.Command != "/bin/sh" || len(c.Tty.OnInit) != 2 {
		t.Fatal("shell/order lost")
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
	c := result.Pairs[0].Blueprint.Spec.Cell.Containers[0]
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
		cmd := startupCommand(t, c, scripts.String(), home, initial, bin, state, prompt)
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
	c api.BlueprintContainer,
	script, home, initial, bin, state, prompt string,
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
		"PROMPT="+prompt,
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
	c := pair.Blueprint.Spec.Cell.Containers[0]
	if c.Privileged != root || pair.Blueprint.Spec.Cell.NestedCgroupRuntime != root ||
		c.Command != "/bin/bash" ||
		c.WorkingDir != "/home/user" {
		t.Fatalf("profile lost for %s: %+v", pair.Harness, c)
	}
	if len(c.Tmpfs) != map[bool]int{true: 2, false: 0}[root] {
		t.Fatal("tmpfs profile lost")
	}
	if len(pair.Config.Spec.Repos) != map[bool]int{true: 2, false: 1}[project] {
		t.Fatal("repository bindings changed")
	}
	if project && pair.Config.Spec.Repos["project"].Ref != "v3" {
		t.Fatal("source revision lost")
	}
	foundSSH, foundState := false, false
	for _, m := range c.Volumes {
		if m.Target == "/home/user/.ssh" {
			foundSSH = m.Source == "/host/user/.ssh" && m.ReadOnly
		}
		if m.Kind == api.VolumeKindVolume {
			foundState = true
			if !m.Ensure || m.Target != "/home/user/."+pair.Harness {
				t.Fatalf("state binding changed: %+v", m)
			}
		}
	}
	if !foundSSH || foundState != (pair.Harness != "opencode") {
		t.Fatal("mounts lost")
	}
	if pair.Harness == "codex" {
		want := "CODEX_SANDBOX=workspace-write"
		if root {
			want = "CODEX_SANDBOX=danger-full-access"
		}
		if !slices.Contains(c.Env, want) {
			t.Fatalf("missing %s", want)
		}
	}
}
