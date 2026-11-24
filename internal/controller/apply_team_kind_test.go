// Copyright 2025 Emiliano Spinella (eminwux)
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

// teamStoreFake is kukeond's Team and TeamDefaults store in memory, wired
// into a fakeRunner, recording the order of every write and delete.
type teamStoreFake struct {
	teams    map[string]intmodel.Team // "realm/name"
	defaults map[string]intmodel.TeamDefaults
	sources  map[string]intmodel.Source
	events   []string
}

func newTeamStoreFake() *teamStoreFake {
	return &teamStoreFake{
		teams: map[string]intmodel.Team{}, defaults: map[string]intmodel.TeamDefaults{},
		sources: map[string]intmodel.Source{},
	}
}

func teamKey(md intmodel.TeamMetadata) string { return md.Realm + "/" + md.Name }

func (s *teamStoreFake) install(f *fakeRunner) {
	if f.GetRealmFn == nil {
		f.GetRealmFn = func(r intmodel.Realm) (intmodel.Realm, error) { return r, nil }
	}
	f.WriteTeamFn = func(team intmodel.Team) (bool, error) {
		_, exists := s.teams[teamKey(team.Metadata)]
		s.teams[teamKey(team.Metadata)] = team
		s.events = append(s.events, "write Team/"+team.Metadata.Name)
		return !exists, nil
	}
	f.GetTeamFn = func(team intmodel.Team) (intmodel.Team, error) {
		got, ok := s.teams[teamKey(team.Metadata)]
		if !ok {
			return intmodel.Team{}, errdefs.ErrTeamNotFound
		}
		return got, nil
	}
	f.ListTeamsFn = func(realm string) ([]intmodel.Team, error) {
		var out []intmodel.Team
		for _, team := range s.teams {
			if realm == "" || team.Metadata.Realm == realm {
				out = append(out, team)
			}
		}
		return out, nil
	}
	f.DeleteTeamFn = func(team intmodel.Team) error {
		if _, ok := s.teams[teamKey(team.Metadata)]; !ok {
			return errdefs.ErrTeamNotFound
		}
		delete(s.teams, teamKey(team.Metadata))
		s.events = append(s.events, "delete Team/"+team.Metadata.Name)
		return nil
	}
	f.WriteTeamDefaultsFn = func(d intmodel.TeamDefaults) (bool, error) {
		_, exists := s.defaults[teamKey(d.Metadata)]
		s.defaults[teamKey(d.Metadata)] = d
		s.events = append(s.events, "write TeamDefaults/"+d.Metadata.Name)
		return !exists, nil
	}
	f.GetTeamDefaultsFn = func(d intmodel.TeamDefaults) (intmodel.TeamDefaults, error) {
		got, ok := s.defaults[teamKey(d.Metadata)]
		if !ok {
			return intmodel.TeamDefaults{}, errdefs.ErrTeamDefaultsNotFound
		}
		return got, nil
	}
	f.WriteSourceFn = func(source intmodel.Source) (bool, error) {
		_, exists := s.sources[teamKey(source.Metadata)]
		s.sources[teamKey(source.Metadata)] = source
		s.events = append(s.events, "write Source/"+source.Metadata.Name)
		return !exists, nil
	}
	f.GetSourceFn = func(source intmodel.Source) (intmodel.Source, error) {
		got, ok := s.sources[teamKey(source.Metadata)]
		if !ok {
			return intmodel.Source{}, errdefs.ErrSourceNotFound
		}
		return got, nil
	}
	f.ListSourcesFn = func(realm string) ([]intmodel.Source, error) {
		var out []intmodel.Source
		for _, source := range s.sources {
			if realm == "" || source.Metadata.Realm == realm {
				out = append(out, source)
			}
		}
		return out, nil
	}
	f.DeleteSourceFn = func(source intmodel.Source) error {
		if _, ok := s.sources[teamKey(source.Metadata)]; !ok {
			return errdefs.ErrSourceNotFound
		}
		delete(s.sources, teamKey(source.Metadata))
		s.events = append(s.events, "delete Source/"+source.Metadata.Name)
		return nil
	}
}

// teamApplyRunner is a fakeRunner for a team apply of one blueprint: the
// store above plus empty Blueprint/Config listings.
func teamApplyRunner(store *teamStoreFake, writeBlueprintErr error) *fakeRunner {
	f := &fakeRunner{
		ListBlueprintsFn: func(_, _, _ string) ([]intmodel.CellBlueprint, error) { return nil, nil },
		ListConfigsFn:    func(_, _, _ string) ([]intmodel.CellConfig, error) { return nil, nil },
		WriteBlueprintFn: func(bp intmodel.CellBlueprint) (bool, error) {
			if writeBlueprintErr != nil {
				return false, writeBlueprintErr
			}
			store.events = append(store.events, "write CellBlueprint/"+bp.Metadata.Name)
			return true, nil
		},
	}
	store.install(f)
	return f
}

