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

package teamrender

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/teamsource"
	model "github.com/eminwux/kukeon/pkg/api/model/kuketeams"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func TestMergeNeedsSortsAndDedupes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		a, b []string
		want []string
	}{
		{"empty inputs", nil, nil, []string{}},
		{"single side", []string{"go"}, nil, []string{"go"}},
		{"both sides", []string{"go"}, []string{"git"}, []string{"git", "go"}},
		{"dedupes overlap", []string{"go", "git"}, []string{"git", "make"}, []string{"git", "go", "make"}},
		{"trims and drops blanks", []string{" go ", ""}, []string{"\tgit\t", "  "}, []string{"git", "go"}},
		{"already sorted reproducible", []string{"a", "b"}, []string{"a", "b"}, []string{"a", "b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := MergeNeeds(tc.a, tc.b)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("MergeNeeds(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestMergeNeedsDeterministicAcrossCalls(t *testing.T) {
	t.Parallel()
	a := []string{"go", "git", "make"}
	b := []string{"docker", "make"}
	first := MergeNeeds(a, b)
	second := MergeNeeds(a, b)
	if !reflect.DeepEqual(first, second) {
		t.Errorf("non-deterministic output: %v vs %v", first, second)
	}
}

// minimalClaudeCatalog returns a catalog with one claude image carrying the
// listed capabilities. Helper for the SelectImage tests; tests that need a
// different harness mutate the returned catalog's images directly (see
// TestSelectImageHonoursHarnessFilter).
func minimalClaudeCatalog(caps ...string) *model.ImageCatalog {
	return &model.ImageCatalog{
		APIVersion: model.APIVersionV1,
		Kind:       model.KindImageCatalog,
		Spec: model.ImageCatalogSpec{
			Images: []model.ImageCatalogEntry{
				{
					Ref:          "claude-base",
					Harness:      "claude",
					Image:        "registry.local/claude:latest",
					Build:        model.ImageCatalogBuild{Context: "harnesses/claude", Dockerfile: "Dockerfile"},
					Capabilities: caps,
				},
			},
		},
	}
}

func TestSelectImagePicksFirstMatch(t *testing.T) {
	t.Parallel()
	ic := minimalClaudeCatalog("go", "git", "make")
	entry, err := SelectImage(ic, "claude", []string{"go", "git"})
	if err != nil {
		t.Fatalf("SelectImage error = %v", err)
	}
	if entry.Image != "registry.local/claude:latest" {
		t.Errorf("entry.Image = %q, want claude:latest", entry.Image)
	}
}

func TestSelectImageHonoursHarnessFilter(t *testing.T) {
	t.Parallel()
	ic := minimalClaudeCatalog("go")
	// Adding a codex image with the right capability set should not satisfy
	// a claude request, since the harness keys differ.
	ic.Spec.Images = append(ic.Spec.Images, model.ImageCatalogEntry{
		Ref:          "codex-base",
		Harness:      "codex",
		Image:        "registry.local/codex:latest",
		Build:        model.ImageCatalogBuild{Context: "harnesses/codex", Dockerfile: "Dockerfile"},
		Capabilities: []string{"go", "git", "make"},
	})
	if _, err := SelectImage(ic, "claude", []string{"git"}); !errors.Is(err, errdefs.ErrTeamImageNoMatch) {
		t.Errorf("err = %v, want ErrTeamImageNoMatch (codex image must not satisfy claude need)", err)
	}
}

func TestSelectImageEmptyNeedsMatchesFirstHarnessImage(t *testing.T) {
	t.Parallel()
	ic := minimalClaudeCatalog() // zero capabilities
	entry, err := SelectImage(ic, "claude", nil)
	if err != nil {
		t.Fatalf("empty needs should match any harness image: %v", err)
	}
	if entry.Harness != "claude" {
		t.Errorf("matched wrong harness: %q", entry.Harness)
	}
}

func TestSelectImageNilCatalogHardErrors(t *testing.T) {
	t.Parallel()
	_, err := SelectImage(nil, "claude", []string{"go"})
	if !errors.Is(err, errdefs.ErrTeamImageNoMatch) {
		t.Fatalf("err = %v, want ErrTeamImageNoMatch", err)
	}
	if !strings.Contains(err.Error(), "build or label") {
		t.Errorf("error missing build-or-label hint: %v", err)
	}
}

func TestSelectImageNamesFirstUnmetCapability(t *testing.T) {
	t.Parallel()
	ic := minimalClaudeCatalog("go")
	_, err := SelectImage(ic, "claude", []string{"git", "go"})
	if !errors.Is(err, errdefs.ErrTeamImageNoMatch) {
		t.Fatalf("err = %v, want ErrTeamImageNoMatch", err)
	}
	if !strings.Contains(err.Error(), `capability="git"`) {
		t.Errorf("error should name first-unmet capability 'git', got: %v", err)
	}
}

func TestSelectImageDistinguishesMultiImagePartialCoverage(t *testing.T) {
	t.Parallel()
	// Each capability is provided by *some* image, but no single image
	// carries both — the renderer should surface a different message than
	// the single-unmet path.
	ic := minimalClaudeCatalog("go")
	ic.Spec.Images = append(ic.Spec.Images, model.ImageCatalogEntry{
		Ref:          "claude-git",
		Harness:      "claude",
		Image:        "registry.local/claude-git:latest",
		Build:        model.ImageCatalogBuild{Context: "harnesses/claude", Dockerfile: "Dockerfile"},
		Capabilities: []string{"git"},
	})
	_, err := SelectImage(ic, "claude", []string{"go", "git"})
	if !errors.Is(err, errdefs.ErrTeamImageNoMatch) {
		t.Fatalf("err = %v, want ErrTeamImageNoMatch", err)
	}
	if !strings.Contains(err.Error(), "no single image") {
		t.Errorf("error should distinguish the consolidation case, got: %v", err)
	}
}

// writeHarnessFile writes a file under <cacheDir>/harnesses/<name>/<file>
// (the standard agents-source harness directory layout teamsource.HarnessDir
// resolves to) so tests can colocate a blueprint template and its sibling
// partials the way the renderer expects.
func writeHarnessFile(t *testing.T, cacheDir, harnessName, filename, body string) {
	t.Helper()
	p := filepath.Join(teamsource.HarnessDir(cacheDir, harnessName), filename)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// buildClaudeTemplate writes a minimal claude blueprint template into
// <cacheDir>/harnesses/claude/blueprint.tmpl.yaml using the Go
// text/template dot-context the agents repo's published blueprints are
// authored against (per AC: relative-to-harness-dir, text/template engine).
func buildClaudeTemplate(t *testing.T, cacheDir string) {
	t.Helper()
	body := `apiVersion: v1beta1
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
          - { name: agents, target: /src/agents }
        secrets:
          - { name: ANTHROPIC_API_KEY, mode: env, envName: ANTHROPIC_API_KEY }
`
	writeHarnessFile(t, cacheDir, "claude", "blueprint.tmpl.yaml", body)
}

// minimalRole returns a role declaring the listed image-needs plus an
// ANTHROPIC_API_KEY secret need and a claude-Settings per-harness config.
func minimalRole() *model.Role {
	return &model.Role{
		APIVersion: model.APIVersionV1,
		Kind:       model.KindRole,
		Metadata:   model.Metadata{Name: "dev"},
		Spec: model.RoleSpec{
			Harnesses: map[string]model.RoleHarness{
				"claude": {Settings: "agents/dev/settings.json"},
				"codex":  {Sandbox: "workspace-write", Approval: "on-request"},
			},
			Needs: model.RoleNeeds{
				Image:   []string{"go", "git"},
				Repos:   []string{"project", "agents"},
				Secrets: []string{"ANTHROPIC_API_KEY"},
			},
		},
	}
}

func TestRenderBlueprintSubstitutesAndStampsTeamLabel(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	buildClaudeTemplate(t, cacheDir)
	r := minimalRole()
	h := &model.Harness{
		APIVersion: model.APIVersionV1,
		Kind:       model.KindHarness,
		Metadata:   model.Metadata{Name: "claude"},
		Spec:       model.HarnessSpec{Template: "blueprint.tmpl.yaml"},
	}
	image := &model.ImageCatalogEntry{
		Ref:          "claude-base",
		Harness:      "claude",
		Image:        "registry.local/claude:latest",
		Capabilities: []string{"go", "git"},
	}
	bp, err := RenderBlueprint(
		cacheDir,
		h,
		r,
		"claude",
		"dev",
		[]string{"git", "go"},
		image,
		nil,
		"sbsh",
		"default",
		false,
		"",
	)
	if err != nil {
		t.Fatalf("RenderBlueprint: %v", err)
	}
	if bp.Metadata.Name != "dev-claude" {
		t.Errorf("blueprint name = %q, want dev-claude", bp.Metadata.Name)
	}
	if bp.Metadata.Realm != "default" {
		t.Errorf("realm = %q, want default", bp.Metadata.Realm)
	}
	if bp.Metadata.Labels[v1beta1.LabelTeam] != "sbsh" {
		t.Errorf("team label = %q, want sbsh", bp.Metadata.Labels[v1beta1.LabelTeam])
	}
	if len(bp.Spec.Cell.Containers) == 0 {
		t.Fatalf("no containers in rendered blueprint")
	}
	c := bp.Spec.Cell.Containers[0]
	if c.ID != "dev" {
		t.Errorf("container id = %q, want dev (substitution failed)", c.ID)
	}
	if c.Image != "registry.local/claude:latest" {
		t.Errorf("container image = %q, want substituted IMAGE", c.Image)
	}
	wantEnv := []string{"ROLE=dev", "NEEDS=git,go", "SETTINGS=agents/dev/settings.json"}
	if !reflect.DeepEqual(c.Env, wantEnv) {
		t.Errorf("env = %v, want %v (verbatim per-harness config wiring)", c.Env, wantEnv)
	}
}

// TestRenderBlueprintBindsInternalRefInBuildMode covers AC: build mode binds
// the locally-built kukeon.internal/<ref>:<version> ref (the tag teambuild
// produces), while ${IMAGE_REF} keeps the catalog selector key unchanged.
func TestRenderBlueprintBindsInternalRefInBuildMode(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	buildClaudeTemplate(t, cacheDir)
	r := minimalRole()
	h := &model.Harness{
		APIVersion: model.APIVersionV1,
		Kind:       model.KindHarness,
		Metadata:   model.Metadata{Name: "claude"},
		Spec:       model.HarnessSpec{Template: "blueprint.tmpl.yaml"},
	}
	image := &model.ImageCatalogEntry{
		Ref:          "claude-base",
		Harness:      "claude",
		Image:        "registry.local/claude:latest",
		Capabilities: []string{"go", "git"},
	}
	bp, err := RenderBlueprint(
		cacheDir,
		h,
		r,
		"claude",
		"dev",
		[]string{"git", "go"},
		image,
		nil,
		"sbsh",
		"default",
		true,
		"v1.4.0",
	)
	if err != nil {
		t.Fatalf("RenderBlueprint: %v", err)
	}
	c := bp.Spec.Cell.Containers[0]
	if want := "kukeon.internal/claude-base:v1.4.0"; c.Image != want {
		t.Errorf("build-mode image = %q, want %q", c.Image, want)
	}
}

// TestRenderBlueprintBindsPublishedRefWithoutBuild is the no-flag counterpart:
// the catalog entry's published Image is bound verbatim.
func TestRenderBlueprintBindsPublishedRefWithoutBuild(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	buildClaudeTemplate(t, cacheDir)
	r := minimalRole()
	h := &model.Harness{Spec: model.HarnessSpec{Template: "blueprint.tmpl.yaml"}}
	image := &model.ImageCatalogEntry{
		Ref:          "claude-base",
		Harness:      "claude",
		Image:        "ghcr.io/eminwux/claude:v1.4.0",
		Capabilities: []string{"go", "git"},
	}
	// build=true but an empty sourceRef must fall back to the published ref
	// rather than emit a floating kukeon.internal/<ref>: ref with no version.
	for _, tc := range []struct {
		name      string
		build     bool
		sourceRef string
	}{
		{"no-build", false, "v1.4.0"},
		{"build-empty-sourceRef", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			bp, err := RenderBlueprint(
				cacheDir,
				h,
				r,
				"claude",
				"dev",
				[]string{"git", "go"},
				image,
				nil,
				"sbsh",
				"default",
				tc.build,
				tc.sourceRef,
			)
			if err != nil {
				t.Fatalf("RenderBlueprint: %v", err)
			}
			if want := "ghcr.io/eminwux/claude:v1.4.0"; bp.Spec.Cell.Containers[0].Image != want {
				t.Errorf("image = %q, want published %q", bp.Spec.Cell.Containers[0].Image, want)
			}
		})
	}
}

func TestRenderBlueprintMissingTemplate(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	r := minimalRole()
	h := &model.Harness{Spec: model.HarnessSpec{Template: "blueprint.tmpl.yaml"}}
	_, err := RenderBlueprint(cacheDir, h, r, "missing", "dev", nil, nil, nil, "sbsh", "default", false, "")
	if !errors.Is(err, errdefs.ErrTeamBlueprintTemplateMissing) {
		t.Fatalf("err = %v, want ErrTeamBlueprintTemplateMissing", err)
	}
}

func TestRenderBlueprintEmptyTemplatePathRejected(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	r := minimalRole()
	h := &model.Harness{Spec: model.HarnessSpec{}}
	_, err := RenderBlueprint(cacheDir, h, r, "claude", "dev", nil, nil, nil, "sbsh", "default", false, "")
	if !errors.Is(err, errdefs.ErrTeamBlueprintTemplateMissing) {
		t.Fatalf("err = %v, want ErrTeamBlueprintTemplateMissing", err)
	}
}

// TestRenderBlueprintResolvesTemplateRelativeToHarnessDir confirms the
// bare-filename form (the agents repo's canonical layout: harness.yaml +
// blueprint.tmpl.yaml as siblings under harnesses/<name>/) resolves under
// the harness dir rather than the cache root. Regression guard for the
// path-resolution AC: a template that references `blueprint.tmpl.yaml`
// (no leading `harnesses/<name>/` prefix) must still render.
func TestRenderBlueprintResolvesTemplateRelativeToHarnessDir(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	buildClaudeTemplate(t, cacheDir)
	// Deliberately *no* harnesses/claude/blueprint.tmpl.yaml at cache root —
	// the file lives at <cacheDir>/harnesses/claude/blueprint.tmpl.yaml and
	// Spec.Template names the bare sibling.
	h := &model.Harness{Spec: model.HarnessSpec{Template: "blueprint.tmpl.yaml"}}
	image := &model.ImageCatalogEntry{
		Ref:          "claude-base",
		Harness:      "claude",
		Image:        "registry.local/claude:latest",
		Capabilities: []string{"go", "git"},
	}
	bp, err := RenderBlueprint(
		cacheDir, h, minimalRole(), "claude", "dev",
		[]string{"git", "go"}, image, nil, "sbsh", "default", false, "",
	)
	if err != nil {
		t.Fatalf("RenderBlueprint with bare-filename template: %v", err)
	}
	if bp.Spec.Cell.Containers[0].Image != "registry.local/claude:latest" {
		t.Errorf("image = %q, want resolved-via-harness-dir image",
			bp.Spec.Cell.Containers[0].Image)
	}
}

// TestRenderBlueprintLoadsSiblingPartials covers AC: a blueprint that calls
// `{{ template "mount_source" . }}` against a sibling partial that defines
// it renders successfully. The renderer must pick up every *.tmpl.yaml in
// the same dir as the resolved template.
func TestRenderBlueprintLoadsSiblingPartials(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	mainBody := `apiVersion: v1beta1
kind: CellBlueprint
metadata:
  name: {{ .role.name }}-{{ .harness }}
spec:
  cell:
    containers:
      - id: {{ .role.name }}
        image: {{ .image }}
        env:
          - "MOUNT_SOURCE={{ template "mount_source" .role.name }}"
`
	partialBody := `{{- define "mount_source" -}}
/srv/{{ . | upper | replace "-" "_" }}
{{- end -}}
`
	writeHarnessFile(t, cacheDir, "claude", "blueprint.tmpl.yaml", mainBody)
	writeHarnessFile(t, cacheDir, "claude", "partials.tmpl.yaml", partialBody)

	h := &model.Harness{Spec: model.HarnessSpec{Template: "blueprint.tmpl.yaml"}}
	image := &model.ImageCatalogEntry{Image: "registry.local/claude:latest"}
	bp, err := RenderBlueprint(
		cacheDir, h, minimalRole(), "claude", "my-dev",
		[]string{"go"}, image, nil, "sbsh", "default", false, "",
	)
	if err != nil {
		t.Fatalf("RenderBlueprint with sibling partials: %v", err)
	}
	wantEnv := []string{"MOUNT_SOURCE=/srv/MY_DEV"}
	if !reflect.DeepEqual(bp.Spec.Cell.Containers[0].Env, wantEnv) {
		t.Errorf("env = %v, want %v (partial + upper/replace funcs)",
			bp.Spec.Cell.Containers[0].Env, wantEnv)
	}
}

// TestRenderBlueprintNeedsAndOperatorContext covers the AC's fixture-template
// requirement: `{{ range .needs.repos }}` and `{{ .operator.GIT_USER_NAME }}`
// round-trip to a valid CellBlueprint. Both keys are wired by #1110 — the
// renderer must iterate role.yaml's needs.repos and expose
// tc.Spec.Git.Author.Name under .operator.GIT_USER_NAME.
func TestRenderBlueprintNeedsAndOperatorContext(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	body := `apiVersion: v1beta1
kind: CellBlueprint
metadata:
  name: {{ .role.name }}-{{ .harness }}
  labels:
    kukeon.io/operator: {{ .operator.GIT_USER_NAME }}
spec:
  cell:
    containers:
      - id: {{ .role.name }}
        image: {{ .image }}
        repos:
{{- range .needs.repos }}
          - { name: {{ . }}, target: /src/{{ . }} }
{{- end }}
`
	writeHarnessFile(t, cacheDir, "claude", "blueprint.tmpl.yaml", body)

	h := &model.Harness{Spec: model.HarnessSpec{Template: "blueprint.tmpl.yaml"}}
	image := &model.ImageCatalogEntry{Image: "registry.local/claude:latest"}
	tc := &model.TeamsConfig{Spec: model.TeamsConfigSpec{
		Git: &model.TeamsConfigGit{
			ContainerGit: v1beta1.ContainerGit{
				Author: &v1beta1.GitIdentity{Name: "Operator Name", Email: "op@example.com"},
			},
		},
	}}
	bp, err := RenderBlueprint(
		cacheDir, h, minimalRole(), "claude", "dev",
		[]string{"go"}, image, tc, "sbsh", "default", false, "",
	)
	if err != nil {
		t.Fatalf("RenderBlueprint: %v", err)
	}
	if got := bp.Metadata.Labels["kukeon.io/operator"]; got != "Operator Name" {
		t.Errorf("operator label = %q, want %q", got, "Operator Name")
	}
	c := bp.Spec.Cell.Containers[0]
	if len(c.Repos) != 2 {
		t.Fatalf("repos = %d, want 2 (one per .needs.repos entry)", len(c.Repos))
	}
	wantRepos := map[string]string{"project": "/src/project", "agents": "/src/agents"}
	for _, repo := range c.Repos {
		want, ok := wantRepos[repo.Name]
		if !ok {
			t.Errorf("unexpected repo: %+v", repo)
			continue
		}
		if repo.Target != want {
			t.Errorf("repo %q target = %q, want %q", repo.Name, repo.Target, want)
		}
	}
}

// TestRenderBlueprintRunsUpperReplaceFuncs covers AC: `upper` and `replace`
// template functions are wired and compose cleanly via the sprig-style pipe
// idiom (`{{ . | upper | replace "-" "_" }}`).
func TestRenderBlueprintRunsUpperReplaceFuncs(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	body := `apiVersion: v1beta1
kind: CellBlueprint
metadata:
  name: {{ .role.name }}-{{ .harness }}
spec:
  cell:
    containers:
      - id: {{ .role.name }}
        image: {{ .image }}
        env:
          - "ENV_NAME={{ .role.name | upper | replace "-" "_" }}"
`
	writeHarnessFile(t, cacheDir, "claude", "blueprint.tmpl.yaml", body)

	h := &model.Harness{Spec: model.HarnessSpec{Template: "blueprint.tmpl.yaml"}}
	image := &model.ImageCatalogEntry{Image: "x"}
	bp, err := RenderBlueprint(
		cacheDir, h, minimalRole(), "claude", "pr-reviewer",
		nil, image, nil, "sbsh", "default", false, "",
	)
	if err != nil {
		t.Fatalf("RenderBlueprint: %v", err)
	}
	wantEnv := []string{"ENV_NAME=PR_REVIEWER"}
	if !reflect.DeepEqual(bp.Spec.Cell.Containers[0].Env, wantEnv) {
		t.Errorf("env = %v, want %v", bp.Spec.Cell.Containers[0].Env, wantEnv)
	}
}

// TestRenderBlueprintWiresCodexConfigVerbatim confirms the codex
// sandbox/approval knobs land in the dot-context verbatim — covers the AC's
// "codex sandbox/approval knobs" wiring path.
func TestRenderBlueprintWiresCodexConfigVerbatim(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	body := `apiVersion: v1beta1
kind: CellBlueprint
metadata:
  name: {{ .role.name }}-{{ .harness }}
spec:
  cell:
    containers:
      - id: {{ .role.name }}
        image: {{ .image }}
        env:
          - "SANDBOX={{ (index .harnesses .harness).sandbox }}"
          - "APPROVAL={{ (index .harnesses .harness).approval }}"
`
	writeHarnessFile(t, cacheDir, "codex", "blueprint.tmpl.yaml", body)
	r := minimalRole()
	h := &model.Harness{Spec: model.HarnessSpec{Template: "blueprint.tmpl.yaml"}}
	image := &model.ImageCatalogEntry{Image: "registry.local/codex:latest"}
	bp, err := RenderBlueprint(cacheDir, h, r, "codex", "dev", nil, image, nil, "sbsh", "default", false, "")
	if err != nil {
		t.Fatalf("RenderBlueprint: %v", err)
	}
	wantEnv := []string{"SANDBOX=workspace-write", "APPROVAL=on-request"}
	if !reflect.DeepEqual(bp.Spec.Cell.Containers[0].Env, wantEnv) {
		t.Errorf("env = %v, want %v", bp.Spec.Cell.Containers[0].Env, wantEnv)
	}
}

func TestBindConfigStampsTeamLabelAndProjectRepoFill(t *testing.T) {
	t.Parallel()
	bp := &v1beta1.CellBlueprintDoc{
		Metadata: v1beta1.CellBlueprintMetadata{Name: "dev-claude", Realm: "default"},
		Spec: v1beta1.CellBlueprintSpec{Cell: v1beta1.BlueprintCellSpec{
			Containers: []v1beta1.BlueprintContainer{{
				ID:    "dev",
				Image: "x",
				Repos: []v1beta1.ContainerRepo{
					{Name: "project", Target: "/src/project"}, // empty URL → structural slot
				},
				Secrets: []v1beta1.BlueprintSecretSlot{
					{Name: "ANTHROPIC_API_KEY", Mode: v1beta1.BlueprintSecretModeEnv, EnvName: "ANTHROPIC_API_KEY"},
				},
			}},
		}},
	}
	role := minimalRole()
	tc := &model.TeamsConfig{Spec: model.TeamsConfigSpec{
		Git: &model.TeamsConfigGit{
			ContainerGit: v1beta1.ContainerGit{
				Author: &v1beta1.GitIdentity{Name: "Op", Email: "op@example.com"},
			},
		},
		Registry: "registry.local",
		Secrets: map[string]model.TeamsConfigSecret{
			"ANTHROPIC_API_KEY": {From: model.SecretFromEnv, Key: "ANTHROPIC_API_KEY"},
		},
	}}
	src := teamsource.Source{
		Repo:      "github.com/eminwux/agents",
		Host:      "github.com",
		OwnerRepo: "eminwux/agents",
		Ref:       "v1.4.0",
		Kind:      teamsource.RefTag,
	}
	in := Inputs{Project: "sbsh", ProjectRepoURL: "git@github.com:eminwux/sbsh.git"}

	cfg := BindConfig(bp, role, "dev", "claude", tc, src, in, "sbsh", "default")
	if cfg.Metadata.Labels[v1beta1.LabelTeam] != "sbsh" {
		t.Errorf("team label = %q, want sbsh", cfg.Metadata.Labels[v1beta1.LabelTeam])
	}
	if cfg.Spec.Blueprint.Name != "dev-claude" {
		t.Errorf("blueprint ref = %+v, want name=dev-claude", cfg.Spec.Blueprint)
	}
	if got := cfg.Spec.Repos["project"].URL; got != "git@github.com:eminwux/sbsh.git" {
		t.Errorf("project repo fill URL = %q, want sbsh.git", got)
	}
	if got := cfg.Spec.Values["GIT_AUTHOR_NAME"]; got != "Op" {
		t.Errorf("Values[GIT_AUTHOR_NAME] = %q, want Op", got)
	}
	if got := cfg.Spec.Values["REGISTRY"]; got != "registry.local" {
		t.Errorf("Values[REGISTRY] = %q, want registry.local", got)
	}
	if cfg.Spec.Secrets["ANTHROPIC_API_KEY"].SecretRef == nil {
		t.Errorf("ANTHROPIC_API_KEY secret slot not filled: %+v", cfg.Spec.Secrets)
	}
}

func TestBindConfigSkipsUndeclaredSlots(t *testing.T) {
	t.Parallel()
	bp := &v1beta1.CellBlueprintDoc{
		Metadata: v1beta1.CellBlueprintMetadata{Name: "dev-claude", Realm: "default"},
		Spec: v1beta1.CellBlueprintSpec{Cell: v1beta1.BlueprintCellSpec{
			Containers: []v1beta1.BlueprintContainer{{ID: "dev", Image: "x"}},
		}},
	}
	in := Inputs{Project: "sbsh", ProjectRepoURL: "git@github.com:eminwux/sbsh.git"}
	cfg := BindConfig(
		bp,
		minimalRole(),
		"dev",
		"claude",
		&model.TeamsConfig{},
		teamsource.Source{},
		in,
		"sbsh",
		"default",
	)
	if len(cfg.Spec.Repos) != 0 {
		t.Errorf("repos should stay empty when template declares no slots: %+v", cfg.Spec.Repos)
	}
	if len(cfg.Spec.Secrets) != 0 {
		t.Errorf("secrets should stay empty: %+v", cfg.Spec.Secrets)
	}
}

func TestBindConfigFillsAgentsSlotFromTeamsConfigSources(t *testing.T) {
	t.Parallel()
	bp := &v1beta1.CellBlueprintDoc{
		Metadata: v1beta1.CellBlueprintMetadata{Name: "dev-claude", Realm: "default"},
		Spec: v1beta1.CellBlueprintSpec{Cell: v1beta1.BlueprintCellSpec{
			Containers: []v1beta1.BlueprintContainer{{
				ID: "dev", Image: "x",
				Repos: []v1beta1.ContainerRepo{
					{Name: "agents", Target: "/src/agents"},
				},
			}},
		}},
	}
	tc := &model.TeamsConfig{Spec: model.TeamsConfigSpec{
		Sources: map[string]string{"eminwux/agents": "git@github.com:eminwux/agents.git"},
	}}
	src := teamsource.Source{
		Repo:      "github.com/eminwux/agents",
		Host:      "github.com",
		OwnerRepo: "eminwux/agents",
		Ref:       "v1.4.0",
		Kind:      teamsource.RefTag,
	}
	cfg := BindConfig(bp, minimalRole(), "dev", "claude", tc, src, Inputs{Project: "sbsh"}, "sbsh", "default")
	if got := cfg.Spec.Repos["agents"].URL; got != "git@github.com:eminwux/agents.git" {
		t.Errorf("agents slot fill URL = %q, want agents.git", got)
	}
}

// TestRenderEndToEnd covers the per-(role × harness) outer loop: a
// project with two harness defaults produces two pairs whose blueprints
// + configs carry the team label.
func TestRenderEndToEnd(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	buildClaudeTemplate(t, cacheDir)
	// Codex template — minimal, mirrors the published agents-side text/template
	// shape (sibling of harness.yaml, dot-context references).
	writeHarnessFile(
		t,
		cacheDir,
		"codex",
		"blueprint.tmpl.yaml",
		"apiVersion: v1beta1\nkind: CellBlueprint\nmetadata: { name: \"{{ .role.name }}-{{ .harness }}\" }\nspec:\n  cell:\n    containers:\n      - { id: \"{{ .role.name }}\", image: \"{{ .image }}\" }\n",
	)
	bundle := &teamsource.Bundle{
		Source: teamsource.Source{
			Repo:      "github.com/eminwux/agents",
			Host:      "github.com",
			OwnerRepo: "eminwux/agents",
			Ref:       "v1.4.0",
			Kind:      teamsource.RefTag,
		},
		CacheDir: cacheDir,
		Roles:    map[string]*model.Role{"dev": minimalRole()},
		Harnesses: map[string]*model.Harness{
			"claude": {Spec: model.HarnessSpec{Template: "blueprint.tmpl.yaml"}},
			"codex":  {Spec: model.HarnessSpec{Template: "blueprint.tmpl.yaml"}},
		},
		ImageCatalog: &model.ImageCatalog{
			Spec: model.ImageCatalogSpec{
				Images: []model.ImageCatalogEntry{
					{
						Ref:          "claude-base",
						Harness:      "claude",
						Image:        "registry.local/claude:latest",
						Capabilities: []string{"go", "git"},
					},
					{
						Ref:          "codex-base",
						Harness:      "codex",
						Image:        "registry.local/codex:latest",
						Capabilities: []string{"go", "git"},
					},
				},
			},
		},
	}
	pt := &model.ProjectTeam{
		Metadata: model.Metadata{Name: "sbsh"},
		Spec: model.ProjectTeamSpec{
			Source:   model.TeamSource{Repo: "github.com/eminwux/agents", Tag: "v1.4.0"},
			Defaults: model.ProjectTeamDefaults{Harnesses: []string{"claude", "codex"}},
			Roles:    []model.ProjectTeamRole{{Ref: "dev"}},
		},
	}
	res, err := Render(bundle, pt, &model.TeamsConfig{}, Inputs{Project: "sbsh"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if len(res.Blueprints) != 2 || len(res.Configs) != 2 {
		t.Fatalf("expected 2 (role × harness) pairs, got %d/%d", len(res.Blueprints), len(res.Configs))
	}
	for i, bp := range res.Blueprints {
		if bp.Metadata.Labels[v1beta1.LabelTeam] != "sbsh" {
			t.Errorf("blueprint[%d] missing team label: %+v", i, bp.Metadata.Labels)
		}
		if res.Configs[i].Metadata.Labels[v1beta1.LabelTeam] != "sbsh" {
			t.Errorf("config[%d] missing team label: %+v", i, res.Configs[i].Metadata.Labels)
		}
		if res.Configs[i].Metadata.Name != bp.Metadata.Name {
			t.Errorf("config name %q != blueprint name %q at index %d",
				res.Configs[i].Metadata.Name, bp.Metadata.Name, i)
		}
	}
}

// TestRenderProjectPerRoleNeedsOverrideUnions confirms the project-side
// per-role image override is unioned with the role's own needs at image
// selection time — the AC's "union" branch.
func TestRenderProjectPerRoleNeedsOverrideUnions(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	buildClaudeTemplate(t, cacheDir)
	role := minimalRole() // image needs: go, git
	bundle := &teamsource.Bundle{
		Source: teamsource.Source{
			Repo:      "github.com/eminwux/agents",
			Host:      "github.com",
			OwnerRepo: "eminwux/agents",
			Ref:       "v1.4.0",
			Kind:      teamsource.RefTag,
		},
		CacheDir: cacheDir,
		Roles:    map[string]*model.Role{"dev": role},
		Harnesses: map[string]*model.Harness{
			"claude": {Spec: model.HarnessSpec{Template: "blueprint.tmpl.yaml"}},
		},
		ImageCatalog: &model.ImageCatalog{Spec: model.ImageCatalogSpec{Images: []model.ImageCatalogEntry{
			// First image has only go+git — should NOT satisfy go+git+rust merged needs.
			{
				Ref:          "claude-base",
				Harness:      "claude",
				Image:        "registry.local/claude:base",
				Capabilities: []string{"go", "git"},
			},
			// Second has go+git+rust — should win.
			{
				Ref:          "claude-rust",
				Harness:      "claude",
				Image:        "registry.local/claude:rust",
				Capabilities: []string{"go", "git", "rust"},
			},
		}}},
	}
	pt := &model.ProjectTeam{
		Metadata: model.Metadata{Name: "sbsh"},
		Spec: model.ProjectTeamSpec{
			Source:   model.TeamSource{Repo: "github.com/eminwux/agents", Tag: "v1.4.0"},
			Defaults: model.ProjectTeamDefaults{Harnesses: []string{"claude"}},
			Roles: []model.ProjectTeamRole{{
				Ref:   "dev",
				Needs: &model.ProjectRoleNeeds{Image: []string{"rust"}},
			}},
		},
	}
	res, err := Render(bundle, pt, &model.TeamsConfig{}, Inputs{Project: "sbsh"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	got := res.Blueprints[0].Spec.Cell.Containers[0].Image
	if got != "registry.local/claude:rust" {
		t.Errorf("merged needs picked wrong image: %q (want rust-bearing image)", got)
	}
}

func TestRenderRejectsMissingRoleInBundle(t *testing.T) {
	t.Parallel()
	bundle := &teamsource.Bundle{
		Roles:        map[string]*model.Role{},
		Harnesses:    map[string]*model.Harness{},
		ImageCatalog: minimalClaudeCatalog("go"),
	}
	pt := &model.ProjectTeam{
		Metadata: model.Metadata{Name: "sbsh"},
		Spec: model.ProjectTeamSpec{
			Defaults: model.ProjectTeamDefaults{Harnesses: []string{"claude"}},
			Roles:    []model.ProjectTeamRole{{Ref: "dev"}},
		},
	}
	_, err := Render(bundle, pt, nil, Inputs{Project: "sbsh"})
	if !errors.Is(err, errdefs.ErrTeamRoleNotLoaded) {
		t.Fatalf("err = %v, want ErrTeamRoleNotLoaded", err)
	}
}

func TestRenderRejectsMissingHarnessInBundle(t *testing.T) {
	t.Parallel()
	bundle := &teamsource.Bundle{
		Roles:        map[string]*model.Role{"dev": minimalRole()},
		Harnesses:    map[string]*model.Harness{},
		ImageCatalog: minimalClaudeCatalog("go", "git"),
	}
	pt := &model.ProjectTeam{
		Metadata: model.Metadata{Name: "sbsh"},
		Spec: model.ProjectTeamSpec{
			Defaults: model.ProjectTeamDefaults{Harnesses: []string{"claude"}},
			Roles:    []model.ProjectTeamRole{{Ref: "dev"}},
		},
	}
	_, err := Render(bundle, pt, nil, Inputs{Project: "sbsh"})
	if !errors.Is(err, errdefs.ErrTeamHarnessNotLoaded) {
		t.Fatalf("err = %v, want ErrTeamHarnessNotLoaded", err)
	}
}

func TestMarshalYAMLProducesMultiDocStream(t *testing.T) {
	t.Parallel()
	bp := &v1beta1.CellBlueprintDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellBlueprint,
		Metadata:   v1beta1.CellBlueprintMetadata{Name: "dev-claude", Realm: "default"},
	}
	cfg := &v1beta1.CellConfigDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellConfig,
		Metadata:   v1beta1.CellConfigMetadata{Name: "dev-claude", Realm: "default"},
	}
	raw, err := MarshalYAML(
		&Result{Blueprints: []*v1beta1.CellBlueprintDoc{bp}, Configs: []*v1beta1.CellConfigDoc{cfg}},
	)
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}
	body := string(raw)
	if !strings.Contains(body, "kind: CellBlueprint") {
		t.Errorf("missing CellBlueprint: %q", body)
	}
	if !strings.Contains(body, "kind: CellConfig") {
		t.Errorf("missing CellConfig: %q", body)
	}
	if !strings.Contains(body, "---") {
		t.Errorf("missing multi-doc separator: %q", body)
	}
}

// TestRenderDefaultRealmFallback confirms the default realm is `default`
// when Inputs.Realm is empty.
func TestRenderDefaultRealmFallback(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	buildClaudeTemplate(t, cacheDir)
	bundle := &teamsource.Bundle{
		CacheDir: cacheDir,
		Roles:    map[string]*model.Role{"dev": minimalRole()},
		Harnesses: map[string]*model.Harness{
			"claude": {Spec: model.HarnessSpec{Template: "blueprint.tmpl.yaml"}},
		},
		ImageCatalog: minimalClaudeCatalog("go", "git"),
	}
	pt := &model.ProjectTeam{
		Metadata: model.Metadata{Name: "sbsh"},
		Spec: model.ProjectTeamSpec{
			Defaults: model.ProjectTeamDefaults{Harnesses: []string{"claude"}},
			Roles:    []model.ProjectTeamRole{{Ref: "dev"}},
		},
	}
	res, err := Render(bundle, pt, nil, Inputs{Project: "sbsh"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if res.Blueprints[0].Metadata.Realm != DefaultRealm {
		t.Errorf("realm = %q, want %q", res.Blueprints[0].Metadata.Realm, DefaultRealm)
	}
}
