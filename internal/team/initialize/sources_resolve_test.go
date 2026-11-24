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
	"reflect"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/pkg/team/config"
	"gopkg.in/yaml.v3"
)

const agentsURL = "git@github.com:eminwux/agents"

// resolveTeam is a team named name that runs role dev from source alias.
func resolveTeam(name, alias string, sources ...config.Source) *config.Team {
	team := inheritingTeam(name)
	team.Spec.Sources = sources
	team.Spec.Roles = []config.Role{{Name: "dev", Source: alias, Path: "dev"}}
	team.Spec.Harnesses = []config.Harness{{Name: "codex", Source: "kukeon", Path: "team/harnesses/codex"}}
	return team
}

func storedKukeon() map[string]config.SourceSpec {
	return map[string]config.SourceSpec{config.KukeonSourceName: config.KukeonSourceSpec()}
}

func findSource(t *testing.T, sources []config.Source, name string) config.Source {
	t.Helper()
	for _, source := range sources {
		if source.Name == name {
			return source
		}
	}
	t.Fatalf("no source %s in %+v", name, sources)
	return config.Source{}
}

// TestNormalize is the definition half of #1643 Step 4's table.
//
//nolint:gocyclo,cyclop // one subtest per row of the table
func TestNormalize(t *testing.T) {
	definition := config.Source{Name: "agents", URL: agentsURL, Branch: "main", ClonePath: "/home/user/agents"}

	t.Run("a definition with no Source stores one", func(t *testing.T) {
		team, created, notes, err := normalize(resolveTeam("app", "agents", definition), storedKukeon())
		if err != nil {
			t.Fatal(err)
		}
		if len(created) != 1 || created[0].name != "agents" ||
			!reflect.DeepEqual(created[0].spec, config.SourceSpec{URL: agentsURL, Branch: "main"}) {
			t.Fatalf("created = %+v", created)
		}
		want := config.Source{Name: "agents", Branch: "main", ClonePath: "/home/user/agents"}
		if team.Spec.Sources[0] != want {
			t.Fatalf("stored entry = %+v, want %+v", team.Spec.Sources[0], want)
		}
		if len(notes) != 1 || !strings.Contains(notes[0], "stores Source agents (github.com/eminwux/agents") {
			t.Fatalf("notes = %v", notes)
		}
	})

	t.Run("the same repository over another transport is a reference", func(t *testing.T) {
		stored := storedKukeon()
		stored["agents"] = config.SourceSpec{URL: "https://github.com/eminwux/agents.git", Tag: "v1"}
		team, created, _, err := normalize(resolveTeam("app", "agents", definition), stored)
		if err != nil || len(created) != 0 {
			t.Fatalf("created = %+v, err = %v", created, err)
		}
		if team.Spec.Sources[0].URL != "" || team.Spec.Sources[0].Branch != "main" {
			t.Fatalf("stored entry = %+v", team.Spec.Sources[0])
		}
	})

	t.Run("another repository is AC6's error", func(t *testing.T) {
		stored := storedKukeon()
		stored["agents"] = config.SourceSpec{URL: "github.com/someone/agents", Branch: "main"}
		_, _, _, err := normalize(resolveTeam("app", "agents", definition), stored)
		if err == nil || !strings.Contains(err.Error(), agentsURL) ||
			!strings.Contains(
				err.Error(),
				"github.com/someone/agents",
			) || !strings.Contains(err.Error(), "source: NAME") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("source: stores it under another name", func(t *testing.T) {
		fork := definition
		fork.Stored = "agents-fork"
		stored := storedKukeon()
		stored["agents"] = config.SourceSpec{URL: "github.com/someone/agents", Branch: "main"}
		team, created, _, err := normalize(resolveTeam("app", "agents", fork), stored)
		if err != nil {
			t.Fatal(err)
		}
		if len(created) != 1 || created[0].name != "agents-fork" {
			t.Fatalf("created = %+v", created)
		}
		if entry := team.Spec.Sources[0]; entry.Name != "agents" || entry.Stored != "agents-fork" ||
			team.Spec.Roles[0].Source != "agents" {
			t.Fatalf("stored team = %+v", team.Spec)
		}
	})

	t.Run("project and entry of one repository are one Source", func(t *testing.T) {
		team := resolveTeam("agents", "agents", definition)
		team.Spec.ProjectSource = &config.Source{
			Name: "project", URL: "github.com/eminwux/agents", Branch: "main", ClonePath: "/home/user/agents0",
		}
		normalized, created, _, err := normalize(team, storedKukeon())
		if err != nil {
			t.Fatal(err)
		}
		if len(created) != 1 || created[0].name != "agents" {
			t.Fatalf("created = %+v", created)
		}
		project := normalized.Spec.ProjectSource
		if project.Stored != "agents" || project.URL != "" || project.ClonePath != "/home/user/agents0" {
			t.Fatalf("project = %+v", project)
		}
		_, hydrated, err := hydrate(config.EmptyDefaults(config.DefaultRealm), team, storedKukeon())
		if err != nil {
			t.Fatal(err)
		}
		if got := findSource(t, hydrated.Spec.Sources, "agents"); got.ClonePath != "/home/user/agents" {
			t.Fatalf("agents = %+v", got)
		}
		if hydrated.Spec.ProjectSource.ClonePath != "/home/user/agents0" {
			t.Fatalf("project = %+v", hydrated.Spec.ProjectSource)
		}
	})

	t.Run("git without an address belongs to the Source", func(t *testing.T) {
		entry := config.Source{Name: "agents", Git: &config.SourceGit{IdentityRef: &config.SecretRef{Name: "x"}}}
		if _, _, _, err := normalize(resolveTeam("app", "agents", entry), storedKukeon()); err == nil ||
			!strings.Contains(err.Error(), "kuke create source agents") {
			t.Fatalf("err = %v", err)
		}
	})
}

// TestHydrate is the resolution half of #1643 Step 4's table.
func TestHydrate(t *testing.T) {
	defaults := InitialDefaults(config.DefaultRealm)

	t.Run("the ref is the entry's, else the Source's", func(t *testing.T) {
		stored := storedKukeon()
		stored["agents"] = config.SourceSpec{URL: agentsURL, Tag: "v1"}
		stored["pinned"] = config.SourceSpec{URL: "github.com/eminwux/pinned", Commit: strings.Repeat("c", 40)}
		team := resolveTeam("app", "agents", config.Source{Name: "agents"},
			config.Source{Name: "pinned", Branch: "main"})
		_, hydrated, err := hydrate(defaults, team, stored)
		if err != nil {
			t.Fatal(err)
		}
		if got := findSource(t, hydrated.Spec.Sources, "agents"); got.Tag != "v1" || got.URL != agentsURL {
			t.Fatalf("agents = %+v", got)
		}
		if got := findSource(t, hydrated.Spec.Sources, "pinned"); got.Branch != "main" || got.Commit != "" {
			t.Fatalf("pinned = %+v", got)
		}
	})

	t.Run("a role reads a stored Source the team does not list", func(t *testing.T) {
		stored := storedKukeon()
		stored["agents"] = config.SourceSpec{URL: agentsURL, Branch: "main"}
		_, hydrated, err := hydrate(defaults, resolveTeam("app", "agents"), stored)
		if err != nil {
			t.Fatal(err)
		}
		if got := findSource(t, hydrated.Spec.Sources, "agents"); got.ClonePath != "/home/user/agents" {
			t.Fatalf("agents = %+v, want it cloned at /home/user/agents", got)
		}
		if got := findSource(t, hydrated.Spec.Sources, "kukeon"); got.ClonePath != "" {
			t.Fatalf("kukeon = %+v; only a role's source is cloned by default", got)
		}
	})

	t.Run("an unknown source names the role and kuke create source", func(t *testing.T) {
		_, _, err := hydrate(defaults, resolveTeam("app", "agents"), storedKukeon())
		if err == nil || !strings.Contains(err.Error(), "role dev") ||
			!strings.Contains(err.Error(), "kuke create source agents") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("the project resolves to the Source of the team's name", func(t *testing.T) {
		stored := storedKukeon()
		stored["app"] = config.SourceSpec{URL: "github.com/acme/app", Branch: "trunk"}
		team := resolveTeam("app", "project")
		team.Spec.ProjectSource = &config.Source{Name: "project"}
		_, hydrated, err := hydrate(defaults, team, stored)
		if err != nil {
			t.Fatal(err)
		}
		want := config.Source{Name: "project", URL: "github.com/acme/app", Branch: "trunk", ClonePath: "/home/user/app"}
		if *hydrated.Spec.ProjectSource != want {
			t.Fatalf("project = %+v, want %+v", *hydrated.Spec.ProjectSource, want)
		}
		delete(stored, "app")
		if _, _, err = hydrate(defaults, team, stored); err == nil || !strings.Contains(err.Error(), "--source") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("the stored documents are not changed", func(t *testing.T) {
		stored := storedKukeon()
		stored["agents"] = config.SourceSpec{URL: agentsURL, Branch: "main"}
		team := resolveTeam("app", "agents", config.Source{Name: "agents"})
		team.Spec.ProjectSource = &config.Source{Name: "project", Stored: "kukeon"}
		before, err := yaml.Marshal(team)
		if err != nil {
			t.Fatal(err)
		}
		defaultsBefore, err := yaml.Marshal(defaults)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err = hydrate(defaults, team, stored); err != nil {
			t.Fatal(err)
		}
		after, _ := yaml.Marshal(team)
		defaultsAfter, _ := yaml.Marshal(defaults)
		if string(before) != string(after) || string(defaultsBefore) != string(defaultsAfter) {
			t.Fatal("hydrate changed its input")
		}
	})

	t.Run("a legacy defaults list reads as stored Sources", func(t *testing.T) {
		legacy := InitialDefaults(config.DefaultRealm)
		legacy.Spec.Defaults.Sources = []config.Source{
			config.KukeonSource(), {Name: "agents", URL: agentsURL, Branch: "main", ClonePath: "/srv/agents"},
		}
		legacy.Spec.Defaults.Roles = []config.Role{{Name: "dev", Source: "agents", Path: "dev"}}
		hydratedDefaults, hydrated, err := hydrate(legacy, inheritingTeam("app"), nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := findSource(t, hydrated.Spec.Sources, "agents"); got.ClonePath != "/srv/agents" ||
			got.URL != agentsURL {
			t.Fatalf("agents = %+v; an inheriting team keeps the legacy clone path", got)
		}
		if err = config.Validate(hydratedDefaults); err != nil {
			t.Fatalf("hydrated defaults do not validate: %v", err)
		}
	})
}
