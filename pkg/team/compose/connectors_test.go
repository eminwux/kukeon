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
	"reflect"
	"slices"
	"strings"
	"testing"

	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/compose"
	"github.com/eminwux/kukeon/pkg/team/config"
	"github.com/eminwux/kukeon/pkg/team/connector/connectortest"
)

// pairOf returns the composed pair of a harness.
func pairOf(t *testing.T, result *compose.Result, harness string) compose.Pair {
	t.Helper()
	for _, pair := range result.Pairs {
		if pair.Harness == harness {
			return pair
		}
	}
	t.Fatalf("no pair for harness %q", harness)
	return compose.Pair{}
}

// componentNames lists a blueprint's component names in order.
func componentNames(bp *api.CellBlueprintDoc) []string {
	out := make([]string, 0, len(bp.Spec.Components))
	for _, c := range bp.Spec.Components {
		out = append(out, c.Name)
	}
	return out
}

// defaultSlots lists the slots a result leaves to the scope defaults.
func defaultSlots(result *compose.Result) []string {
	out := make([]string, 0, len(result.DefaultSecrets))
	for _, d := range result.DefaultSecrets {
		out = append(out, d.Slot)
	}
	return out
}

// TestTeamConnectorChoices is the team side of issue #1548: `spec.connectors`
// picks a method and an identity, a role entry wins over it, and only an
// identity the team chose becomes a Config fill — everything else stays with
// the scope defaults.
func TestTeamConnectorChoices(t *testing.T) {
	const auth = "claude"
	ref := func(name string) *api.ContainerSecretRef {
		return &api.ContainerSecretRef{Name: name, Realm: "default"}
	}
	for _, tc := range []struct {
		name       string
		team, role map[string]config.ConnectorChoice
		component  string
		slot       string
		fill       *api.ContainerSecretRef
		defaults   []string
	}{
		{name: "nothing chosen", component: "claude-login"},
		{
			name:      "team picks a method",
			team:      map[string]config.ConnectorChoice{auth: {Method: "api-key"}},
			component: "claude-api-key", slot: "anthropic-api-key", defaults: []string{"anthropic-api-key"},
		},
		{
			name:      "team picks a method and an identity",
			team:      map[string]config.ConnectorChoice{auth: {Method: "subscription", Identity: "team-sub"}},
			component: "claude-subscription", slot: "claude-code-oauth-token", fill: ref("team-sub"),
		},
		{
			name:      "role identity keeps the team method",
			team:      map[string]config.ConnectorChoice{auth: {Method: "subscription", Identity: "team-sub"}},
			role:      map[string]config.ConnectorChoice{auth: {Identity: "role-sub@auth/shared"}},
			component: "claude-subscription", slot: "claude-code-oauth-token",
			fill: &api.ContainerSecretRef{Name: "role-sub", Realm: "auth", Space: "shared"},
		},
		{
			name:      "role method wins over the team",
			team:      map[string]config.ConnectorChoice{auth: {Method: "subscription", Identity: "team-sub"}},
			role:      map[string]config.ConnectorChoice{auth: {Method: "api-key"}},
			component: "claude-api-key", slot: "anthropic-api-key", defaults: []string{"anthropic-api-key"},
		},
		{
			name:      "role returns to the login",
			team:      map[string]config.ConnectorChoice{auth: {Method: "api-key", Identity: "team-key"}},
			role:      map[string]config.ConnectorChoice{auth: {Method: "login"}},
			component: "claude-login",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, opts := fixture(t)
			allHarnesses(cfg)
			cfg.Spec.Defaults.Harnesses = cfg.Spec.Defaults.Harnesses[:2] // codex, claude
			cfg.Spec.Defaults.Connectors = tc.team
			cfg.Spec.Defaults.Roles[0].Connectors = tc.role
			result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
			if err != nil {
				t.Fatal(err)
			}
			pair := pairOf(t, result, "claude")
			if !slices.Equal(pair.Config.Spec.Components, []string{tc.component}) {
				t.Fatalf("components = %v, want [%s]", pair.Config.Spec.Components, tc.component)
			}
			// Every method is on the form, so a Config can be re-pointed
			// without a new blueprint.
			form := componentNames(pair.Blueprint)
			for _, method := range []string{"claude-login", "claude-subscription", "claude-api-key"} {
				if !slices.Contains(form, method) {
					t.Fatalf("blueprint components %v lack %s", form, method)
				}
			}
			if slices.Contains(form, "codex-login") {
				t.Fatalf("another harness's auth leaked into the claude blueprint: %v", form)
			}
			fill, filled := pair.Config.Spec.Secrets[tc.slot]
			if filled != (tc.fill != nil) || len(pair.Config.Spec.Secrets) > 1 ||
				(filled && !reflect.DeepEqual(fill.SecretRef, tc.fill)) {
				t.Fatalf("secret fills = %+v, want %s -> %+v", pair.Config.Spec.Secrets, tc.slot, tc.fill)
			}
			if got := defaultSlots(result); !slices.Equal(got, tc.defaults) {
				t.Fatalf("default slots = %v, want %v", got, tc.defaults)
			}
			// The other harness is untouched by a choice for this one.
			if other := pairOf(t, result, "codex"); !slices.Equal(
				other.Config.Spec.Components,
				[]string{"codex-login"},
			) {
				t.Fatalf("codex components = %v", other.Config.Spec.Components)
			}
		})
	}
}

