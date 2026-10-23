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

// Workspace is a user-owned manifest that combines projects, their teams, and
// cells shared across the workspace.
type Workspace struct {
	APIVersion string            `json:"apiVersion" yaml:"apiVersion"`
	Kind       string            `json:"kind"       yaml:"kind"`
	Metadata   WorkspaceMetadata `json:"metadata"   yaml:"metadata"`
	Spec       WorkspaceSpec     `json:"spec"       yaml:"spec"`
}

// WorkspaceMetadata identifies the manifest.
type WorkspaceMetadata struct {
	Name string `json:"name" yaml:"name"`
}

// WorkspaceSpec contains all workspace declarations.
type WorkspaceSpec struct {
	AgentSources   []WorkspaceAgentSource `json:"agentSources"   yaml:"agentSources"`
	Projects       []WorkspaceProject     `json:"projects"       yaml:"projects"`
	ProjectTeams   []WorkspaceProjectTeam `json:"projectTeams"   yaml:"projectTeams"`
	WorkspaceCells []WorkspaceCell        `json:"workspaceCells" yaml:"workspaceCells"`
}

// WorkspaceAgentSource identifies a repository and path containing role
// definitions. Ref is a pinned Git tag or commit; Path is repository-relative.
type WorkspaceAgentSource struct {
	Name string `json:"name" yaml:"name"`
	Repo string `json:"repo" yaml:"repo"`
	Ref  string `json:"ref"  yaml:"ref"`
	Path string `json:"path" yaml:"path"`
}

// WorkspaceProject identifies a code repository and its host-local checkout.
type WorkspaceProject struct {
	Name         string                          `json:"name"                   yaml:"name"`
	Repo         string                          `json:"repo"                   yaml:"repo"`
	Path         string                          `json:"path"                   yaml:"path"`
	Integrations map[string]WorkspaceIntegration `json:"integrations,omitempty" yaml:"integrations,omitempty"`
}

// WorkspaceIntegration describes an external project integration and names
// credentials by reference. Credential values are never part of this model.
type WorkspaceIntegration struct {
	Type        string            `json:"type"                  yaml:"type"`
	Credentials map[string]string `json:"credentials,omitempty" yaml:"credentials,omitempty"`
}

// WorkspaceProjectTeam selects a project, agent source, and explicit roles.
type WorkspaceProjectTeam struct {
	Name        string   `json:"name"        yaml:"name"`
	Project     string   `json:"project"     yaml:"project"`
	AgentSource string   `json:"agentSource" yaml:"agentSource"`
	Roles       []string `json:"roles"       yaml:"roles"`
}

// WorkspaceCell describes a cell whose scope is the workspace. Blueprint is
// the name of the cell blueprint used to initialize it.
type WorkspaceCell struct {
	Name      string `json:"name"      yaml:"name"`
	Blueprint string `json:"blueprint" yaml:"blueprint"`
}
