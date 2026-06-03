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

package team

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/teamhost"
	"github.com/eminwux/kukeon/internal/teamsource"
	model "github.com/eminwux/kukeon/pkg/api/model/kuketeams"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"gopkg.in/yaml.v3"
)

const projectTeamYAML = `apiVersion: kuketeams.io/v1
kind: ProjectTeam
metadata: { name: sbsh }
spec:
  source: eminwux/agents@v1.4.0
  roles:
    - { ref: dev }
`

// projectTeamWithHarnessYAML carries a harness default so the render
// pipeline produces at least one (role × harness) pair.
const projectTeamWithHarnessYAML = `apiVersion: kuketeams.io/v1
kind: ProjectTeam
metadata: { name: sbsh }
spec:
  source: eminwux/agents@v1.4.0
  defaults:
    harnesses: [claude]
  roles:
    - { ref: dev }
`

// stubGit returns a GitConfigFunc backed by m.
func stubGit(m map[string]string) GitConfigFunc {
	return func(_ context.Context, key string) (string, bool) {
		v, ok := m[key]
		return v, ok
	}
}

// stubProjectURL returns a ProjectRepoURLFunc that always reports url.
func stubProjectURL(url string) ProjectRepoURLFunc {
	return func(_ context.Context, _ string) (string, bool) {
		if url == "" {
			return "", false
		}
		return url, true
	}
}

// stubResolveErr returns a ResolveFunc that always fails — used by tests
// that should never reach the resolve step.
func stubResolveErr() ResolveFunc {
	return func(_ context.Context, _ teamsource.Cache, _ *model.TeamsConfig, _ *model.ProjectTeam) (*teamsource.Bundle, error) {
		return nil, errors.New("resolve must not be called")
	}
}

// stubBundle returns a ResolveFunc that yields a pre-built bundle. The
// bundle's cacheDir is a tempdir the test writes a single harness blueprint
// template into so RenderBlueprint can read it.
func stubBundle(b *teamsource.Bundle) ResolveFunc {
	return func(_ context.Context, _ teamsource.Cache, _ *model.TeamsConfig, _ *model.ProjectTeam) (*teamsource.Bundle, error) {
		return b, nil
	}
}

// writeProject creates a project dir with a kuketeam.yaml and returns its path.
func writeProject(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, projectFileName), []byte(body), 0o600); err != nil {
		t.Fatalf("write project file: %v", err)
	}
	return dir
}

// buildClaudeBundle writes a minimal claude harness blueprint template,
// role, and image catalog into a fresh temp dir and returns a bundle that
// points at it.
func buildClaudeBundle(t *testing.T) *teamsource.Bundle {
	t.Helper()
	cacheDir := t.TempDir()
	tplPath := filepath.Join(cacheDir, "harnesses", "claude", "blueprint.tmpl.yaml")
	if err := os.MkdirAll(filepath.Dir(tplPath), 0o700); err != nil {
		t.Fatalf("mkdir tpl: %v", err)
	}
	tpl := `apiVersion: v1beta1
kind: CellBlueprint
metadata:
  name: ${ROLE}-${HARNESS}
spec:
  cell:
    containers:
      - id: ${ROLE}
        image: ${IMAGE}
        env:
          - "ROLE=${ROLE}"
          - "NEEDS=${NEEDS}"
          - "SETTINGS=${SETTINGS}"
        repos:
          - { name: project, target: /src/project }
        secrets:
          - { name: ANTHROPIC_API_KEY, mode: env, envName: ANTHROPIC_API_KEY }
`
	if err := os.WriteFile(tplPath, []byte(tpl), 0o600); err != nil {
		t.Fatalf("write tpl: %v", err)
	}
	return &teamsource.Bundle{
		Source: teamsource.Source{
			Raw:       "eminwux/agents@v1.4.0",
			OwnerRepo: "eminwux/agents",
			Version:   "v1.4.0",
		},
		CacheDir: cacheDir,
		Roles: map[string]*model.Role{
			"dev": {
				APIVersion: model.APIVersionV1,
				Kind:       model.KindRole,
				Metadata:   model.Metadata{Name: "dev"},
				Spec: model.RoleSpec{
					Harnesses: map[string]model.RoleHarness{
						"claude": {Settings: "agents/dev/settings.json"},
					},
					Needs: model.RoleNeeds{
						Image:   []string{"go", "git"},
						Secrets: []string{"ANTHROPIC_API_KEY"},
					},
				},
			},
		},
		Harnesses: map[string]*model.Harness{
			"claude": {
				APIVersion: model.APIVersionV1,
				Kind:       model.KindHarness,
				Metadata:   model.Metadata{Name: "claude"},
				Spec: model.HarnessSpec{
					SkillPath:  "/.claude/skills",
					MakeTarget: "harness-claude",
					Template:   "harnesses/claude/blueprint.tmpl.yaml",
				},
			},
		},
		ImageCatalog: &model.ImageCatalog{
			APIVersion: model.APIVersionV1,
			Kind:       model.KindImageCatalog,
			Spec: model.ImageCatalogSpec{
				Images: []model.ImageCatalogEntry{
					{
						Ref:          "claude-go",
						Harness:      "claude",
						Image:        "registry.local/claude-go:latest",
						Build:        model.ImageCatalogBuild{Context: "harnesses/claude", Dockerfile: "Dockerfile"},
						Capabilities: []string{"go", "git", "make"},
					},
				},
			},
		},
	}
}

