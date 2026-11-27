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
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/eminwux/kukeon/internal/apply/parser"
	"github.com/eminwux/kukeon/internal/cellblueprint"
	"github.com/eminwux/kukeon/internal/cellconfig"
	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/compose"
	"github.com/eminwux/kukeon/pkg/team/config"
)

const majordomoRole = "team/roles/majordomo/role.yaml"

// editRole rewrites the majordomo role document of a copied source.
func editRole(source fstest.MapFS, old, replacement string) {
	source[majordomoRole].Data = bytes.ReplaceAll(source[majordomoRole].Data, []byte(old), []byte(replacement))
}

// addRole copies the majordomo role to a second role directory so a team can
// select two roles from one source.
func addRole(source fstest.MapFS, name string) {
	for file, data := range maps.Clone(source) {
		rest, ok := strings.CutPrefix(file, "team/roles/majordomo/")
		if !ok {
			continue
		}
		raw := slices.Clone(data.Data)
		if rest == "role.yaml" {
			raw = bytes.Replace(raw, []byte("name: majordomo"), []byte("name: "+name), 1)
		}
		source["team/roles/"+name+"/"+rest] = &fstest.MapFile{Data: raw}
	}
}

func editFile(source fstest.MapFS, file, old, replacement string) {
	source[file].Data = bytes.ReplaceAll(source[file].Data, []byte(old), []byte(replacement))
}

// TestBlueprintsAreSharedPerHarnessAndProfile is step 12's core contract: one
// blueprint per harness × security profile, referenced by every role config
// that uses it, with role-specific leftovers unioned into the shared form.
func TestBlueprintsAreSharedPerHarnessAndProfile(t *testing.T) {
	cfg, opts := fixture(t)
	allHarnesses(cfg)
	cfg.Spec.Defaults.Harnesses = cfg.Spec.Defaults.Harnesses[:2]
	source := copiedSource(t)
	opts.Sources["kukeon"] = source
	addRole(source, "reviewer")
	addRole(source, "builder")
	// Only the reviewer needs a secret no harness or component declares.
	editFile(source, "team/roles/reviewer/role.yaml", "  needs:", "  needs:\n    secrets: [service-auth]")
	cfg.Spec.Defaults.Roles = append(cfg.Spec.Defaults.Roles,
		config.Role{Name: "reviewer", Source: "kukeon", Path: "team/roles/reviewer"},
		config.Role{
			Name: "builder", Source: "kukeon", Path: "team/roles/builder",
			Needs: &config.Needs{Image: []string{"root"}},
		},
	)
	result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, bp := range result.Blueprints {
		names = append(names, bp.Metadata.Name)
	}
	if want := []string{"codex", "claude", "codex-root", "claude-root"}; !slices.Equal(names, want) {
		t.Fatalf("blueprints = %v, want %v", names, want)
	}
	if len(result.Pairs) != 6 {
		t.Fatalf("pairs = %d", len(result.Pairs))
	}
	byConfig := map[string]compose.Pair{}
	for _, pair := range result.Pairs {
		byConfig[pair.Config.Metadata.Name] = pair
	}
	majordomo, reviewer, builder := byConfig["root-majordomo-codex"], byConfig["root-reviewer-codex"], byConfig["root-builder-codex"]
	if majordomo.Blueprint != reviewer.Blueprint || majordomo.Blueprint != result.Blueprints[0] {
		t.Fatal("roles on the same harness and profile must share one blueprint document")
	}
	if builder.Blueprint == majordomo.Blueprint || builder.Config.Spec.Blueprint.Name != "codex-root" {
		t.Fatal("a root role must use the root blueprint variant")
	}
	// The reviewer's secret is an optional slot of the shared blueprint: its
	// cell gets the secret, the operator's does not.
	slots := majordomo.Blueprint.Spec.Cell.Containers[0].Secrets
	if len(slots) != 1 || slots[0].Name != "service-auth" || slots[0].Required {
		t.Fatalf("shared blueprint slots = %+v", slots)
	}
	if got := work(t, reviewer, interactive).Secrets; len(got) != 1 || got[0].Name != "SERVICE_AUTH" {
		t.Fatalf("reviewer secrets = %+v", got)
	}
	if got := work(t, majordomo, interactive).Secrets; len(got) != 0 {
		t.Fatalf("majordomo inherited another role's secret: %+v", got)
	}
	if len(builder.Blueprint.Spec.Cell.Containers[0].Secrets) != 0 {
		t.Fatal("a role's leftover leaked into a blueprint it does not use")
	}
	// Each role keeps its own state volume and identity in its config.
	for name, pair := range byConfig {
		if pair.Config.Spec.Values["STATE_VOLUME"] != name+"-state" || pair.Config.Spec.Values["AGENT_NAME"] != name {
			t.Fatalf("%s values = %v", name, pair.Config.Spec.Values)
		}
	}
	if reviewer.Config.Spec.Values["ROLE_PATH"] != "team/roles/reviewer" ||
		reviewer.Config.Spec.Values["ROLE_NAME"] != "reviewer" {
		t.Fatalf("reviewer role facts = %v", reviewer.Config.Spec.Values)
	}
	raw, err := result.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(raw, []byte("kind: CellBlueprint")) != 4 || bytes.Count(raw, []byte("kind: CellConfig")) != 6 {
		t.Fatal("document stream must carry each shared blueprint once")
	}
	if bytes.Index(raw, []byte("kind: CellConfig")) < bytes.LastIndex(raw, []byte("kind: CellBlueprint")) {
		t.Fatal("blueprints must precede the configs that reference them")
	}
}

