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
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/eminwux/kukeon/internal/team/bootstrap"
	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/compose"
	"github.com/eminwux/kukeon/pkg/team/config"
	"github.com/eminwux/kukeon/pkg/team/harness"
	"gopkg.in/yaml.v3"
)

// publishedCodexImage returns the committed codex catalog's non-root image so
// tests follow the release tag that release-images.sh stamps before each
// kukeon release instead of pinning one.
func publishedCodexImage(t *testing.T) string {
	t.Helper()
	catalog, err := harness.LoadCatalog(os.DirFS("../../../team/harnesses"), "codex", "codex")
	if err != nil {
		t.Fatal(err)
	}
	image := catalog.Spec.Images[0].Image
	if !strings.HasPrefix(image, "ghcr.io/eminwux/kukeon/codex:") {
		t.Fatalf("codex catalog image = %q", image)
	}
	return image
}

type noGit struct{}

func (noGit) Lookup(context.Context, string, bool) (string, error) { return "", bootstrap.ErrMissing }

func fixture(t *testing.T) (*config.TeamsConfig, compose.Options) {
	t.Helper()
	boot, err := bootstrap.Run(t.Context(), bootstrap.Options{Root: t.TempDir(), Git: noGit{}})
	if err != nil {
		t.Fatal(err)
	}
	boot.Config.Spec.Defaults = config.Operator().Spec
	boot.Config.Spec.Defaults.Harnesses = boot.Config.Spec.Defaults.Harnesses[:1]
	return boot.Config, compose.Options{
		Sources: map[string]fs.FS{"kukeon": os.DirFS("../../..")},
	}
}

func TestRootDefaults(t *testing.T) {
	cfg, opts := fixture(t)
	before, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
	if err != nil {
		t.Fatal(err)
	}
	assertRootResources(t, result)
	raw, err := result.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	for _, absent := range []string{"gh-token", "GH_TOKEN", "<no value>", "sourceDir", "secretRef:", "ROLE=${ROLE}"} {
		if strings.Contains(string(raw), absent) {
			t.Fatalf("unexpected %q in documents", absent)
		}
	}
	if bytes.Count(raw, []byte("kind: CellBlueprint")) != 1 || bytes.Count(raw, []byte("kind: CellConfig")) != 1 {
		t.Fatal("incorrect document stream")
	}
	again, err := compose.Compose(cfg, selectedTeam(cfg), opts)
	if err != nil {
		t.Fatal(err)
	}
	second, err := again.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, second) {
		t.Fatal("nondeterministic rendering")
	}
	after, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || result.Entry.Spec.Roles != nil {
		t.Fatal("composition mutated/materialized authored selection")
	}
}

func allHarnesses(cfg *config.TeamsConfig) {
	cfg.Spec.Defaults.Harnesses = []config.Harness{
		{Name: "codex", Source: "kukeon", Path: "team/harnesses/codex"},
		{Name: "claude", Source: "kukeon", Path: "team/harnesses/claude"},
		{Name: "opencode", Source: "kukeon", Path: "team/harnesses/opencode"},
	}
}

func selectedTeam(cfg *config.TeamsConfig) *config.Team {
	team := config.Root()
	if len(cfg.Spec.Defaults.Harnesses) > 1 {
		team.Spec.Harnesses = slices.Clone(cfg.Spec.Defaults.Harnesses)
	}
	return team
}

func routerBinding() config.SecretBinding {
	return config.SecretBinding{
		Name: "openrouter-api-key", EnvName: "OPENROUTER_API_KEY",
		SecretRef: config.SecretRef{Name: "router-account", Realm: "auth", Space: "shared", Stack: "agents"},
	}
}

func TestSelectedHarnessesAndBindings(t *testing.T) {
	cfg, opts := fixture(t)
	allHarnesses(cfg)
	cfg.Spec.Secrets = []config.SecretBinding{routerBinding()}
	// There is deliberately no local source and no environment lookup. An existing
	// scoped secret reference is enough for structural composition.
	result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Pairs) != 3 {
		t.Fatal("did not render selected defaults")
	}
	claude, router := result.Pairs[1], result.Pairs[2]
	claudeWork := claude.Blueprint.Spec.Cell.Containers[0]
	if !strings.Contains(claudeWork.Tty.OnInit[0].Script, `"hasCompletedOnboarding": true`) {
		t.Fatal("claude onboarding state must be seeded in-cell")
	}
	for _, mount := range claudeWork.Volumes {
		if strings.Contains(mount.Source, "claude.json") {
			t.Fatal("claude onboarding state must not require a host mount")
		}
	}
	if len(router.Secrets) != 1 || router.Secrets[0].Reference.Name != "router-account" ||
		router.Secrets[0].Source != nil {
		t.Fatal("invalid existing-secret requirement")
	}
	ref := router.Config.Spec.Secrets["openrouter-api-key"].SecretRef
	if ref.Name != "router-account" || ref.Realm != "auth" || ref.Space != "shared" || ref.Stack != "agents" {
		t.Fatal("secret scope lost")
	}
	if router.Blueprint.Spec.Cell.Containers[0].Secrets[0].EnvName != "OPENROUTER_API_KEY" {
		t.Fatal("incorrect destination")
	}
	script := router.Blueprint.Spec.Cell.Containers[0].Tty.OnInit[0].Script
	if strings.Contains(script, "cat >") || !strings.Contains(script, "Makefile.start setup") {
		t.Fatal("role settings would be overwritten or materialization missing")
	}
	cfg.Spec.Secrets = nil
	cfg.Spec.Defaults.Roles[0].Harness = "codex"
	// The explicit override must not require OpenCode auth or even load the
	// unselected definitions.
	cfg.Spec.Defaults.Harnesses[1].Path = "absent/claude"
	cfg.Spec.Defaults.Harnesses[2].Path = "absent/opencode"
	result, err = compose.Compose(cfg, selectedTeam(cfg), opts)
	if err != nil || len(result.Pairs) != 1 {
		t.Fatalf("override: %v", err)
	}
	team := selectedTeam(cfg)
	team.Spec.Roles = []config.Role{}
	team.Spec.Harnesses = []config.Harness{}
	result, err = compose.Compose(cfg, team, opts)
	if err != nil || len(result.Pairs) != 0 {
		t.Fatalf("empty selection: %v", err)
	}
}

