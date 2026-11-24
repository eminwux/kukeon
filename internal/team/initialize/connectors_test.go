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

package initialize

import (
	"bytes"
	"errors"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/team/teamtest"
	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/config"
	"github.com/eminwux/kukeon/pkg/team/connector/connectortest"
	"gopkg.in/yaml.v3"
)

// roleTeam is a team named demo that declares one role, majordomo, with
// needs.
func roleTeam(needs *config.Needs) *config.Team {
	return &config.Team{
		APIVersion: config.APIVersion, Kind: config.KindTeam,
		Metadata: config.Metadata{Name: "demo", Realm: config.DefaultRealm},
		Spec: config.TeamSpec{Defaults: config.Defaults{Roles: []config.Role{{
			Name: "majordomo", Source: "kukeon", Path: "team/roles/majordomo", Needs: needs,
		}}}},
	}
}

func marshalTeam(t *testing.T, team *config.Team) string {
	t.Helper()
	raw, err := yaml.Marshal(team)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func ptr(s string) *string { return &s }

func TestEnableRoleConnector(t *testing.T) {
	catalog := connectortest.Catalog(t)
	t.Run("no entry", func(t *testing.T) {
		team := roleTeam(nil)
		if err := EnableRoleConnector(team, catalog, "majordomo", "github", nil); err != nil {
			t.Fatal(err)
		}
		if got := marshalTeam(t, team); !strings.Contains(got, "connectors:") || !strings.Contains(got, "github: {}") {
			t.Fatalf("want connectors: { github: {} }:\n%s", got)
		}
	})
	t.Run("identity kept by a later enable", func(t *testing.T) {
		team := roleTeam(nil)
		if err := EnableRoleConnector(team, catalog, "majordomo", "github", ptr("bot-token@default")); err != nil {
			t.Fatal(err)
		}
		if err := EnableRoleConnector(team, catalog, "majordomo", "github", nil); err != nil {
			t.Fatal(err)
		}
		if got := team.Spec.Roles[0].Connectors["github"].Identity; got != "bot-token@default" {
			t.Fatalf("identity = %q", got)
		}
	})
	t.Run("empty identity removes it", func(t *testing.T) {
		team := roleTeam(nil)
		team.Spec.Roles[0].Connectors = map[string]config.ConnectorChoice{"github": {Identity: "bot-token"}}
		if err := EnableRoleConnector(team, catalog, "majordomo", "github", ptr("")); err != nil {
			t.Fatal(err)
		}
		if got := marshalTeam(t, team); !strings.Contains(got, "github: {}") {
			t.Fatalf("want an empty entry:\n%s", got)
		}
	})
	t.Run("twice changes nothing", func(t *testing.T) {
		team := roleTeam(nil)
		if err := EnableRoleConnector(team, catalog, "majordomo", "github", nil); err != nil {
			t.Fatal(err)
		}
		before := marshalTeam(t, team)
		if err := EnableRoleConnector(team, catalog, "majordomo", "github", nil); err != nil {
			t.Fatal(err)
		}
		if after := marshalTeam(t, team); after != before {
			t.Fatalf("second enable changed the team:\n%s\n---\n%s", before, after)
		}
	})
	t.Run("legacy spelling already enables it", func(t *testing.T) {
		team := roleTeam(&config.Needs{Secrets: []string{"gh-token"}})
		before := marshalTeam(t, team)
		if err := EnableRoleConnector(team, catalog, "majordomo", "github", nil); err != nil {
			t.Fatal(err)
		}
		if after := marshalTeam(t, team); after != before {
			t.Fatalf("enable rewrote a legacy opt-in:\n%s", after)
		}
	})
	t.Run("invalid identity", func(t *testing.T) {
		team := roleTeam(nil)
		err := EnableRoleConnector(team, catalog, "majordomo", "github", ptr("Bad Name"))
		if !errors.Is(err, errdefs.ErrInvalidTeam) || team.Spec.Roles[0].Connectors != nil {
			t.Fatalf("err = %v, connectors = %v", err, team.Spec.Roles[0].Connectors)
		}
	})
	t.Run("a connector no code names", func(t *testing.T) {
		team := roleTeam(nil)
		if err := EnableRoleConnector(team, catalog, "majordomo", connectortest.Name, nil); err != nil {
			t.Fatal(err)
		}
		if _, ok := team.Spec.Roles[0].Connectors[connectortest.Name]; !ok {
			t.Fatal("the test connector is not togglable")
		}
	})
}

func TestDisableRoleConnector(t *testing.T) {
	catalog := connectortest.Catalog(t)
	for name, tc := range map[string]struct {
		needs      *config.Needs
		connectors map[string]config.ConnectorChoice
		want       string
		absent     []string
	}{
		"keeps image needs": {
			needs: &config.Needs{Image: []string{"go"}, Secrets: []string{"gh-token"}},
			want:  "- go", absent: []string{"secrets", "gh-token"},
		},
		"omits an empty needs": {
			needs: &config.Needs{Secrets: []string{"gh-token"}}, absent: []string{"needs", "gh-token"},
		},
		"both spellings": {
			needs:      &config.Needs{Secrets: []string{"github-token", "other"}},
			connectors: map[string]config.ConnectorChoice{"github": {}},
			want:       "- other", absent: []string{"connectors", "github"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			team := roleTeam(tc.needs)
			team.Spec.Roles[0].Connectors = tc.connectors
			if err := DisableRoleConnector(team, catalog, "majordomo", "github"); err != nil {
				t.Fatal(err)
			}
			got := marshalTeam(t, team)
			if tc.want != "" && !strings.Contains(got, tc.want) {
				t.Errorf("want %q in:\n%s", tc.want, got)
			}
			for _, gone := range tc.absent {
				if strings.Contains(got, gone) {
					t.Errorf("%q is still in:\n%s", gone, got)
				}
			}
		})
	}
	t.Run("does not alias the caller's needs", func(t *testing.T) {
		needs := &config.Needs{Image: []string{"go"}, Secrets: []string{"gh-token", "x"}}
		team := roleTeam(needs)
		if err := DisableRoleConnector(team, catalog, "majordomo", "github"); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(needs.Secrets, []string{"gh-token", "x"}) {
			t.Fatalf("the original needs changed: %v", needs.Secrets)
		}
	})
}

func TestRoleConnectorRefusals(t *testing.T) {
	catalog := connectortest.Catalog(t)
	for name, tc := range map[string]struct {
		team      *config.Team
		role      string
		connector string
		want      []string
	}{
		"claude": {
			team: roleTeam(nil), role: "majordomo", connector: "claude",
			want: []string{"claude harness", "spec.connectors", "Edit team form"},
		},
		"registry": {
			team: roleTeam(nil), role: "majordomo", connector: "registry",
			want: []string{"registry credential", "spec.connectors"},
		},
		"unknown": {
			team: roleTeam(nil), role: "majordomo", connector: "gitlab",
			want: []string{`unknown connector "gitlab"`, "github", "git,"},
		},
		"role": {
			team: roleTeam(nil), role: "dev", connector: "github",
			want: []string{`no role "dev"`, "majordomo"},
		},
		"inherited roles": {
			team: inheritingTeam("demo"), role: "majordomo", connector: "github",
			want: []string{
				"inherits its roles", "kuke team export demo --host-fields --explicit-defaults",
				"kuke team apply -f",
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			before := marshalTeam(t, tc.team)
			for verb, change := range map[string]func() error{
				"enable": func() error {
					return EnableRoleConnector(tc.team, catalog, tc.role, tc.connector, nil)
				},
				"disable": func() error { return DisableRoleConnector(tc.team, catalog, tc.role, tc.connector) },
			} {
				err := change()
				if !errors.Is(err, errdefs.ErrInvalidTeam) {
					t.Fatalf("%s: err = %v", verb, err)
				}
				for _, want := range tc.want {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("%s: %q not in %v", verb, want, err)
					}
				}
			}
			if after := marshalTeam(t, tc.team); after != before {
				t.Fatalf("a refusal changed the team:\n%s", after)
			}
		})
	}
}

