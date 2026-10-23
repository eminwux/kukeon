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

package kukeworkspace

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	model "github.com/eminwux/kukeon/pkg/api/model/kukeworkspace"
	"gopkg.in/yaml.v3"
)

const validWorkspace = `
apiVersion: kukeworkspace.io/v1
kind: Workspace
metadata: {name: engineering}
spec:
  agentSources:
    - {name: house, repo: github.com/acme/agents, ref: v2.1.0, path: model1}
  projects:
    - name: api
      repo: github.com/acme/api
      path: /home/alice/src/api
      integrations:
        github: {type: github, credentials: {token: github-work}}
  projectTeams:
    - {name: api-dev, project: api, agentSource: house, roles: [dev, reviewer]}
  workspaceCells:
    - {name: orchestrator, blueprint: workspace-orchestrator}
`

func TestParseWorkspace(t *testing.T) {
	t.Parallel()
	doc, err := Parse([]byte(validWorkspace))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Metadata.Name != "engineering" || len(doc.Spec.AgentSources) != 1 ||
		len(doc.Spec.Projects) != 1 || len(doc.Spec.ProjectTeams) != 1 ||
		len(doc.Spec.WorkspaceCells) != 1 {
		t.Fatalf("parsed workspace lost nested declarations: %+v", doc)
	}
	project := doc.Spec.Projects[0]
	if project.Path != "/home/alice/src/api" || project.Integrations["github"].Credentials["token"] != "github-work" {
		t.Fatalf("project fields were not parsed: %+v", project)
	}
	if got := doc.Spec.ProjectTeams[0].Roles; len(got) != 2 || got[0] != "dev" || got[1] != "reviewer" {
		t.Fatalf("roles = %v", got)
	}
}

func TestWorkspaceRoundTripsJSONAndYAML(t *testing.T) {
	t.Parallel()
	parsed, err := Parse([]byte(validWorkspace))
	if err != nil {
		t.Fatal(err)
	}
	check := func(got model.Workspace) bool {
		return got.Metadata.Name == "engineering" &&
			got.Spec.AgentSources[0].Path == "model1" &&
			got.Spec.Projects[0].Integrations["github"].Credentials["token"] == "github-work" &&
			got.Spec.ProjectTeams[0].Roles[1] == "reviewer" &&
			got.Spec.WorkspaceCells[0].Blueprint == "workspace-orchestrator"
	}

	jsonBytes, marshalErr := json.Marshal(parsed)
	if marshalErr != nil {
		t.Fatalf("json.Marshal: %v", marshalErr)
	}
	var fromJSON model.Workspace
	if unmarshalErr := json.Unmarshal(jsonBytes, &fromJSON); unmarshalErr != nil || !check(fromJSON) {
		t.Fatalf("JSON round-trip failed: err=%v data=%s", unmarshalErr, jsonBytes)
	}

	yamlBytes, marshalErr := yaml.Marshal(parsed)
	if marshalErr != nil {
		t.Fatalf("yaml.Marshal: %v", marshalErr)
	}
	var fromYAML model.Workspace
	if unmarshalErr := yaml.Unmarshal(yamlBytes, &fromYAML); unmarshalErr != nil || !check(fromYAML) {
		t.Fatalf("YAML round-trip failed: err=%v data=%s", unmarshalErr, yamlBytes)
	}
}

func TestParseWorkspaceRejectsMalformedOrInvalidDocuments(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{"malformed YAML", "apiVersion: [", "decode YAML"},
		{"wrong api version", strings.Replace(validWorkspace, "kukeworkspace.io/v1", "kukeworkspace.io/v2", 1), "apiVersion"},
		{"wrong kind", strings.Replace(validWorkspace, "kind: Workspace", "kind: Other", 1), "kind"},
		{"missing source ref", strings.Replace(validWorkspace, "ref: v2.1.0, ", "", 1), "agentSources[0].ref"},
		{"absolute source path", strings.Replace(validWorkspace, "path: model1", "path: /etc", 1), "repository-relative"},
		{"relative project path", strings.Replace(validWorkspace, "/home/alice/src/api", "src/api", 1), "absolute host-local"},
		{"empty roles", strings.Replace(validWorkspace, "roles: [dev, reviewer]", "roles: []", 1), "explicitly select"},
		{
			"duplicate project",
			strings.Replace(validWorkspace, "  projectTeams:", "    - {name: api, repo: github.com/acme/other, path: /tmp/other}\n  projectTeams:", 1),
			"duplicate name",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Parse([]byte(tc.raw))
			if err == nil {
				t.Fatal("Parse succeeded, want error")
			}
			if !errors.Is(err, errdefs.ErrWorkspaceManifestInvalid) {
				t.Errorf("error %v does not wrap ErrWorkspaceManifestInvalid", err)
			}
			if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err, tc.want)
			}
		})
	}
}

func TestValidateDoesNotResolveReferences(t *testing.T) {
	t.Parallel()
	doc, err := Parse([]byte(validWorkspace))
	if err != nil {
		t.Fatal(err)
	}
	doc.Spec.ProjectTeams[0].Project = "not-yet-resolved"
	doc.Spec.ProjectTeams[0].AgentSource = "not-yet-resolved"
	doc.Spec.ProjectTeams[0].Roles[0] = "not-yet-resolved"
	if validationErr := Validate(doc); validationErr != nil {
		t.Fatalf("structural validation resolved semantic references: %v", validationErr)
	}
}