// A role written before connectors existed names the secret; that selects the
// method. Two such names are alternatives: the first wins and a note says so.
func TestLegacySecretNamesSelectAMethod(t *testing.T) {
	cfg, opts := fixture(t)
	allHarnesses(cfg)
	source := copiedSource(t)
	opts.Sources["kukeon"] = source
	before := string(source[majordomoRole].Data)
	editRole(source,
		"      settings: config/claude.settings.json\n",
		"      settings: config/claude.settings.json\n      secrets: [anthropic-api-key, claude-code-oauth-token]\n")
	if string(source[majordomoRole].Data) == before {
		t.Fatal("role fixture was not edited")
	}
	result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
	if err != nil {
		t.Fatal(err)
	}
	pair := pairOf(t, result, "claude")
	// Methods are tried in the connector's order, not the role's.
	if !slices.Equal(pair.Config.Spec.Components, []string{"claude-subscription"}) {
		t.Fatalf("components = %v", pair.Config.Spec.Components)
	}
	if !slices.ContainsFunc(result.Notes, func(note string) bool {
		return strings.Contains(note, "subscription and api-key") && strings.Contains(note, "alternatives")
	}) {
		t.Fatalf("no note about the alternative methods: %v", result.Notes)
	}
	c := work(t, pair, interactive)
	if names := secretNames(c); !slices.Equal(names, []string{"CLAUDE_CODE_OAUTH_TOKEN"}) {
		t.Fatalf("cell secrets = %v", names)
	}

	// A team choice overrides what the role's secret names imply.
	cfg.Spec.Defaults.Connectors = map[string]config.ConnectorChoice{"claude": {Method: "login"}}
	if result, err = compose.Compose(cfg, selectedTeam(cfg), opts); err != nil {
		t.Fatal(err)
	}
	if pair = pairOf(t, result, "claude"); !slices.Equal(pair.Config.Spec.Components, []string{"claude-login"}) {
		t.Fatalf("components with a team choice = %v", pair.Config.Spec.Components)
	}
}

