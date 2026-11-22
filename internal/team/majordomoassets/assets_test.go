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

// Package majordomoassets_test checks the shipped role and native harness layouts.
package majordomoassets_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/pkg/team/config"
	"gopkg.in/yaml.v3"
)

func read(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

type definition struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Skills    []string `yaml:"skills"`
		Harnesses map[string]struct {
			Settings    string   `yaml:"settings"`
			Sandbox     string   `yaml:"sandbox"`
			Approval    string   `yaml:"approval"`
			Permissions string   `yaml:"permissions"`
			Secrets     []string `yaml:"secrets"`
		} `yaml:"harnesses"`
		Needs struct {
			Image   []string `yaml:"image"`
			Repos   []string `yaml:"repos"`
			Mounts  []string `yaml:"mounts"`
			Params  []string `yaml:"params"`
			Secrets []string `yaml:"secrets"`
		} `yaml:"needs"`
		Tasks []struct {
			Name     string `yaml:"name"`
			Skill    string `yaml:"skill"`
			Args     string `yaml:"args"`
			Schedule string `yaml:"schedule"`
			Suspend  bool   `yaml:"suspend"`
		} `yaml:"tasks"`
	} `yaml:"spec"`
}

func TestDefaultRoleAssets(t *testing.T) {
	// Resolve the actual initial majordomo selection, rather than duplicating its path
	// in a fixture that could pass while init points at a different role.
	roles := config.Majordomo().Spec.Roles
	if len(roles) != 1 {
		t.Fatalf("default roles: %+v", roles)
	}
	selection := roles[0]
	if selection.Name != "majordomo" || selection.Source != "kukeon" {
		t.Fatalf("selection: %+v", selection)
	}
	root := repoRoot(t)
	roleDir := filepath.Join(root, selection.Path)
	var role definition
	decoder := yaml.NewDecoder(bytes.NewReader(read(t, filepath.Join(roleDir, "role.yaml"))))
	decoder.KnownFields(true)
	if decodeErr := decoder.Decode(&role); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if role.APIVersion != "kuketeams.io/v1" || role.Kind != "Role" || role.Metadata.Name != selection.Name {
		t.Fatalf("invalid role identity: %+v", role)
	}
	if !reflect.DeepEqual(role.Spec.Needs.Image, selection.Needs.Image) {
		t.Fatalf("role capabilities %v differ from the init selection %v", role.Spec.Needs.Image, selection.Needs.Image)
	}
	if len(role.Spec.Needs.Repos)+len(role.Spec.Needs.Secrets) != 0 {
		t.Fatal("majordomo should not require project repos or non-provider secrets")
	}
	// Daemon access comes from the socket and client mounts alone (issue
	// #1509); the host's /opt/kukeon is not mounted (issue #1537).
	if want := []string{"kukeond-socket", "kuke-binary"}; !reflect.DeepEqual(
		role.Spec.Needs.Mounts,
		want,
	) {
		t.Fatalf("mounts = %v, want %v", role.Spec.Needs.Mounts, want)
	}
	if len(role.Spec.Skills) != 5 {
		t.Fatalf("skills: %v", role.Spec.Skills)
	}
	// The health sweep ships scheduled but switched off, so no install spends
	// model credits until its user turns it on.
	if len(role.Spec.Tasks) != 1 || role.Spec.Tasks[0].Name != "health-sweep" ||
		role.Spec.Tasks[0].Skill != "health-sweep" || role.Spec.Tasks[0].Schedule != "@hourly" ||
		!role.Spec.Tasks[0].Suspend || role.Spec.Tasks[0].Args != "" {
		t.Fatalf("tasks: %+v", role.Spec.Tasks)
	}
	for _, path := range role.Spec.Skills {
		if filepath.IsAbs(path) || !filepath.IsLocal(path) {
			t.Fatalf("nonlocal skill: %s", path)
		}
		skillDir := filepath.Join(root, path)
		rel, relErr := filepath.Rel(roleDir, skillDir)
		if relErr != nil || !filepath.IsLocal(rel) {
			t.Fatalf("skill outside role: %s", path)
		}
		skill := read(t, filepath.Join(skillDir, "SKILL.md"))
		parts := bytes.SplitN(skill, []byte("---\n"), 3)
		if len(parts) != 3 || len(parts[0]) != 0 {
			t.Fatal("missing frontmatter")
		}
		var front struct {
			Name        string `yaml:"name"`
			Description string `yaml:"description"`
		}
		if frontErr := yaml.Unmarshal(parts[1], &front); frontErr != nil {
			t.Fatal(frontErr)
		}
		if front.Name != filepath.Base(skillDir) || len(front.Description) == 0 {
			t.Fatalf("frontmatter: %+v", front)
		}
		links := regexp.MustCompile(`\]\(([^)]+)\)`).FindAllSubmatch(parts[2], -1)
		if len(links) == 0 {
			t.Fatal("missing grounding reference")
		}
		for _, link := range links {
			target := string(link[1])
			if !filepath.IsLocal(target) {
				t.Fatalf("nonlocal skill reference: %s", target)
			}
			_ = read(t, filepath.Join(skillDir, target))
		}
	}
	if !bytes.Equal(read(t, filepath.Join(roleDir, "AGENTS.md")), read(t, filepath.Join(roleDir, "CLAUDE.md"))) {
		t.Fatal("Claude adapter differs from canonical instructions")
	}
	checkHarnessSettings(t, role, roleDir)
}

