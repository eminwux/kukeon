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

//nolint:testpackage // tests the Team store against a temp RunPath
package runner

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
)

func testTeam(realm, name string, doc string) intmodel.Team {
	return intmodel.Team{Metadata: intmodel.TeamMetadata{Name: name, Realm: realm}, Document: []byte(doc)}
}

// TestTeamStore_RoundTrip pins the issue #1618 storage contract: a Team lands
// at <RunPath>/data/<realm>/teams/<name>.yaml, reads back byte for byte,
// lists per realm and across realms, and deletes.
func TestTeamStore_RoundTrip(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	doc := "apiVersion: v1beta1\nkind: Team\nmetadata:\n  name: alpha\n  realm: default\nspec:\n  roles: []\n"
	created, err := r.WriteTeam(testTeam("default", "alpha", doc))
	if err != nil || !created {
		t.Fatalf("WriteTeam() created=%v err=%v, want created", created, err)
	}
	created, err = r.WriteTeam(testTeam("default", "alpha", doc))
	if err != nil || created {
		t.Fatalf("second WriteTeam() created=%v err=%v, want an update", created, err)
	}
	if _, err = r.WriteTeam(testTeam("other", "beta", "b")); err != nil {
		t.Fatalf("WriteTeam(other) error = %v", err)
	}

	path := fs.TeamPath(runPath, "default", "alpha")
	if path != filepath.Join(runPath, "data", "default", "teams", "alpha.yaml") {
		t.Fatalf("TeamPath = %q", path)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("team file stat = %v, %v; want 0644", info, err)
	}

	got, err := r.GetTeam(testTeam("default", "alpha", ""))
	if err != nil || string(got.Document) != doc {
		t.Fatalf("GetTeam() = %q, %v", got.Document, err)
	}

	// A temp file left by an interrupted write is not a team.
	if err = os.WriteFile(filepath.Join(fs.TeamsDir(runPath, "default"), ".team-x.tmp"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	listed, err := r.ListTeams("default")
	if err != nil || len(listed) != 1 || listed[0].Metadata.Name != "alpha" {
		t.Fatalf("ListTeams(default) = %+v, %v", listed, err)
	}
	all, err := r.ListTeams("")
	if err != nil {
		t.Fatalf("ListTeams(all) error = %v", err)
	}
	var names []string
	for _, team := range all {
		names = append(names, team.Metadata.Realm+"/"+team.Metadata.Name)
	}
	sort.Strings(names)
	if len(names) != 2 || names[0] != "default/alpha" || names[1] != "other/beta" {
		t.Fatalf("ListTeams(all) = %v", names)
	}

	if err = r.DeleteTeam(testTeam("default", "alpha", "")); err != nil {
		t.Fatalf("DeleteTeam() error = %v", err)
	}
	if _, err = r.GetTeam(testTeam("default", "alpha", "")); !errors.Is(err, errdefs.ErrTeamNotFound) {
		t.Fatalf("GetTeam after delete err = %v, want ErrTeamNotFound", err)
	}
	if err = r.DeleteTeam(testTeam("default", "alpha", "")); !errors.Is(err, errdefs.ErrTeamNotFound) {
		t.Fatalf("second DeleteTeam err = %v, want ErrTeamNotFound", err)
	}
}

func TestTeamStore_ListMissingRealmDir(t *testing.T) {
	r := newMetadataTestExec(t, t.TempDir(), time.Now())
	listed, err := r.ListTeams("")
	if err != nil || len(listed) != 0 {
		t.Fatalf("ListTeams on an empty run path = %+v, %v", listed, err)
	}
}

func TestTeamDefaultsStore_RoundTrip(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())
	key := intmodel.TeamDefaults{Metadata: intmodel.TeamMetadata{Name: "default", Realm: "default"}}

	if _, err := r.GetTeamDefaults(key); !errors.Is(err, errdefs.ErrTeamDefaultsNotFound) {
		t.Fatalf("GetTeamDefaults before write err = %v, want ErrTeamDefaultsNotFound", err)
	}
	doc := []byte("apiVersion: v1beta1\nkind: TeamDefaults\n")
	key.Document = doc
	created, err := r.WriteTeamDefaults(key)
	if err != nil || !created {
		t.Fatalf("WriteTeamDefaults() created=%v err=%v", created, err)
	}
	if path := fs.TeamDefaultsPath(runPath, "default", "default"); path !=
		filepath.Join(runPath, "data", "default", "team-defaults", "default.yaml") {
		t.Fatalf("TeamDefaultsPath = %q", path)
	}
	got, err := r.GetTeamDefaults(intmodel.TeamDefaults{Metadata: key.Metadata})
	if err != nil || string(got.Document) != string(doc) {
		t.Fatalf("GetTeamDefaults() = %q, %v", got.Document, err)
	}
}

// TestTeamStore_SurvivesRestart: a runner built fresh over the same RunPath
// (a kukeond restart) reads what the previous one stored.
func TestTeamStore_SurvivesRestart(t *testing.T) {
	runPath := t.TempDir()
	first := newMetadataTestExec(t, runPath, time.Now())
	if _, err := first.WriteTeam(testTeam("default", "alpha", "team")); err != nil {
		t.Fatal(err)
	}
	if _, err := first.WriteTeamDefaults(intmodel.TeamDefaults{
		Metadata: intmodel.TeamMetadata{Name: "default", Realm: "default"}, Document: []byte("defaults"),
	}); err != nil {
		t.Fatal(err)
	}

	second := newMetadataTestExec(t, runPath, time.Now())
	team, err := second.GetTeam(testTeam("default", "alpha", ""))
	if err != nil || string(team.Document) != "team" {
		t.Fatalf("GetTeam after restart = %q, %v", team.Document, err)
	}
	defaults, err := second.GetTeamDefaults(intmodel.TeamDefaults{
		Metadata: intmodel.TeamMetadata{Name: "default", Realm: "default"},
	})
	if err != nil || string(defaults.Document) != "defaults" {
		t.Fatalf("GetTeamDefaults after restart = %q, %v", defaults.Document, err)
	}
}

// TestTeamStore_RealmPurgeRemovesTeams drives the reclaim every realm purge
// runs over the realm metadata dir: teams/ and team-defaults/ go with it,
// also when a retained volume keeps the realm dir itself.
func TestTeamStore_RealmPurgeRemovesTeams(t *testing.T) {
	for _, retained := range []bool{false, true} {
		name := "no retained volume"
		if retained {
			name = "retained volume"
		}
		t.Run(name, func(t *testing.T) {
			runPath := t.TempDir()
			r := newMetadataTestExec(t, runPath, time.Now())
			realmDir := fs.RealmMetadataDir(runPath, "default")
			seedScopeMetadataFile(t, realmDir)
			if _, err := r.WriteTeam(testTeam("default", "alpha", "team")); err != nil {
				t.Fatal(err)
			}
			if _, err := r.WriteTeamDefaults(intmodel.TeamDefaults{
				Metadata: intmodel.TeamMetadata{Name: "default", Realm: "default"}, Document: []byte("d"),
			}); err != nil {
				t.Fatal(err)
			}
			if retained {
				md := intmodel.VolumeMetadata{Name: "keep", Realm: "default"}
				if _, err := r.WriteVolume(retainVol(md)); err != nil {
					t.Fatalf("WriteVolume(retain) error = %v", err)
				}
			}

			if err := r.reclaimScopeMetadata(realmDir); err != nil {
				t.Fatalf("reclaimScopeMetadata error = %v", err)
			}
			assertGone(t, fs.TeamsDir(runPath, "default"), "teams dir")
			assertGone(t, fs.TeamDefaultsDir(runPath, "default"), "team-defaults dir")
			listed, err := r.ListTeams("")
			if err != nil || len(listed) != 0 {
				t.Fatalf("ListTeams after purge = %+v, %v", listed, err)
			}
		})
	}
}