// A harness.yaml written before harness auth moved into the catalog still
// declares the slot itself; it keeps it, and the connector's components stay
// out of its blueprint so no slot is declared twice.
func TestHarnessDeclaringItsOwnAuthSlots(t *testing.T) {
	cfg, opts := fixture(t)
	allHarnesses(cfg)
	source := copiedSource(t)
	opts.Sources["kukeon"] = source
	const file = "team/harnesses/claude/harness.yaml"
	before := string(source[file].Data)
	editFile(source, file, "  modes:\n",
		"  secrets:\n    - name: claude-code-oauth-token\n      envName: CLAUDE_CODE_OAUTH_TOKEN\n  modes:\n")
	if string(source[file].Data) == before {
		t.Fatal("harness fixture was not edited")
	}
	editRole(source,
		"      settings: config/claude.settings.json\n",
		"      settings: config/claude.settings.json\n      secrets: [claude-code-oauth-token]\n")
	result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
	if err != nil {
		t.Fatal(err)
	}
	pair := pairOf(t, result, "claude")
	for _, name := range componentNames(pair.Blueprint) {
		if strings.HasPrefix(name, "claude-") {
			t.Fatalf("blueprint of a self-declaring harness carries %s", name)
		}
	}
	if len(pair.Config.Spec.Components) != 0 {
		t.Fatalf("components = %v", pair.Config.Spec.Components)
	}
	if names := secretNames(work(t, pair, interactive)); !slices.Equal(names, []string{"CLAUDE_CODE_OAUTH_TOKEN"}) {
		t.Fatalf("cell secrets = %v", names)
	}
	// The codex blueprint of the same team still gets its connector.
	if other := pairOf(t, result, "codex"); !slices.Equal(other.Config.Spec.Components, []string{"codex-login"}) {
		t.Fatalf("codex components = %v", other.Config.Spec.Components)
	}
}

