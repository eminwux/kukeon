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
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/teamhost"
	"github.com/eminwux/kukeon/internal/teamsource"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	model "github.com/eminwux/kukeon/pkg/api/model/kuketeams"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"gopkg.in/yaml.v3"
)

const projectTeamYAML = `apiVersion: kuketeams.io/v1
kind: ProjectTeam
metadata: { name: sbsh }
spec:
  source: { repo: github.com/eminwux/agents, tag: v1.4.0 }
  roles:
    - { ref: dev }
`

// projectTeamWithHarnessYAML carries a harness default so the render
// pipeline produces at least one (role × harness) pair.
const projectTeamWithHarnessYAML = `apiVersion: kuketeams.io/v1
kind: ProjectTeam
metadata: { name: sbsh }
spec:
  source: { repo: github.com/eminwux/agents, tag: v1.4.0 }
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

// stubApplyErr returns an ApplyForTeamFunc that fails on any call — used by
// tests that should never reach the apply step (dry-run, harness-less roster,
// pre-render failure paths).
func stubApplyErr() ApplyForTeamFunc {
	return func(_ context.Context, _ []byte, _ string) (kukeonv1.ApplyDocumentsResult, error) {
		return kukeonv1.ApplyDocumentsResult{}, errors.New("apply must not be called")
	}
}

// stubBuildErr returns a BuildAllFunc that fails on any call — used by tests
// where --build is not set, so the build path must never fire.
func stubBuildErr() BuildAllFunc {
	return func(
		_ context.Context, _, _, _ string,
		_ []*model.ImageCatalogEntry, _, _, _ io.Writer,
	) error {
		return errors.New("build must not be called")
	}
}

// buildCall captures one composeTeam → BuildAllFunc invocation so tests can
// assert which catalog leaves were handed to kukebuild and against which
// cache dir / source ref / realm.
type buildCall struct {
	CacheDir, SourceRef, Realm string
	Leaves                     []*model.ImageCatalogEntry
}

// recordingBuild returns a BuildAllFunc that appends every invocation to
// *calls under a mutex (so two-project tests can share one recorder) and
// returns nil. The recorder copies the leaves slice so the caller's mutations
// to the catalog do not race with later assertions.
func recordingBuild(mu *sync.Mutex, calls *[]buildCall) BuildAllFunc {
	return func(
		_ context.Context,
		cacheDir, sourceRef, realm string,
		leaves []*model.ImageCatalogEntry,
		_, _, _ io.Writer,
	) error {
		mu.Lock()
		defer mu.Unlock()
		cp := make([]*model.ImageCatalogEntry, len(leaves))
		copy(cp, leaves)
		*calls = append(*calls, buildCall{
			CacheDir: cacheDir, SourceRef: sourceRef, Realm: realm, Leaves: cp,
		})
		return nil
	}
}

// applyCall captures one composeTeam → apply invocation so a test can
// assert which team each project's apply targeted and what YAML it sent.
type applyCall struct {
	Team    string
	RawYAML []byte
}

// recordingApply returns an ApplyForTeamFunc that appends every invocation
// to *calls under a mutex (composeTeam itself is single-goroutine, but the
// recorder is shared across composeTeam calls in two-project tests) and
// returns the given result. Use nil result for an "ack with no per-resource
// detail" reply — composeTeam treats the empty Resources slice as a clean
// apply with zero individual lines to print.
func recordingApply(
	mu *sync.Mutex, calls *[]applyCall, result kukeonv1.ApplyDocumentsResult,
) ApplyForTeamFunc {
	return func(_ context.Context, rawYAML []byte, team string) (kukeonv1.ApplyDocumentsResult, error) {
		mu.Lock()
		defer mu.Unlock()
		buf := make([]byte, len(rawYAML))
		copy(buf, rawYAML)
		*calls = append(*calls, applyCall{Team: team, RawYAML: buf})
		return result, nil
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
  name: {{ .role.name }}-{{ .harness }}
spec:
  cell:
    containers:
      - id: {{ .role.name }}
        image: {{ .image }}
        env:
          - "ROLE={{ .role.name }}"
          - "NEEDS={{ range $i, $n := .needs.image }}{{ if $i }},{{ end }}{{ $n }}{{ end }}"
          - "SETTINGS={{ (index .harnesses .harness).settings }}"
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
			Repo:      "github.com/eminwux/agents",
			Host:      "github.com",
			OwnerRepo: "eminwux/agents",
			Ref:       "v1.4.0",
			Kind:      teamsource.RefTag,
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
					// Bare filename — the renderer resolves it relative to the
					// harness's own dir (harnesses/claude/), matching the
					// agents-repo canonical layout (#1110).
					Template: "blueprint.tmpl.yaml",
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
		context.Background(), &bytes.Buffer{}, io.Discard, emptyDir, layout,
		stubGit(nil), stubProjectURL(""), stubResolveErr(), stubApplyErr(), stubBuildErr(), false, false,
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
		context.Background(), &bytes.Buffer{}, io.Discard, dir, layout,
		stubGit(nil), stubProjectURL(""), stubResolveErr(), stubApplyErr(), stubBuildErr(), false, false,
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
		context.Background(), &out, io.Discard, projectDir, layout,
		git, stubProjectURL(""), stubResolveErr(), stubApplyErr(), stubBuildErr(), false, false,
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
	if te.Metadata.Name != "sbsh" || te.Spec.Path != projectDir || te.Spec.Source == nil ||
		te.Spec.Source.Repo != "github.com/eminwux/agents" || te.Spec.Source.Tag != "v1.4.0" {
		t.Errorf("entry content wrong: %+v", te)
	}
	if !strings.Contains(out.String(), "wrote team \"sbsh\"") {
		t.Errorf("missing write confirmation in output: %q", out.String())
	}
	// The tag source is pinned — init must surface that so a non-reproducible
	// (floating) roster would be visible.
	if !strings.Contains(out.String(), "agents source: github.com/eminwux/agents @ tag=v1.4.0 (pinned)") {
		t.Errorf("missing pinned-source line in output: %q", out.String())
	}
}

func TestComposeTeamReRunDoesNotRescaffold(t *testing.T) {
	t.Parallel()
	projectDir := writeProject(t, projectTeamYAML)
	layout := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	git := stubGit(map[string]string{"user.name": "A", "user.email": "a@example.com"})

	if err := composeTeam(
		context.Background(), &bytes.Buffer{}, io.Discard, projectDir, layout,
		git, stubProjectURL(""), stubResolveErr(), stubApplyErr(), stubBuildErr(), false, false,
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
		context.Background(), &out, io.Discard, projectDir, layout,
		git, stubProjectURL(""), stubResolveErr(), stubApplyErr(), stubBuildErr(), false, false,
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
		context.Background(), &bytes.Buffer{}, io.Discard, projectDir, layout,
		stubGit(nil), stubProjectURL(""), stubResolveErr(), stubApplyErr(), stubBuildErr(), false, false,
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
		context.Background(), &out, io.Discard, projectDir, layout,
		stubGit(nil), stubProjectURL(""), stubResolveErr(), stubApplyErr(), stubBuildErr(), true, false,
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
		context.Background(), &out, io.Discard, projectDir, layout,
		stubGit(map[string]string{"user.name": "Op", "user.email": "op@example.com"}),
		stubProjectURL("git@github.com:eminwux/sbsh.git"),
		stubBundle(bundle), stubApplyErr(), stubBuildErr(), true, false,
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

	var (
		mu    sync.Mutex
		calls []applyCall
	)
	var out bytes.Buffer
	err := composeTeam(
		context.Background(), &out, io.Discard, projectDir, layout,
		stubGit(nil),
		stubProjectURL("git@github.com:eminwux/sbsh.git"),
		stubBundle(bundle),
		recordingApply(&mu, &calls, kukeonv1.ApplyDocumentsResult{}), stubBuildErr(), false, false,
	)
	if err != nil {
		t.Fatalf("composeTeam: %v", err)
	}

	if _, statErr := os.Stat(layout.EntryPath("sbsh")); statErr != nil {
		t.Errorf("entry should have been written on non-dry-run: %v", statErr)
	}
	if !strings.Contains(out.String(), "applied 0 secret/1 blueprint/1 config") {
		t.Errorf("apply-count summary missing: %q", out.String())
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
		context.Background(), &bytes.Buffer{}, io.Discard, projectDir, layout,
		stubGit(nil), stubProjectURL(""),
		stubBundle(bundle), stubApplyErr(), stubBuildErr(), false, false,
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
		context.Background(), &out, io.Discard, projectDir, layout,
		stubGit(nil), stubProjectURL("git@github.com:eminwux/sbsh.git"),
		stubBundle(bundle), stubApplyErr(), stubBuildErr(), true, false,
	)
	if err != nil {
		t.Fatalf("composeTeam: %v", err)
	}
	if !strings.Contains(out.String(), "git@github.com:eminwux/sbsh.git") {
		t.Errorf("project clone URL not stamped into rendered config: %q", out.String())
	}
}

// TestComposeTeamAppliesRenderedSetToDaemon pins the AC: the project's
// labeled set is handed to ApplyDocumentsForTeam with the project as the
// team. The YAML the stub captures carries both kinds and the team label
// teamrender stamped onto every object — the same payload the daemon
// prunes against in #1029.
func TestComposeTeamAppliesRenderedSetToDaemon(t *testing.T) {
	t.Parallel()
	projectDir := writeProject(t, projectTeamWithHarnessYAML)
	layout := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	bundle := buildClaudeBundle(t)

	var (
		mu    sync.Mutex
		calls []applyCall
	)
	result := kukeonv1.ApplyDocumentsResult{
		Resources: []kukeonv1.ApplyResourceResult{
			{Kind: "CellBlueprint", Name: "dev-claude", Action: "created"},
			{Kind: "CellConfig", Name: "dev-claude", Action: "created"},
		},
	}
	var out bytes.Buffer
	if err := composeTeam(
		context.Background(), &out, io.Discard, projectDir, layout,
		stubGit(nil), stubProjectURL("git@github.com:eminwux/sbsh.git"),
		stubBundle(bundle), recordingApply(&mu, &calls, result), stubBuildErr(), false, false,
	); err != nil {
		t.Fatalf("composeTeam: %v", err)
	}

	if len(calls) != 1 {
		t.Fatalf("apply call count = %d, want 1", len(calls))
	}
	call := calls[0]
	if call.Team != "sbsh" {
		t.Errorf("apply team = %q, want %q", call.Team, "sbsh")
	}
	body := string(call.RawYAML)
	if !strings.Contains(body, "kind: CellBlueprint") || !strings.Contains(body, "kind: CellConfig") {
		t.Errorf("applied YAML missing kinds: %q", body)
	}
	if !strings.Contains(body, v1beta1.LabelTeam+": sbsh") {
		t.Errorf("applied YAML missing team label %q: %q", v1beta1.LabelTeam, body)
	}
	// The daemon's per-resource report flows through to the human output.
	if !strings.Contains(out.String(), `CellBlueprint "dev-claude": created`) {
		t.Errorf("per-resource summary missing CellBlueprint line: %q", out.String())
	}
	if !strings.Contains(out.String(), `applied 0 secret/1 blueprint/1 config object(s) to kukeond under team "sbsh"`) {
		t.Errorf("aggregate summary missing: %q", out.String())
	}
}

// TestComposeTeamDryRunSkipsApply covers the AC inversion: --dry-run prints
// rendered objects to stdout but does not call into the daemon.
func TestComposeTeamDryRunSkipsApply(t *testing.T) {
	t.Parallel()
	projectDir := writeProject(t, projectTeamWithHarnessYAML)
	layout := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	bundle := buildClaudeBundle(t)

	// stubApplyErr would error if invoked — composeTeam must not reach it.
	var out bytes.Buffer
	if err := composeTeam(
		context.Background(), &out, io.Discard, projectDir, layout,
		stubGit(nil), stubProjectURL("git@github.com:eminwux/sbsh.git"),
		stubBundle(bundle), stubApplyErr(), stubBuildErr(), true, false,
	); err != nil {
		t.Fatalf("composeTeam dry-run: %v", err)
	}
	if !strings.Contains(out.String(), "not applied to kukeond") {
		t.Errorf("dry-run output missing skip-apply marker: %q", out.String())
	}
}

// TestComposeTeamHarnessLessRosterSkipsApply covers the harness-less branch:
// no (role × harness) pairs → nothing to apply, no daemon hop.
func TestComposeTeamHarnessLessRosterSkipsApply(t *testing.T) {
	t.Parallel()
	projectDir := writeProject(t, projectTeamYAML)
	layout := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))

	var out bytes.Buffer
	if err := composeTeam(
		context.Background(), &out, io.Discard, projectDir, layout,
		stubGit(nil), stubProjectURL(""), stubResolveErr(), stubApplyErr(), stubBuildErr(), false, false,
	); err != nil {
		t.Fatalf("composeTeam: %v", err)
	}
	if !strings.Contains(out.String(), "no apply: no (role × harness) pairs") {
		t.Errorf("harness-less output missing skip-apply marker: %q", out.String())
	}
	// Drop-in entry still written so future re-runs (and future verbs like
	// `kuke team list`) see the project — apply-skip is not entry-skip.
	if _, statErr := os.Stat(layout.EntryPath("sbsh")); statErr != nil {
		t.Errorf("entry should still be written even with no apply: %v", statErr)
	}
}

// TestComposeTeamApplyFailureBlocksDropInWrite covers the failure ordering:
// apply runs before the drop-in entry write, so a daemon-side failure does
// not leave a half-recorded team behind. The next re-run sees a clean tree.
func TestComposeTeamApplyFailureBlocksDropInWrite(t *testing.T) {
	t.Parallel()
	projectDir := writeProject(t, projectTeamWithHarnessYAML)
	layout := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	bundle := buildClaudeBundle(t)

	wantErr := errors.New("daemon refused: kukeond not running")
	apply := func(_ context.Context, _ []byte, _ string) (kukeonv1.ApplyDocumentsResult, error) {
		return kukeonv1.ApplyDocumentsResult{}, wantErr
	}

	err := composeTeam(
		context.Background(), &bytes.Buffer{}, io.Discard, projectDir, layout,
		stubGit(nil), stubProjectURL(""), stubBundle(bundle), apply, stubBuildErr(), false, false,
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want it to wrap %v", err, wantErr)
	}
	if _, statErr := os.Stat(layout.EntryPath("sbsh")); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("drop-in entry written despite apply failure: err=%v", statErr)
	}
}

// TestComposeTeamWritesNothingUnderRendered covers the explicit AC: nothing
// is written under ~/.kuke/rendered/. After a full init the layout base
// holds only kuketeams.yaml + kuketeam.d/, never a rendered/ sibling.
func TestComposeTeamWritesNothingUnderRendered(t *testing.T) {
	t.Parallel()
	projectDir := writeProject(t, projectTeamWithHarnessYAML)
	layout := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	bundle := buildClaudeBundle(t)

	var (
		mu    sync.Mutex
		calls []applyCall
	)
	if err := composeTeam(
		context.Background(), &bytes.Buffer{}, io.Discard, projectDir, layout,
		stubGit(nil), stubProjectURL("git@github.com:eminwux/sbsh.git"),
		stubBundle(bundle), recordingApply(&mu, &calls, kukeonv1.ApplyDocumentsResult{}), stubBuildErr(), false, false,
	); err != nil {
		t.Fatalf("composeTeam: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(layout.Base, "rendered")); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("rendered/ directory should not exist after init: stat err=%v", statErr)
	}
}

// TestComposeTeamTwoProjectsIndependent covers the AC's e2e check at the
// composition layer: `kuke team init` in two project directories sharing
// one ~/.kuke applies each labeled set under its own team, and re-running
// one project re-applies only that project's set — the second project's
// apply is not re-invoked, its drop-in file is untouched, and removing one
// project's drop-in file leaves the other readable.
func TestComposeTeamTwoProjectsIndependent(t *testing.T) {
	t.Parallel()
	layout := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	bundle := buildClaudeBundle(t)

	mkProject := func(name string) string {
		body := fmt.Sprintf(`apiVersion: kuketeams.io/v1
