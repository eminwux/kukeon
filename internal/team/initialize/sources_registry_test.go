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
	"io/fs"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

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
// the sources the tests register.
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

func registered(t *testing.T, f *fakeClient, name string) (config.Source, bool) {
	t.Helper()
	for _, s := range storedDefaults(t, f).Spec.Defaults.Sources {
		if s.Name == name {
			return s, true
		}
	}
	return config.Source{}, false
}

// TestAddSourceDiscoversAndRegisters is AC13: source add discovers what the
// source offers, registers it in the TeamDefaults (created, then updated),
// and gives a role source a clone path.
func TestAddSourceDiscoversAndRegisters(t *testing.T) {
	opts, f := setupSources(t)
	storeTeam(t, f, inheritingTeam("inherits"))
	owns := inheritingTeam("owns")
	owns.Spec.Sources, owns.Spec.Roles = []config.Source{}, []config.Role{}
	// An explicit empty connectors map does not survive the stored document
	// (omitempty), so the team states one choice to own the list.
	owns.Spec.Harnesses, owns.Spec.Connectors = []config.Harness{}, map[string]config.ConnectorChoice{"git": {}}
	storeTeam(t, f, owns)

	change, err := AddSource(t.Context(), SourceOptions{
		Options: opts, Name: "roles-src", URL: "https://example.test/roles.git", Branch: "main",
	})
	if err != nil {
		t.Fatal(err)
	}
	if change.Action != "created" || !slices.Equal(change.Inheriting, []string{"inherits"}) {
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
	source, ok := registered(t, f, "roles-src")
	if !ok || source.ClonePath != "/home/user/roles-src" || source.Branch != "main" || got.Source != source {
		t.Fatalf("registered = %+v", source)
	}
	if !slices.Equal(f.writes, []string{"apply"}) {
		t.Fatalf("writes = %v, want one plain apply of the TeamDefaults", f.writes)
	}

	// A source without roles is cloned nowhere; an explicit path is kept.
	if _, err = AddSource(t.Context(), SourceOptions{
		Options: opts, Name: "harn-src", URL: "https://example.test/h.git", Branch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	if source, _ = registered(t, f, "harn-src"); source.ClonePath != "" {
		t.Fatalf("harness-only source cloned at %q", source.ClonePath)
	}

	// The same name again updates it in place.
	change, err = AddSource(t.Context(), SourceOptions{
		Options: opts, Name: "roles-src", URL: "https://example.test/roles.git", Tag: "v2", ClonePath: "/opt/roles",
	})
	if err != nil || change.Action != "updated" {
		t.Fatalf("change=%+v err=%v", change, err)
	}
	count := 0
	for _, s := range storedDefaults(t, f).Spec.Defaults.Sources {
		if s.Name == "roles-src" {
			count++
			source = s
		}
	}
	if count != 1 || source.Tag != "v2" || source.Branch != "" || source.ClonePath != "/opt/roles" {
		t.Fatalf("count=%d source=%+v", count, source)
	}
}

func TestAddSourceRejectsBadInput(t *testing.T) {
	for _, tc := range []struct{ name, source, url, clonePath, want string }{
		{"reserved", "project", "https://example.test/r.git", "", "project is reserved"},
		{"invalid name", "Bad_Name", "https://example.test/r.git", "", "invalid source name"},
		{"credential url", "creds", "https://user:unsafe-canary@example.test/r.git", "", ""},
		{"relative clone path", "rel", "https://example.test/r.git", "relative/path", "--clone-path"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts, f := setupSources(t)
			_, err := AddSource(t.Context(), SourceOptions{
				Options: opts, Name: tc.source, URL: tc.url, Branch: "main", ClonePath: tc.clonePath,
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

// TestShowSourceMarksDefaults is AC13: show lists what a registered source
// offers and marks the ones the defaults select. It writes nothing.
func TestShowSourceMarksDefaults(t *testing.T) {
	opts, f := setupSources(t)
	changeAddConfig(t, AddOptions{Options: opts}, func(cfg *config.TeamDefaults) {
		cfg.Spec.Defaults.Sources = append(
			cfg.Spec.Defaults.Sources,
			config.Source{
				Name:      "roles-src",
				URL:       "https://example.test/roles.git",
				Branch:    "main",
				ClonePath: "/home/user/roles-src",
			},
		)
		cfg.Spec.Defaults.Roles = append(cfg.Spec.Defaults.Roles,
			config.Role{Name: "dev", Source: "roles-src", Path: "roles/dev/"})
		cfg.Spec.Defaults.Harnesses = append(cfg.Spec.Defaults.Harnesses,
			config.Harness{Name: "codex2", Source: "roles-src", Path: "harnesses/codex"})
	})
	contents, err := ShowSource(t.Context(), opts, "roles-src")
	if err != nil {
		t.Fatal(err)
	}
	if contents.Source.Name != "roles-src" || len(contents.Roles) != 1 || !contents.Roles[0].Default ||
		len(contents.Harnesses) != 1 || !contents.Harnesses[0].Default {
		t.Fatalf("contents = %+v", contents)
	}
	if len(f.writes) != 0 {
		t.Fatalf("writes = %v", f.writes)
	}
	if _, err = ShowSource(t.Context(), opts, "absent"); err == nil ||
		!strings.Contains(err.Error(), "kuke team source add absent") {
		t.Fatalf("err = %v", err)
	}
}

// TestRemoveSourceRefusedWhileReferenced is AC13: remove is refused while a
// default or a stored Team that inherits the sources references the source,
// and the error names each; nothing is fetched.
func TestRemoveSourceRefusedWhileReferenced(t *testing.T) {
	opts, f := setupSources(t)
	opts.Sources = nil // Nothing may be fetched.
	changeAddConfig(t, AddOptions{Options: opts}, func(cfg *config.TeamDefaults) {
		cfg.Spec.Defaults.Sources = append(cfg.Spec.Defaults.Sources,
			config.Source{Name: "roles-src", URL: "https://example.test/roles.git", Branch: "main"})
		cfg.Spec.Defaults.Roles = append(cfg.Spec.Defaults.Roles,
			config.Role{Name: "dev", Source: "roles-src", Path: "roles/dev"})
	})
	user := inheritingTeam("user")
	user.Spec.Roles = []config.Role{{Name: "dev", Source: "roles-src", Path: "roles/dev"}}
	storeTeam(t, f, user)
	own := inheritingTeam("own")
	own.Spec.Sources = []config.Source{{Name: "roles-src", URL: "https://example.test/fork.git", Branch: "main"}}
	own.Spec.Roles = []config.Role{{Name: "dev", Source: "roles-src", Path: "roles/dev"}}
	storeTeam(t, f, own)
	storeTeam(t, f, inheritingTeam("inherits"))

	_, err := RemoveSource(t.Context(), opts, "roles-src")
	if err == nil || !strings.Contains(err.Error(), "default role dev") ||
		!strings.Contains(err.Error(), "team inherits") ||
		!strings.Contains(err.Error(), "team user") ||
		strings.Contains(err.Error(), "team own") {
		t.Fatalf("err = %v", err)
	}
	if len(f.writes) != 0 {
		t.Fatalf("writes = %v", f.writes)
	}
	changeAddConfig(t, AddOptions{Options: opts}, func(cfg *config.TeamDefaults) {
		cfg.Spec.Defaults.Roles = cfg.Spec.Defaults.Roles[:1]
	})
	delete(f.teams, "user")
	change, err := RemoveSource(t.Context(), opts, "roles-src")
	if err != nil || change.Action != "removed" || !slices.Equal(change.Inheriting, []string{"inherits", "own"}) {
		t.Fatalf("change=%+v err=%v", change, err)
	}
	if _, ok := registered(t, f, "roles-src"); ok {
		t.Fatal("source still registered")
	}
	if _, err = RemoveSource(t.Context(), opts, "roles-src"); err == nil ||
		!strings.Contains(err.Error(), "not registered") {
		t.Fatalf("err = %v", err)
	}
}

// TestSetDefaults is AC14: defaults set replaces the default role or harness
// list and reports the teams that inherit from the defaults.
func TestSetDefaults(t *testing.T) {
	opts, f := setupSources(t)
	changeAddConfig(t, AddOptions{Options: opts}, func(cfg *config.TeamDefaults) {
		cfg.Spec.Defaults.Sources = append(cfg.Spec.Defaults.Sources,
			config.Source{Name: "roles-src", URL: "https://example.test/roles.git", Branch: "main"})
	})
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
	doc := "apiVersion: v1beta1\nkind: TeamDefaults\nspec:\n  defaults:\n    sources:\n" +
		"      - {name: kukeon, url: github.com/eminwux/kukeon, branch: main}\n" +
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
	} {
		if _, err = ApplyDefaults(t.Context(), opts, []byte(bad)); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
	if len(f.writes) != writes {
		t.Fatal("a refused document was applied")
	}
}