func parsedDoc(t *testing.T, index int, raw string) parser.Document {
	t.Helper()
	doc, err := parser.ParseDocument(index, []byte(raw))
	if err != nil {
		t.Fatalf("ParseDocument: %v", err)
	}
	if vErr := parser.ValidateDocument(doc); vErr != nil {
		t.Fatalf("ValidateDocument: %v", vErr)
	}
	return *doc
}

func teamDocument(t *testing.T, index int, name, spec string) parser.Document {
	t.Helper()
	raw := "apiVersion: v1beta1\nkind: Team\nmetadata:\n  name: " + name + "\n  realm: default\n"
	if spec != "" {
		raw += "spec:\n" + spec
	}
	return parsedDoc(t, index, raw)
}

// TestApplyDocuments_TeamRejectedOutsideTeamApply pins AC4: a Team sent on
// its own (a plain kuke apply -f path) is refused, names the command that
// stores one, and writes nothing.
func TestApplyDocuments_TeamRejectedOutsideTeamApply(t *testing.T) {
	store := newTeamStoreFake()
	exec := setupTestController(t, teamApplyRunner(store, nil))

	docs := []parser.Document{blueprintDoc(0, "alpha-bp", "default"), teamDocument(t, 1, "alpha", "")}
	_, err := exec.ApplyDocuments(docs, "")
	if !errors.Is(err, errdefs.ErrTeamApplyOnly) {
		t.Fatalf("err = %v, want ErrTeamApplyOnly", err)
	}
	if !strings.Contains(err.Error(), "kuke apply -f") {
		t.Errorf("error %q does not name kuke apply -f", err)
	}
	if len(store.events) != 0 || len(store.teams) != 0 {
		t.Fatalf("writes happened: %v", store.events)
	}
}

func TestApplyDocuments_TeamApplyRejections(t *testing.T) {
	tests := []struct {
		name   string
		team   string
		docs   func(t *testing.T) []parser.Document
		stored []intmodel.Team
		want   error
		text   string
	}{
		{
			name: "name differs from the team apply",
			team: "alpha",
			docs: func(t *testing.T) []parser.Document {
				return []parser.Document{teamDocument(t, 0, "beta", "")}
			},
			want: errdefs.ErrTeamApplyOnly,
		},
		{
			name: "two Team documents",
			team: "alpha",
			docs: func(t *testing.T) []parser.Document {
				return []parser.Document{
					teamDocument(t, 0, "alpha", ""),
					teamDocument(t, 1, "alpha", ""),
				}
			},
			text: "one Team document",
		},
		{
			name: "name stored in another realm",
			team: "alpha",
			docs: func(t *testing.T) []parser.Document {
				return []parser.Document{
					blueprintDoc(0, "alpha-bp", "default"),
					teamDocument(t, 1, "alpha", ""),
				}
			},
			stored: []intmodel.Team{{Metadata: intmodel.TeamMetadata{Name: "alpha", Realm: "other"}}},
			want:   errdefs.ErrTeamNameInUse,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newTeamStoreFake()
			for _, team := range tt.stored {
				store.teams[teamKey(team.Metadata)] = team
			}
			exec := setupTestController(t, teamApplyRunner(store, nil))
			_, err := exec.ApplyDocuments(tt.docs(t), tt.team)
			if err == nil {
				t.Fatal("team apply accepted")
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if tt.text != "" && !strings.Contains(err.Error(), tt.text) {
				t.Fatalf("err = %v, want it to mention %q", err, tt.text)
			}
			if len(store.events) != 0 {
				t.Fatalf("writes happened before the rejection: %v", store.events)
			}
		})
	}
}