// Two roles sharing a blueprint cannot bind different secrets to one
// environment variable: the blueprint would hold both slots.
func TestSharedBlueprintRejectsCollidingRoleSecrets(t *testing.T) {
	cfg, opts := fixture(t)
	source := copiedSource(t)
	opts.Sources["kukeon"] = source
	addRole(source, "reviewer")
	editRole(source, "  needs:", "  needs:\n    secrets: [first-auth]")
	editFile(source, "team/roles/reviewer/role.yaml", "  needs:", "  needs:\n    secrets: [second-auth]")
	cfg.Spec.Defaults.Roles = append(cfg.Spec.Defaults.Roles,
		config.Role{Name: "reviewer", Source: "kukeon", Path: "team/roles/reviewer"})
	cfg.Spec.Secrets = []config.SecretBinding{
		{Name: "first-auth", EnvName: "AUTH_TOKEN", SecretRef: config.SecretRef{Name: "a", Realm: "auth"}},
		{Name: "second-auth", EnvName: "AUTH_TOKEN", SecretRef: config.SecretRef{Name: "b", Realm: "auth"}},
	}
	result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
	if err == nil || result != nil || !strings.Contains(err.Error(), "conflicting secret") {
		t.Fatalf("result=%v err=%v", result, err)
	}
}

// hostMount is one rendered host bind: source and read-only flag by target.
type hostMount struct {
	source   string
	readOnly bool
}

// The daemon socket and kuke binary mounts bind directories, never the files
// the host replaces on a daemon restart or rebuild (issue #1483).
//
//nolint:gochecknoglobals // read-only test table.
var (
	socketMounts = map[string]hostMount{"/run/kukeon/kukeond": {"/run/kukeon/kukeond", true}}
	binaryMounts = map[string]hostMount{
		"/.kukeon/client":     {"/opt/kukeon/bin/client", true},
		"/usr/local/bin/kuke": {"/opt/kukeon/bin/kuke-shim", true},
	}
	installMounts = map[string]hostMount{"/opt/kukeon": {"/opt/kukeon", true}}
)

func mergeMounts(sets ...map[string]hostMount) map[string]hostMount {
	out := map[string]hostMount{}
	for _, set := range sets {
		maps.Copy(out, set)
	}
	return out
}

// Host mounts are part of the security posture, so they select a blueprint
// variant instead of being a config toggle.
func TestHostMountProfiles(t *testing.T) {
	daemon := mergeMounts(socketMounts, binaryMounts)
	all := mergeMounts(socketMounts, binaryMounts, installMounts)
	for _, tc := range []struct {
		name, mounts, want string
		root               bool
		targets            map[string]hostMount
	}{
		{"host", "[kukeond-socket, kuke-binary]", "codex-host", false, daemon},
		{"host-root", "[ssh, kuke-binary, kukeond-socket]", "codex-root-host", true, daemon},
		{"all", "[kukeond-socket, kuke-binary, kukeon-install]", "codex-kuke-binary-kukeon-install-kukeond-socket", false, all},
		{"all-root", "[kukeon-install, ssh, kuke-binary, kukeond-socket]", "codex-root-kuke-binary-kukeon-install-kukeond-socket", true, all},
		{"partial", "[kuke-binary]", "codex-kuke-binary", false, binaryMounts},
		{"socket", "[kukeond-socket]", "codex-kukeond-socket", false, socketMounts},
		{"ssh-only", "[ssh]", "codex", false, map[string]hostMount{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, opts := fixture(t)
			source := copiedSource(t)
			opts.Sources["kukeon"] = source
			editRole(source, "  needs:", "  needs:\n    mounts: "+tc.mounts)
			if tc.root {
				cfg.Spec.Defaults.Roles[0].Needs.Image = append(cfg.Spec.Defaults.Roles[0].Needs.Image, "root")
			}
			result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
			if err != nil {
				t.Fatal(err)
			}
			pair := result.Pairs[0]
			if pair.Blueprint.Metadata.Name != tc.want || pair.Blueprint.Spec.Prefix != tc.want {
				t.Fatalf("blueprint = %q, want %q", pair.Blueprint.Metadata.Name, tc.want)
			}
			_, socket := tc.targets["/run/kukeon/kukeond"]
			// The standard layout renders the literal paths of before #1710;
			// another run path and socket move every source, never a target.
			for _, layout := range []struct {
				paths     cellblueprint.HostPaths
				sources   *strings.Replacer
				kukeonEnv string
			}{
				{cellblueprint.HostPaths{}, strings.NewReplacer(), "unix:///run/kukeon/kukeond/kukeond.sock"},
				{
					cellblueprint.HostPaths{RunPath: "/srv/kx", Socket: "/run/kx/daemon.sock"},
					strings.NewReplacer("/run/kukeon/", "/run/kx/", "/opt/kukeon", "/srv/kx"),
					"unix:///run/kukeon/kukeond/daemon.sock",
				},
			} {
				container := workOf(t, renderCellAt(t, pair, interactive, nil, layout.paths))
				got := map[string]hostMount{}
				for _, volume := range container.Volumes {
					if volume.Kind == api.VolumeKindVolume || volume.Target == "/opt/cni" {
						continue
					}
					got[volume.Target] = hostMount{volume.Source, volume.ReadOnly}
				}
				want := map[string]hostMount{}
				for target, m := range tc.targets {
					want[target] = hostMount{layout.sources.Replace(m.source), m.readOnly}
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("%+v: host mounts = %v, want %v", layout.paths, got, want)
				}
				host := slices.Contains(container.Env, "KUKEON_HOST="+layout.kukeonEnv)
				if host != socket {
					t.Fatalf("%+v: KUKEON_HOST=%s set = %v, want %v: %v",
						layout.paths, layout.kukeonEnv, host, socket, container.Env)
				}
			}
			raw, err := result.MarshalYAML()
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(raw, []byte(".ssh")) || bytes.Contains(raw, []byte("GIT_SSH_COMMAND")) {
				t.Fatal("host ssh material rendered")
			}
			noted := slices.ContainsFunc(
				result.Notes,
				func(n string) bool { return strings.Contains(n, "needs.mounts ssh") },
			)
			if noted != strings.Contains(tc.mounts, "ssh") {
				t.Fatalf("ssh note = %v for mounts %s: %v", noted, tc.mounts, result.Notes)
			}
		})
	}
}