func checkHarnessSettings(t *testing.T, role definition, roleDir string) {
	t.Helper()
	wantSecrets := map[string][]string{
		"claude":   nil,
		"codex":    nil,
		"opencode": {"openrouter-api-key"},
	}
	if len(role.Spec.Harnesses) != len(wantSecrets) {
		t.Fatal("unexpected harness selection")
	}
	for name, secret := range wantSecrets {
		h, ok := role.Spec.Harnesses[name]
		if !ok || !reflect.DeepEqual(h.Secrets, secret) {
			t.Fatalf("%s provider secrets: %v", name, h.Secrets)
		}
	}
	var settings struct {
		Permissions struct {
			DefaultMode string   `json:"defaultMode"`
			Allow       []string `json:"allow"`
		} `json:"permissions"`
	}
	settingsPath := role.Spec.Harnesses["claude"].Settings
	if !filepath.IsLocal(settingsPath) {
		t.Fatal("Claude settings must be role-relative")
	}
	if err := json.Unmarshal(read(t, filepath.Join(roleDir, settingsPath)), &settings); err != nil {
		t.Fatal(err)
	}
	if settings.Permissions.DefaultMode != "default" {
		t.Fatal("Claude settings bypass permissions")
	}
	// Reading a cell's screen is pre-approved; typing into one is not, so
	// Claude asks for every kuke write and the unattended sweep is denied
	// (issue #1538).
	if !slices.Contains(settings.Permissions.Allow, "Bash(kuke screenshot:*)") {
		t.Fatalf("Claude settings do not allow kuke screenshot: %v", settings.Permissions.Allow)
	}
	for _, rule := range settings.Permissions.Allow {
		if strings.Contains(rule, "kuke write") || rule == "Bash" || rule == "Bash(*)" ||
			rule == "Bash(kuke:*)" || rule == "Bash(kuke *)" {
			t.Fatalf("Claude settings allow kuke write: %q", rule)
		}
	}
	codex := role.Spec.Harnesses["codex"]
	if codex.Sandbox != "workspace-write" || codex.Approval != "on-request" {
		t.Fatal("unexpected Codex permissions")
	}
	if role.Spec.Harnesses["opencode"].Permissions != "ask" {
		t.Fatal("OpenCode permissions should ask")
	}
	var oc struct {
		Permission string `json:"permission"`
		Model      string `json:"model"`
		Provider   map[string]struct {
			Options struct {
				APIKey string `json:"apiKey"`
			} `json:"options"`
		} `json:"provider"`
	}
	if err := json.Unmarshal(read(t, filepath.Join(roleDir, "config/opencode.json")), &oc); err != nil {
		t.Fatal(err)
	}
	if oc.Permission != "ask" || oc.Model != "{env:OPENROUTER_MODEL}" ||
		oc.Provider["openrouter"].Options.APIKey != "{env:OPENROUTER_API_KEY}" {
		t.Fatal("OpenCode config should use permission checks and environment credential references")
	}
}

func runMake(t *testing.T, dir, home, target string, overrides ...string) ([]byte, error) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "make", "-C", dir, target)
	cmd.Env = append(
		os.Environ(),
		"HOME="+home,
		"ROLE=team/roles/majordomo",
		"CODEX_HOME=",
		"CLAUDE_CONFIG_DIR=",
		"XDG_CONFIG_HOME=",
		"OPENCODE_CONFIG=",
	)
	cmd.Env = append(cmd.Env, overrides...)
	return cmd.CombinedOutput()
}

// Copy only the installer and role assets so the test cannot accidentally
// depend on unrelated files in the rest of the checkout.
func copyMaterializationSource(t *testing.T) string {
	t.Helper()
	sourceDir := filepath.Join(t.TempDir(), "source with spaces")
	root := repoRoot(t)
	for _, path := range []string{"Makefile", "team/team.mk", "team/scripts/materialize-role.sh", "team/roles/majordomo"} {
		copyAsset(t, filepath.Join(root, path), filepath.Join(sourceDir, path))
	}
	return sourceDir
}

