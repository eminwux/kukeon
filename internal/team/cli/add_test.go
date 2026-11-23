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
	"bytes"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/team/initialize"
	"github.com/eminwux/kukeon/pkg/team/config"
)

const projectURL = "https://example.test/proj.git"

// repoTeam is a project's kuketeam.yaml: one role from the kukeon source.
const repoTeam = `apiVersion: v1beta1
kind: Team
metadata:
  name: other
spec:
  roles:
    - {name: lead, source: kukeon, path: team/roles/majordomo}
`

// projectHarness is a harness whose project repository carries file as its
// kuketeam.yaml (none when empty), with the initial TeamDefaults stored.
func projectHarness(t *testing.T, file string) *harness {
	t.Helper()
	h := newHarness(t)
	project := fstest.MapFS{"README.md": {Data: []byte("project\n")}}
	if file != "" {
		project["kuketeam.yaml"] = &fstest.MapFile{Data: []byte(file)}
	}
	h.sources["project"] = project
	// A role from the kukeon source needs it cloned into the cell.
	defaults := initialize.InitialDefaults("default")
	defaults.Spec.Defaults.Sources[0].ClonePath = "/home/user/kukeon-src"
	h.store.putDefaults(t, defaults)
	return h
}

func addArgs(extra ...string) []string {
	return append([]string{"add", "proj", "--source", projectURL, "--branch", "main"}, extra...)
}

func (s *store) storedTeam(t *testing.T, name string) *config.Team {
	t.Helper()
	raw, ok := s.teams[name]
	if !ok {
		t.Fatalf("team %s is not stored", name)
	}
	team, err := config.DecodeTeam(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("stored team %s: %v\n%s", name, err, raw)
	}
	return team
}

func roleNames(team *config.Team) []string {
	var out []string
	for _, r := range team.Spec.Roles {
		out = append(out, r.Name)
	}
	return out
}

// TestAddReadsTheRepositoryFile is AC8: the repository's kuketeam.yaml is
// read from the fetched tree, NAME wins over its metadata.name, and the
// stored Team carries the project source.
func TestAddReadsTheRepositoryFile(t *testing.T) {
	h := projectHarness(t, repoTeam)
	out, err := h.run(t, addArgs()...)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	team := h.store.storedTeam(t, "proj")
	if h.store.team != "proj" || team.Metadata.Name != "proj" || strings.Join(roleNames(team), ",") != "lead" {
		t.Fatalf("stored %+v via team apply %q", team, h.store.team)
	}
	project := team.Spec.ProjectSource
	if project == nil || project.URL != projectURL || project.Branch != "main" ||
		project.ClonePath != "/home/user/proj" {
		t.Fatalf("project source = %+v", project)
	}
	if _, ok := h.store.teams["other"]; ok {
		t.Fatal("the file's metadata.name was stored")
	}
}

// TestAddDryRunWritesNothing is AC10.
func TestAddDryRunWritesNothing(t *testing.T) {
	h := projectHarness(t, repoTeam)
	out, err := h.run(t, addArgs("--dry-run")...)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if h.store.writes != 0 || len(h.store.teams) != 0 {
		t.Fatalf("dry run wrote: writes=%d teams=%v", h.store.writes, h.store.teams)
	}
	for _, want := range []string{
		"Dry run: nothing was applied", "declaration from repository file kuketeam.yaml at", "Declaration:",
		"name: proj", "Sources:", "project  " + projectURL + "  branch main", "Pairs:", "lead ×", "Configs to create:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run output lacks %q:\n%s", want, out)
		}
	}
}

// TestAddWithoutFileInheritsAndExplainsAnEmptySelection is AC8 and AC11.
func TestAddWithoutFileInheritsAndExplainsAnEmptySelection(t *testing.T) {
	for name, args := range map[string][]string{
		"no file":      addArgs(),
		"no-repo-file": addArgs("--no-repo-file"),
	} {
		t.Run(name, func(t *testing.T) {
			file := ""
			if name == "no-repo-file" {
				file = repoTeam
			}
			h := projectHarness(t, file)
			_, err := h.run(t, args...)
			if err == nil || !strings.Contains(err.Error(), "no kuketeam.yaml") ||
				!strings.Contains(err.Error(), "--role") || !strings.Contains(err.Error(), "kuke team source add") {
				t.Fatalf("error = %v", err)
			}
			if h.store.writes != 0 {
				t.Fatalf("writes = %d", h.store.writes)
			}
		})
	}
}