// TestHarnessModesPerCell is step 11 on the team side: one role config serves
// an attached cell and a dispatched run, and what a mode declares applies only
// to cells created in it.
func TestHarnessModesPerCell(t *testing.T) {
	cfg, opts := fixture(t)
	allHarnesses(cfg)
	cfg.Spec.Defaults.Roles[0].Harness = "claude"
	source := copiedSource(t)
	opts.Sources["kukeon"] = source
	editRole(
		source,
		"      settings: config/claude.settings.json",
		"      settings: config/claude.settings.json\n      secrets: [claude-code-oauth-token, anthropic-api-key]",
	)
	result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
	if err != nil {
		t.Fatal(err)
	}
	pair := result.Pairs[0]
	if got := cellblueprint.Modes(*pair.Blueprint); !slices.Equal(got, []string{headless, interactive}) {
		t.Fatalf("modes = %v", got)
	}
	// The config is mode-neutral: no PROMPT. The two secrets the role lists
	// are the old names of two ways to authenticate, which are alternatives
	// (issue #1548): the first one named picks the method, its identity is
	// left to the Realm default, and composition says so.
	if _, set := pair.Config.Spec.Values["PROMPT"]; set || len(pair.Config.Spec.Secrets) != 0 {
		t.Fatalf("config values %v secrets %v", pair.Config.Spec.Values, pair.Config.Spec.Secrets)
	}
	if !slices.Equal(pair.Config.Spec.Components, []string{"claude-subscription"}) {
		t.Fatalf("components = %v, want the one method the role's first secret selects", pair.Config.Spec.Components)
	}
	if !slices.ContainsFunc(result.Notes, func(note string) bool {
		return strings.Contains(note, "subscription and api-key") && strings.Contains(note, "alternatives")
	}) {
		t.Fatalf("notes = %v, want the two methods reported as alternatives", result.Notes)
	}
	if want := []compose.DefaultSecret{{
		Slot:      "claude-code-oauth-token",
		Reference: config.SecretRef{Name: "claude-code-oauth-token", Realm: "default"},
	}}; !reflect.DeepEqual(result.DefaultSecrets, want) {
		t.Fatalf("default secrets = %+v", result.DefaultSecrets)
	}
	if template := pair.Blueprint.Spec.Cell.Containers[0]; template.Tty == nil || len(template.Tty.OnInit) != 0 {
		t.Fatalf("a harness with modes keeps its startup in the modes: %+v", template.Tty)
	}

	attached := work(t, pair, interactive)
	if _, set := envOf(attached)["PROMPT"]; set {
		t.Fatal("interactive cell carries PROMPT")
	}
	if launcher := script(attached); strings.Contains(launcher, "--print") ||
		!strings.Contains(launcher, `--settings "$ROLE_SOURCE_DIR/$ROLE_PATH/$HARNESS_SETTINGS"`) {
		t.Fatalf("interactive launcher: %s", launcher)
	}
	if names := secretNames(attached); !slices.Equal(names, []string{"CLAUDE_CODE_OAUTH_TOKEN"}) {
		t.Fatalf("interactive secrets = %v", names)
	}
	if envOf(attached)["HARNESS_SETTINGS"] != "config/claude.settings.json" ||
		envOf(attached)["ANTHROPIC_MODEL"] != "opus" {
		t.Fatalf("interactive env = %v", envOf(attached))
	}

	dispatched := workOf(t, renderCell(t, pair, headless, map[string]string{"PROMPT": "ship it"}))
	if envOf(dispatched)["PROMPT"] != "ship it" {
		t.Fatalf("headless PROMPT = %q", envOf(dispatched)["PROMPT"])
	}
	if launcher := script(dispatched); !strings.Contains(launcher, `--print "$next_prompt"`) ||
		strings.Contains(launcher, "${PROMPT}") {
		t.Fatalf("headless launcher: %s", launcher)
	}
	// The method is the Config's, so both modes authenticate the same way.
	if names := secretNames(dispatched); !slices.Equal(names, []string{"CLAUDE_CODE_OAUTH_TOKEN"}) {
		t.Fatalf("headless secrets = %v", names)
	}
	// Both modes share the state volume, so a login made attached serves runs.
	a, _ := stateVolume(attached)
	d, _ := stateVolume(dispatched)
	if a.VolumeRef == nil || d.VolumeRef == nil || a.VolumeRef.Name != "root-majordomo-claude-state" ||
		*a.VolumeRef != *d.VolumeRef || a.Target != d.Target || a.Ensure != d.Ensure {
		t.Fatalf("state volumes differ across modes: %+v %+v", a, d)
	}

	// A headless cell without its prompt is refused; an interactive one needs none.
	flat, err := cellblueprint.Flatten(*pair.Blueprint, cellblueprint.FlattenOptions{Mode: headless})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(flat.ModeParameters, []string{"PROMPT"}) {
		t.Fatalf("headless mode parameters = %v", flat.ModeParameters)
	}
	noPrompt := *pair.Config
	noPrompt.Spec.Secrets = nil
	if _, err = cellconfig.MaterializeWithName(noPrompt, flat.Doc, "cell"); err == nil ||
		!strings.Contains(err.Error(), "PROMPT") {
		t.Fatalf("headless cell without PROMPT: %v", err)
	}
	if _, err = cellblueprint.Flatten(*pair.Blueprint, cellblueprint.FlattenOptions{Mode: "batch"}); err == nil {
		t.Fatal("unknown mode accepted")
	}
}