func copiedSource(t *testing.T) fstest.MapFS {
	t.Helper()
	out := fstest.MapFS{}
	err := fs.WalkDir(os.DirFS("../../.."), ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && name != "." && name != "team" && name != "team/harnesses" && name != "team/roles" &&
			!strings.HasPrefix(name, "team/harnesses/") &&
			!strings.HasPrefix(name, "team/roles/") {
			return fs.SkipDir
		}
		if !d.IsDir() {
			raw, e := os.ReadFile("../../../" + name)
			if e != nil {
				return e
			}
			out[name] = &fstest.MapFile{Data: raw}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMandatoryRequirementsAndSourceDeclarations(t *testing.T) {
	cfg, opts := fixture(t)
	source := copiedSource(t)
	opts.Sources["kukeon"] = source
	role := string(source["team/roles/operator/role.yaml"].Data)
	role = strings.Replace(role, "  needs:\n", "  needs:\n    secrets: [service-auth]\n    params: [REGION]\n", 1)
	source["team/roles/operator/role.yaml"] = &fstest.MapFile{Data: []byte(role)}
	cfg.Spec.Defaults.Roles[0].Needs = &config.Needs{Image: []string{}} // mandatory go survives
	cfg.Spec.Parameters = map[string]string{"REGION": "eu-test"}
	cfg.Spec.Secrets = []config.SecretBinding{{
		Name: "service-auth", EnvName: "SERVICE_ACCESS_TOKEN",
		SecretRef: config.SecretRef{
			Name:  "service-account",
			Realm: "auth",
		}, Source: &config.SecretSource{File: "/does/not/exist/credentials"},
	}}
	result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
	if err != nil {
		t.Fatal(err)
	}
	p := result.Pairs[0]
	if p.Config.Spec.Values["REGION"] != "eu-test" || p.Secrets[0].Destination.EnvName != "SERVICE_ACCESS_TOKEN" {
		t.Fatal("parameter/explicit destination not bound")
	}
	if !slices.Contains(p.Image.Capabilities, "go") {
		t.Fatal("mandatory image capability erased")
	}
	if p.Secrets[0].Source.File != "/does/not/exist/credentials" {
		t.Fatal("lost optional creation source")
	}
	cfg.Spec.Secrets[0].Source.File = "changed"
	if p.Secrets[0].Source.File == "changed" {
		t.Fatal("requirement aliases input")
	}
	raw, err := result.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("credentials")) {
		t.Fatal("source declaration persisted in daemon documents")
	}
}

func TestActionableFailures(t *testing.T) {
	cases := []struct {
		name, want string
		change     func(*config.TeamsConfig, *compose.Options, fstest.MapFS)
	}{
		{
			"role-source",
			"missing clonePath for required role source",
			func(c *config.TeamsConfig, _ *compose.Options, _ fstest.MapFS) {
				c.Spec.Defaults.Sources[0].ClonePath = ""
			},
		},
		{"capability", "no published image", func(c *config.TeamsConfig, _ *compose.Options, _ fstest.MapFS) {
			c.Spec.Defaults.Roles[0].Needs.Image = []string{"absent-capability"}
		}},
		{
			"source",
			"missing materialized",
			func(_ *config.TeamsConfig, o *compose.Options, _ fstest.MapFS) { o.Sources = nil },
		},
		{
			"role",
			"read definition",
			func(_ *config.TeamsConfig, _ *compose.Options, s fstest.MapFS) {
				delete(s, "team/roles/operator/role.yaml")
			},
		},
		{"renderer", "spec.renderer: unsupported", func(_ *config.TeamsConfig, _ *compose.Options, s fstest.MapFS) {
			s["team/harnesses/codex/harness.yaml"].Data = bytes.ReplaceAll(
				s["team/harnesses/codex/harness.yaml"].Data,
				[]byte("renderer: kukeon/v1beta1"),
				[]byte("renderer: absent/v2"),
			)
		}},
		{
			"legacy-template",
			"legacy template/makeTarget",
			func(_ *config.TeamsConfig, _ *compose.Options, s fstest.MapFS) {
				s["team/harnesses/codex/harness.yaml"].Data = bytes.ReplaceAll(
					s["team/harnesses/codex/harness.yaml"].Data,
					[]byte("renderer: kukeon/v1beta1"),
					[]byte("template: blueprint.tmpl.yaml"),
				)
			},
		},
		{"unsupported-setting", "unsupported Codex", func(_ *config.TeamsConfig, _ *compose.Options, s fstest.MapFS) {
			s["team/roles/operator/role.yaml"].Data = bytes.ReplaceAll(
				s["team/roles/operator/role.yaml"].Data,
				[]byte("workspace-write"),
				[]byte("made-up"),
			)
		}},
		{"parameter", "missing required parameter", func(_ *config.TeamsConfig, _ *compose.Options, s fstest.MapFS) {
			s["team/roles/operator/role.yaml"].Data = bytes.ReplaceAll(
				s["team/roles/operator/role.yaml"].Data,
				[]byte("  needs:"),
				[]byte("  needs:\n    params: [REGION]"),
			)
		}},
		{"unknown-parameter", "unsupported parameter", func(c *config.TeamsConfig, _ *compose.Options, _ fstest.MapFS) {
			c.Spec.Parameters = map[string]string{"WRONG": "value"}
		}},
		{"secret", "secrets[0]", func(c *config.TeamsConfig, o *compose.Options, _ fstest.MapFS) {
			allHarnesses(c)
			c.Spec.Defaults.Roles[0].Harness = "opencode"
			o.Secrets = []config.SecretBinding{{Name: "openrouter-api-key", EnvName: "1BAD"}}
		}},
		{"seed", "field seeds not found", func(c *config.TeamsConfig, _ *compose.Options, s fstest.MapFS) {
			allHarnesses(c)
			c.Spec.Defaults.Roles[0].Harness = "claude"
			s["team/harnesses/claude/harness.yaml"].Data = bytes.ReplaceAll(
				s["team/harnesses/claude/harness.yaml"].Data,
				[]byte("  skillPath:"),
				[]byte(
					"  seeds:\n    - path: /seed.json\n      mode: 0o644\n      content: \"{}\"\n  skillPath:",
				),
			)
		}},
		{"mount", "unsupported mount", func(_ *config.TeamsConfig, _ *compose.Options, s fstest.MapFS) {
			s["team/roles/operator/role.yaml"].Data = bytes.ReplaceAll(
				s["team/roles/operator/role.yaml"].Data,
				[]byte("  needs:"),
				[]byte("  needs:\n    mounts: [unknown]"),
			)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, opts := fixture(t)
			source := copiedSource(t)
			opts.Sources["kukeon"] = source
			tc.change(cfg, &opts, source)
			result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
			if err == nil || result != nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("result=%v err=%v; wanted %q", result, err, tc.want)
			}
		})
	}
}

func TestEnvironmentIsNeverRead(t *testing.T) {
	cfg, opts := fixture(t)
	allHarnesses(cfg)
	cfg.Spec.Defaults.Roles[0].Harness = "opencode"
	binding := routerBinding()
	binding.Source = &config.SecretSource{Env: "LOCAL_OPERATOR_AUTH"}
	cfg.Spec.Secrets = []config.SecretBinding{binding}
	t.Setenv("LOCAL_OPERATOR_AUTH", "secret-canary-must-never-appear")
	first, err := compose.Compose(cfg, selectedTeam(cfg), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOCAL_OPERATOR_AUTH", "")
	second, err := compose.Compose(cfg, selectedTeam(cfg), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("composition depends on credential value")
	}
	raw, err := first.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret-canary") {
		t.Fatal("credential leaked")
	}
}

func assertRootResources(t *testing.T, result *compose.Result) {
	t.Helper()
	if len(result.Pairs) != 1 {
		t.Fatalf("pairs = %d", len(result.Pairs))
	}
	p := result.Pairs[0]
	if p.Config.Metadata.Name != "root-operator-codex" || p.Blueprint.Metadata.Name != p.Config.Metadata.Name ||
		p.Blueprint.Spec.Prefix != p.Config.Metadata.Name || p.Config.Spec.Blueprint.Name != p.Config.Metadata.Name {
		t.Fatal("unstable identity")
	}
	if p.Image.Image != publishedCodexImage(t) {
		t.Fatalf("image = %+v", p.Image)
	}
	c := p.Blueprint.Spec.Cell.Containers[0]
	if c.Privileged || p.Blueprint.Spec.Cell.NestedCgroupRuntime || len(p.Secrets) != 0 || c.Git != nil {
		t.Fatal("operator gained privilege, secret, or Git signing requirements")
	}
	if p.Blueprint.Metadata.Labels["kukeon.io/team"] != "root" || p.Config.Metadata.Labels["kukeon.io/team"] != "root" {
		t.Fatal("missing team ownership")
	}
	if p.Config.Metadata.Realm != "default" || p.Config.Metadata.Space != "default" ||
		p.Config.Metadata.Stack != "default" {
		t.Fatal("missing scope")
	}
	if c.Volumes[0].Source != "root-operator-codex" || !c.Volumes[0].Ensure {
		t.Fatal("state volume missing team ownership/ensure")
	}
	if len(p.Config.Spec.Repos) != 1 ||
		p.Config.Spec.Repos["kukeon"].URL != "https://github.com/eminwux/kukeon" {
		t.Fatal("missing explicit role source")
	}
	script := c.Tty.OnInit[0].Script
	for _, want := range []string{"make -f /opt/kuketeams/harness/Makefile.start setup", `cd "$INITIAL_PATH"`, "sandbox=$CODEX_SANDBOX", "approval=$CODEX_APPROVAL"} {
		if !strings.Contains(script, want) {
			t.Fatalf("missing %q in launcher", want)
		}
	}
}

func TestRenderedLaunchScriptsAndExplicitProjectRepo(t *testing.T) {
	cfg, opts := fixture(t)
	allHarnesses(cfg)
	cfg.Spec.Secrets = []config.SecretBinding{routerBinding()}
	source := copiedSource(t)
	opts.Sources["kukeon"] = source
	source["team/roles/operator/role.yaml"].Data = bytes.ReplaceAll(
		source["team/roles/operator/role.yaml"].Data,
		[]byte("  needs:"),
		[]byte("  needs:\n    repos: [project]"),
	)
	cfg.Spec.Defaults.Sources = append(
		cfg.Spec.Defaults.Sources,
		config.Source{
			Name:      "project",
			URL:       "https://example.test/work.git",
			ClonePath: "/home/user/work/explicit-repo",
			Tag:       "v2",
		},
	)
	opts.Scope = compose.Scope{Realm: "ops", Space: "agents", Stack: "root"}
	result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range result.Pairs {
		if pair.Config.Spec.Repos["project"].Ref != "v2" || pair.Config.Spec.Blueprint.Realm != "ops" {
			t.Fatal("repo revision or scope lost")
		}
		var scripts strings.Builder
		for _, c := range pair.Blueprint.Spec.Cell.Containers {
			for _, stage := range c.Tty.OnInit {
				scripts.WriteString(stage.Script)
				command := exec.CommandContext(t.Context(), "bash", "-n")
				command.Stdin = strings.NewReader(stage.Script)
				if output, checkErr := command.CombinedOutput(); checkErr != nil {
					t.Fatalf("%s shell syntax: %v: %s", pair.Harness, checkErr, output)
				}
			}
		}
		if !strings.Contains(scripts.String(), `cd "$INITIAL_PATH"`) {
			t.Fatalf("%s inferred project directory", pair.Harness)
		}
	}
}

func TestSecretDestinationConflicts(t *testing.T) {
	cfg, opts := fixture(t)
	source := copiedSource(t)
	opts.Sources["kukeon"] = source
	source["team/roles/operator/role.yaml"].Data = bytes.ReplaceAll(
		source["team/roles/operator/role.yaml"].Data,
		[]byte("  needs:"),
		[]byte("  needs:\n    secrets: [first-auth, second-auth]"),
	)
	cfg.Spec.Secrets = []config.SecretBinding{
		{Name: "first-auth", EnvName: "AUTH_TOKEN", SecretRef: config.SecretRef{Name: "a", Realm: "auth"}},
		{Name: "second-auth", EnvName: "AUTH_TOKEN", SecretRef: config.SecretRef{Name: "b", Realm: "auth"}},
	}
	result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
	if err == nil || result != nil || !strings.Contains(err.Error(), "conflicting secret") {
		t.Fatalf("result=%v err=%v", result, err)
	}
	cfg.Spec.Secrets = cfg.Spec.Secrets[:1]
	cfg.Spec.Secrets[0].EnvName = "PROMPT"
	source["team/roles/operator/role.yaml"].Data = bytes.ReplaceAll(
		source["team/roles/operator/role.yaml"].Data,
		[]byte("[first-auth, second-auth]"),
		[]byte("[first-auth]"),
	)
	result, err = compose.Compose(cfg, selectedTeam(cfg), opts)
	if err == nil || result != nil || !strings.Contains(err.Error(), "conflicts with template") {
		t.Fatalf("result=%v err=%v", result, err)
	}
}

func TestSecretBindingsGeneratedAndRecorded(t *testing.T) {
	cfg, opts := fixture(t)
	allHarnesses(cfg)
	cfg.Spec.Defaults.Roles[0].Harness = "opencode"
	generated := config.SecretBinding{
		Name: "openrouter-api-key", EnvName: "OPENROUTER_API_KEY",
		SecretRef: config.SecretRef{Name: "openrouter-api-key", Realm: "default"},
	}
	edited := routerBinding()
	edited.EnvName = "ROUTER_KEY"
	stale := config.SecretBinding{
		Name: "gh-token", EnvName: "GH_TOKEN", SecretRef: config.SecretRef{Name: "gh-token", Realm: "default"},
	}
	for _, tc := range []struct {
		name             string
		global, recorded []config.SecretBinding
		want             config.SecretBinding
	}{
		{"generated", nil, nil, generated},
		{"global", []config.SecretBinding{routerBinding()}, nil, routerBinding()},
		{"recorded", []config.SecretBinding{routerBinding()}, []config.SecretBinding{stale, edited}, edited},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg.Spec.Secrets = tc.global
			opts.Secrets = tc.recorded
			result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
			if err != nil {
				t.Fatal(err)
			}
			// Only bindings the selected pairs use are recorded; stale ones drop.
			if !reflect.DeepEqual(result.Entry.Secrets, []config.SecretBinding{tc.want}) {
				t.Fatalf("entry secrets = %+v", result.Entry.Secrets)
			}
			pair := result.Pairs[0]
			if pair.Blueprint.Spec.Cell.Containers[0].Secrets[0].EnvName != tc.want.EnvName ||
				pair.Config.Spec.Secrets[tc.want.Name].SecretRef.Name != tc.want.SecretRef.Name ||
				pair.Secrets[0].Reference != tc.want.SecretRef {
				t.Fatal("binding not applied to resources")
			}
			// Recomposing from the recorded entry alone yields the same bindings.
			cfg.Spec.Secrets = nil
			opts.Secrets = result.Entry.Secrets
			again, err := compose.Compose(cfg, selectedTeam(cfg), opts)
			if err != nil || !reflect.DeepEqual(again.Entry.Secrets, result.Entry.Secrets) {
				t.Fatalf("recompose: %v %+v", err, again)
			}
		})
	}
}