kind: ProjectTeam
metadata: { name: %s }
spec:
  source: { repo: github.com/eminwux/agents, tag: v1.4.0 }
  defaults:
    harnesses: [claude]
  roles:
    - { ref: dev }
`, name)
		return writeProject(t, body)
	}
	alphaDir := mkProject("alpha")
	betaDir := mkProject("beta")

	var (
		mu    sync.Mutex
		calls []applyCall
	)
	apply := recordingApply(&mu, &calls, kukeonv1.ApplyDocumentsResult{})

	// Init alpha.
	if err := composeTeam(
		context.Background(), &bytes.Buffer{}, io.Discard, alphaDir, layout,
		stubGit(nil), stubProjectURL("git@github.com:eminwux/alpha.git"),
		stubBundle(bundle), apply, stubBuildErr(), false, false,
	); err != nil {
		t.Fatalf("alpha init: %v", err)
	}
	// Init beta.
	if err := composeTeam(
		context.Background(), &bytes.Buffer{}, io.Discard, betaDir, layout,
		stubGit(nil), stubProjectURL("git@github.com:eminwux/beta.git"),
		stubBundle(bundle), apply, stubBuildErr(), false, false,
	); err != nil {
		t.Fatalf("beta init: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("apply call count after two-project init = %d, want 2", len(calls))
	}
	if calls[0].Team != "alpha" || calls[1].Team != "beta" {
		t.Errorf("apply teams = %q,%q, want alpha,beta", calls[0].Team, calls[1].Team)
	}

	// Per-project drop-in files written, independent of each other.
	for _, project := range []string{"alpha", "beta"} {
		if _, statErr := os.Stat(layout.EntryPath(project)); statErr != nil {
			t.Errorf("entry %q not written: %v", project, statErr)
		}
	}

	// Re-init alpha → one more apply for alpha; beta's apply is not
	// re-invoked, beta's drop-in stays put.
	betaEntryBefore, readErr := os.ReadFile(layout.EntryPath("beta"))
	if readErr != nil {
		t.Fatalf("read beta entry: %v", readErr)
	}
	if reErr := composeTeam(
		context.Background(), &bytes.Buffer{}, io.Discard, alphaDir, layout,
		stubGit(nil), stubProjectURL("git@github.com:eminwux/alpha.git"),
		stubBundle(bundle), apply, stubBuildErr(), false, false,
	); reErr != nil {
		t.Fatalf("alpha re-init: %v", reErr)
	}
	if len(calls) != 3 {
		t.Fatalf("apply call count after re-init alpha = %d, want 3", len(calls))
	}
	if calls[2].Team != "alpha" {
		t.Errorf("re-init apply team = %q, want alpha", calls[2].Team)
	}
	betaEntryAfter, readErr2 := os.ReadFile(layout.EntryPath("beta"))
	if readErr2 != nil {
		t.Fatalf("read beta entry after alpha re-init: %v", readErr2)
	}
	if !bytes.Equal(betaEntryBefore, betaEntryAfter) {
		t.Errorf("beta drop-in entry changed by alpha re-init:\n--- before ---\n%s\n--- after ---\n%s",
			betaEntryBefore, betaEntryAfter)
	}

	// Removing one project's drop-in does not disturb the other — the
	// per-project file layout (the resized step-1 "kuketeam.d/" decision)
	// makes this trivially true; the test pins the invariant.
	if rmErr := os.Remove(layout.EntryPath("alpha")); rmErr != nil {
		t.Fatalf("remove alpha drop-in: %v", rmErr)
	}
	if _, statErr := os.Stat(layout.EntryPath("beta")); statErr != nil {
		t.Errorf("beta drop-in disturbed by alpha removal: %v", statErr)
	}
}

func TestNewInitCmdParsesDryRunFlag(t *testing.T) {
	t.Parallel()
	cmd := NewInitCmd()
	if cmd.Flags().Lookup("dry-run") == nil {
		t.Fatalf("--dry-run flag not registered")
	}
	if cmd.Flags().Lookup("build") == nil {
		t.Fatalf("--build flag not registered")
	}
	if cmd.Name() != "init" {
		t.Errorf("init cmd name = %q", cmd.Name())
	}
}

// TestComposeTeamBuildInvokesBuildAll pins AC 2: `kuke team init --build`
// invokes the build-set runner against the bundle's cache dir, the source's
// pinned ref, the default realm, and the catalog leaves teamrender selected
// for the (role × harness) pairs.
func TestComposeTeamBuildInvokesBuildAll(t *testing.T) {
	t.Parallel()
	projectDir := writeProject(t, projectTeamWithHarnessYAML)
	layout := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	bundle := buildClaudeBundle(t)

	var (
		mu         sync.Mutex
		buildCalls []buildCall
	)
	build := recordingBuild(&mu, &buildCalls)

	var (
		applyMu    sync.Mutex
		applyCalls []applyCall
	)
	apply := recordingApply(&applyMu, &applyCalls, kukeonv1.ApplyDocumentsResult{})

	var out bytes.Buffer
	if err := composeTeam(
		context.Background(), &out, io.Discard, projectDir, layout,
		stubGit(nil), stubProjectURL("git@github.com:eminwux/sbsh.git"),
		stubBundle(bundle), apply, build, false, true,
	); err != nil {
		t.Fatalf("composeTeam: %v", err)
	}

	if len(buildCalls) != 1 {
		t.Fatalf("build call count = %d, want 1", len(buildCalls))
	}
	call := buildCalls[0]
	if call.CacheDir != bundle.CacheDir {
		t.Errorf("build cacheDir = %q, want %q", call.CacheDir, bundle.CacheDir)
	}
	if call.SourceRef != bundle.Source.Ref {
		t.Errorf("build sourceRef = %q, want %q", call.SourceRef, bundle.Source.Ref)
	}
	if call.Realm != "default" {
		t.Errorf("build realm = %q, want %q", call.Realm, "default")
	}
	if len(call.Leaves) != 1 || call.Leaves[0].Ref != "claude-go" {
		t.Errorf("build leaves = %+v, want one entry ref=claude-go", call.Leaves)
	}

	// Build runs alongside the apply path — both fire.
	if len(applyCalls) != 1 {
		t.Errorf("apply call count = %d, want 1 (build does not gate apply)", len(applyCalls))
	}
}

// TestComposeTeamBuildBindsInternalImageRefTwoProjects pins the bind half of
// AC step 3 at the compose tier: a `--build` compose run renders each project's
// blueprint binding the locally-built kukeon.internal/<ref>:<version> image
// (matching the tag teambuild produces), not the catalog's published image. Two
// distinct projects share one agents bundle — the two-project compose shape —
// and both must bind the internal ref. (The full containerd + kukebuild
// stand-up that turns those refs into running cells is the deferred e2e; this
// asserts the deterministic bind wiring the e2e would run on top of.)
func TestComposeTeamBuildBindsInternalImageRefTwoProjects(t *testing.T) {
	t.Parallel()
	const otherProjectYAML = `apiVersion: kuketeams.io/v1
