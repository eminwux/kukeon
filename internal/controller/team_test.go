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
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/cellconfig"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

const alphaTeamDoc = "apiVersion: v1beta1\nkind: Team\nmetadata:\n  name: alpha\n  realm: default\n" +
	"spec:\n  projectSource:\n    name: project\n    url: https://example.test/alpha\n    branch: main\n" +
	"  roles:\n    - {name: dev, source: project, path: roles/dev}\n"

// deleteTeamFixture is alpha and beta, each with one Config; alpha's
// blueprint is its own, and a shared blueprint is referenced by both.
func deleteTeamFixture(t *testing.T) (*teamStore, *teamStoreFake, *fakeRunner) {
	t.Helper()
	configs := newTeamStore(t)
	configs.storeBlueprint(sharedBlueprint("shared-bp"))
	alphaBP := *blueprintDoc(0, "alpha-bp", "default").CellBlueprintDoc
	alphaBP.Metadata.Labels = map[string]string{"kukeon.io/team": "alpha"}
	configs.storeBlueprint(alphaBP)
	configs.storeConfig(teamConfig("alpha-dev", "alpha", sharedRef("shared-bp")))
	configs.storeConfig(teamConfig("alpha-ops", "alpha", sharedRef("alpha-bp")))
	configs.storeConfig(teamConfig("beta-dev", "beta", sharedRef("shared-bp")))

	teams := newTeamStoreFake()
	f := configs.runner()
	teams.install(f)
	teams.teams["default/alpha"] = intmodel.Team{
		Metadata: intmodel.TeamMetadata{Name: "alpha", Realm: "default"}, Document: []byte(alphaTeamDoc),
	}
	cell := func(name, config string, state intmodel.CellState) intmodel.Cell {
		c := intmodel.Cell{}
		c.Metadata.Name = name
		c.Metadata.Labels = map[string]string{cellconfig.LabelConfig: config}
		c.Spec.RealmName, c.Spec.SpaceName, c.Spec.StackName = "default", "default", "default"
		c.Status.State = state
		return c
	}
	f.ListCellsFn = func(_, _, _ string) ([]intmodel.Cell, error) {
		return []intmodel.Cell{
			cell("alpha-dev-1", "alpha-dev", intmodel.CellStateReady),
			cell("alpha-ops-1", "alpha-ops", intmodel.CellStateStopped),
			cell("beta-dev-1", "beta-dev", intmodel.CellStateReady),
		}, nil
	}
	f.ListVolumesFn = func(_, _, _ string) ([]intmodel.Volume, error) {
		vol := func(name string) intmodel.Volume {
			return intmodel.Volume{Metadata: intmodel.VolumeMetadata{
				Name: name, Realm: "default", Space: "default", Stack: "default",
			}}
		}
		return []intmodel.Volume{vol("alpha-dev-state"), vol("beta-dev-state"), vol("unrelated")}, nil
	}
	return configs, teams, f
}

// TestDeleteTeam pins AC6: the team's Configs, the blueprints no remaining
// Config references, and then the Team go; cells and volumes stay and are
// reported.
func TestDeleteTeam(t *testing.T) {
	configs, teams, f := deleteTeamFixture(t)
	exec := setupTestController(t, f)

	res, err := exec.DeleteTeam("alpha", "")
	if err != nil {
		t.Fatalf("DeleteTeam error = %v", err)
	}
	if _, ok := teams.teams["default/alpha"]; ok {
		t.Fatal("Team document survived")
	}
	sort.Strings(configs.deleted)
	want := []string{"CellBlueprint/alpha-bp", "CellConfig/alpha-dev", "CellConfig/alpha-ops"}
	if !slices.Equal(configs.deleted, want) {
		t.Fatalf("deleted = %v, want %v", configs.deleted, want)
	}
	if got := configs.blueprintNames(); !slices.Contains(got, "shared-bp") {
		t.Fatalf("shared blueprint still used by beta was deleted: %v", got)
	}
	if !slices.Equal(res.LiveCells, []string{"default/default/default/alpha-dev-1"}) {
		t.Fatalf("live cells = %v", res.LiveCells)
	}
	if !slices.Equal(res.KeptVolumes, []string{"default/default/default/alpha-dev-state"}) {
		t.Fatalf("kept volumes = %v", res.KeptVolumes)
	}
	var removed []string
	for _, r := range res.Removed {
		removed = append(removed, r.Kind+"/"+r.Name)
	}
	sort.Strings(removed)
	if !slices.Equal(removed, want) {
		t.Fatalf("reported removed = %v, want %v", removed, want)
	}
	// The Team is deleted last.
	if last := teams.events[len(teams.events)-1]; last != "delete Team/alpha" {
		t.Fatalf("last event = %q", last)
	}
}

func TestDeleteTeam_RefusesSystemTeam(t *testing.T) {
	exec := setupTestController(t, &fakeRunner{})
	_, err := exec.DeleteTeam("kuke", "default")
	if !errors.Is(err, errdefs.ErrTeamReserved) || !strings.Contains(err.Error(), "team kuke is a system team") {
		t.Fatalf("err = %v, want ErrTeamReserved naming kuke", err)
	}
}