func TestComposeTeamNoProjectFile(t *testing.T) {
	t.Parallel()
	emptyDir := t.TempDir()
	layout := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	err := composeTeam(
		context.Background(), &bytes.Buffer{}, emptyDir, layout,
		stubGit(nil), stubProjectURL(""), stubResolveErr(), false,
	)
	if !errors.Is(err, errdefs.ErrTeamProjectFileNotFound) {
		t.Fatalf("err = %v, want ErrTeamProjectFileNotFound", err)
	}
}

func TestComposeTeamWrongKind(t *testing.T) {
	t.Parallel()
	dir := writeProject(t, "apiVersion: kuketeams.io/v1\nkind: TeamsConfig\nspec: {}\n")
	layout := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	err := composeTeam(
		context.Background(), &bytes.Buffer{}, dir, layout,
		stubGit(nil), stubProjectURL(""), stubResolveErr(), false,
	)
	if !errors.Is(err, errdefs.ErrTeamProjectFileKind) {
		t.Fatalf("err = %v, want ErrTeamProjectFileKind", err)
	}
}

func TestComposeTeamScaffoldsAndWrites(t *testing.T) {
	t.Parallel()
	projectDir := writeProject(t, projectTeamYAML)
	layout := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	git := stubGit(map[string]string{
		"user.name":                  "Op Erator",
		"user.email":                 "op@example.com",
		"user.signingkey":            "/home/op/.ssh/id_ed25519.pub",
		"commit.gpgsign":             "true",
		"tag.gpgsign":                "true",
		"gpg.ssh.allowedSignersFile": "/home/op/.ssh/allowed_signers",
	})

	var out bytes.Buffer
	if err := composeTeam(
		context.Background(), &out, projectDir, layout,
		git, stubProjectURL(""), stubResolveErr(), false,
	); err != nil {
		t.Fatalf("composeTeam: %v", err)
	}

	// Global facts scaffolded with seeded git identity + signing.
	rawGlobal, err := os.ReadFile(layout.GlobalConfigPath())
	if err != nil {
		t.Fatalf("global config not written: %v", err)
	}
	var gc model.TeamsConfig
	if unmarshalErr := yaml.Unmarshal(rawGlobal, &gc); unmarshalErr != nil {
		t.Fatalf("global config parse: %v", unmarshalErr)
	}
	if gc.Spec.Git == nil || gc.Spec.Git.Author == nil || gc.Spec.Git.Author.Email != "op@example.com" {
		t.Fatalf("git identity not seeded: %+v", gc.Spec.Git)
	}
	if gc.Spec.Git.SigningKey != "/home/op/.ssh/id_ed25519.pub" {
		t.Errorf("signing key not seeded: %q", gc.Spec.Git.SigningKey)
	}
	if len(gc.Spec.Git.Sign) != 2 {
		t.Errorf("git.sign = %v, want [commits tags]", gc.Spec.Git.Sign)
	}
	if gc.Spec.Git.AllowedSigners != "/home/op/.ssh/allowed_signers" {
		t.Errorf("allowedSigners not seeded: %q", gc.Spec.Git.AllowedSigners)
	}

	// Per-project entry written with locator + source.
	rawEntry, err := os.ReadFile(layout.EntryPath("sbsh"))
	if err != nil {
		t.Fatalf("entry not written: %v", err)
	}
	var te model.TeamEntry
	if unmarshalErr := yaml.Unmarshal(rawEntry, &te); unmarshalErr != nil {
		t.Fatalf("entry parse: %v", unmarshalErr)
	}
	if te.Metadata.Name != "sbsh" || te.Spec.Path != projectDir || te.Spec.Source != "eminwux/agents@v1.4.0" {
		t.Errorf("entry content wrong: %+v", te)
	}
	if !strings.Contains(out.String(), "wrote team \"sbsh\"") {
		t.Errorf("missing write confirmation in output: %q", out.String())
	}
}