// The git connector: a role that works on repositories requires a git
// identity, and the one a team names is written on the Config as an
// identityRef — never on the Stack, which other teams share.
func TestGitConnectorOnAConfig(t *testing.T) {
	compose1 := func(t *testing.T, repos bool, choice map[string]config.ConnectorChoice) *compose.Result {
		t.Helper()
		cfg, opts := fixture(t)
		cfg.Spec.Defaults.Connectors = choice
		if repos {
			source := copiedSource(t)
			opts.Sources["kukeon"] = source
			editRole(source, "  needs:", "  needs:\n    repos: [project]")
			cfg.Spec.Defaults.Sources = append(cfg.Spec.Defaults.Sources, config.Source{
				Name: "project", URL: "https://example.test/work.git", ClonePath: "/home/user/work/project", Tag: "v2",
			})
		}
		result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	result := compose1(t, false, nil)
	if cfg := result.Pairs[0].Config; slices.Contains(cfg.Spec.Components, "git") || cfg.Spec.Git != nil {
		t.Fatalf("a role without repositories requires git: %v %+v", cfg.Spec.Components, cfg.Spec.Git)
	}
	if !slices.Contains(componentNames(result.Pairs[0].Blueprint), "git") {
		t.Fatal("the git connector is not on the form")
	}

	result = compose1(t, true, nil)
	if cfg := result.Pairs[0].Config; !slices.Equal(cfg.Spec.Components, []string{"codex-login", "git"}) ||
		cfg.Spec.Git != nil {
		t.Fatalf("role with repositories: components=%v git=%+v", cfg.Spec.Components, cfg.Spec.Git)
	}

	result = compose1(t, true, map[string]config.ConnectorChoice{"git": {Identity: "bot-key@ops"}})
	cfg := result.Pairs[0].Config
	want := &api.ContainerGit{IdentityRef: &api.ContainerSecretRef{Name: "bot-key", Realm: "ops"}}
	if !reflect.DeepEqual(cfg.Spec.Git, want) {
		t.Fatalf("config git = %+v, want %+v", cfg.Spec.Git, want)
	}
	// Task configs of the pair carry the same identity.
	for _, task := range result.Pairs[0].Tasks {
		if !reflect.DeepEqual(task.Spec.Git, want) {
			t.Fatalf("task %s git = %+v", task.Metadata.Name, task.Spec.Git)
		}
	}
}

// TestConnectorIsData adds one connector.yaml kuke has never heard of and
// selects it from a team: it is on the blueprint with its descriptor, on the
// Config, and its secret reaches the cell — with no code naming it.
func TestConnectorIsData(t *testing.T) {
	cfg, opts := fixture(t)
	opts.Connectors = connectortest.FS(t)
	cfg.Spec.Defaults.Roles[0].Connectors = map[string]config.ConnectorChoice{connectortest.Name: {}}
	result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
	if err != nil {
		t.Fatal(err)
	}
	pair := result.Pairs[0]
	component := connectortest.Name + "-token"
	if !slices.Equal(pair.Config.Spec.Components, []string{component, "codex-login"}) &&
		!slices.Equal(pair.Config.Spec.Components, []string{"codex-login", component}) {
		t.Fatalf("components = %v", pair.Config.Spec.Components)
	}
	i := slices.IndexFunc(pair.Blueprint.Spec.Components, func(c api.BlueprintComponent) bool {
		return c.Name == component
	})
	if i < 0 {
		t.Fatalf("blueprint components = %v", componentNames(pair.Blueprint))
	}
	desc := pair.Blueprint.Spec.Components[i].Connector
	if desc == nil || desc.Name != connectortest.Name || desc.Delivery != api.ConnectorDeliveryEnv ||
		len(desc.Fields) != 1 || desc.Fields[0].Name != connectortest.Field || !desc.Fields[0].Required {
		t.Fatalf("descriptor = %+v", desc)
	}
	// Nothing chosen: the slot is left to the Realm default.
	if len(pair.Config.Spec.Secrets) != 0 || !slices.Equal(defaultSlots(result), []string{connectortest.Slot}) {
		t.Fatalf("fills=%+v defaults=%+v", pair.Config.Spec.Secrets, result.DefaultSecrets)
	}
	c := work(t, pair, interactive)
	if names := secretNames(c); !slices.Equal(names, []string{connectortest.EnvName}) {
		t.Fatalf("cell secrets = %v", names)
	}

	// The team names an identity: it becomes the Config's fill.
	cfg.Spec.Defaults.Connectors = map[string]config.ConnectorChoice{connectortest.Name: {Identity: "example-work"}}
	if result, err = compose.Compose(cfg, selectedTeam(cfg), opts); err != nil {
		t.Fatal(err)
	}
	fill := result.Pairs[0].Config.Spec.Secrets[connectortest.Slot]
	if fill.SecretRef == nil || fill.SecretRef.Name != "example-work" || fill.SecretRef.Realm != "default" ||
		len(result.DefaultSecrets) != 0 {
		t.Fatalf("fill=%+v defaults=%+v", fill, result.DefaultSecrets)
	}

	// Without the file the connector does not exist.
	opts.Connectors = nil
	if result, err = compose.Compose(cfg, selectedTeam(cfg), opts); err == nil || result != nil ||
		!strings.Contains(err.Error(), "unknown connector") {
		t.Fatalf("result=%v err=%v", result, err)
	}
}

func TestConnectorChoiceRejections(t *testing.T) {
	for _, tc := range []struct {
		name       string
		team, role map[string]config.ConnectorChoice
		want       string
	}{
		{
			name: "unknown connector",
			team: map[string]config.ConnectorChoice{"nope": {}},
			want: `spec.connectors.nope: unknown connector "nope"`,
		},
		{
			name: "unknown method",
			team: map[string]config.ConnectorChoice{"claude": {Method: "magic"}},
			want: `has no method "magic" (methods: login, subscription, api-key)`,
		},
		{
			name: "ephemeral state with a login",
			team: map[string]config.ConnectorChoice{"codex": {State: "ephemeral"}},
			want: "the login is kept in the state volume and would not survive the cell",
		},
		{
			name: "state on a connector without a harness",
			team: map[string]config.ConnectorChoice{"github": {State: "ephemeral"}},
			want: "authenticates no harness",
		},
		{
			name: "method on a connector without methods",
			team: map[string]config.ConnectorChoice{"git": {Method: "ssh"}},
			want: `connector "git" has no methods`,
		},
		{
			name: "registry on a team",
			team: map[string]config.ConnectorChoice{"registry": {Identity: "registry-ghcr-io"}},
			want: "is set on a Realm",
		},
		{
			name: "role entry",
			role: map[string]config.ConnectorChoice{"claude": {Method: "magic"}},
			want: "roles majordomo: connectors.claude.method",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, opts := fixture(t)
			cfg.Spec.Defaults.Connectors = tc.team
			cfg.Spec.Defaults.Roles[0].Connectors = tc.role
			result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
			if err == nil || result != nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("result=%v err=%v, want %q", result, err, tc.want)
			}
		})
	}
}