func secretNames(c api.ContainerSpec) []string {
	var out []string
	for _, secret := range c.Secrets {
		out = append(out, secret.Name)
	}
	slices.Sort(out)
	return out
}

// TestBlueprintIsASelfDescribingForm locks what the web UI builds its form
// from: typed, grouped parameters, generic repo slots, and the optional
// connector and forge components.
//
//nolint:gocyclo,cyclop // a flat list of independent assertions on one result.
func TestBlueprintIsASelfDescribingForm(t *testing.T) {
	cfg, opts := fixture(t)
	result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
	if err != nil {
		t.Fatal(err)
	}
	bp := result.Blueprints[0]
	params := map[string]api.CellBlueprintParameter{}
	for _, p := range bp.Spec.Parameters {
		params[p.Name] = p
	}
	for name, want := range map[string]struct {
		kind, group string
		required    bool
	}{
		"IMAGE":              {api.ParameterTypeImage, "image", false},
		"ROLE_SOURCE_DIR":    {api.ParameterTypePath, "role", true},
		"ROLE_PATH":          {api.ParameterTypePath, "role", true},
		"INITIAL_PATH":       {api.ParameterTypePath, "role", false},
		"PROJECT_DIR":        {api.ParameterTypePath, "role", false},
		"ROLE_NAME":          {"", "role", true},
		"STATE_VOLUME":       {"", "state", true},
		"STATE_VOLUME_REALM": {"", "state", true},
		"STATE_VOLUME_SPACE": {"", "state", true},
		"STATE_VOLUME_STACK": {"", "state", true},
	} {
		got, ok := params[name]
		if !ok || got.Type != want.kind || got.Group != want.group || got.Required != want.required ||
			got.Description == "" {
			t.Errorf("parameter %s = %+v (declared=%v)", name, got, ok)
		}
	}
	if _, legacy := params["PROMPT"]; legacy {
		t.Error("PROMPT must be the headless mode's parameter, not the blueprint's")
	}
	byName := map[string]api.BlueprintComponent{}
	var order []string
	for _, c := range bp.Spec.Components {
		byName[c.Name] = c
		order = append(order, c.Name)
	}
	// The harness, the ways it authenticates (one exclusive group), then the
	// connectors and forges a role may enable.
	if want := []string{
		"codex", "codex-login", "codex-api-key", "codex-access-token", "git", "github", "github-issues",
	}; !slices.Equal(order, want) {
		t.Fatalf("components = %v, want %v", order, want)
	}
	harnessComp, connector, forge := byName["codex"], byName["github"], byName["github-issues"]
	for _, name := range []string{"codex-login", "codex-api-key", "codex-access-token"} {
		auth := byName[name]
		if auth.Kind != api.ComponentKindConnector || !auth.Optional || auth.Group != "codex-auth" ||
			auth.Connector == nil || auth.Connector.Name != "codex" || auth.Connector.Title == "" ||
			!slices.Equal(auth.Connector.SecretTypes, []string{api.SecretTypeOpaque}) {
			t.Fatalf("auth component %s = %+v (connector %+v)", name, auth, auth.Connector)
		}
	}
	if login := byName["codex-login"]; login.Connector.Delivery != api.ConnectorDeliveryStateVolume ||
		login.Connector.Method != "login" || len(login.Secrets) != 0 {
		t.Fatalf("codex-login = %+v (connector %+v)", login, login.Connector)
	}
	if key := byName["codex-api-key"]; key.Connector.Delivery != api.ConnectorDeliveryEnv ||
		len(key.Secrets) != 1 || key.Secrets[0].Name != "openai-api-key" ||
		key.Secrets[0].EnvName != "OPENAI_API_KEY" || !key.Secrets[0].Required {
		t.Fatalf("codex-api-key = %+v", key)
	}
	// The git connector carries the identity schema the daemon and the UI
	// validate against, and no slot of its own.
	if git := byName["git"]; git.Group != "" || git.Connector == nil ||
		git.Connector.Delivery != api.ConnectorDeliveryGit || !git.Connector.Optional || len(git.Secrets) != 0 ||
		!slices.ContainsFunc(git.Connector.Fields, func(f api.ConnectorField) bool {
			return f.Name == "hosts" && f.Required && f.Type == api.ConnectorFieldList
		}) {
		t.Fatalf("git connector component = %+v (connector %+v)", git, git.Connector)
	}
	if connector.Group != "" || connector.Connector == nil || connector.Connector.Name != "github" ||
		connector.Connector.Delivery != api.ConnectorDeliveryEnv {
		t.Fatalf("github connector descriptor = %+v", connector.Connector)
	}
	if harnessComp.Kind != api.ComponentKindHarness || harnessComp.Name != "codex" || harnessComp.Optional ||
		!slices.Equal(harnessComp.Requires, []string{"codex"}) {
		t.Fatalf("harness component = %+v", harnessComp)
	}
	settings := map[string]api.CellBlueprintParameter{}
	for _, p := range harnessComp.Parameters {
		settings[p.Name] = p
	}
	if sandbox := settings["SANDBOX"]; sandbox.Type != api.ParameterTypeEnum ||
		!slices.Contains(sandbox.Enum, "workspace-write") || sandbox.Default == nil || sandbox.Group != "harness" {
		t.Fatalf("SANDBOX = %+v", sandbox)
	}
	if approval := settings["APPROVAL"]; approval.Type != api.ParameterTypeEnum ||
		!slices.Contains(approval.Enum, "never") {
		t.Fatalf("APPROVAL = %+v", approval)
	}
	if _, foreign := settings["PERMISSIONS"]; foreign {
		t.Fatal("codex blueprint declares an OpenCode setting")
	}
	if connector.Kind != api.ComponentKindConnector || connector.Name != "github" || !connector.Optional ||
		!slices.Equal(connector.Requires, []string{"gh"}) || len(connector.Secrets) != 1 ||
		connector.Secrets[0].Name != "github-token" || connector.Secrets[0].EnvName != "GH_TOKEN" ||
		!connector.Secrets[0].Required {
		t.Fatalf("github connector = %+v", connector)
	}
	if forge.Kind != api.ComponentKindForge || forge.Name != "github-issues" || !forge.Optional ||
		!slices.Equal(forge.Requires, []string{"gh"}) || len(forge.Secrets) != 0 {
		t.Fatalf("github-issues forge = %+v", forge)
	}
	template := bp.Spec.Cell.Containers[0]
	if template.Image != "${IMAGE}" || template.Git != nil {
		t.Fatalf("template image %q git %v", template.Image, template.Git)
	}
	slots := map[string]bool{}
	for _, repo := range template.Repos {
		if repo.URL != "" || repo.Branch != "" {
			t.Fatalf("repo slot %s carries a source", repo.Name)
		}
		slots[repo.Name] = repo.Required
	}
	if !reflect.DeepEqual(slots, map[string]bool{"role-source": true, "project": false}) {
		t.Fatalf("repo slots = %v", slots)
	}
}