// TestDeleteTeam_RefusesTheSystemDefaultTeam (issue #1692): the system
// default team is reserved, and the error names it; a user team stored as
// default before it became a system team can still be deleted, which is how
// it is renamed.
func TestDeleteTeam_RefusesTheSystemDefaultTeam(t *testing.T) {
	_, teams, f := deleteTeamFixture(t)
	system := "apiVersion: v1beta1\nkind: Team\nmetadata:\n  name: default\n  realm: default\nspec:\n  configs: template\n"
	teams.teams["default/default"] = intmodel.Team{
		Metadata: intmodel.TeamMetadata{Name: "default", Realm: "default"}, Document: []byte(system),
	}
	exec := setupTestController(t, f)
	_, err := exec.DeleteTeam("default", "default")
	if !errors.Is(err, errdefs.ErrTeamReserved) || !strings.Contains(err.Error(), "team default is a system team") {
		t.Fatalf("err = %v, want ErrTeamReserved naming default", err)
	}
	if _, ok := teams.teams["default/default"]; !ok {
		t.Fatal("the system default team was deleted")
	}

	teams.teams["default/default"] = intmodel.Team{
		Metadata: intmodel.TeamMetadata{Name: "default", Realm: "default"},
		Document: []byte("apiVersion: v1beta1\nkind: Team\nmetadata:\n  name: default\n  realm: default\n"),
	}
	if _, err = exec.DeleteTeam("default", "default"); err != nil {
		t.Fatalf("a user team named default: %v", err)
	}
	if _, ok := teams.teams["default/default"]; ok {
		t.Fatal("the user's default team was not deleted")
	}
}

func TestDeleteTeam_NotFound(t *testing.T) {
	_, _, f := deleteTeamFixture(t)
	exec := setupTestController(t, f)
	if _, err := exec.DeleteTeam("gamma", "default"); !errors.Is(err, errdefs.ErrTeamNotFound) {
		t.Fatalf("err = %v, want ErrTeamNotFound", err)
	}
}

func TestDeleteTeam_InvalidName(t *testing.T) {
	exec := setupTestController(t, &fakeRunner{})
	if _, err := exec.DeleteTeam("Bad_Name", "default"); !errors.Is(err, errdefs.ErrInvalidTeam) {
		t.Fatalf("err = %v, want ErrInvalidTeam", err)
	}
}

// TestGetAndListTeams pins the summary kuke get teams prints (AC5).
func TestGetAndListTeams(t *testing.T) {
	_, teams, f := deleteTeamFixture(t)
	teams.teams["other/beta"] = intmodel.Team{
		Metadata: intmodel.TeamMetadata{Name: "beta", Realm: "other"},
		Document: []byte(
			"apiVersion: v1beta1\nkind: Team\nmetadata:\n  name: beta\n  realm: other\nspec:\n  roles: []\n",
		),
	}
	exec := setupTestController(t, f)

	rec, found, err := exec.GetTeam("alpha", "")
	if err != nil || !found {
		t.Fatalf("GetTeam = %v, %v", found, err)
	}
	s := rec.Summary
	if s.ProjectURL != "https://example.test/alpha" || s.ProjectRef != "main" ||
		!slices.Equal(s.Roles, []string{"dev"}) || s.RolesInherited || !s.HarnessesInherited ||
		!slices.Equal(s.Configs, []string{"alpha-dev", "alpha-ops"}) {
		t.Fatalf("summary = %+v", s)
	}
	if string(rec.Team.Document) != alphaTeamDoc {
		t.Fatalf("document = %q", rec.Team.Document)
	}

	if _, found, err = exec.GetTeam("gamma", "default"); err != nil || found {
		t.Fatalf("GetTeam(gamma) = %v, %v; want not found without error", found, err)
	}

	all, err := exec.ListTeams("")
	if err != nil || len(all) != 2 || all[0].Summary.Name != "alpha" || all[1].Summary.Realm != "other" {
		t.Fatalf("ListTeams = %+v, %v", all, err)
	}
	if beta := all[1].Summary; beta.RolesInherited || len(beta.Roles) != 0 {
		t.Fatalf("beta summary = %+v; roles: [] is not inherited", beta)
	}
	one, err := exec.ListTeams("other")
	if err != nil || len(one) != 1 || one[0].Summary.Name != "beta" {
		t.Fatalf("ListTeams(other) = %+v, %v", one, err)
	}
}

func TestGetTeamDefaults(t *testing.T) {
	teams := newTeamStoreFake()
	f := &fakeRunner{}
	teams.install(f)
	exec := setupTestController(t, f)
	if _, found, err := exec.GetTeamDefaults(""); err != nil || found {
		t.Fatalf("GetTeamDefaults before write = %v, %v", found, err)
	}
	teams.defaults["default/default"] = intmodel.TeamDefaults{
		Metadata: intmodel.TeamMetadata{Name: "default", Realm: "default"}, Document: []byte("d"),
	}
	got, found, err := exec.GetTeamDefaults("")
	if err != nil || !found || string(got.Document) != "d" {
		t.Fatalf("GetTeamDefaults = %q, %v, %v", got.Document, found, err)
	}
}