// A team's choices for the connector that authenticates a harness decide where
// the harness keeps its state (issue #1685): nothing chosen keeps the team's
// own state volume and persistent state, unchanged; a login identity named for
// the login method replaces the state volume, scope included; `state:
// ephemeral` with a token method is carried to the Config.
func TestTeamHarnessState(t *testing.T) {
	const auth = "claude"
	for _, tc := range []struct {
		name       string
		team, role map[string]config.ConnectorChoice
		volume     [4]string // name, realm, space, stack; empty name: the team's own
		state      string
	}{
		{name: "nothing chosen"},
		{
			name:   "team names a login identity",
			team:   map[string]config.ConnectorChoice{auth: {Identity: "personal-max"}},
			volume: [4]string{"personal-max", "default", "", ""},
		},
		{
			name:   "role names a scoped login identity",
			team:   map[string]config.ConnectorChoice{auth: {Method: "login"}},
			role:   map[string]config.ConnectorChoice{auth: {Identity: "shared-login@auth/teams/core"}},
			volume: [4]string{"shared-login", "auth", "teams", "core"},
		},
		{
			// "Own per role" on the Identities page's Assignments tab (#1790): a
			// role entry that names the method and no identity replaces the
			// team's choice whole, so the role keeps its own state volume.
			name: "role keeps its own state volume under a team login identity",
			team: map[string]config.ConnectorChoice{auth: {Identity: "work-max"}},
			role: map[string]config.ConnectorChoice{auth: {Method: "login"}},
		},
		{
			name:  "ephemeral state with a token method",
			team:  map[string]config.ConnectorChoice{auth: {Method: "api-key", State: "ephemeral"}},
			state: "ephemeral",
		},
		{
			name: "explicit persistent state is the default",
			team: map[string]config.ConnectorChoice{auth: {Method: "api-key", State: "persistent"}},
		},
		{
			name:  "role inherits the team's state",
			team:  map[string]config.ConnectorChoice{auth: {Method: "api-key", State: "ephemeral"}},
			role:  map[string]config.ConnectorChoice{auth: {Identity: "role-key"}},
			state: "ephemeral",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, baseOpts := fixture(t)
			allHarnesses(base)
			base.Spec.Defaults.Harnesses = base.Spec.Defaults.Harnesses[:2] // codex, claude
			plain, err := compose.Compose(base, selectedTeam(base), baseOpts)
			if err != nil {
				t.Fatal(err)
			}
			own := pairOf(t, plain, "claude").Config.Spec.Values

			cfg, opts := fixture(t)
			allHarnesses(cfg)
			cfg.Spec.Defaults.Harnesses = cfg.Spec.Defaults.Harnesses[:2]
			cfg.Spec.Defaults.Connectors = tc.team
			cfg.Spec.Defaults.Roles[0].Connectors = tc.role
			result, err := compose.Compose(cfg, selectedTeam(cfg), opts)
			if err != nil {
				t.Fatal(err)
			}
			pair := pairOf(t, result, "claude")
			want := [4]string{
				own["STATE_VOLUME"], own["STATE_VOLUME_REALM"], own["STATE_VOLUME_SPACE"], own["STATE_VOLUME_STACK"],
			}
			if tc.volume[0] != "" {
				want = tc.volume
			}
			values := pair.Config.Spec.Values
			got := [4]string{
				values["STATE_VOLUME"], values["STATE_VOLUME_REALM"],
				values["STATE_VOLUME_SPACE"], values["STATE_VOLUME_STACK"],
			}
			if got != want {
				t.Fatalf("state volume = %v, want %v", got, want)
			}
			if pair.Config.Spec.State != tc.state {
				t.Fatalf("spec.state = %q, want %q", pair.Config.Spec.State, tc.state)
			}
			// A login identity is never a secret fill.
			if tc.volume[0] != "" && len(pair.Config.Spec.Secrets) != 0 {
				t.Fatalf("secret fills = %+v, want none", pair.Config.Spec.Secrets)
			}
			// The other harness keeps its own state volume.
			other := pairOf(t, result, "codex").Config
			if other.Spec.Values["STATE_VOLUME"] == values["STATE_VOLUME"] || other.Spec.State != "" {
				t.Fatalf("codex state = %q / %q, want its own volume and no state choice",
					other.Spec.Values["STATE_VOLUME"], other.Spec.State)
			}
		})
	}
}