func copyAsset(t *testing.T, source, destination string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		t.Fatal(err)
	}
	copyCmd := exec.CommandContext(t.Context(), "cp", "-a", source, destination)
	if output, err := copyCmd.CombinedOutput(); err != nil {
		t.Fatalf("copy: %s: %v", output, err)
	}
}

func TestMaterialization(t *testing.T) {
	for _, harness := range []string{"claude", "codex", "opencode-openrouter"} {
		t.Run(harness, func(t *testing.T) {
			// A copied source with the shared installer must work offline,
			// independently of any agents checkout, harness catalog, or daemon.
			sourceDir := copyMaterializationSource(t)
			roleDir := filepath.Join(sourceDir, "team/roles/majordomo")
			for _, custom := range []bool{false, true} {
				t.Run(map[bool]string{false: "native", true: "overrides"}[custom], func(t *testing.T) {
					home := filepath.Join(t.TempDir(), "cell home")
					configDir := filepath.Join(home, ".claude")
					policyName := "CLAUDE.md"
					skills := filepath.Join(configDir, "skills")
					var overrides []string
					switch harness {
					case "claude":
						if custom {
							configDir = filepath.Join(home, "custom claude")
							overrides = []string{"CLAUDE_CONFIG_DIR=" + configDir}
							skills = filepath.Join(configDir, "skills")
						}
					case "codex":
						configDir = filepath.Join(home, ".codex")
						if custom {
							configDir = filepath.Join(home, "custom codex")
							overrides = []string{"CODEX_HOME=" + configDir}
						}
						policyName = "AGENTS.md"
						skills = filepath.Join(home, ".agents/skills")
					case "opencode-openrouter":
						configDir = filepath.Join(home, ".config/opencode")
						if custom {
							configDir = filepath.Join(home, "custom xdg/opencode")
							overrides = []string{
								"XDG_CONFIG_HOME=" + filepath.Dir(configDir),
								"OPENCODE_CONFIG=" + filepath.Join(home, "custom settings.json"),
							}
						}
						policyName = "AGENTS.md"
						skills = filepath.Join(home, ".agents/skills")
					}
					for _, makeDir := range []string{roleDir, sourceDir} {
						if output, err := runMake(t, makeDir, home, harness, overrides...); err != nil {
							t.Fatalf("install: %s: %v", output, err)
						}
					}
					if !bytes.Equal(
						read(t, filepath.Join(configDir, policyName)),
						read(t, filepath.Join(roleDir, "AGENTS.md")),
					) {
						t.Fatal("native policy differs")
					}
					installed := filepath.Join(skills, "create-cell-config")
					if !bytes.Equal(
						read(t, filepath.Join(installed, "SKILL.md")),
						read(t, filepath.Join(roleDir, "skills/create-cell-config/SKILL.md")),
					) {
						t.Fatal("skill differs")
					}
					_ = read(t, filepath.Join(installed, "references/kukeon.md"))
					if harness == "opencode-openrouter" {
						settings := filepath.Join(configDir, "opencode.json")
						if custom {
							settings = filepath.Join(home, "custom settings.json")
						}
						if !bytes.Equal(read(t, settings), read(t, filepath.Join(roleDir, "config/opencode.json"))) {
							t.Fatal("OpenCode settings differ")
						}
					}
				})
			}
		})
	}
}

func TestConflictsPreserveHome(t *testing.T) {
	sourceDir := repoRoot(t)
	for _, tc := range []struct{ target, conflict, absent string }{
		{"claude", ".claude/CLAUDE.md", ".claude/skills/create-cell-config"},
		{"codex", ".codex/AGENTS.md", ".agents/skills/create-cell-config"},
		{"codex", ".codex/AGENTS.override.md", ".agents/skills/create-cell-config"},
		{"codex", ".agents/skills/create-cell-config", ".codex/AGENTS.md"},
		{"opencode-openrouter", ".config/opencode/opencode.json", ".config/opencode/AGENTS.md"},
	} {
		t.Run(tc.conflict, func(t *testing.T) {
			home := t.TempDir()
			conflict := filepath.Join(home, tc.conflict)
			if err := os.MkdirAll(filepath.Dir(conflict), 0o700); err != nil {
				t.Fatal(err)
			}
			original := []byte("user-owned content\n")
			if err := os.WriteFile(conflict, original, 0o600); err != nil {
				t.Fatal(err)
			}
			if output, err := runMake(t, sourceDir, home, tc.target); err == nil {
				t.Fatalf("expected conflict, got %s", output)
			}
			if !bytes.Equal(read(t, conflict), original) {
				t.Fatal("user asset changed")
			}
			if _, err := os.Lstat(filepath.Join(home, tc.absent)); !os.IsNotExist(err) {
				t.Fatalf("partial install before conflict: %v", err)
			}
		})
	}
}