// TestGitHubConnector is step 14: the token reaches the cell as GH_TOKEN only
// when the role asks for the connector, in either spelling, and never as a git
// credential.
//
//nolint:gocyclo,cyclop // a flat list of independent assertions on one result.
func TestGitHubConnector(t *testing.T) {
	team := config.SecretBinding{
		Name: "gh-token", EnvName: "GH_TOKEN", SecretRef: config.SecretRef{Name: "team-gh", Realm: "auth"},
	}
	// top is added to the role's spec, needs to its needs block; ghImage adds the
	// transition-era `needs.image: [gh]`.
	for _, tc := range []struct {
		name, top, needs string
		ghImage          bool
		bindings         []config.SecretBinding
		components       []string
		secret           string
		entry            string
	}{
		{"connectors", "connectors: [github]", "", false, nil, []string{"github"}, "gh-token", "github-token"},
		{"connectors-team-binding", "connectors: [github]", "", false, []config.SecretBinding{team}, []string{"github"}, "team-gh", "gh-token"},
		{"legacy-secret", "", "secrets: [gh-token]", false, nil, []string{"github"}, "gh-token", "gh-token"},
		{"legacy-secret-team-binding", "", "secrets: [gh-token]", false, []config.SecretBinding{team}, []string{"github"}, "team-gh", "gh-token"},
		{"legacy-secret-and-image", "", "secrets: [gh-token]", true, nil, []string{"github"}, "gh-token", "gh-token"},
		{"forge", "forges: [github-issues]", "", false, nil, []string{"github", "github-issues"}, "gh-token", "github-token"},
		{"both", "connectors: [github]\n  forges: [github-issues]", "secrets: [gh-token]", false, nil, []string{"github", "github-issues"}, "gh-token", "gh-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, opts := fixture(t)
			allHarnesses(cfg)
			cfg.Spec.Defaults.Harnesses = cfg.Spec.Defaults.Harnesses[:2]
			cfg.Spec.Secrets = tc.bindings
			source := copiedSource(t)
			opts.Sources["kukeon"] = source
			before := string(source[majordomoRole].Data)
			if tc.top != "" {
				editRole(source, "  needs:\n", "  "+tc.top+"\n  needs:\n")
			}
			if tc.needs != "" {
				editRole(source, "  needs:\n", "  needs:\n    "+tc.needs+"\n")
			}
			if tc.ghImage {
				editRole(source, "      - go\n", "      - go\n      - gh\n")
			}
			if string(source[majordomoRole].Data) == before {
				t.Fatal("role fixture was not edited")
			}
			result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
			if err != nil {
				t.Fatal(err)
			}
			// An identity the operator bound is a Config fill; the default
			// one is left to the Realm default, written once on application.
			chosen := len(tc.bindings) > 0
			wantDefaults := []compose.DefaultSecret{{
				Slot: "github-token", Reference: config.SecretRef{Name: "gh-token", Realm: "default"},
			}}
			if chosen {
				wantDefaults = nil
			}
			if !reflect.DeepEqual(result.DefaultSecrets, wantDefaults) {
				t.Fatalf("default secrets = %+v, want %+v", result.DefaultSecrets, wantDefaults)
			}
			for _, pair := range result.Pairs {
				// Every harness Config also says how the harness
				// authenticates: its login, with nothing chosen.
				want := append([]string{pair.Harness + "-login"}, tc.components...)
				if !slices.Equal(pair.Config.Spec.Components, want) {
					t.Fatalf("%s components = %v, want %v", pair.Harness, pair.Config.Spec.Components, want)
				}
				fill, filled := pair.Config.Spec.Secrets["github-token"]
				if filled != chosen || len(pair.Config.Spec.Secrets) > 1 ||
					(filled && (fill.SecretRef == nil || fill.SecretRef.Name != tc.secret)) {
					t.Fatalf("%s secret fills = %+v", pair.Harness, pair.Config.Spec.Secrets)
				}
				if pair.Secrets[0].Default == chosen {
					t.Fatalf("%s requirement default = %v, chosen = %v", pair.Harness, pair.Secrets[0].Default, chosen)
				}
				if len(pair.Secrets) != 1 || pair.Secrets[0].Identifier != "github-token" ||
					pair.Secrets[0].Destination.EnvName != "GH_TOKEN" || pair.Secrets[0].Reference.Name != tc.secret {
					t.Fatalf("%s requirement = %+v", pair.Harness, pair.Secrets)
				}
				for _, mode := range []string{interactive, headless} {
					doc := renderCell(t, pair, mode, map[string]string{"PROMPT": "p"})
					c := workOf(t, doc)
					if len(c.Secrets) != 1 || c.Secrets[0].Name != "GH_TOKEN" || c.Secrets[0].MountPath != "" ||
						c.Secrets[0].SecretRef.Name != tc.secret {
						t.Fatalf("%s/%s cell secrets = %+v", pair.Harness, mode, c.Secrets)
					}
					// The token is API-only: it is not a git credential and
					// nothing wires it into git.
					if c.Git != nil {
						t.Fatalf("%s/%s connector contributed a git block: %+v", pair.Harness, mode, c.Git)
					}
					for key := range envOf(c) {
						if strings.HasPrefix(key, "GIT_") {
							t.Fatalf("%s/%s connector set %s", pair.Harness, mode, key)
						}
					}
				}
				// The connector's capability joins the cell's requirements.
				flat, flatErr := cellblueprint.Flatten(*pair.Blueprint, cellblueprint.FlattenOptions{
					Components: pair.Config.Spec.Components,
				})
				if flatErr != nil || !slices.Contains(flat.Doc.Spec.Requires, "gh") {
					t.Fatalf("%s requires = %v err=%v", pair.Harness, flat.Doc.Spec.Requires, flatErr)
				}
			}
			if len(result.Team.Spec.Secrets) != 1 || result.Team.Spec.Secrets[0].Name != tc.entry ||
				result.Team.Spec.Secrets[0].SecretRef.Name != tc.secret {
				t.Fatalf("recorded bindings = %+v", result.Team.Spec.Secrets)
			}
			// Recomposing from the recorded entry keeps the same wiring.
			opts.Secrets = result.Team.Spec.Secrets
			again, err := compose.Compose(cfg, selectedTeam(cfg), opts)
			if err != nil || !reflect.DeepEqual(again.Pairs[0].Config, result.Pairs[0].Config) {
				t.Fatalf("recompose changed the config: %v", err)
			}
		})
	}
}

