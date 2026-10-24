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
	"os"
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
  sources:
    - {name: house, repo: github.com/acme/agents, ref: v2.1.0, path: model1}
  projects:
    - name: api
      repo: github.com/acme/api
      path: /home/alice/src/api
      integrations:
        github:
          gh:
            credentials: {token: {secretRef: github-cli}}
          git:
            credentials: {sshKey: {secretRef: github-ssh}}
  teams:
    - project: api
      apiVersion: kuketeams.io/v1
      kind: ProjectTeam
      metadata: {name: api-dev}
      spec:
        source: {repo: github.com/acme/agents, tag: v2.1.0}
        roles: [{ref: dev}]
    - apiVersion: kuketeams.io/v1
      kind: ProjectTeam
      metadata: {name: orchestrator}
      spec:
        source: {repo: github.com/acme/agents, tag: v2.1.0}
        roles: [{ref: orchestrator}]
      role: orchestrator
      agentSource: house
      workspaceCell: {blueprint: workspace-orchestrator}
status: {configs: 3, blueprints: 2, runningCells: 1}
`

func TestParseDocumentedWorkspaceExample(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../../docs/examples/workspace.yaml")
	if err != nil {
		t.Fatalf("read documented example: %v", err)
	}
	doc, err := Parse(raw)
	if err != nil {
		t.Fatalf("parse documented example: %v", err)
	}
	if len(doc.Spec.Projects) != 2 || len(doc.Spec.Teams) != 3 {
		t.Fatalf("documented example missing declarations: %+v", doc.Spec)
	}
	if doc.Spec.Teams[2].WorkspaceCell == nil || doc.Spec.Teams[2].Role != "orchestrator" {
		t.Fatalf("documented workspace team is missing its cell or role: %+v", doc.Spec.Teams[2])
	}
}

func TestParseWorkspace(t *testing.T) {
	t.Parallel()
	doc, err := Parse([]byte(validWorkspace))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Metadata.Name != "engineering" || len(doc.Spec.Sources) != 1 ||
		len(doc.Spec.Projects) != 1 || len(doc.Spec.Teams) != 2 {
		t.Fatalf("parsed workspace lost nested declarations: %+v", doc)
	}
	project := doc.Spec.Projects[0]
	github := project.Integrations["github"]
	if project.Path != "/home/alice/src/api" ||
		github["gh"].Credentials["token"].SecretRef != "github-cli" ||
		github["git"].Credentials["sshKey"].SecretRef != "github-ssh" {
		t.Fatalf("project fields were not parsed: %+v", project)
	}
	if got := doc.Spec.Teams[0].ProjectTeam.Metadata.Name; got != "api-dev" {
		t.Fatalf("team document metadata.name = %q", got)
	}
	if got := doc.Spec.Teams[1]; got.WorkspaceCell == nil || got.Role != "orchestrator" {
		t.Fatalf("workspace team = %+v", got)
	}
	if doc.Status.Configs != 3 || doc.Status.Blueprints != 2 || doc.Status.RunningCells != 1 {
		t.Fatalf("workspace status counts were not parsed: %+v", doc.Status)
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
			got.Spec.Sources[0].Path == "model1" &&
			got.Spec.Projects[0].Integrations["github"]["git"].Credentials["sshKey"].SecretRef == "github-ssh" &&
			got.Spec.Teams[0].ProjectTeam.Spec.Roles[0].Ref == "dev" &&
			got.Spec.Teams[1].WorkspaceCell.Blueprint == "workspace-orchestrator" &&
			got.Status.Configs == 3
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
		{"missing source ref", strings.Replace(validWorkspace, "ref: v2.1.0, ", "", 1), "sources[0].ref"},
		{"absolute source path", strings.Replace(validWorkspace, "path: model1", "path: /etc", 1), "repository-relative"},
		{"relative project path", strings.Replace(validWorkspace, "/home/alice/src/api", "src/api", 1), "absolute host-local"},
		{"missing ProjectTeam apiVersion", strings.Replace(validWorkspace, "apiVersion: kuketeams.io/v1\n      kind: ProjectTeam", "apiVersion: \"\"\n      kind: ProjectTeam", 1), "ProjectTeam document schema"},
		{"missing secret ref", strings.Replace(validWorkspace, "secretRef: github-cli", "secretRef: \"\"", 1), "credentials.token.secretRef"},
		{"workspace team missing role", strings.Replace(validWorkspace, "      role: orchestrator\n", "", 1), "spec.teams[1].role"},
		{
			"duplicate project",
			strings.Replace(validWorkspace, "  teams:", "    - {name: api, repo: github.com/acme/other, path: /tmp/other}\n  teams:", 1),
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
	doc.Spec.Teams[0].Project = "not-yet-resolved"
	doc.Spec.Teams[0].ProjectTeam.Metadata.Name = "unresolved-local-team"
	if validationErr := Validate(doc); validationErr != nil {
		t.Fatalf("structural validation resolved semantic references: %v", validationErr)
	}
}

func TestWorkspaceTeamAgentSourceIsOptional(t *testing.T) {
	t.Parallel()
	doc, err := Parse([]byte(validWorkspace))
	if err != nil {
		t.Fatal(err)
	}
	doc.Spec.Teams[1].AgentSource = ""
	if validationErr := Validate(doc); validationErr != nil {
		t.Fatalf("workspace team should allow an omitted agentSource: %v", validationErr)
	}
}
