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

package kuketeams_test

import (
	"encoding/json"
	"strings"
	"testing"

	model "github.com/eminwux/kukeon/pkg/api/model/kuketeams"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"gopkg.in/yaml.v3"
)

// TestTeamsConfigGitMarshalsContainerGitSuperset asserts the embedded
// v1beta1.ContainerGit fields promote in both JSON and YAML, and the added
// sshKey field round-trips — i.e. TeamsConfig.git is a strict superset of
// ContainerGit on the wire, not just in Go.
func TestTeamsConfigGitMarshalsContainerGitSuperset(t *testing.T) {
	t.Parallel()
	git := model.TeamsConfigGit{
		ContainerGit: v1beta1.ContainerGit{
			Author:         &v1beta1.GitIdentity{Name: "A", Email: "a@example.com"},
			SigningKey:     "/k.pub",
			Sign:           []string{model.GitSignCommits, model.GitSignTags},
			AllowedSigners: "/allowed",
		},
		SSHKey: "/id_ed25519",
	}

	jsonBytes, err := json.Marshal(git)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	for _, key := range []string{`"author"`, `"signingKey"`, `"sign"`, `"allowedSigners"`, `"sshKey"`} {
		if !strings.Contains(string(jsonBytes), key) {
			t.Errorf("JSON missing promoted key %s: %s", key, jsonBytes)
		}
	}

	yamlBytes, err := yaml.Marshal(git)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	for _, key := range []string{"author:", "signingKey:", "sign:", "allowedSigners:", "sshKey:"} {
		if !strings.Contains(string(yamlBytes), key) {
			t.Errorf("YAML missing promoted key %q: %s", key, yamlBytes)
		}
	}
}

// TestRoundTripAllKinds round-trips each kind through JSON and YAML and checks
// a representative field survives, exercising the JSON+YAML marshalling AC.
func TestRoundTripAllKinds(t *testing.T) {
	t.Parallel()

	pt := model.ProjectTeam{
		APIVersion: model.APIVersionV1, Kind: model.KindProjectTeam,
		Metadata: model.Metadata{Name: "sbsh"},
		Spec: model.ProjectTeamSpec{
			Source:   model.TeamSource{Repo: "github.com/eminwux/agents", Tag: "v1.4.0"},
			Defaults: model.ProjectTeamDefaults{Harnesses: []string{"claude"}},
			Roles:    []model.ProjectTeamRole{{Ref: "dev", Needs: &model.ProjectRoleNeeds{Image: []string{"go"}}}},
		},
	}
	assertJSONYAML(t, "ProjectTeam", pt, func(got model.ProjectTeam) bool {
		return got.Spec.Source.Repo == "github.com/eminwux/agents" && got.Spec.Source.Tag == "v1.4.0" &&
			len(got.Spec.Roles) == 1 && got.Spec.Roles[0].Needs.Image[0] == "go"
	})

	te := model.TeamEntry{
		APIVersion: model.APIVersionV1, Kind: model.KindTeamEntry,
		Metadata: model.Metadata{Name: "sbsh"},
		Spec: model.TeamEntrySpec{
			Path:    "/home/op/src/sbsh",
			TeamDir: "/home/op/.kuke/teams/sbsh",
			Source:  &model.TeamSource{Repo: "github.com/eminwux/agents", Branch: "main"},
		},
	}
	assertJSONYAML(t, "TeamEntry", te, func(got model.TeamEntry) bool {
		return got.Metadata.Name == "sbsh" && got.Spec.Path == "/home/op/src/sbsh" &&
			got.Spec.TeamDir == "/home/op/.kuke/teams/sbsh" &&
			got.Spec.Source != nil && got.Spec.Source.Branch == "main" && got.Spec.Source.Floating()
	})

	role := model.Role{
		APIVersion: model.APIVersionV1, Kind: model.KindRole,
		Metadata: model.Metadata{Name: "dev"},
		Spec: model.RoleSpec{
			Harnesses: map[string]model.RoleHarness{"claude": {Settings: "s.json"}},
			Needs:     model.RoleNeeds{Repos: []string{"project", "agents"}, Mounts: []string{"ssh"}},
		},
	}
	assertJSONYAML(t, "Role", role, func(got model.Role) bool {
		return len(got.Spec.Needs.Repos) == 2 && len(got.Spec.Needs.Mounts) == 1 &&
			got.Spec.Harnesses["claude"].Settings == "s.json"
	})

	h := model.Harness{
		APIVersion: model.APIVersionV1, Kind: model.KindHarness,
		Metadata: model.Metadata{Name: "claude"},
		Spec: model.HarnessSpec{
			SkillPath:  "/s",
			MakeTarget: "claude",
			Template:   "t.yaml",
			Seeds: []model.HarnessSeed{
				{Path: "${TEAM_ROOT}/${HARNESS}.json", Mode: 0o644, Content: "{}"},
			},
		},
	}
	assertJSONYAML(t, "Harness", h, func(got model.Harness) bool {
		return got.Spec.SkillPath == "/s" && got.Spec.MakeTarget == "claude" &&
			len(got.Spec.Seeds) == 1 &&
			got.Spec.Seeds[0].Path == "${TEAM_ROOT}/${HARNESS}.json" &&
			got.Spec.Seeds[0].Mode == 0o644 &&
			got.Spec.Seeds[0].Content == "{}"
	})

	ic := model.ImageCatalog{
		APIVersion: model.APIVersionV1, Kind: model.KindImageCatalog,
		Spec: model.ImageCatalogSpec{Images: []model.ImageCatalogEntry{{
			Ref: "claude", Harness: "claude", Image: "registry.eminwux.com/claude:latest",
			Build:        model.ImageCatalogBuild{Context: "harnesses/claude", Dockerfile: "Dockerfile"},
			Capabilities: []string{"git", "gh"},
		}}},
	}
	assertJSONYAML(t, "ImageCatalog", ic, func(got model.ImageCatalog) bool {
		return len(got.Spec.Images) == 1 && got.Spec.Images[0].Build.Context == "harnesses/claude"
	})
}