func TestUnknownComponentsAreRejected(t *testing.T) {
	for _, tc := range []struct{ role, want string }{
		{"  connectors: [gitlab]\n  needs:", `unknown connector component "gitlab"`},
		{"  connectors: [github-issues]\n  needs:", `unknown connector component "github-issues"`},
		{"  forges: [github]\n  needs:", `unknown forge component "github"`},
	} {
		cfg, opts := fixture(t)
		source := copiedSource(t)
		opts.Sources["kukeon"] = source
		editRole(source, "  needs:", tc.role)
		result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
		if err == nil || result != nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%q: result=%v err=%v", tc.role, result, err)
		}
	}
}

// A custom component library replaces the bundled one; an invalid one fails
// composition before anything is rendered.
func TestComponentLibraryOverride(t *testing.T) {
	cfg, opts := fixture(t)
	opts.Components = fstest.MapFS{"tracker/component.yaml": &fstest.MapFile{Data: []byte(`apiVersion: v1beta1
kind: Component
metadata: {name: tracker}
spec:
  kind: connector
  requires: [curl]
  environment: {TRACKER_URL: https://tracker.test}
  secrets:
    - {name: tracker-token, envName: TRACKER_TOKEN, required: true}
`)}}
	source := copiedSource(t)
	opts.Sources["kukeon"] = source
	editRole(source, "  needs:", "  connectors: [tracker]\n  needs:")
	result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
	if err != nil {
		t.Fatal(err)
	}
	pair := result.Pairs[0]
	c := work(t, pair, interactive)
	if envOf(c)["TRACKER_URL"] != "https://tracker.test" || len(c.Secrets) != 1 ||
		c.Secrets[0].Name != "TRACKER_TOKEN" ||
		c.Secrets[0].SecretRef.Name != "tracker-token" ||
		c.Secrets[0].SecretRef.Realm != "default" {
		t.Fatalf("custom connector not applied: env=%v secrets=%+v", envOf(c), c.Secrets)
	}
	// The override replaces the component library, not the connector catalog
	// (Options.Connectors): the bundled forge is gone, the custom connector is
	// in, and the catalog's connectors are still on the form.
	names := []string{}
	for _, c := range pair.Blueprint.Spec.Components {
		names = append(names, c.Name)
	}
	if slices.Contains(names, "github-issues") || !slices.Contains(names, "tracker") ||
		!slices.Contains(names, "github") {
		t.Fatalf("components of an overridden library = %v", names)
	}
	opts.Components = fstest.MapFS{"bad/component.yaml": &fstest.MapFile{Data: []byte("kind: Component\n")}}
	if result, err = compose.Compose(cfg, selectedTeam(cfg), opts); err == nil || result != nil ||
		!strings.Contains(err.Error(), "component library") {
		t.Fatalf("result=%v err=%v", result, err)
	}
}