func TestComposeTeamReRunDoesNotRescaffold(t *testing.T) {
	t.Parallel()
	projectDir := writeProject(t, projectTeamYAML)
	layout := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	git := stubGit(map[string]string{"user.name": "A", "user.email": "a@example.com"})

	if err := composeTeam(
		context.Background(), &bytes.Buffer{}, projectDir, layout,
		git, stubProjectURL(""), stubResolveErr(), false,
	); err != nil {
		t.Fatalf("first composeTeam: %v", err)
	}
	// Tamper the global facts; a re-run must leave them untouched.
	if err := os.WriteFile(layout.GlobalConfigPath(), []byte(
		"apiVersion: kuketeams.io/v1\nkind: TeamsConfig\nspec:\n  registry: sentinel.example.com\n",
	), 0o600); err != nil {
		t.Fatalf("seed sentinel: %v", err)
	}
	var out bytes.Buffer
	if err := composeTeam(
		context.Background(), &out, projectDir, layout,
		git, stubProjectURL(""), stubResolveErr(), false,
	); err != nil {
		t.Fatalf("second composeTeam: %v", err)
	}
	got, err := os.ReadFile(layout.GlobalConfigPath())
	if err != nil {
		t.Fatalf("read global after re-run: %v", err)
	}
	if !strings.Contains(string(got), "sentinel.example.com") {
		t.Errorf("re-run re-scaffolded global facts: %q", got)
	}
	if strings.Contains(out.String(), "scaffolded") {
		t.Errorf("re-run printed a scaffold message: %q", out.String())
	}
}

func TestComposeTeamNoGitIdentityOmitsGitBlock(t *testing.T) {
	t.Parallel()
	projectDir := writeProject(t, projectTeamYAML)
	layout := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))

	if err := composeTeam(
		context.Background(), &bytes.Buffer{}, projectDir, layout,
		stubGit(nil), stubProjectURL(""), stubResolveErr(), false,
	); err != nil {
		t.Fatalf("composeTeam: %v", err)
	}
	raw, err := os.ReadFile(layout.GlobalConfigPath())
	if err != nil {
		t.Fatalf("global config not written: %v", err)
	}
	var gc model.TeamsConfig
	if unmarshalErr := yaml.Unmarshal(raw, &gc); unmarshalErr != nil {
		t.Fatalf("parse: %v", unmarshalErr)
	}
	if gc.Spec.Git != nil {
		t.Errorf("git block seeded with no identity available: %+v", gc.Spec.Git)
	}
}

func TestComposeTeamDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	projectDir := writeProject(t, projectTeamYAML)
	layout := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))

	var out bytes.Buffer
	if err := composeTeam(
		context.Background(), &out, projectDir, layout,
		stubGit(nil), stubProjectURL(""), stubResolveErr(), true,
	); err != nil {
		t.Fatalf("composeTeam dry-run: %v", err)
	}
	if _, err := os.Stat(layout.GlobalConfigPath()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("dry-run wrote global config: err=%v", err)
	}
	if _, err := os.Stat(layout.EntryPath("sbsh")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("dry-run wrote entry file: err=%v", err)
	}
	if !strings.Contains(out.String(), "dry-run") || !strings.Contains(out.String(), "sbsh") {
		t.Errorf("dry-run output missing expected content: %q", out.String())
	}
}

// TestComposeTeamDryRunRendersToStdout covers the AC: "--dry-run renders
// to stdout and neither applies nor writes kuketeam.d/<project>.yaml".
func TestComposeTeamDryRunRendersToStdout(t *testing.T) {
	t.Parallel()
	projectDir := writeProject(t, projectTeamWithHarnessYAML)
	layout := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	bundle := buildClaudeBundle(t)

	var out bytes.Buffer
	err := composeTeam(
		context.Background(), &out, projectDir, layout,
		stubGit(map[string]string{"user.name": "Op", "user.email": "op@example.com"}),
		stubProjectURL("git@github.com:eminwux/sbsh.git"),
		stubBundle(bundle), true,
	)
	if err != nil {
		t.Fatalf("composeTeam dry-run: %v", err)
	}

	// No drop-in entry, no global config — dry-run touches no files.
	if _, statErr := os.Stat(layout.EntryPath("sbsh")); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("dry-run wrote entry: err=%v", statErr)
	}
	if _, statErr := os.Stat(layout.GlobalConfigPath()); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("dry-run wrote global config: err=%v", statErr)
	}

	body := out.String()
	if !strings.Contains(body, "kind: CellBlueprint") {
		t.Errorf("dry-run output missing rendered CellBlueprint: %q", body)
	}
	if !strings.Contains(body, "kind: CellConfig") {
		t.Errorf("dry-run output missing rendered CellConfig: %q", body)
	}
	if !strings.Contains(body, "registry.local/claude-go:latest") {
		t.Errorf("rendered output missing selected image: %q", body)
	}
	if !strings.Contains(body, "kukeon.io/team: sbsh") {
		t.Errorf("rendered output missing team label: %q", body)
	}
}

