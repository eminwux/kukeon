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
    - name: api
      repo: github.com/acme/api
      ref: main
  credentials:
    - {name: github-cli, secretRef: cli-token}
    - name: github-ssh
      secretRef: ssh-key
      volumeMount: {target: /home/agent/.ssh/id_ed25519, readOnly: true}
  teams:
    - name: api-dev
      sources: [{name: api, path: api}]
      cwd: api
      integrations: {gh: {credentials: [github-cli]}}
      harnesses: [{name: claude, source: house, path: harnesses/claude, default: true}]
      agents: [{name: dev, source: house, path: dev, harness: claude}]
    - name: orchestrator
      cwd: $HOME
      integrations: {gh: {credentials: [github-cli]}}
      harnesses: [{name: claude, source: house, path: harnesses/claude, default: true}]
      agents: [{name: root, source: house, path: orchestrator}]
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
	if len(doc.Spec.Sources) != 3 || len(doc.Spec.Teams) != 3 {
		t.Fatalf("documented example missing declarations: %+v", doc.Spec)
	}
	if len(doc.Spec.Teams[2].Sources) != 0 || doc.Spec.Teams[2].Agents[0].Name != "root" {
		t.Fatalf("documented orchestrator should have only its agent role: %+v", doc.Spec.Teams[2])
	}
}

func TestParseWorkspace(t *testing.T) {
	t.Parallel()
	doc, err := Parse([]byte(validWorkspace))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Metadata.Name != "engineering" || len(doc.Spec.Sources) != 2 || len(doc.Spec.Teams) != 2 {
		t.Fatalf("parsed workspace lost nested declarations: %+v", doc)
	}
	if len(doc.Spec.Teams[0].Sources) != 1 || doc.Spec.Teams[0].Sources[0].Name != "api" ||
		doc.Spec.Teams[0].Sources[0].Path != "api" ||
		doc.Spec.Teams[0].CWD != "api" ||
		doc.Spec.Teams[0].Integrations["gh"].Credentials[0] != "github-cli" ||
		doc.Spec.Teams[0].Harnesses[0].Name != "claude" ||
		doc.Spec.Credentials[1].VolumeMount.Target != "/home/agent/.ssh/id_ed25519" {
		t.Fatalf("source/team fields were not parsed: %+v", doc.Spec)
	}
	if got := doc.Spec.Teams[0].Name; got != "api-dev" {
		t.Fatalf("team name = %q", got)
	}
	if got := doc.Spec.Teams[1]; got.Agents[0].Path != "orchestrator" || len(got.Sources) != 0 || got.CWD != "$HOME" {
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
			got.Spec.Teams[0].Integrations["gh"].Credentials[0] == "github-cli" &&
			got.Spec.Teams[0].Sources[0].Name == "api" &&
			got.Spec.Teams[0].Agents[0].Source == "house" &&
			got.Spec.Teams[0].Agents[0].Path == "dev" &&
			got.Spec.Teams[1].Agents[0].Name == "root" &&
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
		{"unknown team source", strings.Replace(validWorkspace, "{name: api, path: api}", "{name: missing, path: api}", 1), "unknown source"},
		{"cwd traversal rejected", strings.Replace(validWorkspace, "cwd: api", "cwd: $HOME/../etc", 1), "must not traverse"},
		{"unknown role source", strings.Replace(validWorkspace, "source: house", "source: missing", 1), "unknown source"},
		{"duplicate agent name", strings.Replace(validWorkspace, "agents: [{name: dev, source: house, path: dev, harness: claude}]", "agents: [{name: dev, source: house, path: dev, harness: claude}, {name: dev, source: house, path: dev, harness: claude}]", 1), "duplicate name"},
		{
			"agent path required",
			strings.Replace(validWorkspace, "path: dev", "path: \"\"", 1),
			"requires source and path",
		},
		{"missing secret ref", strings.Replace(validWorkspace, "secretRef: cli-token", "secretRef: \"\"", 1), "spec.credentials[0].secretRef"},
		{"unknown credential reference", strings.Replace(validWorkspace, "credentials: [github-cli]", "credentials: [unknown]", 1), "unknown credential"},
		{"team missing agent", strings.Replace(validWorkspace, "agents: [{name: root, source: house, path: orchestrator}]", "agents: []", 1), "must contain at least one agent"},
		{
			"duplicate team",
			strings.Replace(validWorkspace, "    - name: orchestrator", "    - {name: api-dev, sources: [{name: api, path: api}], cwd: api, harnesses: [{name: claude, source: house, path: harnesses/claude}], agents: [{name: dev, source: house, path: dev}]}\n    - name: orchestrator", 1),
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

func TestValidateDoesNotResolveRepositoryIdentities(t *testing.T) {
	t.Parallel()
	doc, err := Parse([]byte(validWorkspace))
	if err != nil {
		t.Fatal(err)
	}
	doc.Spec.Teams[0].Name = "unresolved-local-team"
	if validationErr := Validate(doc); validationErr != nil {
		t.Fatalf("structural validation resolved semantic references: %v", validationErr)
	}
}

func TestWorkspaceOrchestratorCanOmitProjectSources(t *testing.T) {
	t.Parallel()
	doc, err := Parse([]byte(validWorkspace))
	if err != nil {
		t.Fatal(err)
	}
	if validationErr := Validate(doc); validationErr != nil {
		t.Fatalf("workspace orchestrator should allow no project code sources: %v", validationErr)
	}
}
