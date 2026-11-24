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
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/team/config"
)

// roleSource is a source tree with one role and one harness, plus
// definitions discovery must skip (D9, G3) or ignore.
func roleSource() fstest.MapFS {
	role := "apiVersion: v1beta1\nkind: Role\nmetadata: {name: %s}\nspec:\n" +
		"  harnesses: {codex: {}, claude: {}}\n  tasks:\n    - {name: nightly}\n"
	return fstest.MapFS{
		"roles/dev/role.yaml":          {Data: []byte(strings.ReplaceAll(role, "%s", "dev"))},
		"harnesses/codex/harness.yaml": {Data: []byte("apiVersion: v1beta1\nkind: Harness\nmetadata: {name: codex}\n")},
		"testdata/role.yaml":           {Data: []byte(strings.ReplaceAll(role, "%s", "fixture"))},
		".hidden/role.yaml":            {Data: []byte(strings.ReplaceAll(role, "%s", "hidden"))},
		"notes/role.yaml":              {Data: []byte("apiVersion: v1beta1\nkind: Other\nmetadata: {name: other}\n")},
		"old/role.yaml": {
			Data: []byte(strings.ReplaceAll(strings.Replace(role, "v1beta1", oldTeamHeader, 1), "%s", "old")),
		},
	}
}

// setupSources is a fake daemon with the add defaults and injected trees for
// the sources the tests store.
func setupSources(t *testing.T) (Options, *fakeClient) {
	t.Helper()
	f := newFake()
	storeDefaults(t, f, addDefaults())
	return Options{Client: f, Sources: map[string]fs.FS{
		"roles-src": roleSource(),
		"harn-src": fstest.MapFS{
			"x/harness.yaml": {Data: []byte("apiVersion: v1beta1\nkind: Harness\nmetadata: {name: x}\n")},
		},
	}}, f
}