func TestGeneratedSecretDestinationConflicts(t *testing.T) {
	cfg, opts := fixture(t)
	source := copiedSource(t)
	opts.Sources["kukeon"] = source
	source["team/roles/operator/role.yaml"].Data = bytes.ReplaceAll(
		source["team/roles/operator/role.yaml"].Data,
		[]byte("  needs:"),
		[]byte("  needs:\n    secrets: [prompt]"),
	)
	result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
	if err == nil || result != nil || !strings.Contains(err.Error(), "conflicts with template") {
		t.Fatalf("result=%v err=%v", result, err)
	}
}

func TestParametersAcrossSelectedHarnesses(t *testing.T) {
	cfg, opts := fixture(t)
	allHarnesses(cfg)
	cfg.Spec.Defaults.Harnesses = cfg.Spec.Defaults.Harnesses[:2]
	cfg.Spec.Parameters = map[string]string{"ANTHROPIC_MODEL": "sonnet", "PROMPT": "hello"}
	result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range result.Pairs {
		if pair.Config.Spec.Values["PROMPT"] != "hello" {
			t.Fatal("shared parameter lost")
		}
		value, ok := pair.Config.Spec.Values["ANTHROPIC_MODEL"]
		if pair.Harness == "claude" {
			if !ok || value != "sonnet" {
				t.Fatal("Claude parameter lost")
			}
		} else if ok {
			t.Fatal("Claude parameter bound to Codex")
		}
	}
	cfg.Spec.Defaults.Roles[0].Harness = "codex"
	if result, err = compose.Compose(cfg, selectedTeam(cfg), opts); err == nil || result != nil ||
		!strings.Contains(err.Error(), `unsupported parameter "ANTHROPIC_MODEL"`) {
		t.Fatalf("unselected harness accepted parameter: %v", err)
	}
}