// componentsOf lists the components the last apply sent for the Config
// named name.
func componentsOf(t *testing.T, raw []byte, name string) []string {
	t.Helper()
	for _, node := range decodeStream(raw) {
		var doc api.CellConfigDoc
		if kind, docName := docHeader(&node); kind != string(api.KindCellConfig) || docName != name {
			continue
		}
		if err := node.Decode(&doc); err != nil {
			t.Fatal(err)
		}
		return doc.Spec.Components
	}
	t.Fatalf("config %s is not in the apply:\n%s", name, raw)
	return nil
}

func enableGitHub() ConnectorChange {
	return ConnectorChange{Team: "kuke", Connector: "github", Roles: []string{"majordomo"}, Enable: true}
}

func TestSetRoleConnector(t *testing.T) {
	configs := []string{"kuke-majordomo-claude", "kuke-majordomo-claude-health-sweep"}
	t.Run("enable", func(t *testing.T) {
		opts, fake := setupReapply(t)
		out, err := SetRoleConnector(t.Context(), opts.Options, enableGitHub())
		if err != nil {
			t.Fatal(err)
		}
		if out.Result == nil || out.Plan.Command != "kuke team connector enable kuke github --role majordomo" {
			t.Fatalf("outcome = %+v", out)
		}
		if _, ok := storedTeam(t, fake, "kuke").Spec.Roles[0].Connectors["github"]; !ok {
			t.Fatal("the stored team has no github entry")
		}
		for _, name := range configs {
			if !slices.Contains(componentsOf(t, fake.raw, name), "github") {
				t.Errorf("%s does not render github", name)
			}
		}
	})
	t.Run("dry run", func(t *testing.T) {
		opts, fake := setupReapply(t)
		storedBefore, applies := slices.Clone(fake.teams["kuke"]), fake.applies
		opts.DryRun = true
		out, err := SetRoleConnector(t.Context(), opts.Options, enableGitHub())
		if err != nil {
			t.Fatal(err)
		}
		if out.Result != nil || fake.applies != applies || !bytes.Equal(storedBefore, fake.teams["kuke"]) {
			t.Fatal("a dry run applied")
		}
		for _, name := range configs {
			if !slices.Contains(out.Plan.Rerender, name) {
				t.Errorf("rerender = %v, want %s", out.Plan.Rerender, name)
			}
		}
	})
	t.Run("disable after enable", func(t *testing.T) {
		opts, fake := setupReapply(t)
		if _, err := SetRoleConnector(t.Context(), opts.Options, enableGitHub()); err != nil {
			t.Fatal(err)
		}
		change := enableGitHub()
		change.Enable = false
		if _, err := SetRoleConnector(t.Context(), opts.Options, change); err != nil {
			t.Fatal(err)
		}
		for _, name := range configs {
			if slices.Contains(componentsOf(t, fake.raw, name), "github") {
				t.Errorf("%s still renders github", name)
			}
		}
		if storedTeam(t, fake, "kuke").Spec.Roles[0].Connectors != nil {
			t.Fatal("the stored team kept the entry")
		}
	})
	t.Run("disable a connector the role definition lists", func(t *testing.T) {
		opts, fake := setupReapply(t)
		if _, err := SetRoleConnector(t.Context(), opts.Options, enableGitHub()); err != nil {
			t.Fatal(err)
		}
		opts.Sources = map[string]fs.FS{"kukeon": roleListing(t, "github")}
		storedBefore, applies := slices.Clone(fake.teams["kuke"]), fake.applies
		change := enableGitHub()
		change.Enable = false
		_, err := SetRoleConnector(t.Context(), opts.Options, change)
		if err == nil || !strings.Contains(err.Error(), "role's definition") ||
			!strings.Contains(err.Error(), "kuke-majordomo-claude") {
			t.Fatalf("err = %v", err)
		}
		if fake.applies != applies || !bytes.Equal(storedBefore, fake.teams["kuke"]) {
			t.Fatal("a contradicted disable wrote something")
		}
	})
	t.Run("not stored", func(t *testing.T) {
		opts, fake := setupInit(t)
		change := enableGitHub()
		change.Team = "other"
		_, err := SetRoleConnector(t.Context(), opts.Options, change)
		if !errors.Is(err, errdefs.ErrTeamNotFound) || len(fake.writes) != 0 {
			t.Fatalf("err = %v, writes = %v", err, fake.writes)
		}
	})
	t.Run("refusal writes nothing", func(t *testing.T) {
		opts, fake := setupReapply(t)
		applies := fake.applies
		change := enableGitHub()
		change.Connector = "claude"
		if _, err := SetRoleConnector(t.Context(), opts.Options, change); !errors.Is(err, errdefs.ErrInvalidTeam) ||
			fake.applies != applies {
			t.Fatalf("err = %v, applies = %d", err, fake.applies-applies)
		}
	})
}