// Native installation targets must agree with the packaged Harness skill paths.
func TestHarnessContracts(t *testing.T) {
	root := repoRoot(t)
	for _, name := range []string{"claude", "codex", "opencode"} {
		t.Run(name, func(t *testing.T) {
			manifest := filepath.Join(root, "team", "harnesses", name, "harness.yaml")
			var harness struct {
				Spec struct {
					SkillPath string `yaml:"skillPath"`
				} `yaml:"spec"`
			}
			if err := yaml.Unmarshal(read(t, manifest), &harness); err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(harness.Spec.SkillPath, "$HOME/") {
				t.Fatalf("unexpected skill path: %s", harness.Spec.SkillPath)
			}
			home := t.TempDir()
			if output, err := runMake(t, root, home, map[string]string{"claude": "claude", "codex": "codex", "opencode": "opencode-openrouter"}[name]); err != nil {
				t.Fatalf("root make target: %s: %v", output, err)
			}
			installed := filepath.Join(
				home,
				strings.TrimPrefix(harness.Spec.SkillPath, "$HOME/"),
				"create-cell-config/SKILL.md",
			)
			source := filepath.Join(root, "team/roles/majordomo/skills/create-cell-config/SKILL.md")
			if !bytes.Equal(read(t, installed), read(t, source)) {
				t.Fatal("skill not installed at harness-declared path")
			}
		})
	}
}

func TestMultipleRoles(t *testing.T) {
	sourceDir := copyMaterializationSource(t)
	copyAsset(t, "testdata/inspector", filepath.Join(sourceDir, "team/roles/inspector"))
	for _, harness := range []string{"claude", "codex", "opencode-openrouter"} {
		t.Run(harness, func(t *testing.T) {
			for _, role := range []string{"majordomo", "inspector"} {
				t.Run(role, func(t *testing.T) {
					home := t.TempDir()
					configDir := filepath.Join(home, ".claude")
					policy := filepath.Join(configDir, "CLAUDE.md")
					skills := filepath.Join(configDir, "skills")
					switch harness {
					case "codex":
						policy = filepath.Join(home, ".codex/AGENTS.md")
						skills = filepath.Join(home, ".agents/skills")
					case "opencode-openrouter":
						configDir = filepath.Join(home, ".config/opencode")
						policy = filepath.Join(configDir, "AGENTS.md")
						skills = filepath.Join(home, ".agents/skills")
					}
					other := filepath.Join(skills, "user-skill/SKILL.md")
					if err := os.MkdirAll(filepath.Dir(other), 0o700); err != nil {
						t.Fatal(err)
					}
					original := []byte("user skill\n")
					if err := os.WriteFile(other, original, 0o600); err != nil {
						t.Fatal(err)
					}
					for range 2 {
						if output, err := runMake(t, sourceDir, home, harness, "ROLE=team/roles/"+role); err != nil {
							t.Fatalf("install: %s: %v", output, err)
						}
					}
					roleDir := filepath.Join(sourceDir, "team/roles", role)
					if !bytes.Equal(read(t, policy), read(t, filepath.Join(roleDir, "AGENTS.md"))) {
						t.Fatal("wrong role policy")
					}
					entries, err := os.ReadDir(filepath.Join(roleDir, "skills"))
					if err != nil {
						t.Fatal(err)
					}
					for _, entry := range entries {
						path := filepath.Join(entry.Name(), "SKILL.md")
						if !bytes.Equal(
							read(t, filepath.Join(skills, path)),
							read(t, filepath.Join(roleDir, "skills", path)),
						) {
							t.Fatalf("wrong skill: %s", entry.Name())
						}
					}
					installed, err := os.ReadDir(skills)
					if err != nil {
						t.Fatal(err)
					}
					if len(installed) != len(entries)+1 {
						t.Fatalf("unexpected skills: %v", installed)
					}
					if !bytes.Equal(read(t, other), original) {
						t.Fatal("unrelated user skill changed")
					}
					if harness == "opencode-openrouter" && role == "inspector" {
						if _, statErr := os.Lstat(filepath.Join(configDir, "opencode.json")); !os.IsNotExist(statErr) {
							t.Fatalf("config installed for role without config: %v", statErr)
						}
					}
				})
			}
		})
	}
}

// TestLegacyOperatorPathResolves keeps team/roles/operator readable. Releases
// up to v0.10.0 select that path on the main branch from `kuke team init`, and
// their kukeon-operator-* cells materialize the role from it at start, so
// removing it would break installs that have not upgraded.
func TestLegacyOperatorPathResolves(t *testing.T) {
	root := repoRoot(t)
	legacy := read(t, filepath.Join(root, "team/roles/operator/role.yaml"))
	current := read(t, filepath.Join(root, "team/roles/majordomo/role.yaml"))
	if !bytes.Equal(legacy, current) {
		t.Fatal("team/roles/operator must resolve to the majordomo role")
	}
}