kind: ProjectTeam
metadata: { name: kuke }
spec:
  source: { repo: github.com/eminwux/agents, tag: v1.4.0 }
  defaults:
    harnesses: [claude]
  roles:
    - { ref: dev }
`
	const wantInternal = "kukeon.internal/claude-go:v1.4.0"
	const publishedRef = "registry.local/claude-go:latest"

	for _, tc := range []struct {
		name string
		body string
	}{
		{"sbsh", projectTeamWithHarnessYAML},
		{"kuke", otherProjectYAML},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			projectDir := writeProject(t, tc.body)
			layout := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
			bundle := buildClaudeBundle(t)

			var (
				mu         sync.Mutex
				buildCalls []buildCall
			)
			var (
				applyMu    sync.Mutex
				applyCalls []applyCall
			)
			if err := composeTeam(
				context.Background(), &bytes.Buffer{}, io.Discard, projectDir, layout,
				stubGit(nil), stubProjectURL("git@github.com:eminwux/"+tc.name+".git"),
				stubBundle(bundle), recordingApply(&applyMu, &applyCalls, kukeonv1.ApplyDocumentsResult{}),
				recordingBuild(&mu, &buildCalls), false, true,
			); err != nil {
				t.Fatalf("composeTeam: %v", err)
			}

			if len(applyCalls) != 1 {
				t.Fatalf("apply call count = %d, want 1", len(applyCalls))
			}
			applied := string(applyCalls[0].RawYAML)
			if !strings.Contains(applied, wantInternal) {
				t.Errorf("rendered blueprint does not bind internal ref %q:\n%s", wantInternal, applied)
			}
			if strings.Contains(applied, publishedRef) {
				t.Errorf("rendered blueprint still binds published ref %q in build mode:\n%s", publishedRef, applied)
			}
		})
	}
}

// TestComposeTeamBindsPublishedImageRefWithoutBuild is the no-flag counterpart:
// the catalog's published image is bound and no kukeon.internal ref appears.
func TestComposeTeamBindsPublishedImageRefWithoutBuild(t *testing.T) {
	t.Parallel()
	projectDir := writeProject(t, projectTeamWithHarnessYAML)
	layout := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	bundle := buildClaudeBundle(t)

	var (
		applyMu    sync.Mutex
		applyCalls []applyCall
	)
	if err := composeTeam(
		context.Background(), &bytes.Buffer{}, io.Discard, projectDir, layout,
		stubGit(nil), stubProjectURL("git@github.com:eminwux/sbsh.git"),
		stubBundle(bundle), recordingApply(&applyMu, &applyCalls, kukeonv1.ApplyDocumentsResult{}),
		stubBuildErr(), false, false,
	); err != nil {
		t.Fatalf("composeTeam: %v", err)
	}
	if len(applyCalls) != 1 {
		t.Fatalf("apply call count = %d, want 1", len(applyCalls))
	}
	applied := string(applyCalls[0].RawYAML)
	if !strings.Contains(applied, "registry.local/claude-go:latest") {
		t.Errorf("no-flag mode should bind the published image:\n%s", applied)
	}
	if strings.Contains(applied, "kukeon.internal/") {
		t.Errorf("no-flag mode must not bind a kukeon.internal ref:\n%s", applied)
	}
}

// TestComposeTeamBuildSkippedOnDryRun pins the dry-run semantics: --build
// with --dry-run announces what would build but invokes neither kukebuild
// nor the daemon apply.
func TestComposeTeamBuildSkippedOnDryRun(t *testing.T) {
	t.Parallel()
	projectDir := writeProject(t, projectTeamWithHarnessYAML)
	layout := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	bundle := buildClaudeBundle(t)

	var out bytes.Buffer
	if err := composeTeam(
		context.Background(), &out, io.Discard, projectDir, layout,
		stubGit(nil), stubProjectURL("git@github.com:eminwux/sbsh.git"),
		stubBundle(bundle), stubApplyErr(), stubBuildErr(), true, true,
	); err != nil {
		t.Fatalf("composeTeam: %v", err)
	}

	if !strings.Contains(out.String(), "--build skipped") {
		t.Errorf("dry-run output missing --build skip marker: %q", out.String())
	}
	if !strings.Contains(out.String(), "1 catalog leaf(s) selected") {
		t.Errorf("dry-run output missing selection count: %q", out.String())
	}
}

// TestComposeTeamBuildNotInvokedByDefault pins the inversion: --build off
// (default) → BuildAllFunc never called. The stubBuildErr would error if it
// were.
func TestComposeTeamBuildNotInvokedByDefault(t *testing.T) {
	t.Parallel()
	projectDir := writeProject(t, projectTeamWithHarnessYAML)
	layout := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	bundle := buildClaudeBundle(t)

	var (
		applyMu    sync.Mutex
		applyCalls []applyCall
	)
	if err := composeTeam(
		context.Background(), &bytes.Buffer{}, io.Discard, projectDir, layout,
		stubGit(nil), stubProjectURL("git@github.com:eminwux/sbsh.git"),
		stubBundle(bundle), recordingApply(&applyMu, &applyCalls, kukeonv1.ApplyDocumentsResult{}),
		stubBuildErr(), false, false,
	); err != nil {
		t.Fatalf("composeTeam: %v", err)
	}
}

// TestComposeTeamAutoFillsTeamDir confirms the AC: a clean init writes the
// drop-in entry with spec.teamDir auto-populated from Layout.TeamDir(project).
func TestComposeTeamAutoFillsTeamDir(t *testing.T) {
	t.Parallel()
	projectDir := writeProject(t, projectTeamYAML)
	layout := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))

	if err := composeTeam(
		context.Background(), &bytes.Buffer{}, io.Discard, projectDir, layout,
		stubGit(nil), stubProjectURL(""), stubResolveErr(), stubApplyErr(), stubBuildErr(), false, false,
	); err != nil {
		t.Fatalf("composeTeam: %v", err)
	}
	raw, err := os.ReadFile(layout.EntryPath("sbsh"))
	if err != nil {
		t.Fatalf("entry not written: %v", err)
	}
	var te model.TeamEntry
	if unmarshalErr := yaml.Unmarshal(raw, &te); unmarshalErr != nil {
		t.Fatalf("entry parse: %v", unmarshalErr)
	}
	if want := layout.TeamDir("sbsh"); te.Spec.TeamDir != want {
		t.Errorf("entry.spec.teamDir = %q, want auto-filled %q", te.Spec.TeamDir, want)
	}
}

// TestComposeTeamPreservesTeamDirOverride covers the AC: a hand-edited
// spec.teamDir survives re-init. The test seeds an existing drop-in entry
// with an override and asserts the re-write keeps it.
func TestComposeTeamPreservesTeamDirOverride(t *testing.T) {
	t.Parallel()
	projectDir := writeProject(t, projectTeamYAML)
	layout := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))

	// Seed an existing drop-in entry with an operator-relocated teamDir.
	override := filepath.Join(t.TempDir(), "nfs-mounted", "sbsh")
	prior := &model.TeamEntry{
		APIVersion: model.APIVersionV1,
		Kind:       model.KindTeamEntry,
		Metadata:   model.Metadata{Name: "sbsh"},
		Spec: model.TeamEntrySpec{
			Path:    projectDir,
			TeamDir: override,
			Source:  &model.TeamSource{Repo: "github.com/eminwux/agents", Tag: "v1.4.0"},
		},
	}
	if err := teamhost.WriteEntry(layout, prior); err != nil {
		t.Fatalf("seed prior entry: %v", err)
	}

	if err := composeTeam(
		context.Background(), &bytes.Buffer{}, io.Discard, projectDir, layout,
		stubGit(nil), stubProjectURL(""), stubResolveErr(), stubApplyErr(), stubBuildErr(), false, false,
	); err != nil {
		t.Fatalf("composeTeam re-init: %v", err)
	}

	raw, err := os.ReadFile(layout.EntryPath("sbsh"))
	if err != nil {
		t.Fatalf("entry not written: %v", err)
	}
	var te model.TeamEntry
	if unmarshalErr := yaml.Unmarshal(raw, &te); unmarshalErr != nil {
		t.Fatalf("entry parse: %v", unmarshalErr)
	}
	if te.Spec.TeamDir != override {
		t.Errorf("entry.spec.teamDir = %q, want preserved override %q", te.Spec.TeamDir, override)
	}

	// State dir + per-team root land under the override path, not the
	// layout default — the override governs provisioning too.
	if _, statErr := os.Stat(override); statErr != nil {
		t.Errorf("override teamDir not provisioned: %v", statErr)
	}
	if _, statErr := os.Stat(layout.TeamDir("sbsh")); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("default teamDir leaked alongside override: err=%v", statErr)
	}
}

// TestComposeTeamProvisionsRosterStateDirs covers the AC: every roster
// (role × harness) pair gets a state dir under `~/.kuke/teams/<team>/`.
func TestComposeTeamProvisionsRosterStateDirs(t *testing.T) {
	t.Parallel()
	projectDir := writeProject(t, projectTeamWithHarnessYAML)
	layout := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	bundle := buildClaudeBundle(t)
	// Add a seed to the claude harness so the AC's "seeds applied" branch
	// also fires alongside the state-dir mkdir.
	bundle.Harnesses["claude"].Spec.Seeds = []model.HarnessSeed{
		{Path: "${HARNESS}.json", Mode: 0o644, Content: "{}\n"},
	}

	var (
		mu    sync.Mutex
		calls []applyCall
	)
	if err := composeTeam(
		context.Background(), &bytes.Buffer{}, io.Discard, projectDir, layout,
		stubGit(nil), stubProjectURL("git@github.com:eminwux/sbsh.git"),
		stubBundle(bundle), recordingApply(&mu, &calls, kukeonv1.ApplyDocumentsResult{}),
		stubBuildErr(), false, false,
	); err != nil {
		t.Fatalf("composeTeam: %v", err)
	}

	stateDir := layout.RoleHarnessStateDir("sbsh", "dev", "claude")
	if info, err := os.Stat(stateDir); err != nil {
		t.Errorf("state dir not created: %v", err)
	} else if !info.IsDir() {
		t.Errorf("%q is not a dir", stateDir)
	}

	seedPath := layout.HarnessSeedPath("sbsh", "claude", "")
	if _, err := os.Stat(seedPath); err != nil {
		t.Errorf("seed not written: %v", err)
	}
}

// TestComposeTeamProvisionsTwoProjectsIsolated checks the AC's e2e
// invariant at the composition layer: dezot and kukeon get isolated
// state trees under one shared `~/.kuke/teams/` root.
func TestComposeTeamProvisionsTwoProjectsIsolated(t *testing.T) {
	t.Parallel()
	const otherProjectYAML = `apiVersion: kuketeams.io/v1