// roleListing is the repository's definitions with the majordomo role
// listing connectors in its own role.yaml.
func roleListing(t *testing.T, connectors ...string) fs.FS {
	t.Helper()
	plain, err := teamtest.PlainSource("../../..")
	if err != nil {
		t.Fatal(err)
	}
	source := fstest.MapFS{}
	for name, file := range plain {
		source[name] = file
	}
	raw := string(plain[teamtest.MajordomoRole].Data)
	edited := strings.Replace(raw, "spec:\n", "spec:\n  connectors: ["+strings.Join(connectors, ", ")+"]\n", 1)
	if edited == raw {
		t.Fatalf("role edit did not apply:\n%s", raw)
	}
	source[teamtest.MajordomoRole] = &fstest.MapFile{Data: []byte(edited)}
	return source
}

func TestRoleConnectors(t *testing.T) {
	fake := newFake()
	team := roleTeam(&config.Needs{Secrets: []string{"gh-token"}})
	team.Metadata.Name = "kuke"
	storeLegacyTeam(t, fake, team)
	labels := func(task string) map[string]string {
		out := map[string]string{api.LabelTeam: "kuke", api.LabelRole: "majordomo"}
		if task != "" {
			out[api.LabelTask] = task
		}
		return out
	}
	config := func(name, task string, statuses ...api.ConnectorStatus) api.CellConfigDoc {
		return api.CellConfigDoc{
			Metadata: api.CellConfigMetadata{Name: name, Realm: "default", Labels: labels(task)},
			Status:   &api.CellConfigStatus{Connectors: statuses},
		}
	}
	login := api.ConnectorStatus{Connector: "claude", Method: "login", State: api.ConnectorResolved}
	fake.configs = []api.CellConfigDoc{
		config("kuke-majordomo-claude-health-sweep", "health-sweep", login, api.ConnectorStatus{
			Connector: "github", Method: "token", State: api.ConnectorUnresolved, Required: true,
		}),
		config("kuke-majordomo-claude", "", api.ConnectorStatus{
			Connector: "github", Method: "token", State: api.ConnectorResolved, Origin: "realm",
			Identity: &api.ContainerSecretRef{Name: "gh-token", Realm: "default"},
		}, login),
		{
			Metadata: api.CellConfigMetadata{Name: "other", Labels: map[string]string{api.LabelTeam: "other"}},
			Status:   &api.CellConfigStatus{Connectors: []api.ConnectorStatus{login}},
		},
	}
	rows, err := RoleConnectors(t.Context(), fake, "kuke")
	if err != nil {
		t.Fatal(err)
	}
	want := []RoleConnector{
		{
			Role: "majordomo", Config: "kuke-majordomo-claude", Connector: "claude", Method: "login",
			State: api.ConnectorResolved, Fill: "-",
		},
		{
			Role: "majordomo", Config: "kuke-majordomo-claude", Connector: "github", Method: "token",
			State: api.ConnectorResolved, Identity: "gh-token", Origin: "realm", Fill: "gh-token (realm)",
			SetByTeam: true,
		},
		{
			Role: "majordomo", Config: "kuke-majordomo-claude-health-sweep", Task: "health-sweep",
			Connector: "claude", Method: "login", State: api.ConnectorResolved, Fill: "-",
		},
		{
			Role: "majordomo", Config: "kuke-majordomo-claude-health-sweep", Task: "health-sweep",
			Connector: "github", Method: "token", State: api.ConnectorUnresolved, Required: true,
			Fill: "MISSING", SetByTeam: true,
		},
	}
	if !slices.Equal(rows, want) {
		t.Fatalf("rows:\n%+v\nwant:\n%+v", rows, want)
	}
	if _, err = RoleConnectors(t.Context(), fake, "absent"); !errors.Is(err, errdefs.ErrTeamNotFound) {
		t.Fatalf("absent team: %v", err)
	}
}

func TestIdentityText(t *testing.T) {
	for want, ref := range map[string]api.ContainerSecretRef{
		"a":                   {Name: "a", Realm: "default"},
		"a@other":             {Name: "a", Realm: "other"},
		"a@default/team/kuke": {Name: "a", Realm: "default", Space: "team", Stack: "kuke"},
	} {
		if got := identityText(ref, ""); got != want {
			t.Errorf("identityText(%+v) = %q, want %q", ref, got, want)
		}
	}
}