// TestPerSourceGitIdentity: a source may state its own identity, rendered into
// its repo fill; sources that do not keep using the default.
func TestPerSourceGitIdentity(t *testing.T) {
	cfg, opts := fixture(t)
	deploy := config.SecretRef{Name: "kukeon-deploy", Realm: "default"}
	signers := config.SecretRef{Name: "signers", Realm: "default"}
	cfg.Spec.Defaults.Sources[0].Git = &config.SourceGit{
		Author:     &config.Identity{Name: "kukeon-bot", Email: "bot@kukeon.io"},
		Credential: &config.SourceCredential{SecretRef: deploy, User: "git", Port: 2222, HostKeyPolicy: "strict"},
		Signing:    &config.GitSigning{SecretRef: deploy, AllowedSignersRef: &signers},
		Sign:       []string{"tags"},
	}
	cfg.Spec.Defaults.Sources = append(cfg.Spec.Defaults.Sources, config.Source{
		Name: "project", URL: "https://example.test/work", Branch: "main", ClonePath: "/home/user/work",
	})
	result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
	if err != nil {
		t.Fatal(err)
	}
	pair := result.Pairs[0]
	want := &api.RepoGit{
		Author: &api.GitIdentity{Name: "kukeon-bot", Email: "bot@kukeon.io"},
		Credential: &api.GitCredential{
			SecretRef: &api.ContainerSecretRef{Name: "kukeon-deploy", Realm: "default"},
			User:      "git", Port: 2222, HostKeyPolicy: "strict",
		},
		Signing: &api.GitSigning{
			SecretRef:         &api.ContainerSecretRef{Name: "kukeon-deploy", Realm: "default"},
			AllowedSignersRef: &api.ContainerSecretRef{Name: "signers", Realm: "default"},
		},
		Sign: []string{"tags"},
	}
	if got := pair.Config.Spec.Repos["role-source"].Git; !reflect.DeepEqual(got, want) {
		t.Fatalf("role-source git = %+v, want %+v", got, want)
	}
	if pair.Config.Spec.Repos["project"].Git != nil {
		t.Fatal("a source without its own identity must fall back to the default")
	}
	for _, repo := range work(t, pair, interactive).Repos {
		if (repo.Name == "role-source") != (repo.Git != nil) {
			t.Fatalf("cell repo %s git = %+v", repo.Name, repo.Git)
		}
	}
	// Composition does not alias the authored declaration.
	cfg.Spec.Defaults.Sources[0].Git.Author.Name = "changed"
	if pair.Config.Spec.Repos["role-source"].Git.Author.Name != "kukeon-bot" {
		t.Fatal("repo fill aliases the configuration")
	}
}

// TestSSHSourcesAreReported: composition names every in-cell SSH clone that
// states no credential of its own, so application can say when nothing gives
// the team a git identity (issue #1591). It fails nothing and notes nothing.
func TestSSHSourcesAreReported(t *testing.T) {
	sshSource := func(cfg *config.TeamDefaults) {
		cfg.Spec.Defaults.Sources = append(
			cfg.Spec.Defaults.Sources,
			config.Source{
				Name:      "agents",
				URL:       "git@github.com:eminwux/agents",
				Branch:    "main",
				ClonePath: "/home/user/agents",
			},
			config.Source{Name: "definitions", URL: "git@gitlab.com:team/defs", Tag: "v1"}, // not cloned in the cell
		)
	}
	ref := config.SecretRef{Name: "git-ssh", Realm: "default"}
	for _, tc := range []struct {
		name   string
		change func(*config.TeamDefaults)
		want   []string
	}{
		{"ssh-source", func(*config.TeamDefaults) {}, []string{"agents"}},
		{"source-credential", func(c *config.TeamDefaults) {
			c.Spec.Defaults.Sources[1].Git = &config.SourceGit{Credential: &config.SourceCredential{SecretRef: ref}}
		}, nil},
		// A stored Source's identity (#1643, D2) authenticates the clone.
		{"source-identity", func(c *config.TeamDefaults) {
			c.Spec.Defaults.Sources[1].Git = &config.SourceGit{IdentityRef: &ref}
		}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, opts := fixture(t)
			sshSource(cfg)
			tc.change(cfg)
			result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(result.SSHSources, tc.want) {
				t.Fatalf("ssh sources = %v, want %v", result.SSHSources, tc.want)
			}
			for _, note := range result.Notes {
				if strings.Contains(note, "source ") {
					t.Fatalf("composition notes a source: %s", note)
				}
			}
			// The SSH URL is kept: the transport follows the credential.
			fill := result.Pairs[0].Config.Spec.Repos["agents"]
			if got := fill.URL; got != "git@github.com:eminwux/agents" {
				t.Fatalf("agents url = %q", got)
			}
			if tc.name == "source-identity" &&
				(fill.Git == nil || fill.Git.IdentityRef == nil || fill.Git.IdentityRef.Name != "git-ssh") {
				t.Fatalf("agents repo fill git = %+v, want the Source's identityRef", fill.Git)
			}
		})
	}
}

