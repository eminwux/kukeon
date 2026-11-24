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
	"strings"
	"testing"
	"testing/fstest"

	"github.com/eminwux/kukeon/pkg/team/config"
)

const draftRepoFile = `apiVersion: v1beta1
kind: Team
metadata: {name: acme}
spec:
  sources:
    - {name: tools, url: https://example.test/tools.git, branch: main}
    - {name: shared, url: https://example.test/other.git}
    - {name: kept}
  roles:
    - {name: dev, source: project, path: roles/dev}
`

// draftOptions inject a project tree carrying draftRepoFile and a role.
func draftOptions(t *testing.T) (AddOptions, *fakeClient) {
	t.Helper()
	opts := setupAddRun(t)
	f := opts.Client.(*fakeClient)
	f.storeSource("shared", config.SourceSpec{URL: "https://example.test/shared.git", Branch: "main"})
	opts.Sources["project"] = fstest.MapFS{
		"kuketeam.yaml": {Data: []byte(draftRepoFile)},
		"roles/dev/role.yaml": {Data: []byte("apiVersion: v1beta1\nkind: Role\nmetadata: {name: dev}\n" +
			"spec:\n  tasks:\n    - {name: pick, schedule: '@hourly'}\n")},
	}
	return AddOptions{Options: opts, Source: "https://example.test/acme.git", Branch: "main"}, f
}

// TestDraftTeamReadsTheRepositoryFile: a draft reads the project's
// kuketeam.yaml, names the team after it, reports each source's state and
// what the project offers, and writes nothing.
func TestDraftTeamReadsTheRepositoryFile(t *testing.T) {
	opts, f := draftOptions(t)
	draft, err := DraftTeam(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.writes) != 0 {
		t.Fatalf("a draft wrote: %v", f.writes)
	}
	if draft.Name != "acme" || draft.Origin.Kind != OriginRepository || draft.Team.Metadata.Name != "acme" {
		t.Fatalf("draft = %+v", draft)
	}
	project := draft.Team.Spec.ProjectSource
	if project == nil || project.URL != opts.Source || project.Branch != "main" ||
		project.ClonePath != "/home/user/acme" {
		t.Fatalf("project = %+v", project)
	}
	states := map[string]string{}
	for _, source := range draft.Sources {
		states[source.Alias] = source.State
	}
	want := map[string]string{"project": DraftNew, "tools": DraftNew, "shared": DraftConflict, "kept": DraftMissing}
	for alias, state := range want {
		if states[alias] != state {
			t.Fatalf("states = %v, want %v", states, want)
		}
	}
	if draft.Sources[0].Alias != "project" || draft.Sources[0].Contents == nil ||
		len(draft.Sources[0].Contents.Roles) != 1 || draft.Sources[0].Contents.Roles[0].Tasks[0].Schedule != "@hourly" {
		t.Fatalf("project contents = %+v", draft.Sources[0].Contents)
	}
	for _, source := range draft.Sources {
		if source.Alias == "shared" && source.Have != "https://example.test/shared.git" {
			t.Fatalf("conflict does not name the stored address: %+v", source)
		}
		if source.Alias == "tools" && source.Ref != "branch main" {
			t.Fatalf("tools ref = %q", source.Ref)
		}
	}
}

// TestDraftTeamWithoutAFile: --no-repo-file and a given name inherit the
// defaults; a file given instead is the declaration; a stored team is noted.
func TestDraftTeamWithoutAFile(t *testing.T) {
	opts, f := draftOptions(t)
	opts.NoRepoFile, opts.Name = true, "web"
	draft, err := DraftTeam(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Name != "web" || draft.Origin.Kind != OriginDefaults || draft.Team.Spec.Roles != nil {
		t.Fatalf("draft = %+v", draft)
	}
	storeTeam(t, f, inheritingTeam("web"))
	opts.NoRepoFile, opts.Source, opts.Branch = false, "", ""
	opts.File = []byte("apiVersion: v1beta1\nkind: Team\nmetadata: {name: web}\nspec:\n" +
		"  projectSource: {url: 'https://example.test/acme.git', branch: trunk}\n  roles: []\n")
	opts.FileName = "pasted"
	draft, err = DraftTeam(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Origin.Kind != OriginFile || draft.Team.Spec.Roles == nil ||
		draft.Team.Spec.ProjectSource.Branch != "trunk" {
		t.Fatalf("file draft = %+v", draft)
	}
	if !hasNote(draft.Notes, "team web is stored") {
		t.Fatalf("notes = %v", draft.Notes)
	}
	opts.File = []byte("apiVersion: v1beta1\nkind: Team\nmetadata: {name: web}\n")
	if _, err = DraftTeam(t.Context(), opts); err == nil || !strings.Contains(err.Error(), "spec.projectSource.url") {
		t.Fatalf("err = %v", err)
	}
}

// TestDraftTeamFromAStoredSource: the project named by a stored Source is a
// reference to it, read at its ref.
func TestDraftTeamFromAStoredSource(t *testing.T) {
	opts, f := draftOptions(t)
	f.storeSource("app", config.SourceSpec{
		URL: "https://example.test/acme.git", Branch: "trunk",
		Git: &config.SourceGit{IdentityRef: &config.SecretRef{Name: "git-ssh", Realm: "default"}},
	})
	opts.Source, opts.Branch, opts.NoRepoFile = "app", "", true
	draft, err := DraftTeam(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	got := draft.Sources[0]
	if draft.Name != "app" || got.State != DraftStored || got.Stored != "app" || got.Ref != "branch trunk" ||
		got.Identity != "git-ssh" || draft.Team.Spec.ProjectSource.Stored != "app" {
		t.Fatalf("draft = %+v, project = %+v", draft, got)
	}
}

// TestInspectSource is Step 1: an unstored source is fetched and walked like
// a stored one, and nothing is stored or marked a default.
func TestInspectSource(t *testing.T) {
	opts, f := setupSources(t)
	opts.Sources["roles"] = roleSource()
	contents, err := InspectSource(t.Context(), opts, config.SourceSpec{
		URL: "https://example.test/roles.git", Branch: "main",
		Git: &config.SourceGit{IdentityRef: &config.SecretRef{Name: "git-ssh"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.writes) != 0 || len(contents.Roles) != 1 || contents.Roles[0].Default ||
		contents.Source.Name != "roles" || contents.Source.Git.IdentityRef.Realm != "default" {
		t.Fatalf("contents = %+v, writes = %v", contents, f.writes)
	}
	if _, err = InspectSource(t.Context(), opts, config.SourceSpec{URL: "not an address"}); err == nil {
		t.Fatal("inspect accepted a bad address")
	}
	if _, err = InspectSource(t.Context(), opts, config.SourceSpec{
		URL: "https://example.test/roles.git", Branch: "a", Tag: "b",
	}); err == nil {
		t.Fatal("inspect accepted two refs")
	}
}
