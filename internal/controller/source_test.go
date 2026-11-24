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

package controller_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/apply/parser"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

func sourceDocument(t *testing.T, index int, name, spec string) parser.Document {
	t.Helper()
	return parsedDoc(t, index, "apiVersion: v1beta1\nkind: Source\nmetadata:\n  name: "+name+
		"\n  realm: default\nspec:\n"+spec)
}

func storeRaw(store *teamStoreFake, kind, name, doc string) {
	md := intmodel.TeamMetadata{Name: name, Realm: "default"}
	switch kind {
	case "Team":
		store.teams[teamKey(md)] = intmodel.Team{Metadata: md, Document: []byte(doc)}
	case "TeamDefaults":
		store.defaults[teamKey(md)] = intmodel.TeamDefaults{Metadata: md, Document: []byte(doc)}
	case "Source":
		store.sources[teamKey(md)] = intmodel.Source{Metadata: md, Document: []byte(doc)}
	}
}

const agentsSourceDoc = "apiVersion: v1beta1\nkind: Source\nmetadata:\n  name: agents\n  realm: default\n" +
	"spec:\n  url: github.com/eminwux/agents\n  branch: main\n"

// TestApplySource is #1643 AC1: kuke apply -f stores a Source under its
// realm; applying it again changes nothing.
func TestApplySource(t *testing.T) {
	store := newTeamStoreFake()
	exec := setupTestController(t, teamApplyRunner(store, nil))
	doc := sourceDocument(t, 0, "agents", "  url: github.com/eminwux/agents\n  branch: main\n")
	for i, want := range []string{"created", "unchanged"} {
		res, err := exec.ApplyDocuments([]parser.Document{doc}, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Resources) != 1 || res.Resources[0].Action != want || res.Resources[0].Kind != "Source" {
			t.Fatalf("apply %d = %+v, want %s", i, res.Resources, want)
		}
	}
	rec, found, err := exec.GetSource("agents", "")
	if err != nil || !found {
		t.Fatalf("GetSource = %v, %v", found, err)
	}
	if s := rec.Summary; s.URL != "github.com/eminwux/agents" || s.Ref != "branch main" || s.Identity != "" {
		t.Fatalf("summary = %+v", s)
	}
	all, err := exec.ListSources("")
	if err != nil || len(all) != 1 {
		t.Fatalf("ListSources = %+v, %v", all, err)
	}
}