// One subscription identity, set once as the Realm default, authenticates the
// cells of every role — an attached session of one and a dispatched run of
// another — without a login in either role's state volume. The Configs carry
// no fill, so the default stays the single place the token is chosen.
func TestSubscriptionDefaultServesEveryRole(t *testing.T) {
	cfg, opts := fixture(t)
	allHarnesses(cfg)
	cfg.Spec.Defaults.Harnesses = cfg.Spec.Defaults.Harnesses[1:2] // claude
	source := copiedSource(t)
	opts.Sources["kukeon"] = source
	addRole(source, "reviewer")
	cfg.Spec.Defaults.Roles = append(cfg.Spec.Defaults.Roles,
		config.Role{Name: "reviewer", Source: "kukeon", Path: "team/roles/reviewer"})
	cfg.Spec.Defaults.Connectors = map[string]config.ConnectorChoice{"claude": {Method: "subscription"}}
	team := selectedTeam(cfg)
	team.Spec.Harnesses = slices.Clone(cfg.Spec.Defaults.Harnesses)
	result, err := compose.Compose(cfg, team, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Pairs) != 2 || result.Pairs[0].Role == result.Pairs[1].Role {
		t.Fatalf("pairs = %+v", result.Pairs)
	}
	const slot = "claude-code-oauth-token"
	want := []compose.DefaultSecret{{Slot: slot, Reference: config.SecretRef{Name: slot, Realm: "default"}}}
	if !reflect.DeepEqual(result.DefaultSecrets, want) {
		t.Fatalf("default secrets = %+v, want %+v", result.DefaultSecrets, want)
	}
	volumes := map[string]bool{}
	for i, mode := range []string{interactive, headless} {
		pair := result.Pairs[i]
		if !slices.Equal(pair.Config.Spec.Components, []string{"claude-subscription"}) ||
			len(pair.Config.Spec.Secrets) != 0 {
			t.Fatalf("%s: components=%v fills=%+v", pair.Role, pair.Config.Spec.Components, pair.Config.Spec.Secrets)
		}
		c := work(t, pair, mode)
		if len(c.Secrets) != 1 || c.Secrets[0].Name != "CLAUDE_CODE_OAUTH_TOKEN" ||
			c.Secrets[0].SecretRef.Name != slot || c.Secrets[0].SecretRef.Realm != "default" {
			t.Fatalf("%s %s cell secrets = %+v", pair.Role, mode, c.Secrets)
		}
		volume, ok := stateVolume(c)
		if !ok {
			t.Fatalf("%s %s cell has no state volume", pair.Role, mode)
		}
		volumes[volume.VolumeRef.Name] = true
	}
	// The login stays per role: the token, not a shared volume, is what both use.
	if len(volumes) != 2 {
		t.Fatalf("state volumes = %v, want one per role", volumes)
	}
}