func TestEffectiveSourcesControlRuntimeClones(t *testing.T) {
	cfg, opts := fixture(t)
	allHarnesses(cfg)
	cfg.Spec.Secrets = []config.SecretBinding{routerBinding()}
	cfg.Spec.Defaults.Sources = append(cfg.Spec.Defaults.Sources,
		config.Source{Name: "definitions-only", URL: "https://example.test/defs", Tag: "v1"})
	team := selectedTeam(cfg)
	team.Spec.Sources = []config.Source{
		{Name: "kukeon", URL: "https://example.test/roles", Commit: "abc123", ClonePath: "/opt/roles"},
		{Name: "extra", URL: "https://example.test/extra", Branch: "topic", ClonePath: "/opt/extra"},
		{Name: "project", URL: "https://example.test/project", Tag: "v3", ClonePath: "/opt/project"},
		{Name: "definitions-only", URL: "https://example.test/defs", Tag: "v1"},
	}
	result, err := compose.Compose(cfg, team, opts)
	if err != nil {
		t.Fatal(err)
	}
	pair := result.Pairs[0]
	if len(pair.Config.Spec.Repos) != 3 || pair.Config.Spec.Repos["kukeon"].URL != "https://example.test/roles" ||
		pair.Config.Spec.Repos["kukeon"].Ref != "abc123" || pair.Config.Spec.Repos["extra"].Branch != "topic" {
		t.Fatalf("effective source clone declarations lost: %+v", pair.Config.Spec.Repos)
	}
	if !slices.Contains(pair.Blueprint.Spec.Cell.Containers[0].Env, "ROLE_SOURCE_DIR=/opt/roles") {
		t.Fatal("global source path used instead of team override")
	}
	for _, selected := range result.Pairs {
		if len(selected.Config.Spec.Repos) != 3 || len(selected.Blueprint.Spec.Cell.Containers[0].Repos) != 3 {
			t.Fatalf("%s omitted explicitly requested clones", selected.Harness)
		}
	}
	team.Spec.Sources[0].ClonePath = ""
	if result, err = compose.Compose(cfg, team, opts); err == nil || result != nil ||
		!strings.Contains(err.Error(), "clonePath") {
		t.Fatalf("required role clone silently inferred: %v", err)
	}
}