// TestAddOverrides is AC9: --role replaces the declaration's list, -f
// replaces the whole declaration, and the stored Team is what was applied.
func TestAddOverrides(t *testing.T) {
	t.Run("role", func(t *testing.T) {
		h := projectHarness(t, repoTeam)
		if _, err := h.run(t, addArgs("--role", "boss=kukeon:team/roles/majordomo")...); err != nil {
			t.Fatal(err)
		}
		if got := roleNames(h.store.storedTeam(t, "proj")); strings.Join(got, ",") != "boss" {
			t.Fatalf("roles = %v", got)
		}
	})
	t.Run("file", func(t *testing.T) {
		h := projectHarness(t, repoTeam)
		file := writeFile(t, "team.yaml", strings.ReplaceAll(repoTeam, "name: lead", "name: chief"))
		if _, err := h.run(t, addArgs("-f", file)...); err != nil {
			t.Fatal(err)
		}
		if got := roleNames(h.store.storedTeam(t, "proj")); strings.Join(got, ",") != "chief" {
			t.Fatalf("roles = %v", got)
		}
	})
	t.Run("unknown default role", func(t *testing.T) {
		h := projectHarness(t, repoTeam)
		if _, err := h.run(t, addArgs("--role", "nobody")...); err == nil || h.store.writes != 0 {
			t.Fatalf("err=%v writes=%d", err, h.store.writes)
		}
	})
}

// TestAddCreatesReplacingNeedsForce is AC8a: a stored team of the name stops
// add before anything is fetched; --force replaces the declaration. (That the
// recorded secret bindings a composition uses are kept is pinned in
// internal/team/initialize.)
func TestAddCreatesReplacingNeedsForce(t *testing.T) {
	h := projectHarness(t, repoTeam)
	binding, err := config.DefaultSecretBinding("gh-token", "default")
	if err != nil {
		t.Fatal(err)
	}
	stored := &config.Team{
		APIVersion: config.APIVersion, Kind: config.KindTeam,
		Metadata: config.Metadata{Name: "proj", Realm: "default"},
		Spec: config.TeamSpec{
			Secrets: []config.SecretBinding{binding},
			Defaults: config.Defaults{
				Roles: []config.Role{{Name: "old", Source: "kukeon", Path: "team/roles/majordomo"}},
			},
		},
	}
	h.store.putTeam(t, stored)
	// No project tree: a fetch would fail, so the error proves none ran.
	delete(h.sources, "project")
	_, err = h.run(t, "add", "proj", "--source", "https://example.invalid/none.git", "--branch", "main")
	if !errors.Is(err, errdefs.ErrTeamExists) || !strings.Contains(err.Error(), "kuke team apply proj") ||
		!strings.Contains(err.Error(), "--force") {
		t.Fatalf("error = %v", err)
	}
	if h.store.writes != 0 {
		t.Fatalf("writes = %d", h.store.writes)
	}
	h = projectHarness(t, repoTeam)
	h.store.putTeam(t, stored)
	if out, forceErr := h.run(t, addArgs("--force")...); forceErr != nil {
		t.Fatalf("%v\n%s", forceErr, out)
	}
	if got := roleNames(h.store.storedTeam(t, "proj")); strings.Join(got, ",") != "lead" {
		t.Fatalf("forced team roles = %v\n%s", got, h.store.teams["proj"])
	}
}

func TestAddRejectsInvalidArgumentsWithoutEffects(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"add", "proj"}, "NAME --source REPOSITORY"},
		{[]string{"add", "proj", "--source", " "}, "must not be empty"},
		{[]string{"add", "./missing"}, "unavailable or is not a directory"},
		{[]string{"add", "proj", "--source", projectURL, "--tag", "t", "--commit", "c"}, "none of the others can be"},
		{[]string{"add", "proj", "--source", projectURL, "-f", "x", "--no-repo-file"}, "none of the others can be"},
		{[]string{"add", "proj", "--source", projectURL, "-f", "/does/not/exist"}, "read /does/not/exist"},
	} {
		h := newHarness(t)
		if _, err := h.run(t, tc.args...); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%v: error = %v, want %q", tc.args, err, tc.want)
		}
		if len(h.connect) != 0 || h.store.writes != 0 {
			t.Fatalf("%v dialled kukeond or wrote", tc.args)
		}
	}
}