// TestCreateSourceDiscoversAndStores is #1643 Step 6: create source reads
// what the source offers and stores a Source document; a stored name needs
// --force, and the replacement names the teams that use it.
func TestCreateSourceDiscoversAndStores(t *testing.T) {
	opts, f := setupSources(t)
	change, err := CreateSource(t.Context(), SourceOptions{
		Options: opts, Name: "roles-src", URL: "https://example.test/roles.git", Branch: "main",
		Identity: &config.SecretRef{Name: "git-ssh"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if change.Action != "created" || change.Users != nil {
		t.Fatalf("change = %+v", change)
	}
	got := change.Discovered
	if len(got.Roles) != 1 || got.Roles[0].Name != "dev" || got.Roles[0].Path != "roles/dev" ||
		!slices.Equal(got.Roles[0].Harnesses, []string{"claude", "codex"}) ||
		!slices.Equal(got.Roles[0].Tasks, []string{"nightly"}) || got.Roles[0].Default {
		t.Fatalf("discovered roles = %+v", got.Roles)
	}
	if len(got.Harnesses) != 1 || got.Harnesses[0] != (SourceItem{Name: "codex", Path: "harnesses/codex"}) {
		t.Fatalf("discovered harnesses = %+v", got.Harnesses)
	}
	spec := storedSource(t, f, "roles-src")
	if spec.URL != "https://example.test/roles.git" || spec.Branch != "main" || spec.Git == nil ||
		*spec.Git.IdentityRef != (config.SecretRef{Name: "git-ssh", Realm: "default"}) {
		t.Fatalf("stored = %+v", spec)
	}
	if !slices.Equal(f.writes, []string{"apply"}) {
		t.Fatalf("writes = %v, want one plain apply of the Source", f.writes)
	}
	if defaults := storedDefaults(t, f); defaults.Spec.Defaults.Sources != nil {
		t.Fatal("create source wrote the TeamDefaults' sources")
	}

	// A stored name is refused without --force.
	again := SourceOptions{Options: opts, Name: "roles-src", URL: "https://example.test/roles.git", Tag: "v2"}
	if _, err = CreateSource(t.Context(), again); !errors.Is(err, errdefs.ErrSourceExists) ||
		!strings.Contains(err.Error(), "--force") {
		t.Fatalf("err = %v", err)
	}
	user := inheritingTeam("user")
	user.Spec.Roles = []config.Role{{Name: "dev", Source: "roles-src", Path: "roles/dev"}}
	storeTeam(t, f, user)
	again.Force = true
	if change, err = CreateSource(t.Context(), again); err != nil || change.Action != "updated" {
		t.Fatalf("change=%+v err=%v", change, err)
	}
	if want := []SourceUse{{Team: "user", How: UseRole, Name: "dev"}}; !slices.Equal(change.Users, want) {
		t.Fatalf("users = %+v, want %+v", change.Users, want)
	}
	if spec = storedSource(t, f, "roles-src"); spec.Tag != "v2" || spec.Branch != "" || spec.Git != nil {
		t.Fatalf("replaced = %+v", spec)
	}
}

func TestCreateSourceRejectsBadInput(t *testing.T) {
	for _, tc := range []struct{ name, source, url, want string }{
		{"reserved", "project", "https://example.test/r.git", "project is reserved"},
		{"invalid name", "Bad_Name", "https://example.test/r.git", "invalid source name"},
		{"credential url", "creds", "https://user:unsafe-canary@example.test/r.git", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts, f := setupSources(t)
			_, err := CreateSource(t.Context(), SourceOptions{
				Options: opts, Name: tc.source, URL: tc.url, Branch: "main",
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "unsafe-canary") {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if len(f.writes) != 0 {
				t.Fatalf("writes = %v", f.writes)
			}
		})
	}
}

// TestShowSourceMarksDefaults: show lists what a stored Source offers and
// marks the ones the defaults select. It writes nothing.
func TestShowSourceMarksDefaults(t *testing.T) {
	opts, f := setupSources(t)
	f.storeSource("roles-src", config.SourceSpec{URL: "https://example.test/roles.git", Branch: "main"})
	changeAddConfig(t, AddOptions{Options: opts}, func(cfg *config.TeamDefaults) {
		cfg.Spec.Defaults.Roles = append(cfg.Spec.Defaults.Roles,
			config.Role{Name: "dev", Source: "roles-src", Path: "roles/dev/"})
		cfg.Spec.Defaults.Harnesses = append(cfg.Spec.Defaults.Harnesses,
			config.Harness{Name: "codex2", Source: "roles-src", Path: "harnesses/codex"})
	})
	contents, err := ShowSource(t.Context(), opts, "roles-src")
	if err != nil {
		t.Fatal(err)
	}
	if contents.Source.Name != "roles-src" || contents.Source.URL != "https://example.test/roles.git" ||
		len(contents.Roles) != 1 || !contents.Roles[0].Default ||
		len(contents.Harnesses) != 1 || !contents.Harnesses[0].Default {
		t.Fatalf("contents = %+v", contents)
	}
	if len(f.writes) != 0 {
		t.Fatalf("writes = %v", f.writes)
	}
	if _, err = ShowSource(t.Context(), opts, "absent"); !errors.Is(err, errdefs.ErrSourceNotFound) ||
		!strings.Contains(err.Error(), "kuke create source absent") {
		t.Fatalf("err = %v", err)
	}
}

// TestDeleteSource: the delete is kukeond's, which refuses a referenced
// Source (AC7, tested in internal/controller); a missing one is not found.
func TestDeleteSource(t *testing.T) {
	opts, f := setupSources(t)
	f.storeSource("roles-src", config.SourceSpec{URL: "https://example.test/roles.git", Branch: "main"})
	f.deleteSourceErr = fmt.Errorf("%w: source roles-src is used by team user", errdefs.ErrSourceInUse)
	if err := DeleteSource(t.Context(), opts, "roles-src"); !errors.Is(err, errdefs.ErrSourceInUse) {
		t.Fatalf("err = %v", err)
	}
	f.deleteSourceErr = nil
	if err := DeleteSource(t.Context(), opts, "roles-src"); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.sources["roles-src"]; ok {
		t.Fatal("the Source is still stored")
	}
	if err := DeleteSource(t.Context(), opts, "roles-src"); !errors.Is(err, errdefs.ErrSourceNotFound) {
		t.Fatalf("err = %v", err)
	}
}

// TestSourceUsers lists, per Source, the teams and defaults that use it.
func TestSourceUsers(t *testing.T) {
	opts, f := setupSources(t)
	team := inheritingTeam("app")
	team.Spec.ProjectSource = &config.Source{Name: "project", Stored: "app-repo"}
	team.Spec.Sources = []config.Source{{Name: "roles", Stored: "roles-src"}}
	team.Spec.Roles = []config.Role{{Name: "dev", Source: "roles", Path: "roles/dev"}}
	storeTeam(t, f, team)
	storeTeam(t, f, inheritingTeam("inherits"))
	users, err := SourceUsers(t.Context(), opts.Client)
	if err != nil {
		t.Fatal(err)
	}
	for source, want := range map[string][]SourceUse{
		"app-repo":  {{Team: "app", How: UseProject, Name: "project"}},
		"roles-src": {{Team: "app", How: UseDeclared, Name: "roles"}, {Team: "app", How: UseRole, Name: "dev"}},
		"kukeon": {
			{How: UseDefaultRole, Name: "majordomo"},
			{How: UseDefaultHarness, Name: "codex"},
			{Team: "app", How: UseHarness, Name: "codex"},
			{Team: "inherits", How: UseRole, Name: "majordomo"},
			{Team: "inherits", How: UseHarness, Name: "codex"},
		},
	} {
		if !slices.Equal(users[source], want) {
			t.Errorf("users[%s] = %+v, want %+v", source, users[source], want)
		}
	}
}

// TestSetDefaults is AC14: defaults set replaces the default role or harness
// list and reports the teams that inherit from the defaults.
func TestSetDefaults(t *testing.T) {
	opts, f := setupSources(t)
	f.storeSource("roles-src", config.SourceSpec{URL: "https://example.test/roles.git", Branch: "main"})
	storeTeam(t, f, inheritingTeam("inherits"))
	before := storedDefaults(t, f).Spec.Defaults.Harnesses
	change, err := SetDefaults(t.Context(), opts, []string{"majordomo", "dev=roles-src:roles/dev"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	d := storedDefaults(t, f).Spec.Defaults
	if change.Action != "updated" || !slices.Equal(change.Inheriting, []string{"inherits"}) ||
		!slices.Equal(roleNames(d.Roles), []string{"majordomo", "dev"}) || d.Roles[1].Source != "roles-src" ||
		!slices.Equal(d.Harnesses, before) {
		t.Fatalf("change=%+v defaults=%+v", change, d)
	}
	// An explicit empty list selects none.
	if _, err = SetDefaults(t.Context(), opts, []string{}, nil); err != nil {
		t.Fatal(err)
	}
	if d = storedDefaults(t, f).Spec.Defaults; d.Roles == nil || len(d.Roles) != 0 {
		t.Fatalf("roles = %#v, want []", d.Roles)
	}
	writes := len(f.writes)
	for _, bad := range [][]string{{"ghost"}, {"x=unregistered:roles/x"}} {
		if _, err = SetDefaults(t.Context(), opts, nil, bad); err == nil {
			t.Fatalf("accepted %v", bad)
		}
	}
	if _, err = SetDefaults(t.Context(), opts, nil, nil); err == nil ||
		!strings.Contains(err.Error(), "--role or --harness") {
		t.Fatalf("err = %v", err)
	}
	if len(f.writes) != writes {
		t.Fatal("a refused defaults set wrote")
	}
}

// TestApplyDefaults is AC14: a whole TeamDefaults document is stored, and the
// change reports the inheriting teams.
func TestApplyDefaults(t *testing.T) {
	f := newFake()
	opts := Options{Client: f}
	storeTeam(t, f, inheritingTeam("inherits"))
	doc := "apiVersion: v1beta1\nkind: TeamDefaults\nspec:\n  defaults:\n    harnesses:\n" +
		"      - {name: codex, source: kukeon, path: team/harnesses/codex}\n" +
		"  parameters: {REGION: eu}\n"
	change, err := ApplyDefaults(t.Context(), opts, []byte(doc))
	if err != nil || change.Action != "created" || !slices.Equal(change.Inheriting, []string{"inherits"}) {
		t.Fatalf("change=%+v err=%v", change, err)
	}
	stored := storedDefaults(t, f)
	if stored.Metadata != (config.Metadata{Name: "default", Realm: "default"}) ||
		stored.Spec.Parameters["REGION"] != "eu" {
		t.Fatalf("stored = %+v", stored)
	}
	if change, err = ApplyDefaults(t.Context(), opts, []byte(doc)); err != nil || change.Action != "updated" {
		t.Fatalf("change=%+v err=%v", change, err)
	}
	writes := len(f.writes)
	for _, bad := range []string{
		strings.Replace(doc, "kind: TeamDefaults\n", "kind: TeamDefaults\nmetadata: {realm: other}\n", 1),
		strings.Replace(doc, "parameters:", "unknown:", 1),
		strings.Replace(doc, "v1beta1", oldTeamHeader, 1),
		// AC2: a TeamDefaults lists no sources.
		strings.Replace(doc, "    harnesses:\n", "    sources: [{name: kukeon, url: github.com/eminwux/kukeon, branch: main}]\n"+
			"    harnesses:\n", 1),
	} {
		if _, err = ApplyDefaults(t.Context(), opts, []byte(bad)); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
	if len(f.writes) != writes {
		t.Fatal("a refused document was applied")
	}
}

// TestDefaultsSetConvertsLegacySources is AC13: a TeamDefaults written before
// #1643 keeps reading, and the first write converts it: its sources become
// Sources in the same apply, the list is cleared and a note says so.
func TestDefaultsSetConvertsLegacySources(t *testing.T) {
	f := newFake()
	opts := Options{Client: f}
	legacy := config.EmptyDefaults(config.DefaultRealm)
	legacy.Spec.Defaults = config.Defaults{
		Sources: []config.Source{
			config.KukeonSource(),
			{Name: "agents", URL: "git@github.com:eminwux/agents", Branch: "main", ClonePath: "/opt/agents"},
		},
		Roles:     []config.Role{{Name: "dev", Source: "agents", Path: "dev"}},
		Harnesses: config.MajordomoHarnesses(),
	}
	storeLegacyDefaults(t, f, legacy)
	change, err := SetDefaults(t.Context(), opts, nil, []string{"codex"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.writes, []string{"apply"}) {
		t.Fatalf("writes = %v, want one plain apply", f.writes)
	}
	if got := storedDefaults(t, f).Spec.Defaults; got.Sources != nil || len(got.Harnesses) != 1 ||
		len(got.Roles) != 1 {
		t.Fatalf("defaults = %+v", got)
	}
	if got := storedSource(t, f, "agents"); got.URL != "git@github.com:eminwux/agents" || got.Branch != "main" {
		t.Fatalf("agents Source = %+v", got)
	}
	if got := storedSource(t, f, config.KukeonSourceName); got.URL != config.KukeonSourceURL {
		t.Fatalf("kukeon Source = %+v", got)
	}
	if !hasNote(change.Notes, "converted 2 registered sources to Source documents") ||
		!hasNote(change.Notes, "/opt/agents") || !hasNote(change.Notes, "spec.sources[].clonePath") {
		t.Fatalf("notes = %v", change.Notes)
	}
}