// Execute the rendered launch stages in a real local checkout. Harness binaries
// and materialization are stubbed; Git and the launcher's working directory are real.
func TestLaunchPreservesSourceRevision(t *testing.T) {
	for _, revision := range []string{"branch", "tag", "commit"} {
		t.Run(revision, func(t *testing.T) {
			cfg, opts := fixture(t)
			allHarnesses(cfg)
			cfg.Spec.Secrets = []config.SecretBinding{routerBinding()}
			root := t.TempDir()
			repoDir := filepath.Join(root, "project")
			runGit := func(args ...string) string {
				t.Helper()
				cmd := exec.CommandContext(t.Context(), "git", args...)
				cmd.Dir = root
				raw, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("git %v: %v: %s", args, err, raw)
				}
				return strings.TrimSpace(string(raw))
			}
			sourceDir, err := filepath.Abs("../../..")
			if err != nil {
				t.Fatal(err)
			}
			runGit("clone", "--quiet", "--shared", "--no-checkout", sourceDir, repoDir)
			sha := runGit("-C", repoDir, "rev-parse", "HEAD")
			project := config.Source{Name: "project", URL: "https://example.test/project", ClonePath: repoDir}
			wantBranch := ""
			switch revision {
			case "branch":
				project.Branch, wantBranch = "selected-topic", "selected-topic"
				runGit("-C", repoDir, "checkout", "-b", project.Branch, sha)
			case "tag":
				project.Tag = "selected-v2"
				runGit("-C", repoDir, "tag", "--no-sign", project.Tag, sha)
				runGit("-C", repoDir, "checkout", "--detach", project.Tag)
			case "commit":
				project.Commit = sha
				runGit("-C", repoDir, "checkout", "--detach", sha)
			}
			cfg.Spec.Defaults.Sources = append(cfg.Spec.Defaults.Sources, project)
			cfg.Spec.Defaults.Roles[0].InitialPath = repoDir
			source := copiedSource(t)
			source["team/roles/operator/role.yaml"].Data = bytes.ReplaceAll(
				source["team/roles/operator/role.yaml"].Data,
				[]byte("  needs:"),
				[]byte("  needs:\n    repos: [project]"),
			)
			opts.Sources["kukeon"] = source
			result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
			if err != nil {
				t.Fatal(err)
			}
			binDir := filepath.Join(root, "bin")
			if err = os.Mkdir(binDir, 0o700); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"make", "codex", "claude", "opencode"} {
				script := "#!/bin/sh\nexit 0\n"
				if name != "make" {
					script = "#!/bin/sh\n(git symbolic-ref --quiet --short HEAD || true) > \"$LAUNCH_STATE\"\ngit rev-parse HEAD >> \"$LAUNCH_STATE\"\n"
				}
				if err = os.WriteFile(filepath.Join(binDir, name), []byte(script), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			for _, pair := range result.Pairs {
				fill := pair.Config.Spec.Repos["project"]
				if fill.Branch != project.Branch || fill.Ref != project.Tag+project.Commit {
					t.Fatalf("%s lost source revision: %+v", pair.Harness, fill)
				}
				var launch strings.Builder
				for _, stage := range pair.Blueprint.Spec.Cell.Containers[0].Tty.OnInit {
					if strings.Contains(stage.Script, "Makefile.start setup") ||
						strings.Contains(stage.Script, `cd "$INITIAL_PATH"`) {
						launch.WriteString(stage.Script + "\n")
					}
				}
				state := filepath.Join(root, pair.Harness+"-state")
				cmd := exec.CommandContext(t.Context(), "bash", "-eu", "-c", launch.String())
				cmd.Env = append(
					append(os.Environ(), pair.Blueprint.Spec.Cell.Containers[0].Env...),
					"PATH="+binDir+":"+os.Getenv("PATH"),
					"PROMPT=",
					"OPENROUTER_MODEL=test",
					"LAUNCH_STATE="+state,
				)
				if output, launchErr := cmd.CombinedOutput(); launchErr != nil {
					t.Fatalf("%s launch: %v: %s", pair.Harness, launchErr, output)
				}
				raw, readErr := os.ReadFile(state)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if strings.TrimSpace(string(raw)) != strings.TrimSpace(wantBranch+"\n"+sha) {
					t.Fatalf("%s changed checkout at launch: %q", pair.Harness, raw)
				}
			}
		})
	}
}