// TestParseSourceRules is AC1's validation, applied by kukeond to every
// Source document.
func TestParseSourceRules(t *testing.T) {
	for name, tc := range map[string]struct{ meta, spec string }{
		"two refs":    {"name: agents", "  url: github.com/eminwux/agents\n  branch: main\n  tag: v1\n"},
		"no ref":      {"name: agents", "  url: github.com/eminwux/agents\n"},
		"project":     {"name: project", "  url: github.com/eminwux/agents\n  branch: main\n"},
		"bad address": {"name: agents", "  url: eminwux/agents\n  branch: main\n"},
		"bad realm":   {"name: agents\n  realm: Bad", "  url: github.com/eminwux/agents\n  branch: main\n"},
	} {
		raw := "apiVersion: v1beta1\nkind: Source\nmetadata:\n  " + tc.meta + "\n"
		if !strings.Contains(tc.meta, "realm") {
			raw += "  realm: default\n"
		}
		doc, err := parser.ParseDocument(0, []byte(raw+"spec:\n"+tc.spec))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		vErr := parser.ValidateDocument(doc)
		if vErr == nil || !errors.Is(vErr.Err, errdefs.ErrInvalidSource) {
			t.Errorf("%s: validation = %v, want ErrInvalidSource", name, vErr)
		}
	}
	defaults := "apiVersion: v1beta1\nkind: TeamDefaults\nmetadata: {name: default, realm: default}\n" +
		"spec:\n  defaults:\n"
	for _, list := range []string{"    sources: []\n", "    sources:\n      - {name: kukeon, url: github.com/eminwux/kukeon, branch: main}\n"} {
		doc, err := parser.ParseDocument(0, []byte(defaults+list))
		if err != nil {
			t.Fatal(err)
		}
		vErr := parser.ValidateDocument(doc)
		if vErr == nil || !strings.Contains(vErr.Error(), "kuke create source") {
			t.Errorf("TeamDefaults with %q: validation = %v", list, vErr)
		}
	}
	// Default harnesses name a source kukeond does not resolve (D8).
	doc, err := parser.ParseDocument(0, []byte(defaults+
		"    harnesses:\n      - {name: claude, source: kukeon, path: team/harnesses/claude}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if vErr := parser.ValidateDocument(doc); vErr != nil {
		t.Fatalf("defaults naming a stored source: %v", vErr)
	}
	team := "apiVersion: v1beta1\nkind: Team\nmetadata: {name: alpha, realm: default}\n" +
		"spec:\n  sources:\n    - {name: agents, url: github.com/eminwux/agents, branch: main}\n"
	if doc, err = parser.ParseDocument(0, []byte(team)); err != nil {
		t.Fatal(err)
	}
	if vErr := parser.ValidateDocument(doc); vErr == nil || !strings.Contains(vErr.Error(), "spec.sources[0].url") {
		t.Fatalf("team entry with url: validation = %v", vErr)
	}
}

// TestDeleteSource is #1643 AC7: a Source a default role or harness or a
// stored team references is refused, naming them; an unreferenced one goes.
func TestDeleteSource(t *testing.T) {
	store := newTeamStoreFake()
	exec := setupTestController(t, teamApplyRunner(store, nil))
	storeRaw(store, "Source", "agents", agentsSourceDoc)
	storeRaw(store, "Source", "kukeon", strings.ReplaceAll(agentsSourceDoc, "agents", "kukeon"))
	storeRaw(store, "Source", "unused", strings.ReplaceAll(agentsSourceDoc, "agents", "unused"))
	storeRaw(store, "TeamDefaults", "default", "apiVersion: v1beta1\nkind: TeamDefaults\n"+
		"metadata: {name: default, realm: default}\nspec:\n  defaults:\n"+
		"    harnesses:\n      - {name: claude, source: kukeon, path: team/harnesses/claude}\n")
	storeRaw(store, "Team", "kukeon", "apiVersion: v1beta1\nkind: Team\nmetadata: {name: kukeon, realm: default}\n"+
		"spec:\n  projectSource: {name: project, source: kukeon, branch: main}\n"+
		"  sources:\n    - {name: agents, branch: main, clonePath: /home/user/agents}\n")
	storeRaw(store, "Team", "legacy", "apiVersion: v1beta1\nkind: Team\nmetadata: {name: legacy, realm: default}\n"+
		"spec:\n  sources:\n    - {name: unused, url: github.com/elsewhere/unused, branch: main}\n")

	err := exec.DeleteSource("kukeon", "")
	if !errors.Is(err, errdefs.ErrSourceInUse) ||
		!strings.Contains(err.Error(), "default harness claude") || !strings.Contains(err.Error(), "team kukeon") {
		t.Fatalf("delete kukeon: %v", err)
	}
	if err = exec.DeleteSource("agents", ""); !errors.Is(err, errdefs.ErrSourceInUse) ||
		!strings.Contains(err.Error(), "team kukeon") {
		t.Fatalf("delete agents: %v", err)
	}
	// A legacy entry with its own url does not reference the stored Source.
	if err = exec.DeleteSource("unused", ""); err != nil {
		t.Fatalf("delete unused: %v", err)
	}
	if _, ok := store.sources["default/unused"]; ok {
		t.Fatal("unused Source survived")
	}
	if err = exec.DeleteSource("absent", ""); !errors.Is(err, errdefs.ErrSourceNotFound) {
		t.Fatalf("delete absent: %v", err)
	}
	// Deleting a team deletes no Source (AC7) and frees it.
	delete(store.teams, "default/kukeon")
	if err = exec.DeleteSource("agents", ""); err != nil {
		t.Fatalf("delete agents after its team went: %v", err)
	}
}

// TestTeamSummaryReadsTheProjectSource is #1643 Step 4 item 6: a stored
// Team's project address comes from the Source it resolves to; its ref is
// the entry's, else the Source's.
func TestTeamSummaryReadsTheProjectSource(t *testing.T) {
	store := newTeamStoreFake()
	f := teamApplyRunner(store, nil)
	exec := setupTestController(t, f)
	storeRaw(store, "Source", "kukeon", strings.ReplaceAll(agentsSourceDoc, "agents", "kukeon"))
	storeRaw(store, "Team", "kukeon", "apiVersion: v1beta1\nkind: Team\nmetadata: {name: kukeon, realm: default}\n"+
		"spec:\n  projectSource: {name: project, source: kukeon, tag: v2}\n")
	storeRaw(store, "Team", "other", "apiVersion: v1beta1\nkind: Team\nmetadata: {name: other, realm: default}\n"+
		"spec:\n  projectSource: {name: project, source: kukeon}\n")
	teams, err := exec.ListTeams("")
	if err != nil || len(teams) != 2 {
		t.Fatalf("ListTeams = %+v, %v", teams, err)
	}
	for i, want := range []string{"v2", "main"} {
		if s := teams[i].Summary; s.ProjectURL != "github.com/eminwux/kukeon" || s.ProjectRef != want {
			t.Fatalf("team %d summary = %+v, want ref %s", i, s, want)
		}
	}
}