kind: ProjectTeam
metadata: { name: kukeon }
spec:
  source: { repo: github.com/eminwux/agents, tag: v1.4.0 }
  defaults:
    harnesses: [claude]
  roles:
    - { ref: dev }
`
	layout := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	bundle := buildClaudeBundle(t)
	bundle.Harnesses["claude"].Spec.Seeds = []model.HarnessSeed{
		{Path: "${HARNESS}.json", Mode: 0o644, Content: "{}\n"},
	}

	var (
		mu    sync.Mutex
		calls []applyCall
	)
	apply := recordingApply(&mu, &calls, kukeonv1.ApplyDocumentsResult{})

	for _, tc := range []struct{ name, body string }{
		{"dezot", strings.ReplaceAll(otherProjectYAML, "kukeon", "dezot")},
		{"kukeon", otherProjectYAML},
	} {
		dir := writeProject(t, tc.body)
		if err := composeTeam(
			context.Background(), &bytes.Buffer{}, io.Discard, dir, layout,
			stubGit(nil), stubProjectURL("git@github.com:eminwux/"+tc.name+".git"),
			stubBundle(bundle), apply, stubBuildErr(), false, false,
		); err != nil {
			t.Fatalf("composeTeam(%q): %v", tc.name, err)
		}
	}

	for _, team := range []string{"dezot", "kukeon"} {
		if _, err := os.Stat(layout.RoleHarnessStateDir(team, "dev", "claude")); err != nil {
			t.Errorf("team %q state dir missing: %v", team, err)
		}
		if _, err := os.Stat(layout.HarnessSeedPath(team, "claude", "")); err != nil {
			t.Errorf("team %q seed missing: %v", team, err)
		}
	}

	// Tampering dezot's seed must not affect kukeon's.
	dezotSeed := layout.HarnessSeedPath("dezot", "claude", "")
	kukeonSeed := layout.HarnessSeedPath("kukeon", "claude", "")
	kukeonBefore, err := os.ReadFile(kukeonSeed)
	if err != nil {
		t.Fatalf("read kukeon seed: %v", err)
	}
	if writeErr := os.WriteFile(dezotSeed, []byte("tampered\n"), 0o644); writeErr != nil {
		t.Fatalf("tamper dezot: %v", writeErr)
	}
	kukeonAfter, err := os.ReadFile(kukeonSeed)
	if err != nil {
		t.Fatalf("read kukeon seed after tamper: %v", err)
	}
	if !bytes.Equal(kukeonBefore, kukeonAfter) {
		t.Errorf("kukeon seed disturbed by dezot edit:\nbefore=%q\nafter=%q",
			kukeonBefore, kukeonAfter)
	}
}

// TestComposeTeamProvisionDryRunWritesNothing extends the existing
// dry-run AC to the host-state provisioning pass: the announcements
// land on stdout, but no per-team root materializes.
func TestComposeTeamProvisionDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	projectDir := writeProject(t, projectTeamWithHarnessYAML)
	layout := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	bundle := buildClaudeBundle(t)
	bundle.Harnesses["claude"].Spec.Seeds = []model.HarnessSeed{
		{Path: "${HARNESS}.json", Mode: 0o644, Content: "{}\n"},
	}

	var out bytes.Buffer
	if err := composeTeam(
		context.Background(), &out, io.Discard, projectDir, layout,
		stubGit(nil), stubProjectURL("git@github.com:eminwux/sbsh.git"),
		stubBundle(bundle), stubApplyErr(), stubBuildErr(), true, false,
	); err != nil {
		t.Fatalf("composeTeam dry-run: %v", err)
	}
	if _, err := os.Stat(layout.TeamDir("sbsh")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("dry-run created team dir: stat err=%v", err)
	}
	body := out.String()
	stateDir := layout.RoleHarnessStateDir("sbsh", "dev", "claude")
	if !strings.Contains(body, stateDir) {
		t.Errorf("dry-run output missing state dir %q:\n%s", stateDir, body)
	}
	if !strings.Contains(body, layout.HarnessSeedPath("sbsh", "claude", "")) {
		t.Errorf("dry-run output missing seed path:\n%s", body)
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

// TestComposeTeamScaffoldsSecretsEnvFilesOnFirstInit confirms AC: a fresh
// `~/.kuke/teams/` gets a templated shared `secrets.env`; the team's dir
// gets a per-team `secrets.env`; both 0o600; both seeded with one `KEY=`
// line per secret name the team's roles need.
func TestComposeTeamScaffoldsSecretsEnvFilesOnFirstInit(t *testing.T) {
	t.Parallel()
	projectDir := writeProject(t, projectTeamWithHarnessYAML)
	layout := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	bundle := buildClaudeBundle(t)
	// Add a second secret to the role's needs so the scaffold body has more
	// than one line — the sort-and-emit invariant is then visible.
	bundle.Roles["dev"].Spec.Needs.Secrets = []string{"OPENROUTER_API_KEY", "ANTHROPIC_AUTH_TOKEN"}

	var (
		mu      sync.Mutex
		applies []applyCall
	)
	var errOut bytes.Buffer
	err := composeTeam(
		context.Background(), &bytes.Buffer{}, &errOut, projectDir, layout,
		stubGit(nil), stubProjectURL("git@github.com:eminwux/sbsh.git"),
		stubBundle(bundle), recordingApply(&mu, &applies, kukeonv1.ApplyDocumentsResult{}),
		stubBuildErr(), false, false,
	)
	if err != nil {
		t.Fatalf("composeTeam: %v", err)
	}

	sharedPath := layout.SharedSecretsEnvPath()
	teamPath := filepath.Join(layout.TeamDir("sbsh"), "secrets.env")
	for _, p := range []string{sharedPath, teamPath} {
		info, statErr := os.Stat(p)
		if statErr != nil {
			t.Fatalf("expected scaffolded file %q: %v", p, statErr)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("file %q mode = %o, want 0600", p, info.Mode().Perm())
		}
		raw, readErr := os.ReadFile(p)
		if readErr != nil {
			t.Fatalf("read %q: %v", p, readErr)
		}
		want := "ANTHROPIC_AUTH_TOKEN=\nOPENROUTER_API_KEY=\n"
		if string(raw) != want {
			t.Errorf("file %q body = %q, want %q", p, raw, want)
		}
	}

	// One warning per empty key fires on errOut. Values would never appear
	// in the warning anyway — the lines were KEY= with no value — but pin
	// the AC: warning fires once per empty key.
	body := errOut.String()
	for _, k := range []string{"ANTHROPIC_AUTH_TOKEN", "OPENROUTER_API_KEY"} {
		if !strings.Contains(body, k) {
			t.Errorf("missing warning for empty key %q: %q", k, body)
		}
	}
}

// TestComposeTeamRespectsPopulatedSecretsEnv covers the AC: re-running
// `kuke team init` against a populated shared or per-team secrets.env
// never overwrites it, and the populated values land on the rendered
// Secret docs (per-team value wins for shared keys).
func TestComposeTeamRespectsPopulatedSecretsEnv(t *testing.T) {
	t.Parallel()
	projectDir := writeProject(t, projectTeamWithHarnessYAML)
	layout := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	bundle := buildClaudeBundle(t)
	bundle.Roles["dev"].Spec.Needs.Secrets = []string{"ANTHROPIC_AUTH_TOKEN", "OPENROUTER_API_KEY"}

	// First init: scaffolds both files (empty).
	var (
		mu      sync.Mutex
		applies []applyCall
	)
	apply := recordingApply(&mu, &applies, kukeonv1.ApplyDocumentsResult{})
	if err := composeTeam(
		context.Background(), &bytes.Buffer{}, io.Discard, projectDir, layout,
		stubGit(nil), stubProjectURL("git@github.com:eminwux/sbsh.git"),
		stubBundle(bundle), apply, stubBuildErr(), false, false,
	); err != nil {
		t.Fatalf("first init: %v", err)
	}

	// Operator fills shared (both keys) and per-team (one override).
	sharedPath := layout.SharedSecretsEnvPath()
	teamPath := filepath.Join(layout.TeamDir("sbsh"), "secrets.env")
	if err := os.WriteFile(sharedPath, []byte(
		"ANTHROPIC_AUTH_TOKEN=shared-anth\nOPENROUTER_API_KEY=shared-or\n"), 0o600); err != nil {
		t.Fatalf("populate shared: %v", err)
	}
	if err := os.WriteFile(teamPath, []byte(
		"OPENROUTER_API_KEY=team-or\n"), 0o600); err != nil {
		t.Fatalf("populate team: %v", err)
	}

	// Re-init: shared + per-team carry through to the apply payload.
	if err := composeTeam(
		context.Background(), &bytes.Buffer{}, io.Discard, projectDir, layout,
		stubGit(nil), stubProjectURL("git@github.com:eminwux/sbsh.git"),
		stubBundle(bundle), apply, stubBuildErr(), false, false,
	); err != nil {
		t.Fatalf("re-init: %v", err)
	}
	if len(applies) != 2 {
		t.Fatalf("apply call count = %d, want 2", len(applies))
	}
	body := string(applies[1].RawYAML)
	if !strings.Contains(body, "shared-anth") {
		t.Errorf("re-init apply missing shared-only value carry-through:\n%s", body)
	}
	if !strings.Contains(body, "team-or") {
		t.Errorf("re-init apply missing per-team override:\n%s", body)
	}
	if strings.Contains(body, "shared-or") {
		t.Errorf("re-init apply leaked shared value overridden per-team:\n%s", body)
	}

	// Files untouched by the re-init.
	rawShared, _ := os.ReadFile(sharedPath)
	if !strings.Contains(string(rawShared), "shared-anth") || !strings.Contains(string(rawShared), "shared-or") {
		t.Errorf("re-init overwrote populated shared file: %q", rawShared)
	}
	rawTeam, _ := os.ReadFile(teamPath)
	if !strings.Contains(string(rawTeam), "team-or") {
		t.Errorf("re-init overwrote populated team file: %q", rawTeam)
	}
}

// TestComposeTeamBundlesSecretsBeforeBlueprintsAndConfigs pins AC:
// bundle order is Secrets → Blueprints → Configs in the applied YAML.
// The renderer's MarshalYAML emits the three sections in that order; a
// reader can verify by the byte-offset of each section's "kind:" line.
func TestComposeTeamBundlesSecretsBeforeBlueprintsAndConfigs(t *testing.T) {
	t.Parallel()
	projectDir := writeProject(t, projectTeamWithHarnessYAML)
	layout := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	bundle := buildClaudeBundle(t)
	bundle.Roles["dev"].Spec.Needs.Secrets = []string{"ANTHROPIC_AUTH_TOKEN"}
	// Scaffold a populated shared so the rendered set carries one Secret.
	if err := os.MkdirAll(filepath.Join(layout.Base, "teams"), 0o700); err != nil {
		t.Fatalf("mkdir teams: %v", err)
	}
	if err := os.WriteFile(layout.SharedSecretsEnvPath(),
		[]byte("ANTHROPIC_AUTH_TOKEN=populated\n"), 0o600); err != nil {
		t.Fatalf("seed shared: %v", err)
	}

	var (
		mu      sync.Mutex
		applies []applyCall
	)
	if err := composeTeam(
		context.Background(), &bytes.Buffer{}, io.Discard, projectDir, layout,
		stubGit(nil), stubProjectURL("git@github.com:eminwux/sbsh.git"),
		stubBundle(bundle), recordingApply(&mu, &applies, kukeonv1.ApplyDocumentsResult{}),
		stubBuildErr(), false, false,
	); err != nil {
		t.Fatalf("composeTeam: %v", err)
	}
	if len(applies) != 1 {
		t.Fatalf("apply call count = %d, want 1", len(applies))
	}
	body := string(applies[0].RawYAML)
	secretIdx := strings.Index(body, "kind: Secret")
	bpIdx := strings.Index(body, "kind: CellBlueprint")
	cfgIdx := strings.Index(body, "kind: CellConfig")
	if secretIdx < 0 || bpIdx < 0 || cfgIdx < 0 {
		t.Fatalf("missing one of Secret/CellBlueprint/CellConfig in bundle:\n%s", body)
	}
	if secretIdx >= bpIdx || bpIdx >= cfgIdx {
		t.Errorf("bundle order wrong: secret=%d blueprint=%d config=%d\n%s",
			secretIdx, bpIdx, cfgIdx, body)
	}
	// Rendered Secret carries the default realm.
	if !strings.Contains(body, "realm: default") {
		t.Errorf("rendered Secret missing realm: default\n%s", body)
	}
	// Kebab-case name landed.
	if !strings.Contains(body, "name: anthropic-auth-token") {
		t.Errorf("rendered Secret missing kebab-cased name\n%s", body)
	}
	if !strings.Contains(body, "data: populated") {
		t.Errorf("rendered Secret missing spec.data\n%s", body)
	}
}

// TestComposeTeamDryRunDoesNotScaffoldSecretsEnv pins AC: --dry-run
// touches no files on disk — including the secrets.env scaffold path.
// Render still announces what would have rendered; on a clean host with
// no populated values, no Secret docs surface (empty values are skipped).
func TestComposeTeamDryRunDoesNotScaffoldSecretsEnv(t *testing.T) {
	t.Parallel()
	projectDir := writeProject(t, projectTeamWithHarnessYAML)
	layout := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	bundle := buildClaudeBundle(t)
	bundle.Roles["dev"].Spec.Needs.Secrets = []string{"ANTHROPIC_AUTH_TOKEN"}

	var (
		out    bytes.Buffer
		errOut bytes.Buffer
	)
	if err := composeTeam(
		context.Background(), &out, &errOut, projectDir, layout,
		stubGit(nil), stubProjectURL("git@github.com:eminwux/sbsh.git"),
		stubBundle(bundle), stubApplyErr(), stubBuildErr(), true, false,
	); err != nil {
		t.Fatalf("composeTeam dry-run: %v", err)
	}
	sharedPath := layout.SharedSecretsEnvPath()
	teamPath := filepath.Join(layout.TeamDir("sbsh"), "secrets.env")
	if _, statErr := os.Stat(sharedPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("dry-run wrote shared secrets.env: %v", statErr)
	}
	if _, statErr := os.Stat(teamPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("dry-run wrote team secrets.env: %v", statErr)
	}
	// Warning still fires on empty key — the AC's "Empty-value warning fires
	// once per empty key" applies regardless of dry-run mode.
	if !strings.Contains(errOut.String(), "ANTHROPIC_AUTH_TOKEN") {
		t.Errorf("dry-run missing empty-key warning: %q", errOut.String())
	}
}

func TestEmitSourceKind(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		src  model.TeamSource
		want string
	}{
		{
			"tag pinned",
			model.TeamSource{Repo: "github.com/eminwux/agents", Tag: "v1.4.0"},
			"agents source: github.com/eminwux/agents @ tag=v1.4.0 (pinned)\n",
		},
		{
			"branch floating",
			model.TeamSource{Repo: "eminwux/agents", Branch: "main"},
			"agents source: github.com/eminwux/agents @ branch=main (floating)\n",
		},
		{
			"commit pinned",
			model.TeamSource{Repo: "gitlab.com/grp/repo", Commit: "9ae9606"},
			"agents source: gitlab.com/grp/repo @ commit=9ae9606 (pinned)\n",
		},
		{"malformed prints nothing", model.TeamSource{Repo: "github.com/eminwux/agents"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			emitSourceKind(&out, tc.src)
			if out.String() != tc.want {
				t.Errorf("emitSourceKind = %q, want %q", out.String(), tc.want)
			}
		})
	}
}

// TestValidateTeamCleanExitsZero covers `kuke team init --validate` on a
// roster whose catalog, templates, partials, and facts all check out: the gap
// report prints all four section headers, validateTeam returns nil (exit 0),
// and nothing is written to disk (no global config, no drop-in entry).
func TestValidateTeamCleanExitsZero(t *testing.T) {
	t.Parallel()
	projectDir := writeProject(t, projectTeamWithHarnessYAML)
	layout := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	bundle := buildClaudeBundle(t)

	var out bytes.Buffer
	if err := validateTeam(
		context.Background(), &out, projectDir, layout,
		stubGit(nil), stubBundle(bundle),
	); err != nil {
		t.Fatalf("validateTeam: %v", err)
	}

	got := out.String()
	for _, want := range []string{"== catalog ==", "== templates ==", "== partials ==", "== facts =="} {
		if !strings.Contains(got, want) {
			t.Errorf("report missing %q\n%s", want, got)
		}
	}
	// Read-only: validate must not scaffold the global facts file or write the
	// drop-in entry.
	if _, err := os.Stat(layout.GlobalConfigPath()); !os.IsNotExist(err) {
		t.Errorf("validate wrote global config (err=%v); must be read-only", err)
	}
	if _, err := os.Stat(layout.EntryPath("sbsh")); !os.IsNotExist(err) {
		t.Errorf("validate wrote drop-in entry (err=%v); must be read-only", err)
	}
}

// TestValidateTeamGapExitsNonZero covers the non-zero exit: a catalog gap
// surfaces ErrTeamValidateGaps so the CLI exits 1, while the report still
// prints.
func TestValidateTeamGapExitsNonZero(t *testing.T) {
	t.Parallel()
	projectDir := writeProject(t, projectTeamWithHarnessYAML)
	layout := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	bundle := buildClaudeBundle(t)
	// Drop a capability the dev role needs (go,git) so SelectImage misses.
	bundle.ImageCatalog.Spec.Images[0].Capabilities = []string{"go"}

	var out bytes.Buffer
	err := validateTeam(
		context.Background(), &out, projectDir, layout,
		stubGit(nil), stubBundle(bundle),
	)
	if !errors.Is(err, errdefs.ErrTeamValidateGaps) {
		t.Fatalf("err = %v, want ErrTeamValidateGaps", err)
	}
	if !strings.Contains(out.String(), `capability "git" not provided by any claude image`) {
		t.Errorf("report should name the unmet capability:\n%s", out.String())
	}
}

// TestValidateTeamHarnessLessRosterSkipsResolve covers the harness-less
// roster: there is nothing to validate, so resolve is never called (a clone is
// not triggered) and the four empty sections still render at exit 0.
func TestValidateTeamHarnessLessRosterSkipsResolve(t *testing.T) {
	t.Parallel()
	projectDir := writeProject(t, projectTeamYAML) // no defaults.harnesses
	layout := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))

	var out bytes.Buffer
	if err := validateTeam(
		context.Background(), &out, projectDir, layout,
		stubGit(nil), stubResolveErr(), // resolve must not be called
	); err != nil {
		t.Fatalf("validateTeam: %v", err)
	}
	for _, want := range []string{"== catalog ==", "== templates ==", "== partials ==", "== facts =="} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report missing %q\n%s", want, out.String())
		}
	}
}

// TestInitValidateBuildMutuallyExclusive covers the cobra-level guard: passing
// both --build and --validate is rejected before any side effect runs.
func TestInitValidateBuildMutuallyExclusive(t *testing.T) {
	t.Parallel()
	cmd := NewInitCmd()
	cmd.SetArgs([]string{"--build", "--validate"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected mutual-exclusion error for --build --validate, got nil")
	}
}