// TestComposeTeamRendersOnNonDryRun confirms the render pipeline runs on
// the non-dry-run path too (validating image-select before the drop-in
// entry is written), and that the drop-in entry IS written then.
func TestComposeTeamRendersOnNonDryRun(t *testing.T) {
	t.Parallel()
	projectDir := writeProject(t, projectTeamWithHarnessYAML)
	layout := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	bundle := buildClaudeBundle(t)

	var out bytes.Buffer
	err := composeTeam(
		context.Background(), &out, projectDir, layout,
		stubGit(nil),
		stubProjectURL("git@github.com:eminwux/sbsh.git"),
		stubBundle(bundle), false,
	)
	if err != nil {
		t.Fatalf("composeTeam: %v", err)
	}

	if _, statErr := os.Stat(layout.EntryPath("sbsh")); statErr != nil {
		t.Errorf("entry should have been written on non-dry-run: %v", statErr)
	}
	if !strings.Contains(out.String(), "rendered 1 blueprint/1 config") {
		t.Errorf("render-count summary missing: %q", out.String())
	}
}

// TestComposeTeamImageSelectHardError confirms a missing capability hits
// the operator-actionable error path with the unmet capability named.
func TestComposeTeamImageSelectHardError(t *testing.T) {
	t.Parallel()
	projectDir := writeProject(t, projectTeamWithHarnessYAML)
	layout := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	bundle := buildClaudeBundle(t)
	// Drop the role's `git` from images' capability set so the merged needs
	// can't be satisfied — leaving a single unmet capability.
	bundle.ImageCatalog.Spec.Images[0].Capabilities = []string{"go"}

	err := composeTeam(
		context.Background(), &bytes.Buffer{}, projectDir, layout,
		stubGit(nil), stubProjectURL(""),
		stubBundle(bundle), false,
	)
	if !errors.Is(err, errdefs.ErrTeamImageNoMatch) {
		t.Fatalf("err = %v, want ErrTeamImageNoMatch", err)
	}
	if !strings.Contains(err.Error(), "git") {
		t.Errorf("error should name unmet capability 'git', got: %v", err)
	}
	if !strings.Contains(err.Error(), "build or label") {
		t.Errorf("error should carry build-or-label hint, got: %v", err)
	}
}

// TestComposeTeamProjectRepoURLFilledIntoConfig confirms the bind step
// stamps the project clone URL into the CellConfig's `project` repo slot
// fill — the AC's "project's cloned repo URL → CellConfig" check.
func TestComposeTeamProjectRepoURLFilledIntoConfig(t *testing.T) {
	t.Parallel()
	projectDir := writeProject(t, projectTeamWithHarnessYAML)
	layout := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	bundle := buildClaudeBundle(t)

	var out bytes.Buffer
	err := composeTeam(
		context.Background(), &out, projectDir, layout,
		stubGit(nil), stubProjectURL("git@github.com:eminwux/sbsh.git"),
		stubBundle(bundle), true,
	)
	if err != nil {
		t.Fatalf("composeTeam: %v", err)
	}
	if !strings.Contains(out.String(), "git@github.com:eminwux/sbsh.git") {
		t.Errorf("project clone URL not stamped into rendered config: %q", out.String())
	}
}

func TestNewInitCmdParsesDryRunFlag(t *testing.T) {
	t.Parallel()
	cmd := NewInitCmd()
	if cmd.Flags().Lookup("dry-run") == nil {
		t.Fatalf("--dry-run flag not registered")
	}
	if cmd.Name() != "init" {
		t.Errorf("init cmd name = %q", cmd.Name())
	}
}

func TestNewTeamCmdRegistersInit(t *testing.T) {
	t.Parallel()
	cmd := NewTeamCmd()
	if cmd.Name() != "team" {
		t.Errorf("team cmd name = %q", cmd.Name())
	}
	var found bool
	for _, c := range cmd.Commands() {
		if c.Name() == "init" {
			found = true
		}
	}
	if !found {
		t.Errorf("team init subcommand not registered")
	}
}

// silence the unused-import linter when v1beta1 is referenced only through
// the rendered output bytes.
var _ = v1beta1.LabelTeam
