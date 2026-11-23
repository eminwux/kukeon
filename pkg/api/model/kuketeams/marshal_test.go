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
	"github.com/eminwux/kukeon/pkg/api/version"
	"gopkg.in/yaml.v3"
)

// TestRoundTripAllKinds round-trips each kind through JSON and YAML and checks
// a representative field survives, exercising the JSON+YAML marshalling AC.
func TestRoundTripAllKinds(t *testing.T) {
	t.Parallel()

	roleDefault := "${ROLE_SOURCE_REPO}"
	role := model.Role{
		APIVersion: version.V1Beta1, Kind: model.KindRole,
		Metadata: model.Metadata{Name: "dev"},
		Spec: model.RoleSpec{
			Harnesses: map[string]model.RoleHarness{"claude": {Settings: "s.json"}},
			Needs: model.RoleNeeds{
				Repos: []string{"project", "agents"}, Mounts: []string{"ssh"},
				Params: []model.RoleParam{{Name: "PROMPT"}, {Name: "AGENTS_REPO", Default: &roleDefault}},
			},
		},
	}
	assertJSONYAML(t, "Role", role, func(got model.Role) bool {
		params := got.Spec.Needs.Params
		return len(got.Spec.Needs.Repos) == 2 && len(got.Spec.Needs.Mounts) == 1 &&
			got.Spec.Harnesses["claude"].Settings == "s.json" &&
			len(params) == 2 && params[0].Name == "PROMPT" && params[0].Default == nil &&
			params[1].Name == "AGENTS_REPO" && params[1].Default != nil && *params[1].Default == roleDefault
	})

	h := model.Harness{
		APIVersion: version.V1Beta1, Kind: model.KindHarness,
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
		APIVersion: version.V1Beta1, Kind: model.KindImageCatalog,
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

// TestRoleParamForms asserts a needs.params entry decodes from a bare name or
// a {name, default} mapping in YAML and JSON, and that a name-only entry
// marshals back to the bare name.
func TestRoleParamForms(t *testing.T) {
	t.Parallel()
	var needs model.RoleNeeds
	raw := "params:\n  - PROMPT\n  - { name: AGENTS_REPO, default: \"${ROLE_SOURCE_REPO}\" }\n  - { name: EMPTY, default: \"\" }\n"
	if err := yaml.Unmarshal([]byte(raw), &needs); err != nil {
		t.Fatal(err)
	}
	if len(needs.Params) != 3 || needs.Params[0] != (model.RoleParam{Name: "PROMPT"}) ||
		needs.Params[1].Default == nil || *needs.Params[1].Default != "${ROLE_SOURCE_REPO}" ||
		needs.Params[2].Default == nil || *needs.Params[2].Default != "" {
		t.Fatalf("params = %+v", needs.Params)
	}
	out, err := yaml.Marshal(needs)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "- PROMPT\n") || !strings.Contains(string(out), "default: \"\"") {
		t.Fatalf("marshaled = %s", out)
	}
	var fromJSON model.RoleNeeds
	if err = json.Unmarshal([]byte(`{"params":["PROMPT",{"name":"REGION","default":"eu"}]}`), &fromJSON); err != nil {
		t.Fatal(err)
	}
	if len(fromJSON.Params) != 2 || fromJSON.Params[1].Name != "REGION" || *fromJSON.Params[1].Default != "eu" {
		t.Fatalf("json params = %+v", fromJSON.Params)
	}
	for _, bad := range []string{"params:\n  - { name: X, value: y }\n", "params:\n  - [X]\n"} {
		if err = yaml.Unmarshal([]byte(bad), &needs); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	if err = json.Unmarshal([]byte(`{"params":[{"name":"X","value":"y"}]}`), &fromJSON); err == nil {
		t.Fatal("json accepted an unknown field")
	}
}