func TestReviewerConfigNamePreservesExistingState(t *testing.T) {
	cfg, opts := fixture(t)
	team := selectedTeam(cfg)
	team.Metadata.Name = "esp32"
	cfg.Spec.Defaults.Roles[0].Name = "pr-reviewer"
	result, err := compose.Compose(cfg, team, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Pairs) != 1 {
		t.Fatalf("expected one pair, got %d", len(result.Pairs))
	}
	pair := result.Pairs[0]
	if pair.Config.Metadata.Name != "esp32-pr-reviewer-codex" {
		t.Fatalf("unexpected cell config name: %q", pair.Config.Metadata.Name)
	}
	for _, container := range pair.Blueprint.Spec.Cell.Containers {
		for _, volume := range container.Volumes {
			if volume.Source == "esp32-pr-reviewer-codex-84a6981913a0eaf3" {
				return
			}
		}
	}
	t.Fatal("PR #37 state-volume identity was not preserved")
}

func TestReadableResourceNamesAndIsolatedState(t *testing.T) {
	runtimeID := regexp.MustCompile(`^[A-Za-z0-9]+(?:[._-](?:[A-Za-z0-9]+))*$`)
	for _, project := range []bool{false, true} {
		scenario := "role-state"
		if project {
			scenario = "project-state"
		}
		t.Run(scenario, func(t *testing.T) {
			cfg, opts := fixture(t)
			allHarnesses(cfg)
			cfg.Spec.Secrets = []config.SecretBinding{routerBinding()}
			if project {
				source := copiedSource(t)
				source["team/roles/operator/role.yaml"].Data = bytes.ReplaceAll(
					source["team/roles/operator/role.yaml"].Data,
					[]byte("  needs:"), []byte("  needs:\n    repos: [project]"))
				opts.Sources["kukeon"] = source
				cfg.Spec.Defaults.Sources = append(cfg.Spec.Defaults.Sources, config.Source{
					Name: "project", URL: "https://example.test/work", Branch: "topic", ClonePath: "/home/user/work",
				})
			}
			volumes := map[string]string{}
			for _, selection := range []struct{ team, role string }{
				{"root-a", "operator"},
				{"root", "a-operator"},
				{"root--a", "operator"},
				{"root", "a--operator"},
				{"root", "operator"},
				{"root.a", "operator"},
				{"root", "a.operator"},
				{"esp32", "pr-reviewer"},
				{"esp32", "pr--reviewer"},
				{"esp32", "pr..reviewer"},
				{"esp32", "pr.-reviewer"},
			} {
				team := selectedTeam(cfg)
				team.Metadata.Name = selection.team
				cfg.Spec.Defaults.Roles[0].Name = selection.role
				result, err := compose.Compose(cfg, team, opts)
				if err != nil {
					t.Fatal(err)
				}
				for _, pair := range result.Pairs {
					owner := selection.team + "/" + selection.role + "/" + pair.Harness
					name := pair.Config.Metadata.Name
					wantName := strings.Join(strings.FieldsFunc(selection.team+"-"+selection.role+"-"+pair.Harness,
						func(r rune) bool { return r == '-' || r == '.' }), "-")
					if name != wantName {
						t.Fatalf("resource name = %q, want %q", name, wantName)
					}
					if !runtimeID.MatchString(name) {
						t.Fatalf("invalid runtime resource name %q for %s", name, owner)
					}
					if !runtimeID.MatchString("default_default_" + name + "-00b5e6_work") {
						t.Fatalf("invalid derived container ID for %s", owner)
					}
					if selection.team == "root" && selection.role == "operator" &&
						name != "root-operator-"+pair.Harness {
						t.Fatalf("simple resource identity changed: %q", name)
					}
					if !config.ValidName(name) || pair.Blueprint.Metadata.Name != name ||
						pair.Blueprint.Spec.Prefix != name || pair.Config.Spec.Blueprint.Name != name {
						t.Fatalf("inconsistent resource identity for %s", owner)
					}
					if pair.Config.Metadata.Labels[api.LabelTeam] != selection.team ||
						pair.Blueprint.Metadata.Labels[api.LabelTeam] != selection.team {
						t.Fatalf("authored team label changed for %s", owner)
					}
					for _, container := range pair.Blueprint.Spec.Cell.Containers {
						for _, volume := range container.Volumes {
							if volume.Kind != api.VolumeKindVolume || volume.VolumeRef != nil {
								continue
							}
							if previous, exists := volumes[volume.Source]; exists && previous != owner {
								t.Fatalf("%s and %s share state volume %q", previous, owner, volume.Source)
							}
							volumes[volume.Source] = owner
							if !runtimeID.MatchString(volume.Source) {
								t.Fatalf("invalid runtime volume name %q for %s", volume.Source, owner)
							}
						}
					}
				}
				repeated, err := compose.Compose(cfg, team, opts)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(result, repeated) {
					t.Fatalf("resource composition is not deterministic for %s/%s", selection.team, selection.role)
				}
			}
		})
	}
}

