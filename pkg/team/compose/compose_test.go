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
	"errors"
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

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/team/teamtest"
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

// emptyConfig is the TeamsConfig of a host with no kuketeams.yaml (issue
// #1591: the file is optional).
func emptyConfig() *config.TeamsConfig {
	return &config.TeamsConfig{APIVersion: config.APIVersion, Kind: config.Kind}
}

func fixture(t *testing.T) (*config.TeamsConfig, compose.Options) {
	t.Helper()
	base := emptyConfig()
	base.Spec.Defaults = config.Majordomo().Spec
	base.Spec.Defaults.Harnesses = base.Spec.Defaults.Harnesses[:1]
	return base, compose.Options{
		Sources: map[string]fs.FS{"kukeon": copiedSource(t)},
	}
}

// shippedSource is the repository as it is, majordomo's extras included.
func shippedSource() map[string]fs.FS { return map[string]fs.FS{"kukeon": os.DirFS("../../..")} }

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
	for _, absent := range []string{"gh-token", "<no value>", "sourceDir", "secretRef:", "ROLE=${ROLE}", ".ssh", "GIT_SSH_COMMAND"} {
		if strings.Contains(string(raw), absent) {
			t.Fatalf("unexpected %q in documents", absent)
		}
	}
	// The GitHub connector is part of the shared form, but a role that does not
	// ask for it gets neither the component nor its token.
	rendered, err := yaml.Marshal(result.Pairs[0].Config)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(result.Pairs[0].Config.Spec.Components, "github") ||
		strings.Contains(string(rendered), "GH_TOKEN") {
		t.Fatalf("majordomo config enables the GitHub connector: %s", rendered)
	}
	if _, set := envOf(work(t, result.Pairs[0], interactive))["GH_TOKEN"]; set {
		t.Fatal("GH_TOKEN reached a cell that did not enable the connector")
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
	if len(result.Blueprints) != 3 {
		t.Fatalf("blueprints = %d, want one per harness", len(result.Blueprints))
	}
	claude, router := result.Pairs[1], result.Pairs[2]
	claudeWork := work(t, claude, interactive)
	if !strings.Contains(script(claudeWork), `"hasCompletedOnboarding": true`) {
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
	for _, mode := range []string{interactive, headless} {
		routerWork := work(t, router, mode)
		if len(routerWork.Secrets) != 1 || routerWork.Secrets[0].Name != "OPENROUTER_API_KEY" ||
			routerWork.Secrets[0].SecretRef == nil || routerWork.Secrets[0].SecretRef.Name != "router-account" {
			t.Fatalf("%s: incorrect secret destination: %+v", mode, routerWork.Secrets)
		}
		launcher := script(routerWork)
		if strings.Contains(launcher, "cat >") || !strings.Contains(launcher, "Makefile.start setup") {
			t.Fatal("role settings would be overwritten or materialization missing")
		}
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
	out, err := teamtest.PlainSource("../../..")
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMandatoryRequirementsAndSourceDeclarations(t *testing.T) {
	cfg, opts := fixture(t)
	source := copiedSource(t)
	opts.Sources["kukeon"] = source
	role := string(source["team/roles/majordomo/role.yaml"].Data)
	role = strings.Replace(role, "  needs:\n", "  needs:\n    secrets: [service-auth]\n    params: [REGION]\n", 1)
	source["team/roles/majordomo/role.yaml"] = &fstest.MapFile{Data: []byte(role)}
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
	// A role secret no harness or component declares becomes an optional slot
	// of the shared blueprint, filled by this role's config.
	slots := p.Blueprint.Spec.Cell.Containers[0].Secrets
	if len(slots) != 1 || slots[0].Name != "service-auth" || slots[0].EnvName != "SERVICE_ACCESS_TOKEN" ||
		slots[0].Required {
		t.Fatalf("extra secret slot = %+v", slots)
	}
	rendered := work(t, p, interactive)
	if len(rendered.Secrets) != 1 || rendered.Secrets[0].Name != "SERVICE_ACCESS_TOKEN" ||
		rendered.Secrets[0].SecretRef.Name != "service-account" {
		t.Fatalf("cell secrets = %+v", rendered.Secrets)
	}
	if !slices.Contains(p.Config.Spec.Requires, "go") {
		t.Fatalf("config requires = %v, want the role's go capability", p.Config.Spec.Requires)
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
				delete(s, "team/roles/majordomo/role.yaml")
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
			s["team/roles/majordomo/role.yaml"].Data = bytes.ReplaceAll(
				s["team/roles/majordomo/role.yaml"].Data,
				[]byte("workspace-write"),
				[]byte("made-up"),
			)
		}},
		{"parameter", "missing required parameter", func(_ *config.TeamsConfig, _ *compose.Options, s fstest.MapFS) {
			s["team/roles/majordomo/role.yaml"].Data = bytes.ReplaceAll(
				s["team/roles/majordomo/role.yaml"].Data,
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
			s["team/roles/majordomo/role.yaml"].Data = bytes.ReplaceAll(
				s["team/roles/majordomo/role.yaml"].Data,
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
	if len(result.Pairs) != 1 || len(result.Blueprints) != 1 {
		t.Fatalf("pairs = %d blueprints = %d", len(result.Pairs), len(result.Blueprints))
	}
	p := result.Pairs[0]
	bp := result.Blueprints[0]
	if p.Blueprint != bp {
		t.Fatal("pair does not reference the shared blueprint document")
	}
	// The config keeps the team/role/harness identity; the blueprint is the
	// shared realm-wide form named after the harness alone.
	if p.Config.Metadata.Name != "root-majordomo-codex" || bp.Metadata.Name != "codex" ||
		bp.Spec.Prefix != "codex" || p.Config.Spec.Blueprint.Name != "codex" {
		t.Fatalf("unstable identity: config %q blueprint %q", p.Config.Metadata.Name, bp.Metadata.Name)
	}
	if bp.Metadata.Realm != "default" || bp.Metadata.Space != "" || bp.Metadata.Stack != "" {
		t.Fatalf("shared blueprint must be realm-wide: %+v", bp.Metadata)
	}
	ref := p.Config.Spec.Blueprint
	if ref.Realm != "default" || ref.Space != "" || ref.Stack != "" {
		t.Fatalf("config must reference the realm-wide blueprint: %+v", ref)
	}
	if bp.Metadata.Labels[api.LabelShared] != "true" || len(bp.Metadata.Labels[api.LabelContentDigest]) != 16 {
		t.Fatalf("shared blueprint labels = %v", bp.Metadata.Labels)
	}
	if _, owned := bp.Metadata.Labels[api.LabelTeam]; owned {
		t.Fatal("a shared blueprint belongs to no team")
	}
	if p.Config.Metadata.Labels[api.LabelTeam] != "root" {
		t.Fatal("missing team ownership")
	}
	if p.Config.Metadata.Labels[api.LabelRole] != "majordomo" {
		t.Fatalf("missing role label: %v", p.Config.Metadata.Labels)
	}
	if p.Image.Image != publishedCodexImage(t) || p.Config.Spec.Values["IMAGE"] != p.Image.Image {
		t.Fatalf("image = %+v values = %v", p.Image, p.Config.Spec.Values)
	}
	// Security posture is fixed in the blueprint; git identity never is.
	c := bp.Spec.Cell.Containers[0]
	if c.Privileged || bp.Spec.Cell.NestedCgroupRuntime || len(p.Secrets) != 0 || c.Git != nil ||
		p.Config.Spec.Git != nil {
		t.Fatal("majordomo gained privilege, secret, or Git signing requirements")
	}
	if p.Config.Metadata.Realm != "default" || p.Config.Metadata.Space != "default" ||
		p.Config.Metadata.Stack != "default" {
		t.Fatal("missing scope")
	}
	// Role facts are answers in the config, not literals in the blueprint.
	want := map[string]string{
		"IMAGE":           publishedCodexImage(t),
		"ROLE_SOURCE_DIR": "/home/user/kukeon-src",
		"ROLE_PATH":       "team/roles/majordomo",
		"INITIAL_PATH":    "/home/user",
		"ROLE_NAME":       "majordomo",
		"SANDBOX":         "workspace-write",
		"APPROVAL":        "on-request",
		"AGENT_NAME":      "root-majordomo-codex",
		"STATE_VOLUME":    "root-majordomo-codex-state",
		// The state volume lives in the Config's own scope.
		"STATE_VOLUME_REALM": "default",
		"STATE_VOLUME_SPACE": "default",
		"STATE_VOLUME_STACK": "default",
	}
	if !reflect.DeepEqual(p.Config.Spec.Values, want) {
		t.Fatalf("config values = %v, want %v", p.Config.Spec.Values, want)
	}
	blueprint, err := yaml.Marshal(bp)
	if err != nil {
		t.Fatal(err)
	}
	for _, fact := range []string{"team/roles/majordomo", "kukeon-src", "root-majordomo"} {
		if strings.Contains(string(blueprint), fact) {
			t.Fatalf("role fact %q baked into the shared blueprint", fact)
		}
	}
	if len(p.Config.Spec.Repos) != 1 ||
		p.Config.Spec.Repos["role-source"].URL != "https://github.com/eminwux/kukeon" ||
		p.Config.Spec.Repos["role-source"].Branch != "main" {
		t.Fatalf("missing explicit role source: %+v", p.Config.Spec.Repos)
	}
	// The one component every harness Config enables is how the harness
	// authenticates (issue #1548): with nothing chosen, the login kept in the
	// state volume.
	if !slices.Equal(p.Config.Spec.Requires, []string{"go"}) ||
		!slices.Equal(p.Config.Spec.Components, []string{"codex-login"}) {
		t.Fatalf("config requires %v components %v", p.Config.Spec.Requires, p.Config.Spec.Components)
	}
	if !slices.Equal(bp.Spec.Requires, []string{"git"}) {
		t.Fatalf("blueprint requires = %v", bp.Spec.Requires)
	}
	rendered := work(t, p, interactive)
	state, ok := stateVolume(rendered)
	if !ok || *state.VolumeRef != (api.VolumeRef{
		Name: "root-majordomo-codex-state", Realm: "default", Space: "default", Stack: "default",
	}) || state.Source != "" || !state.Ensure || state.Target != "/home/user/.codex" {
		t.Fatalf("state volume missing team ownership/ensure: %+v", state)
	}
	if rendered.Image != publishedCodexImage(t) || len(rendered.Repos) != 1 ||
		rendered.Repos[0].Target != "/home/user/kukeon-src" {
		t.Fatalf("cell image/repos = %q %+v", rendered.Image, rendered.Repos)
	}
	env := envOf(rendered)
	for key, value := range map[string]string{
		"ROLE_SOURCE_DIR": "/home/user/kukeon-src", "ROLE_PATH": "team/roles/majordomo",
		"INITIAL_PATH": "/home/user", "CODEX_SANDBOX": "workspace-write", "CODEX_APPROVAL": "on-request",
		"AGENTS_ROLE": "majordomo", "AGENT_NAME": "root-majordomo-codex",
	} {
		if env[key] != value {
			t.Fatalf("cell env %s = %q, want %q", key, env[key], value)
		}
	}
	launcher := script(rendered)
	for _, want := range []string{"make -f /opt/kuketeams/harness/Makefile.start setup", `cd "$INITIAL_PATH"`, "sandbox=$CODEX_SANDBOX", "approval=$CODEX_APPROVAL"} {
		if !strings.Contains(launcher, want) {
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
	source["team/roles/majordomo/role.yaml"].Data = bytes.ReplaceAll(
		source["team/roles/majordomo/role.yaml"].Data,
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
		if pair.Blueprint.Metadata.Realm != "ops" || pair.Config.Metadata.Space != "agents" ||
			pair.Config.Metadata.Stack != "root" {
			t.Fatal("scope lost")
		}
		if pair.Config.Spec.Values["PROJECT_DIR"] != "/home/user/work/explicit-repo" {
			t.Fatalf("project checkout = %q", pair.Config.Spec.Values["PROJECT_DIR"])
		}
		for _, mode := range []string{interactive, headless} {
			rendered := work(t, pair, mode)
			for _, stage := range rendered.Tty.OnInit {
				command := exec.CommandContext(t.Context(), "bash", "-n")
				command.Stdin = strings.NewReader(stage.Script)
				if output, checkErr := command.CombinedOutput(); checkErr != nil {
					t.Fatalf("%s/%s shell syntax: %v: %s", pair.Harness, mode, checkErr, output)
				}
			}
			// sbsh writes the prompt verbatim as `export PS1=<prompt>` (#1622).
			prompt := exec.CommandContext(
				t.Context(),
				"bash",
				"-c",
				"export PS1="+rendered.Tty.Prompt+"\nprintf %s \"$PS1\"",
			)
			if output, promptErr := prompt.CombinedOutput(); promptErr != nil || string(output) != pair.Harness+"> " {
				t.Fatalf(
					"%s/%s prompt %q exports as %q: %v",
					pair.Harness,
					mode,
					rendered.Tty.Prompt,
					output,
					promptErr,
				)
			}
			// A headless codex exec cannot answer the trust prompt for a non-git initialPath (#1622).
			if pair.Harness == "codex" && mode == headless &&
				!strings.Contains(script(rendered), "--skip-git-repo-check") {
				t.Fatal("codex headless exec lacks --skip-git-repo-check")
			}
			if !strings.Contains(script(rendered), `cd "$INITIAL_PATH"`) {
				t.Fatalf("%s/%s inferred project directory", pair.Harness, mode)
			}
			// The project slot is filled at the explicit checkout path.
			if !slices.ContainsFunc(rendered.Repos, func(r api.ContainerRepo) bool {
				return r.Name == "project" && r.Target == "/home/user/work/explicit-repo" && r.Ref == "v2"
			}) {
				t.Fatalf("%s/%s project repo = %+v", pair.Harness, mode, rendered.Repos)
			}
		}
	}
}

func TestSecretDestinationConflicts(t *testing.T) {
	cfg, opts := fixture(t)
	source := copiedSource(t)
	opts.Sources["kukeon"] = source
	source["team/roles/majordomo/role.yaml"].Data = bytes.ReplaceAll(
		source["team/roles/majordomo/role.yaml"].Data,
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
	source["team/roles/majordomo/role.yaml"].Data = bytes.ReplaceAll(
		source["team/roles/majordomo/role.yaml"].Data,
		[]byte("[first-auth, second-auth]"),
		[]byte("[first-auth]"),
	)
	for _, tc := range []struct{ destination, want string }{
		// An env var the template itself sets.
		{"ROLE_PATH", "conflicts with template"},
		// One only the headless mode sets: a cell created in that mode would
		// fail, so composition refuses it for every mode.
		{"PROMPT", `mode headless sets env "PROMPT"`},
		// The destination of an optional connector no role here enables.
		{"GH_TOKEN", `component github secret slot "github-token"`},
	} {
		cfg.Spec.Secrets[0].EnvName = tc.destination
		result, err = compose.Compose(cfg, selectedTeam(cfg), opts)
		if err == nil || result != nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: result=%v err=%v", tc.destination, result, err)
		}
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
	// An operator edit of the recorded binding repoints the runtime secret. The
	// destination is fixed by the harness's slot in the shared blueprint.
	edited := routerBinding()
	edited.SecretRef.Name = "edited-router-account"
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
			rendered := work(t, pair, interactive)
			if len(rendered.Secrets) != 1 || rendered.Secrets[0].Name != tc.want.EnvName ||
				rendered.Secrets[0].SecretRef.Name != tc.want.SecretRef.Name ||
				pair.Secrets[0].Reference != tc.want.SecretRef ||
				pair.Secrets[0].Destination.EnvName != tc.want.EnvName {
				t.Fatal("binding not applied to resources")
			}
			// A binding nobody chose is not written into the Config: the
			// slot is left to the Realm default, which application writes
			// once (issue #1548). A chosen one is a Config fill.
			fill, filled := pair.Config.Spec.Secrets[tc.want.Name]
			wantDefault := tc.name == "generated"
			if filled == wantDefault || pair.Secrets[0].Default != wantDefault ||
				(filled && fill.SecretRef.Name != tc.want.SecretRef.Name) {
				t.Fatalf("config fill = %+v (filled=%v), default=%v", fill, filled, pair.Secrets[0].Default)
			}
			wantDefaults := []compose.DefaultSecret(nil)
			if wantDefault {
				wantDefaults = []compose.DefaultSecret{{Slot: tc.want.Name, Reference: tc.want.SecretRef}}
			}
			if !reflect.DeepEqual(result.DefaultSecrets, wantDefaults) {
				t.Fatalf("default secrets = %+v, want %+v", result.DefaultSecrets, wantDefaults)
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

// A shared blueprint fixes the destination of a slot its harness declares, so a
// binding cannot move it: every role using the blueprint would otherwise need
// the same override.
func TestHarnessSecretDestinationIsFixed(t *testing.T) {
	cfg, opts := fixture(t)
	allHarnesses(cfg)
	cfg.Spec.Defaults.Roles[0].Harness = "opencode"
	moved := routerBinding()
	moved.EnvName = "ROUTER_KEY"
	cfg.Spec.Secrets = []config.SecretBinding{moved}
	result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
	if err == nil || result != nil || !strings.Contains(err.Error(), "differs from the harness destination") ||
		!strings.Contains(err.Error(), "OPENROUTER_API_KEY") {
		t.Fatalf("result=%v err=%v", result, err)
	}
}

func TestGeneratedSecretDestinationConflicts(t *testing.T) {
	cfg, opts := fixture(t)
	source := copiedSource(t)
	opts.Sources["kukeon"] = source
	source["team/roles/majordomo/role.yaml"].Data = bytes.ReplaceAll(
		source["team/roles/majordomo/role.yaml"].Data,
		[]byte("  needs:"),
		[]byte("  needs:\n    secrets: [prompt]"),
	)
	// The generated default destination of a secret named prompt is PROMPT,
	// which the headless mode's parameter owns.
	result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
	if err == nil || result != nil || !errors.Is(err, errdefs.ErrBlueprintComponentConflict) ||
		!strings.Contains(err.Error(), `"PROMPT"`) {
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
	// The role's own source fills role-source, the project fills project, and
	// any other cloned source fills the slot named after it.
	repos := pair.Config.Spec.Repos
	if len(repos) != 3 || repos["role-source"].URL != "https://example.test/roles" ||
		repos["role-source"].Ref != "abc123" || repos["extra"].Branch != "topic" ||
		repos["project"].Ref != "v3" {
		t.Fatalf("effective source clone declarations lost: %+v", repos)
	}
	if pair.Config.Spec.Values["ROLE_SOURCE_DIR"] != "/opt/roles" ||
		pair.Config.Spec.Values["PROJECT_DIR"] != "/opt/project" {
		t.Fatal("global source path used instead of team override")
	}
	for _, selected := range result.Pairs {
		rendered := work(t, selected, interactive)
		targets := map[string]string{}
		for _, repo := range rendered.Repos {
			targets[repo.Name] = repo.Target
		}
		want := map[string]string{"role-source": "/opt/roles", "extra": "/opt/extra", "project": "/opt/project"}
		if len(selected.Config.Spec.Repos) != 3 || !reflect.DeepEqual(targets, want) {
			t.Fatalf("%s omitted explicitly requested clones: %v", selected.Harness, targets)
		}
		if envOf(rendered)["ROLE_SOURCE_DIR"] != "/opt/roles" {
			t.Fatalf("%s reads the role from %q", selected.Harness, envOf(rendered)["ROLE_SOURCE_DIR"])
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
			source["team/roles/majordomo/role.yaml"].Data = bytes.ReplaceAll(
				source["team/roles/majordomo/role.yaml"].Data,
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
				rendered := work(t, pair, interactive)
				var launch strings.Builder
				for _, stage := range rendered.Tty.OnInit {
					if strings.Contains(stage.Script, "Makefile.start setup") ||
						strings.Contains(stage.Script, `cd "$INITIAL_PATH"`) {
						launch.WriteString(stage.Script + "\n")
					}
				}
				state := filepath.Join(root, pair.Harness+"-state")
				cmd := exec.CommandContext(t.Context(), "bash", "-eu", "-c", launch.String())
				cmd.Env = append(
					append(os.Environ(), rendered.Env...),
					"PATH="+binDir+":"+os.Getenv("PATH"),
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

func TestStateVolumeIsConfigNamePlusState(t *testing.T) {
	cfg, opts := fixture(t)
	opts.Scope = compose.Scope{Realm: "teams", Space: "esp", Stack: "agents"}
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
	// No project-dir part and no digest: the Config name plus -state, in the
	// Config's own realm, space and stack.
	want := api.VolumeRef{Name: "esp32-pr-reviewer-codex-state", Realm: "teams", Space: "esp", Stack: "agents"}
	if pair.Config.Spec.Values["STATE_VOLUME"] != want.Name {
		t.Fatalf("STATE_VOLUME = %q", pair.Config.Spec.Values["STATE_VOLUME"])
	}
	for _, mode := range []string{interactive, headless} {
		state, ok := stateVolume(work(t, pair, mode))
		if !ok || *state.VolumeRef != want || state.Source != "" || !state.Ensure {
			t.Fatalf("state volume in %s mode = %+v, want ensured volumeRef %+v", mode, state, want)
		}
	}
}

// TestStateVolumeNames pins literal state volume names, with and without a
// project-bound role: neither the project checkout nor punctuation in the
// selections changes the shape.
func TestStateVolumeNames(t *testing.T) {
	for _, tc := range []struct {
		team, role string
		project    bool
		want       map[string]string
	}{
		{"root", "majordomo", false, map[string]string{
			"codex": "root-majordomo-codex-state", "claude": "root-majordomo-claude-state",
		}},
		{"root", "majordomo", true, map[string]string{
			"codex": "root-majordomo-codex-state", "claude": "root-majordomo-claude-state",
		}},
		{"kukeon", "pm", true, map[string]string{
			"codex": "kukeon-pm-codex-state", "claude": "kukeon-pm-claude-state",
		}},
		{"esp32", "pr-reviewer", false, map[string]string{"codex": "esp32-pr-reviewer-codex-state"}},
		{"root.a", "majordomo", false, map[string]string{"claude": "root-a-majordomo-claude-state"}},
	} {
		cfg, opts := fixture(t)
		allHarnesses(cfg)
		cfg.Spec.Secrets = []config.SecretBinding{routerBinding()}
		if tc.project {
			withProject(t, cfg, opts)
		}
		team := selectedTeam(cfg)
		team.Metadata.Name = tc.team
		cfg.Spec.Defaults.Roles[0].Name = tc.role
		result, err := compose.Compose(cfg, team, opts)
		if err != nil {
			t.Fatal(err)
		}
		for _, pair := range result.Pairs {
			want, pinned := tc.want[pair.Harness]
			if !pinned {
				continue
			}
			if got := pair.Config.Spec.Values["STATE_VOLUME"]; got != want {
				t.Errorf("%s/%s/%s project=%v: STATE_VOLUME = %q, want %q",
					tc.team, tc.role, pair.Harness, tc.project, got, want)
			}
		}
	}
}

// withProject makes the majordomo role need the project repo and gives the
// team a project source.
func withProject(t *testing.T, cfg *config.TeamsConfig, opts compose.Options) {
	t.Helper()
	source := copiedSource(t)
	source["team/roles/majordomo/role.yaml"].Data = bytes.ReplaceAll(
		source["team/roles/majordomo/role.yaml"].Data,
		[]byte("  needs:"), []byte("  needs:\n    repos: [project]"))
	opts.Sources["kukeon"] = source
	cfg.Spec.Defaults.Sources = append(cfg.Spec.Defaults.Sources, config.Source{
		Name: "project", URL: "https://example.test/work", Branch: "topic", ClonePath: "/home/user/work",
	})
}

// TestComposeRejectsCollidingPairNames composes two role selections whose
// names join to the same CellConfig (and state volume) name.
func TestComposeRejectsCollidingPairNames(t *testing.T) {
	cfg, opts := fixture(t)
	first := cfg.Spec.Defaults.Roles[0]
	first.Name = "a-majordomo"
	second := first
	second.Name = "a.majordomo"
	cfg.Spec.Defaults.Roles = []config.Role{first, second}
	result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
	if err == nil || result != nil {
		t.Fatalf("Compose = %v, %v; want a name collision error and no result", result, err)
	}
	for _, part := range []string{
		`role "a-majordomo" harness "codex"`, `role "a.majordomo" harness "codex"`,
		`"root-a-majordomo-codex"`, `"root-a-majordomo-codex-state"`,
	} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("error %q does not name %s", err, part)
		}
	}
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
				withProject(t, cfg, opts)
			}
			digests := map[string]string{}
			for _, selection := range []struct{ team, role string }{
				{"root-a", "majordomo"},
				{"root", "a-majordomo"},
				{"root--a", "majordomo"},
				{"root", "a--majordomo"},
				{"root", "majordomo"},
				{"root.a", "majordomo"},
				{"root", "a.majordomo"},
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
					if selection.team == "root" && selection.role == "majordomo" &&
						name != "root-majordomo-"+pair.Harness {
						t.Fatalf("simple resource identity changed: %q", name)
					}
					// Every team and role shares the one blueprint of its harness;
					// only the config carries the team/role identity.
					if !config.ValidName(name) || pair.Blueprint.Metadata.Name != pair.Harness ||
						pair.Blueprint.Spec.Prefix != pair.Harness ||
						pair.Config.Spec.Blueprint.Name != pair.Harness {
						t.Fatalf("inconsistent resource identity for %s", owner)
					}
					if pair.Config.Metadata.Labels[api.LabelTeam] != selection.team {
						t.Fatalf("authored team label changed for %s", owner)
					}
					if pair.Config.Metadata.Labels[api.LabelRole] != selection.role {
						t.Fatalf("authored role label changed for %s", owner)
					}
					if _, stamped := pair.Blueprint.Metadata.Labels[api.LabelTeam]; stamped {
						t.Fatalf("shared blueprint carries a team label for %s", owner)
					}
					digest := pair.Blueprint.Metadata.Labels[api.LabelContentDigest]
					if previous, seen := digests[pair.Harness]; seen && previous != digest {
						t.Fatalf("%s rendered a different %s blueprint than another team", owner, pair.Harness)
					}
					digests[pair.Harness] = digest
					state, hasState := stateVolume(work(t, pair, interactive))
					if hasState != (pair.Harness != "opencode") {
						t.Fatalf("state volume presence changed for %s", owner)
					}
					// The state volume follows the Config name: two selections
					// that join to one name already share their Config, and
					// Compose rejects them when they meet in one team.
					if hasState {
						if state.VolumeRef.Name != pair.Config.Spec.Values["STATE_VOLUME"] ||
							state.VolumeRef.Name != name+"-state" {
							t.Fatalf("state volume %q for %s is not %s-state", state.VolumeRef.Name, owner, name)
						}
						if !runtimeID.MatchString(state.VolumeRef.Name) {
							t.Fatalf("invalid runtime volume name %q for %s", state.VolumeRef.Name, owner)
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
			source["team/roles/majordomo/role.yaml"].Data = bytes.ReplaceAll(
				source["team/roles/majordomo/role.yaml"].Data,
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
				rendered := work(t, pair, interactive)
				var launch strings.Builder
				for _, stage := range rendered.Tty.OnInit {
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
					append(os.Environ(), rendered.Env...),
					"INITIAL_PATH="+target,
					"HOME="+home,
					"CODEX_HOME="+home+"/.codex",
					"CLAUDE_CONFIG_DIR="+home+"/.claude",
					"XDG_CONFIG_HOME="+home+"/.config",
					"OPENCODE_CONFIG="+home+"/.opencode-config",
					"PATH="+bin+":"+os.Getenv("PATH"),
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

// TestFreshMajordomoComposition composes the kuke team exactly as `kuke team
// init` does on a new host, from the role as shipped (issue #1509): both
// harness configs on the shared socket-and-client blueprint with daemon
// access but no /opt/kukeon mount (issue #1537), and the health sweep
// rendered hourly but suspended, with no secret required.
func TestFreshMajordomoComposition(t *testing.T) {
	base := emptyConfig()
	result, err := compose.Compose(base, config.Majordomo(), compose.Options{Sources: shippedSource()})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Pairs) != 2 {
		t.Fatalf("pairs=%d", len(result.Pairs))
	}
	wantMounts := mergeMounts(socketMounts, binaryMounts)
	for i, harnessName := range []string{"codex", "claude"} {
		p := result.Pairs[i]
		blueprint := harnessName + "-host"
		if p.Config.Metadata.Name != "kuke-majordomo-"+harnessName || p.Blueprint.Metadata.Name != blueprint ||
			p.Config.Spec.Blueprint.Name != blueprint || result.Blueprints[i] != p.Blueprint {
			t.Fatalf("incorrect pair identity: %s on %s", p.Config.Metadata.Name, p.Blueprint.Metadata.Name)
		}
		if p.Config.Metadata.Labels[api.LabelRole] != "majordomo" ||
			p.Config.Metadata.Labels[api.LabelTeam] != "kuke" ||
			p.Config.Metadata.Labels[api.LabelTask] != "" {
			t.Fatalf("%s labels = %v", p.Config.Metadata.Name, p.Config.Metadata.Labels)
		}
		if len(p.Secrets) != 0 || len(p.Config.Spec.Secrets) != 0 {
			t.Fatalf("%s requires secrets: %v", p.Config.Metadata.Name, p.Config.Spec.Secrets)
		}
		// GitHub is opt-in (issue #1532): no gh-token binding, no connector.
		if slices.Contains(p.Config.Spec.Components, "github") {
			t.Fatalf("%s enables the github connector by default", p.Config.Metadata.Name)
		}
		container := work(t, p, interactive)
		got := map[string]hostMount{}
		for _, volume := range container.Volumes {
			if volume.Kind == api.VolumeKindVolume || volume.Target == "/opt/cni" {
				continue
			}
			got[volume.Target] = hostMount{volume.Source, volume.ReadOnly}
		}
		if !reflect.DeepEqual(got, wantMounts) {
			t.Fatalf("%s host mounts = %v, want %v", harnessName, got, wantMounts)
		}
		for _, volume := range container.Volumes {
			if volume.Target == "/opt/kukeon" || volume.Source == "/opt/kukeon" {
				t.Fatalf("%s mounts the host's /opt/kukeon: %+v", harnessName, volume)
			}
		}
		if !slices.Contains(container.Env, "KUKEON_HOST=unix:///run/kukeon/kukeond/kukeond.sock") {
			t.Fatalf("%s is not pointed at the host daemon: %v", harnessName, container.Env)
		}

		if len(p.Tasks) != 1 {
			t.Fatalf("%s tasks = %d", harnessName, len(p.Tasks))
		}
		sweep := p.Tasks[0]
		if sweep.Metadata.Name != p.Config.Metadata.Name+"-health-sweep" ||
			sweep.Metadata.Labels[api.LabelTask] != "health-sweep" ||
			sweep.Metadata.Labels[api.LabelRole] != "majordomo" ||
			sweep.Spec.Values["PROMPT"] != "Use the health-sweep skill" || !sweep.Spec.AutoDelete ||
			sweep.Spec.Values["STATE_VOLUME"] != p.Config.Spec.Values["STATE_VOLUME"] {
			t.Fatalf("sweep config = %+v", sweep)
		}
		if sweep.Spec.Schedule == nil || sweep.Spec.Schedule.Cron != "@hourly" || !sweep.Spec.Schedule.Suspend {
			t.Fatalf("sweep must ship hourly and suspended: %+v", sweep.Spec.Schedule)
		}
	}
	if len(result.Blueprints) != 2 || len(result.SSHSources) != 0 || len(result.Notes) != 0 {
		t.Fatalf("blueprints=%d ssh sources=%v notes=%v", len(result.Blueprints), result.SSHSources, result.Notes)
	}
}

// TestMajordomoSweepOptIn is the per-install opt-in an edited team entry makes
// (issues #1509, #1532): the sweep is switched on and told where to file, and
// one gh-token binding enables the github connector for the whole role, so the
// interactive cell and the sweep cell both get GH_TOKEN and a gh image.
func TestMajordomoSweepOptIn(t *testing.T) {
	base := emptyConfig()
	team := config.Majordomo()
	on := false
	role := &team.Spec.Roles[0]
	role.Needs.Secrets = []string{"gh-token"}
	role.Tasks = &[]config.TaskSelection{{
		Name: "health-sweep", Suspend: &on, Args: "--file-issues example/kukeon-ops",
	}}
	result, err := compose.Compose(base, team, compose.Options{Sources: shippedSource()})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range result.Pairs {
		sweep := p.Tasks[0]
		if sweep.Spec.Schedule == nil || sweep.Spec.Schedule.Suspend || sweep.Spec.Schedule.Cron != "@hourly" {
			t.Fatalf("schedule = %+v", sweep.Spec.Schedule)
		}
		if got := sweep.Spec.Values["PROMPT"]; got != "Use the health-sweep skill with arguments: --file-issues example/kukeon-ops" {
			t.Fatalf("prompt = %q", got)
		}
		// The token is not chosen by the team, so neither Config fills it:
		// both inherit the Realm default, and both enable the connector.
		if len(sweep.Spec.Secrets) != 0 || len(p.Config.Spec.Secrets) != 0 ||
			!slices.Contains(sweep.Spec.Components, "github") ||
			!slices.Equal(sweep.Spec.Components, p.Config.Spec.Components) {
			t.Fatalf(
				"%s: sweep secrets %v components %v, interactive secrets %v components %v",
				p.Harness,
				sweep.Spec.Secrets,
				sweep.Spec.Components,
				p.Config.Spec.Secrets,
				p.Config.Spec.Components,
			)
		}
		if c := work(t, p, interactive); len(c.Secrets) != 1 || c.Secrets[0].Name != "GH_TOKEN" ||
			c.Secrets[0].SecretRef.Name != "gh-token" {
			t.Fatalf("%s: interactive cell secrets = %+v", p.Harness, c.Secrets)
		}
		if !slices.Contains(p.Image.Capabilities, "gh") {
			t.Fatalf("%s: image %s does not provide gh: %v", p.Harness, p.Image.Image, p.Image.Capabilities)
		}
		// The enabled connector requires gh of the image; the image selected
		// for needs.image [go] provides it, so gh is on PATH in both cells.
		connector := slices.IndexFunc(p.Blueprint.Spec.Components, func(c api.BlueprintComponent) bool {
			return c.Name == "github" && slices.Contains(c.Requires, "gh")
		})
		if !slices.Contains(p.Config.Spec.Components, "github") || connector < 0 {
			t.Fatalf("%s: the github connector is not enabled: components=%v", p.Harness, p.Config.Spec.Components)
		}
		for _, mode := range []string{interactive, headless} {
			container := work(t, p, mode)
			if !slices.ContainsFunc(
				container.Secrets,
				func(s api.ContainerSecret) bool { return s.Name == "GH_TOKEN" },
			) {
				t.Fatalf("%s %s: the token is not delivered as GH_TOKEN: %+v", p.Harness, mode, container.Secrets)
			}
		}
	}
	// The recorded entry keeps the opt-in, so a later --reapply renders the same.
	raw, err := yaml.Marshal(result.Entry)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"gh-token", "suspend: false", "--file-issues example/kukeon-ops"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("recorded entry lacks %q:\n%s", want, raw)
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
	if got := work(t, result.Pairs[0], interactive).Image; got != "registry.test/custom/codex:v9" {
		t.Fatalf("did not select the source's image: %s", got)
	}
	// The blueprint's own default is the catalog's plain image for the profile.
	for _, param := range result.Blueprints[0].Spec.Parameters {
		if param.Name == "IMAGE" && (param.Default == nil || *param.Default != "registry.test/custom/codex:v9" ||
			param.Type != api.ParameterTypeImage) {
			t.Fatalf("IMAGE parameter = %+v", param)
		}
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
