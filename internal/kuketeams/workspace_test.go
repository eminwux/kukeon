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

package kuketeams

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	model "github.com/eminwux/kukeon/pkg/api/model/kuketeams"
	"gopkg.in/yaml.v3"
)

func readExample(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("../../docs/examples", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestConfigurationExamplesRoundTrip(t *testing.T) {
	for _, name := range []string{"workspace.yaml", "kuketeam.d/minimal.yaml", "kuketeam.d/overrides.yaml", "kuketeam.d/orchestrator.yaml"} {
		t.Run(name, func(t *testing.T) {
			raw := readExample(t, name)
			doc, err := Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			var value any = doc.Team
			if doc.Workspace != nil {
				value = doc.Workspace
			}
			for _, format := range []string{"yaml", "json"} {
				var encoded []byte
				if format == "yaml" {
					encoded, err = yaml.Marshal(value)
				} else {
					encoded, err = json.Marshal(value)
				}
				if err != nil {
					t.Fatal(err)
				}
				var before, after any
				if err = yaml.Unmarshal(raw, &before); err != nil {
					t.Fatal(err)
				}
				if err = yaml.Unmarshal(encoded, &after); err != nil {
					t.Fatal(err)
				}
				// Compare the full authored field tree as well as the typed document.
				// This catches a field silently discarded by decoding or omitempty.
				if !reflect.DeepEqual(before, after) {
					t.Fatalf("%s lost authored values:\n%#v\n%#v", format, before, after)
				}
				round, parseErr := Parse(encoded)
				if parseErr != nil {
					t.Fatal(parseErr)
				}
				if !reflect.DeepEqual(doc, round) {
					t.Fatalf("%s typed round trip changed values", format)
				}
			}
		})
	}
}

func exampleWorkspace(t *testing.T) *model.Workspace {
	t.Helper()
	w, err := ParseWorkspace(readExample(t, "workspace.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func exampleTeam(t *testing.T, name string) *model.Team {
	t.Helper()
	team, err := ParseTeam(readExample(t, "kuketeam.d/"+name+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return team
}

func TestConfigurationDefaults(t *testing.T) {
	w := exampleWorkspace(t)
	minimal := exampleTeam(t, "minimal")
	resolved, err := ResolveDefaults(*w, *minimal)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(resolved.Spec.TeamSelections, w.Spec.Defaults) {
		t.Fatal("minimal did not inherit exact defaults")
	}
	if minimal.Spec.Roles != nil {
		t.Fatal("resolution mutated input Team")
	}
	(*resolved.Spec.Roles)[0].Ref = "changed"
	(*resolved.Spec.Integrations)["gh"] = model.CredentialGrant{Credentials: []string{}}
	if (*w.Spec.Defaults.Roles)[0].Ref != "dev" || len((*w.Spec.Defaults.Integrations)["gh"].Credentials) != 1 {
		t.Fatal("resolved Team aliases Workspace memory")
	}
	override := exampleTeam(t, "overrides")
	result, err := ResolveDefaults(*w, *override)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result, override) {
		t.Fatal("explicit Team selections must replace, not merge")
	}
	if result.Spec.Repo.Repo != minimal.Spec.Repo.Repo || result.Metadata.Name == minimal.Metadata.Name {
		t.Fatal("distinct Teams should independently target the same repo")
	}
	orchestrator := exampleTeam(t, "orchestrator")
	result, err = ResolveDefaults(*w, *orchestrator)
	if err != nil {
		t.Fatal(err)
	}
	if result.Spec.Repo != nil || len(*result.Spec.Integrations) != 0 || (*result.Spec.Roles)[0].Name != "root" {
		t.Fatal("repo-less identity/grant overrides changed")
	}
	empty := []model.TeamRole{}
	minimal.Spec.Roles = &empty
	if _, err = ResolveDefaults(*w, *minimal); err == nil || !strings.Contains(err.Error(), "spec.roles") {
		t.Fatalf("explicit empty roles must not mean inherit or all: %v", err)
	}
	minimal.Spec.Roles = nil
	w.Spec.Defaults = model.TeamSelections{}
	if _, err = ResolveDefaults(*w, *minimal); err == nil {
		t.Fatal("missing selections must not mean all")
	}
}

func TestConfigurationNegativeFixtures(t *testing.T) {
	var cases []struct {
		File  string `json:"file"`
		Kind  string `json:"kind"`
		Field string `json:"field"`
	}
	raw, err := os.ReadFile("testdata/configuration/errors.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.File, func(t *testing.T) {
			data, readErr := os.ReadFile(filepath.Join("testdata/configuration", tc.File))
			if readErr != nil {
				t.Fatal(readErr)
			}
			var parseErr error
			if tc.Kind == "workspace" {
				_, parseErr = ParseWorkspace(data)
			} else {
				_, parseErr = ParseTeam(data)
			}
			if parseErr == nil || !strings.Contains(parseErr.Error(), tc.Field+":") {
				t.Fatalf("want field %s, got %v", tc.Field, parseErr)
			}
		})
	}
}

func TestConfigurationExplicitEmptyAndFalse(t *testing.T) {
	w := exampleWorkspace(t)
	if w.Spec.Credentials[1].VolumeMount.ReadOnly == nil || !*w.Spec.Credentials[1].VolumeMount.ReadOnly ||
		w.Spec.Credentials[2].VolumeMount.ReadOnly == nil || *w.Spec.Credentials[2].VolumeMount.ReadOnly ||
		w.Spec.Credentials[3].VolumeMount.ReadOnly != nil {
		t.Fatal("mount readOnly tri-state lost")
	}
	raw := []byte(`apiVersion: kuketeams.io/v1
kind: Team
metadata: {name: empty}
spec: {roles: [], harnesses: [], modes: [], integrations: {git: {credentials: []}}}
`)
	team, err := ParseTeam(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, marshal := range []func(any) ([]byte, error){yaml.Marshal, json.Marshal} {
		data, marshalErr := marshal(team)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		round, parseErr := ParseTeam(data)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		if !reflect.DeepEqual(team, round) {
			t.Fatal("explicit empty selections lost")
		}
	}
}

func TestConfigurationPathsAndRevisionForms(t *testing.T) {
	for _, cwd := range []string{"project/subdir", "/work/project", "~", "$HOME", "~/project", "$HOME/project", "."} {
		team := exampleTeam(t, "minimal")
		team.Spec.Cwd = cwd
		for _, repo := range []model.TeamSource{
			{Repo: "example/project"},
			{Repo: "github.com/example/project", Tag: "v1.0"},
			{Repo: "git.example.com:2222/group/project", Branch: "feature/work"},
			{Repo: "example/project", Commit: "abcdef0123456789"},
		} {
			team.Spec.Repo = &repo
			data, err := yaml.Marshal(team)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = ParseTeam(data); err != nil {
				t.Fatalf("cwd %s repo %+v: %v", cwd, repo, err)
			}
		}
	}
}

func TestConfigurationDefersReferenceResolution(t *testing.T) {
	team := exampleTeam(t, "overrides")
	(*team.Spec.Roles)[0].Ref = "not-yet-published"
	(*team.Spec.Roles)[0].Harness = "not-yet-published"
	(*team.Spec.Integrations)["gh"] = model.CredentialGrant{Credentials: []string{"not-yet-published"}}
	raw, err := yaml.Marshal(team)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseTeam(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ResolveDefaults(*exampleWorkspace(t), *parsed); err != nil {
		t.Fatal(err)
	}
	// Existence, grants, role/harness membership and source metadata are #1348.
}

func TestConfigurationCompletenessAndIndependentSources(t *testing.T) {
	w := exampleWorkspace(t)
	team := exampleTeam(t, "overrides")
	team.Spec.Source = nil
	w.Spec.Defaults.Source = nil
	if _, err := ResolveDefaults(*w, *team); err == nil || !strings.Contains(err.Error(), "spec.roles[1].source:") {
		t.Fatalf("each role needs its own or inherited source: %v", err)
	}
	(*team.Spec.Roles)[1].Source = (*team.Spec.Roles)[0].Source
	if _, err := ResolveDefaults(*w, *team); err == nil || !strings.Contains(err.Error(), "spec.harnesses[1].source:") {
		t.Fatalf("each harness needs its own or inherited source: %v", err)
	}
	(*team.Spec.Harnesses)[1].Source = (*team.Spec.Harnesses)[0].Source
	(*team.Spec.Harnesses)[0].Default = nil
	result, err := ResolveDefaults(*w, *team)
	if err != nil {
		t.Fatal(err)
	}
	if result.Spec.Source != nil || (*result.Spec.Harnesses)[0].Default != nil {
		t.Fatal("resolution must not infer a global source or first-harness default")
	}
	// A present empty harness list disables inheritance, then fails completeness.
	empty := []model.TeamHarness{}
	team.Spec.Harnesses = &empty
	if _, err = ResolveDefaults(*w, *team); err == nil || !strings.Contains(err.Error(), "spec.harnesses:") {
		t.Fatalf("empty enabled set must not inherit: %v", err)
	}
}

func TestConfigurationModesAndGrantsReplaceWithEmpty(t *testing.T) {
	w := exampleWorkspace(t)
	team := exampleTeam(t, "minimal")
	modes := []string{}
	grants := map[string]model.CredentialGrant{}
	team.Spec.Modes = &modes
	team.Spec.Integrations = &grants
	result, err := ResolveDefaults(*w, *team)
	if err != nil {
		t.Fatal(err)
	}
	if result.Spec.Modes == nil || len(*result.Spec.Modes) != 0 || result.Spec.Integrations == nil ||
		len(*result.Spec.Integrations) != 0 {
		t.Fatal("empty overrides must replace inherited modes and grants")
	}
}

func TestConfigurationRemovedKindsRejected(t *testing.T) {
	for _, name := range []string{"legacy-config", "legacy-project", "legacy-entry", "aggregate"} {
		raw, err := os.ReadFile("testdata/configuration/" + name + ".invalid")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = Parse(raw); err == nil {
			t.Fatalf("removed kind accepted: %s", name)
		} else if !strings.Contains(err.Error(), "kind:") && !strings.Contains(err.Error(), "apiVersion:") {
			t.Fatalf("removed format lacks field-path diagnostic: %s: %v", name, err)
		}
	}
}

func TestConfigurationCredentialDeclarationsDoNotGrantAccess(t *testing.T) {
	w := exampleWorkspace(t)
	w.Spec.Defaults.Integrations = nil
	team := exampleTeam(t, "minimal")
	result, err := ResolveDefaults(*w, *team)
	if err != nil {
		t.Fatal(err)
	}
	if result.Spec.Integrations != nil {
		t.Fatal("global credential declarations must not imply grants")
	}
}

func TestRepositoryTeamManifest(t *testing.T) {
	raw, err := os.ReadFile("../../kuketeam.yaml")
	if err != nil {
		t.Fatal(err)
	}
	team, err := ParseTeam(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(team.SelectedRoles()) == 0 || len(team.HarnessNames()) == 0 {
		t.Fatal("repository Team must select its roster explicitly")
	}
	if err = ValidateSource(team.AgentSource()); err != nil {
		t.Fatal(err)
	}
}