// TestTeamsConfigSpecRoundTripsHomeDirAndRepoOwner asserts the
// .spec.homeDir / .spec.repoOwner override fields (added with the
// .operator.HOME_DIR / .operator.REPO_OWNER blueprint facts) survive a
// JSON/YAML round-trip — both default to derived values at render time,
// but the explicit override must marshal in both encodings.
func TestTeamsConfigSpecRoundTripsHomeDirAndRepoOwner(t *testing.T) {
	t.Parallel()
	tc := model.TeamsConfig{
		APIVersion: model.APIVersionV1, Kind: model.KindTeamsConfig,
		Spec: model.TeamsConfigSpec{
			Registry:  "registry.local",
			HomeDir:   "/home/op",
			RepoOwner: "eminwux",
		},
	}
	assertJSONYAML(t, "TeamsConfig+HomeDir+RepoOwner", tc, func(got model.TeamsConfig) bool {
		return got.Spec.HomeDir == "/home/op" && got.Spec.RepoOwner == "eminwux" &&
			got.Spec.Registry == "registry.local"
	})
}

func assertJSONYAML[T any](t *testing.T, name string, in T, ok func(T) bool) {
	t.Helper()
	jb, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("%s json.Marshal: %v", name, err)
	}
	var fromJSON T
	if unmarshalErr := json.Unmarshal(jb, &fromJSON); unmarshalErr != nil {
		t.Fatalf("%s json.Unmarshal: %v", name, unmarshalErr)
	}
	if !ok(fromJSON) {
		t.Errorf("%s JSON round-trip lost data: %s", name, jb)
	}
	yb, err := yaml.Marshal(in)
	if err != nil {
		t.Fatalf("%s yaml.Marshal: %v", name, err)
	}
	var fromYAML T
	if unmarshalErr := yaml.Unmarshal(yb, &fromYAML); unmarshalErr != nil {
		t.Fatalf("%s yaml.Unmarshal: %v", name, unmarshalErr)
	}
	if !ok(fromYAML) {
		t.Errorf("%s YAML round-trip lost data: %s", name, yb)
	}
}