// Values composition derives from the role and source selections cannot be
// overridden through spec.parameters: the shared form's answers must match the
// repos and volumes actually bound.
func TestComposedParametersCannotBeOverridden(t *testing.T) {
	for _, name := range []string{"IMAGE", "ROLE_PATH", "ROLE_SOURCE_DIR", "STATE_VOLUME", "SANDBOX", "PROJECT_DIR"} {
		cfg, opts := fixture(t)
		cfg.Spec.Parameters = map[string]string{name: "override"}
		result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
		if err == nil || result != nil || !strings.Contains(err.Error(), "is set by composition") ||
			!strings.Contains(err.Error(), name) {
			t.Fatalf("%s: result=%v err=%v", name, result, err)
		}
	}
	// A parameter the form leaves to the operator is still theirs, and the
	// operator's value overrides a role-declared default.
	cfg, opts := withRoleParams(t, agentsRepoDefault, "git@github.com:eminwux/agents")
	cfg.Spec.Parameters = map[string]string{"AGENTS_REPO": "fork/agents", "AGENT_NAME": "custom"}
	result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
	if err != nil {
		t.Fatal(err)
	}
	values := result.Pairs[0].Config.Spec.Values
	if values["AGENTS_REPO"] != "fork/agents" || values["AGENT_NAME"] != "custom" {
		t.Fatalf("values = %v", values)
	}
	if envOf(work(t, result.Pairs[0], interactive))["AGENTS_REPO"] != "fork/agents" {
		t.Fatal("AGENTS_REPO did not reach the cell")
	}
	// ROLE_SOURCE_REPO is a fact for role defaults, never an operator input.
	cfg, opts = fixture(t)
	cfg.Spec.Parameters = map[string]string{"ROLE_SOURCE_REPO": "x/y"}
	if _, err = compose.Compose(cfg, selectedTeam(cfg), opts); err == nil ||
		!strings.Contains(err.Error(), "unsupported parameter") {
		t.Fatalf("ROLE_SOURCE_REPO override: err = %v", err)
	}
}

// TestRenameBlueprintOnContentConflict covers the helpers application uses to
// keep a different same-named blueprint in the realm from being overwritten.
func TestRenameBlueprintOnContentConflict(t *testing.T) {
	cfg, opts := fixture(t)
	allHarnesses(cfg)
	cfg.Spec.Defaults.Harnesses = cfg.Spec.Defaults.Harnesses[:2]
	source := copiedSource(t)
	opts.Sources["kukeon"] = source
	addRole(source, "reviewer")
	cfg.Spec.Defaults.Roles = append(cfg.Spec.Defaults.Roles,
		config.Role{Name: "reviewer", Source: "kukeon", Path: "team/roles/reviewer"})
	result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
	if err != nil {
		t.Fatal(err)
	}
	codex := result.Blueprints[0]
	digest := codex.Metadata.Labels[api.LabelContentDigest]
	name := compose.DigestName(codex)
	if name != "codex-"+digest || len(digest) != 16 || !config.ValidName(name) {
		t.Fatalf("digest name = %q", name)
	}
	result.RenameBlueprint(codex, name)
	// Renaming twice must not stack suffixes: the name derives from the prefix.
	if compose.DigestName(codex) != name {
		t.Fatalf("digest name is not stable after a rename: %q", compose.DigestName(codex))
	}
	for _, pair := range result.Pairs {
		want := pair.Harness
		if pair.Harness == "codex" {
			want = name
		}
		if pair.Config.Spec.Blueprint.Name != want || pair.Blueprint.Metadata.Name != want {
			t.Fatalf("%s references %q, want %q", pair.Config.Metadata.Name, pair.Config.Spec.Blueprint.Name, want)
		}
	}
	raw, err := result.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(raw, []byte("name: "+name+"\n")) != 3 { // blueprint metadata + two config refs
		t.Fatalf("renamed blueprint not reflected in the document stream:\n%s", raw)
	}

	// The digest follows content, not the name or the team: a harness revision
	// changes it, another team rendering the same harness does not.
	other := selectedTeam(cfg)
	other.Metadata.Name = "other"
	same, err := compose.Compose(cfg, other, opts)
	if err != nil {
		t.Fatal(err)
	}
	if same.Blueprints[1].Metadata.Labels[api.LabelContentDigest] != result.Blueprints[1].Metadata.Labels[api.LabelContentDigest] {
		t.Fatal("two teams rendered different content for the same harness")
	}
	editFile(source, "team/harnesses/claude/harness.yaml", "default: opus", "default: sonnet")
	changed, err := compose.Compose(cfg, other, opts)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Blueprints[1].Metadata.Labels[api.LabelContentDigest] == result.Blueprints[1].Metadata.Labels[api.LabelContentDigest] {
		t.Fatal("a harness revision did not change the content digest")
	}
	if changed.Blueprints[0].Metadata.Labels[api.LabelContentDigest] != digest {
		t.Fatal("an unrelated harness revision changed the codex digest")
	}
}

// TestComposedDocumentsPassApplyValidation runs every composed document
// through the same parser validation `kuke apply` and the team apply RPC
// perform. Composition and the daemon agree on the schema only if this holds;
// a blueprint the daemon would refuse (for example a slot whose target is a
// ${PARAM} reference the parser did not allow) must fail here, not at init.
func TestComposedDocumentsPassApplyValidation(t *testing.T) {
	cfg, opts := fixture(t)
	allHarnesses(cfg)
	result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := result.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	documents, err := parser.ParseDocuments(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if want := len(result.Blueprints) + len(result.Pairs); len(documents) != want {
		t.Fatalf("stream has %d documents, want %d", len(documents), want)
	}
	for i, document := range documents {
		doc, parseErr := parser.ParseDocument(i, document)
		if parseErr != nil {
			t.Fatalf("document %d: %v", i, parseErr)
		}
		if validationErr := parser.ValidateDocument(doc); validationErr != nil {
			t.Errorf("document %d: %v", i, validationErr)
		}
	}
}