// TestApplyDocuments_TeamStoredLast pins D6: the Team is written after every
// other document and the prune, and reported as the last result.
func TestApplyDocuments_TeamStoredLast(t *testing.T) {
	store := newTeamStoreFake()
	f := teamApplyRunner(store, nil)
	// One stale Config of the team, which the prune removes before the Team
	// is written.
	stale := []intmodel.CellConfig{labeledConfig("alpha-old", "default", "alpha")}
	f.ListConfigsFn = func(_, _, _ string) ([]intmodel.CellConfig, error) { return stale, nil }
	f.GetConfigFn = func(cfg intmodel.CellConfig) (intmodel.CellConfig, error) {
		return storedConfigCarrier(t, cfg.Metadata.Name, "alpha", "alpha-old-bp"), nil
	}
	f.DeleteConfigFn = func(cfg intmodel.CellConfig) error {
		store.events = append(store.events, "delete CellConfig/"+cfg.Metadata.Name)
		stale = nil
		return nil
	}
	exec := setupTestController(t, f)

	docs := []parser.Document{
		teamDocument(t, 0, "alpha", "  roles: []\n"),
		blueprintDoc(1, "alpha-bp", "default"),
	}
	result, err := exec.ApplyDocuments(docs, "alpha")
	if err != nil {
		t.Fatalf("ApplyDocuments error = %v", err)
	}
	want := []string{"write CellBlueprint/alpha-bp", "delete CellConfig/alpha-old", "write Team/alpha"}
	if !equalStringSlices(store.events, want) {
		t.Fatalf("events = %v, want %v", store.events, want)
	}
	last := result.Resources[len(result.Resources)-1]
	if last.Kind != "Team" || last.Name != "alpha" || last.Action != "created" || last.Error != nil {
		t.Fatalf("last result = %+v, want Team/alpha created", last)
	}

	// The stored document keeps an authored [] and leaves omitted lists out.
	stored := string(store.teams["default/alpha"].Document)
	if !strings.Contains(stored, "roles: []") || strings.Contains(stored, "harnesses") {
		t.Fatalf("stored Team lost list presence:\n%s", stored)
	}

	// The same apply again leaves the Team unchanged.
	store.events = nil
	result, err = exec.ApplyDocuments(docs, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if last = result.Resources[len(result.Resources)-1]; last.Action != "unchanged" {
		t.Fatalf("reapply Team action = %q, want unchanged", last.Action)
	}
}

// TestApplyDocuments_TeamNotStoredWhenADocumentFails pins AC4: a Team is
// never stored without its Configs.
func TestApplyDocuments_TeamNotStoredWhenADocumentFails(t *testing.T) {
	store := newTeamStoreFake()
	exec := setupTestController(t, teamApplyRunner(store, errors.New("disk full")))

	docs := []parser.Document{
		blueprintDoc(0, "alpha-bp", "default"),
		teamDocument(t, 1, "alpha", ""),
	}
	result, err := exec.ApplyDocuments(docs, "alpha")
	if err != nil {
		t.Fatalf("ApplyDocuments error = %v", err)
	}
	if len(store.teams) != 0 {
		t.Fatalf("Team stored although a document failed: %v", store.events)
	}
	last := result.Resources[len(result.Resources)-1]
	if last.Kind != "Team" || last.Action != "failed" || last.Error == nil ||
		!strings.Contains(last.Error.Error(), "team not stored") {
		t.Fatalf("last result = %+v, want a failed Team", last)
	}
}

// TestApplyDocuments_TeamDefaults: a TeamDefaults is an ordinary document of
// a plain apply, created, then unchanged, then updated, and keeps omitted
// apart from [].
func TestApplyDocuments_TeamDefaults(t *testing.T) {
	store := newTeamStoreFake()
	exec := setupTestController(t, teamApplyRunner(store, nil))
	const head = "apiVersion: v1beta1\nkind: TeamDefaults\nmetadata:\n  name: default\n  realm: default\nspec:\n"
	first := head + "  defaults:\n    roles: []\n"
	second := head + "  defaults:\n    roles: []\n    harnesses: []\n"

	for i, step := range []struct{ raw, action string }{
		{first, "created"}, {first, "unchanged"}, {second, "updated"},
	} {
		result, err := exec.ApplyDocuments([]parser.Document{parsedDoc(t, 0, step.raw)}, "")
		if err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		if len(result.Resources) != 1 || result.Resources[0].Action != step.action ||
			result.Resources[0].Kind != "TeamDefaults" {
			t.Fatalf("step %d: results = %+v, want TeamDefaults %s", i, result.Resources, step.action)
		}
	}
	stored := string(store.defaults["default/default"].Document)
	if !strings.Contains(stored, "roles: []") || !strings.Contains(stored, "harnesses: []") ||
		strings.Contains(stored, "sources") {
		t.Fatalf("stored TeamDefaults lost list presence:\n%s", stored)
	}
}

// TestApplyDocuments_TeamDefaultsRealmMissing: the realm must exist.
func TestApplyDocuments_TeamDefaultsRealmMissing(t *testing.T) {
	store := newTeamStoreFake()
	f := teamApplyRunner(store, nil)
	f.GetRealmFn = func(intmodel.Realm) (intmodel.Realm, error) { return intmodel.Realm{}, errdefs.ErrRealmNotFound }
	exec := setupTestController(t, f)
	raw := "apiVersion: v1beta1\nkind: TeamDefaults\nmetadata:\n  name: default\n  realm: nowhere\n"
	result, err := exec.ApplyDocuments([]parser.Document{parsedDoc(t, 0, raw)}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Resources[0]; got.Action != "failed" || !errors.Is(got.Error, errdefs.ErrTeamScopeNotFound) {
		t.Fatalf("result = %+v, want a failed TeamDefaults with ErrTeamScopeNotFound", got)
	}
	if len(store.defaults) != 0 {
		t.Fatal("TeamDefaults stored in a missing realm")
	}
}
