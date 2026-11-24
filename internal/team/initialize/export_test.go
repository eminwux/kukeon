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
	"strings"
	"testing"

	"github.com/eminwux/kukeon/pkg/team/config"
)

// storedFixtureTeam creates the kukeon fixture's team over the state init
// leaves, with a secret binding and a git identity on the agents Source.
func storedFixtureTeam(t *testing.T) (*fakeClient, Options) {
	t.Helper()
	fx := teamFixtures()[0]
	f := newFake()
	f.storeSource(config.KukeonSourceName, config.KukeonSourceSpec())
	f.storeSource("agents", config.SourceSpec{
		URL: "git@github.com:eminwux/agents", Branch: "main",
		Git: &config.SourceGit{IdentityRef: &config.SecretRef{Name: "git-ssh", Realm: "default"}},
	})
	storeDefaults(t, f, InitialDefaults(config.DefaultRealm))
	opts := Options{Client: f, Sources: fixtureSources(t, fx)}
	if _, err := Add(t.Context(), AddOptions{
		Options: opts, Name: fx.name, Source: "github.com/eminwux/kukeon", Branch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	editTeam(t, f, fx.name, func(team *config.Team) {
		team.Spec.Secrets = []config.SecretBinding{{
			Name: "gh-token", EnvName: "GH_TOKEN", SecretRef: config.SecretRef{Name: "gh", Realm: "default"},
		}}
	})
	return f, opts
}

// TestExport is AC12.
func TestExport(t *testing.T) {
	f, opts := storedFixtureTeam(t)

	raw, err := Export(t.Context(), f, "kukeon", ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	team, err := config.DecodeTeamDeclaration(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("export does not decode: %v\n%s", err, raw)
	}
	if team.Metadata != (config.Metadata{Name: "kukeon"}) || team.Spec.Secrets != nil {
		t.Fatalf("export keeps host fields:\n%s", raw)
	}
	if *team.Spec.ProjectSource != (config.Source{Name: "project", URL: "github.com/eminwux/kukeon"}) {
		t.Fatalf("projectSource = %+v", *team.Spec.ProjectSource)
	}
	want := config.Source{
		Name: "agents", URL: "github.com/eminwux/agents", Branch: "main", ClonePath: "/home/user/agents",
	}
	if len(team.Spec.Sources) != 1 || team.Spec.Sources[0] != want {
		t.Fatalf("sources = %+v, want [%+v]", team.Spec.Sources, want)
	}
	if len(team.Spec.Roles) != 4 || len(team.Spec.Harnesses) != 3 {
		t.Fatalf("declared lists lost:\n%s", raw)
	}
	for _, leak := range []string{"realm", "secrets", "identityRef"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("export has %q:\n%s", leak, raw)
		}
	}

	hosted, err := Export(t.Context(), f, "kukeon", ExportOptions{HostFields: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{
		"realm: default", "source: kukeon", "branch: main", "identityRef:", "name: git-ssh", "secrets:",
	} {
		if !strings.Contains(string(hosted), field) {
			t.Errorf("--host-fields export lacks %q:\n%s", field, hosted)
		}
	}

	// A round trip of the --host-fields export stores the same Team.
	before := f.teams["kukeon"]
	if _, err = Add(t.Context(), AddOptions{
		Options: opts, Name: "kukeon", File: hosted, FileName: "export.yaml", Force: true,
	}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, f.teams["kukeon"]) {
		t.Fatalf("round trip changed the Team:\nbefore:\n%s\nafter:\n%s", before, f.teams["kukeon"])
	}
}

// TestExportExplicitDefaults: the lists a team inherits are written out,
// with a definition for each source they read.
func TestExportExplicitDefaults(t *testing.T) {
	f := newFake()
	f.storeSource(config.KukeonSourceName, config.KukeonSourceSpec())
	defaults := InitialDefaults(config.DefaultRealm)
	defaults.Spec.Defaults.Roles = []config.Role{{Name: "majordomo", Source: "kukeon", Path: "team/roles/majordomo"}}
	storeDefaults(t, f, defaults)
	team := inheritingTeam("app")
	team.Spec.ProjectSource = &config.Source{Name: "project", URL: "github.com/acme/app", Branch: "main"}
	storeTeam(t, f, team)

	plain, err := Export(t.Context(), f, "app", ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), "roles:") {
		t.Fatalf("an inherited list was written:\n%s", plain)
	}
	raw, err := Export(t.Context(), f, "app", ExportOptions{ExplicitDefaults: true})
	if err != nil {
		t.Fatal(err)
	}
	exported, err := config.DecodeTeamDeclaration(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("export does not decode: %v\n%s", err, raw)
	}
	if len(exported.Spec.Roles) != 1 || exported.Spec.Roles[0].Name != "majordomo" ||
		len(exported.Spec.Harnesses) != 2 {
		t.Fatalf("inherited lists not written:\n%s", raw)
	}
	if len(exported.Spec.Sources) != 1 || exported.Spec.Sources[0].URL != "github.com/eminwux/kukeon" ||
		!hasRef(exported.Spec.Sources[0]) {
		t.Fatalf("sources = %+v, want the kukeon definition", exported.Spec.Sources)
	}
	if _, err = Export(t.Context(), f, "absent", ExportOptions{}); err == nil {
		t.Fatal("exported a team that is not stored")
	}
}
