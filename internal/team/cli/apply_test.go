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

package cli_test

import (
	"strings"
	"testing"

	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/config"
)

// putProject stores storedProject and the Source proj its project names.
func (s *store) putProject(t *testing.T) {
	t.Helper()
	s.storeSource(t, "proj", config.SourceSpec{URL: projectURL, Branch: "main"})
	s.putTeam(t, storedProject())
}

// storedProject is a stored team with a project source and role "old", as
// kukeond stores it since #1643: the project refers to the Source proj.
func storedProject() *config.Team {
	return &config.Team{
		APIVersion: config.APIVersion, Kind: config.KindTeam,
		Metadata: config.Metadata{Name: "proj", Realm: "default"},
		Spec: config.TeamSpec{
			ProjectSource: &config.Source{
				Name: "project", Stored: "proj", Branch: "main", ClonePath: "/home/user/proj",
			},
			Defaults: config.Defaults{
				Roles: []config.Role{{Name: "old", Source: "kukeon", Path: "team/roles/majordomo"}},
			},
		},
	}
}

// TestApplyNameUsesTheStoredTeam is AC12: apply NAME recomposes from the
// stored Team, never from the repository's file.
func TestApplyNameUsesTheStoredTeam(t *testing.T) {
	h := projectHarness(t, repoTeam)
	h.store.putProject(t)
	out, err := h.run(t, "apply", "proj")
	if err != nil || !strings.Contains(out, "stored team proj") {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := roleNames(h.store.storedTeam(t, "proj")); strings.Join(got, ",") != "old" {
		t.Fatalf("roles = %v: the repository file was consulted", got)
	}
}

func TestApplyNameNotStored(t *testing.T) {
	h := projectHarness(t, "")
	_, err := h.run(t, "apply", "proj")
	if err == nil || !strings.Contains(err.Error(), "kuke create team proj") || h.store.writes != 0 {
		t.Fatalf("err=%v writes=%d", err, h.store.writes)
	}
}

// TestApplyKukeIsInitReapply is AC12.
func TestApplyKukeIsInitReapply(t *testing.T) {
	h := newHarness(t)
	h.store.storeSource(t, config.KukeonSourceName, config.KukeonSourceSpec())
	h.store.putTeam(t, config.Majordomo())
	out, err := h.run(t, "apply", "kuke")
	if err != nil || !strings.Contains(out, "stored team kuke") || h.store.team != "kuke" {
		t.Fatalf("%v\n%s", err, out)
	}
}

// TestApplyFileCreatesAndKeepsTheStoredProject is AC12: -f FILE creates a
// new team, and a file without spec.projectSource keeps the stored one.
func TestApplyFileCreatesAndKeepsTheStoredProject(t *testing.T) {
	h := projectHarness(t, "")
	created := strings.Replace(repoTeam, "name: other", "name: fresh", 1)
	if _, err := h.run(t, "apply", "-f", writeFile(t, "fresh.yaml", created)); err != nil {
		t.Fatal(err)
	}
	if team := h.store.storedTeam(t, "fresh"); team.Spec.ProjectSource != nil || h.store.team != "fresh" {
		t.Fatalf("fresh = %+v", team)
	}
	h.store.putProject(t)
	edited := strings.Replace(repoTeam, "name: other", "name: proj", 1)
	if _, err := h.run(t, "apply", "-f", writeFile(t, "proj.yaml", edited)); err != nil {
		t.Fatal(err)
	}
	team := h.store.storedTeam(t, "proj")
	if strings.Join(roleNames(team), ",") != "lead" || team.Spec.ProjectSource == nil ||
		team.Spec.ProjectSource.Stored != "proj" {
		t.Fatalf("proj = %+v", team.Spec)
	}
}

// TestApplyDryRunWritesNothing is AC10: the plan names the origin and the
// Configs the team would prune.
func TestApplyDryRunWritesNothing(t *testing.T) {
	h := projectHarness(t, "")
	h.store.putProject(t)
	h.store.configs = []api.CellConfigDoc{{Metadata: api.CellConfigMetadata{
		Name: "proj-gone-codex", Labels: map[string]string{api.LabelTeam: "proj"},
	}}}
	edited := strings.Replace(repoTeam, "name: other", "name: proj", 1)
	path := writeFile(t, "proj.yaml", edited)
	out, err := h.run(t, "apply", "-f", path, "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if h.store.writes != 0 {
		t.Fatalf("writes = %d", h.store.writes)
	}
	for _, want := range []string{"declaration from file " + path, "Configs to prune: proj-gone-codex", "lead ×"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestApplyArguments(t *testing.T) {
	for _, args := range [][]string{{"apply"}, {"apply", "proj", "-f", "x.yaml"}} {
		h := newHarness(t)
		if _, err := h.run(t, args...); err == nil || !strings.Contains(err.Error(), "name a stored team or -f FILE") {
			t.Fatalf("%v: %v", args, err)
		}
		if len(h.connect) != 0 {
			t.Fatalf("%v dialled kukeond", args)
		}
	}
	h := newHarness(t)
	if _, err := h.run(t, "apply", "-f", writeFile(t, "bad.yaml", "apiVersion: v1\nkind: Team\n")); err == nil ||
		h.store.writes != 0 {
		t.Fatalf("accepted an invalid Team document: %v", err)
	}
}