// Run the actual rendered launch scripts with harness executables replaced by
// observers. Materialization is real, so instructions/skills must remain available
// when the starting directory is outside the role source and project checkout.
func TestInitialPathLaunchAcrossHarnesses(t *testing.T) {
	for _, selection := range []string{"omitted", "home", "subdirectory", "absolute", "missing"} {
		t.Run(selection, func(t *testing.T) {
			cfg, opts := fixture(t)
			allHarnesses(cfg)
			cfg.Spec.Secrets = []config.SecretBinding{routerBinding()}
			home := t.TempDir()
			target := home
			initial := ""
			switch selection {
			case "home":
				initial = "$HOME"
			case "subdirectory":
				initial, target = "$HOME/work", filepath.Join(home, "work")
			case "absolute":
				target = t.TempDir()
				initial = target
			case "missing":
				target = filepath.Join(home, "missing")
				initial = target
			}
			if selection != "missing" {
				if err := os.MkdirAll(target, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			sourceDir, err := filepath.Abs("../../..")
			if err != nil {
				t.Fatal(err)
			}
			cfg.Spec.Defaults.Sources[0].ClonePath = sourceDir
			cfg.Spec.Defaults.Roles[0].InitialPath = initial
			// A declared project must not override the selected initialPath or home fallback.
			cfg.Spec.Defaults.Sources = append(
				cfg.Spec.Defaults.Sources,
				config.Source{
					Name:      "project",
					URL:       "https://example.test/project",
					Branch:    "main",
					ClonePath: "/project/ignored",
				},
			)
			source := copiedSource(t)
			source["team/roles/operator/role.yaml"].Data = bytes.ReplaceAll(
				source["team/roles/operator/role.yaml"].Data,
				[]byte("  needs:"),
				[]byte("  needs:\n    repos: [project]"),
			)
			opts.Sources["kukeon"] = source
			result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
			if err != nil {
				t.Fatal(err)
			}
			bin := t.TempDir()
			state := filepath.Join(home, "observed")
			for _, name := range []string{"codex", "claude", "opencode"} {
				observer := "#!/bin/sh\npwd > \"$LAUNCH_STATE\"\n"
				if err = os.WriteFile(filepath.Join(bin, name), []byte(observer), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			for _, pair := range result.Pairs {
				var launch strings.Builder
				for _, stage := range pair.Blueprint.Spec.Cell.Containers[0].Tty.OnInit {
					if strings.Contains(stage.Script, "make -C") || strings.Contains(stage.Script, "cd ") {
						launch.WriteString(stage.Script + "\n")
					}
				}
				adapter, absErr := filepath.Abs("../../../team/harnesses/" + pair.Harness + "/context/Makefile.start")
				if absErr != nil {
					t.Fatal(absErr)
				}
				launchScript := strings.ReplaceAll(launch.String(), "/opt/kuketeams/harness/Makefile.start", adapter)
				cmd := exec.CommandContext(t.Context(), "bash", "-u", "-c", launchScript)
				cmd.Env = append(
					append(os.Environ(), pair.Blueprint.Spec.Cell.Containers[0].Env...),
					"INITIAL_PATH="+target,
					"HOME="+home,
					"CODEX_HOME="+home+"/.codex",
					"CLAUDE_CONFIG_DIR="+home+"/.claude",
					"XDG_CONFIG_HOME="+home+"/.config",
					"OPENCODE_CONFIG="+home+"/.opencode-config",
					"PATH="+bin+":"+os.Getenv("PATH"),
					"PROMPT=",
					"OPENROUTER_MODEL=test",
					"LAUNCH_STATE="+state,
				)
				output, launchErr := cmd.CombinedOutput()
				if selection == "missing" {
					if launchErr == nil {
						t.Fatalf("%s launched from nonexistent directory: %s", pair.Harness, output)
					}
					if _, statErr := os.Stat(state); !os.IsNotExist(statErr) {
						t.Fatal("harness ran despite failed cd")
					}
					continue
				}
				if launchErr != nil {
					t.Fatalf("%s launch: %v: %s", pair.Harness, launchErr, output)
				}
				raw, readErr := os.ReadFile(state)
				if readErr != nil || strings.TrimSpace(string(raw)) != target {
					t.Fatalf("%s started at %q, expected %q: %v", pair.Harness, raw, target, readErr)
				}
				policy := map[string]string{"codex": ".codex/AGENTS.md", "claude": ".claude/CLAUDE.md", "opencode": ".config/opencode/AGENTS.md"}[pair.Harness]
				if raw, readErr = os.ReadFile(filepath.Join(home, policy)); readErr != nil || len(raw) == 0 {
					t.Fatalf("%s missing role instructions: %v", pair.Harness, readErr)
				}
				skills := ".agents/skills"
				if pair.Harness == "claude" {
					skills = ".claude/skills"
				}
				if _, statErr := os.Stat(filepath.Join(home, skills, "create-cell-config", "SKILL.md")); statErr != nil {
					t.Fatal(statErr)
				}
			}
		})
	}
}

func TestFreshOperatorComposition(t *testing.T) {
	boot, err := bootstrap.Run(t.Context(), bootstrap.Options{Root: t.TempDir(), Git: noGit{}})
	if err != nil {
		t.Fatal(err)
	}
	_, opts := fixture(t)
	result, err := compose.Compose(boot.Config, config.Operator(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Pairs) != 2 {
		t.Fatalf("pairs=%d", len(result.Pairs))
	}
	for i, name := range []string{"kukeon-operator-codex", "kukeon-operator-claude"} {
		p := result.Pairs[i]
		if p.Config.Metadata.Name != name || p.Blueprint.Metadata.Name != name || p.Config.Spec.Blueprint.Name != name {
			t.Fatalf("incorrect pair identity: %+v", p)
		}
	}
}

func TestInheritedHarnessListAndRoleOverride(t *testing.T) {
	cfg, opts := fixture(t)
	allHarnesses(cfg)
	cfg.Spec.Defaults.Harnesses = cfg.Spec.Defaults.Harnesses[:2] // opencode needs a provider binding
	// An inherited harness list selects every listed harness, exactly like an
	// explicit Team list. Role-level selection narrows to one.
	for _, test := range []struct {
		harness string
		want    []string
	}{
		{"", []string{"codex", "claude"}},
		{"claude", []string{"claude"}},
	} {
		cfg.Spec.Defaults.Roles[0].Harness = test.harness
		result, err := compose.Compose(cfg, config.Root(), opts)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, pair := range result.Pairs {
			got = append(got, pair.Harness)
		}
		if !slices.Equal(got, test.want) {
			t.Fatalf("inherited selection %q: %v", test.harness, got)
		}
	}
}

func TestCatalogFollowsSelectedSourceAndPath(t *testing.T) {
	cfg, opts := fixture(t)
	source := copiedSource(t)
	external := fstest.MapFS{}
	for name, file := range source {
		if strings.HasPrefix(name, "team/harnesses/codex/") {
			external["nested/runtime/"+strings.TrimPrefix(name, "team/harnesses/codex/")] = &fstest.MapFile{
				Data: slices.Clone(file.Data),
			}
		}
	}
	catalogPath := "nested/runtime/images.yaml"
	external[catalogPath].Data = bytes.ReplaceAll(
		external[catalogPath].Data,
		[]byte(publishedCodexImage(t)),
		[]byte("registry.test/custom/codex:v9"),
	)
	cfg.Spec.Defaults.Sources = append(
		cfg.Spec.Defaults.Sources,
		config.Source{Name: "external", URL: "https://github.com/example/external", Tag: "v9"},
	)
	cfg.Spec.Defaults.Harnesses[0].Source = "external"
	cfg.Spec.Defaults.Harnesses[0].Path = "nested/runtime"
	opts.Sources["external"] = external
	result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Pairs[0].Blueprint.Spec.Cell.Containers[0].Image; got != "registry.test/custom/codex:v9" {
		t.Fatalf("did not bake selected source image: %s", got)
	}
	for _, test := range []struct {
		name string
		raw  []byte
		want string
	}{
		{"missing", nil, "nested/runtime/images.yaml"},
		{"wrong-identity", bytes.Replace(external[catalogPath].Data, []byte("name: codex"), []byte("name: claude"), 1), "metadata.name"},
		{"unsuitable", bytes.ReplaceAll(external[catalogPath].Data, []byte("        - go\n"), []byte("        - rust\n")), "no published image"},
		{"local", bytes.Replace(external[catalogPath].Data, []byte("registry.test/custom/codex:v9"), []byte("codex:dev"), 1), "registry-qualified"},
	} {
		t.Run(test.name, func(t *testing.T) {
			original := external[catalogPath]
			defer func() { external[catalogPath] = original }()
			if test.raw == nil {
				delete(external, catalogPath)
			} else {
				external[catalogPath] = &fstest.MapFile{Data: test.raw}
			}
			_, composeErr := compose.Compose(cfg, selectedTeam(cfg), opts)
			if composeErr == nil || !strings.Contains(composeErr.Error(), test.want) ||
				!strings.Contains(composeErr.Error(), catalogPath) {
				t.Fatalf("catalog error: %v", composeErr)
			}
		})
	}
}
