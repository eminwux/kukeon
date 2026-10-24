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

import "github.com/eminwux/kukeon/pkg/api/model/kuketeams"

// Workspace is a user-owned manifest that combines projects, their teams, and
// cells shared across the workspace.
type Workspace struct {
	APIVersion string            `json:"apiVersion"       yaml:"apiVersion"`
	Kind       string            `json:"kind"             yaml:"kind"`
	Metadata   WorkspaceMetadata `json:"metadata"         yaml:"metadata"`
	Spec       WorkspaceSpec     `json:"spec"             yaml:"spec"`
	Status     WorkspaceStatus   `json:"status,omitempty" yaml:"status,omitempty"`
}

// WorkspaceStatus reports resources currently materialized from this
// Workspace configuration. Counts are observations, not desired values.
type WorkspaceStatus struct {
	// Configs counts Config resources resolved from this Workspace spec.
	Configs int `json:"configs"      yaml:"configs"`
	// Blueprints counts Blueprint resources resolved from this Workspace spec.
	Blueprints int `json:"blueprints"   yaml:"blueprints"`
	// RunningCells counts currently running cells materialized from those
	// configurations.
	RunningCells int `json:"runningCells" yaml:"runningCells"`
}

// WorkspaceMetadata identifies the manifest.
type WorkspaceMetadata struct {
	Name string `json:"name" yaml:"name"`
}

// WorkspaceSpec contains all workspace declarations.
type WorkspaceSpec struct {
	Sources     []WorkspaceSource     `json:"sources"     yaml:"sources"`
	Teams       []WorkspaceTeam       `json:"teams"       yaml:"teams"`
	Credentials []WorkspaceCredential `json:"credentials" yaml:"credentials"`
}

// WorkspaceSource identifies a repository revision and optional repository
// subdirectory. Sources may describe projects or agent role libraries.
type WorkspaceSource struct {
	Name string `json:"name"           yaml:"name"`
	Repo string `json:"repo"           yaml:"repo"`
	Ref  string `json:"ref"            yaml:"ref"`
	Path string `json:"path,omitempty" yaml:"path,omitempty"`
}

// WorkspaceIntegrationClient contains credentials for one client of an
// integration, for example GitHub CLI (gh) or Git authentication (git).
type WorkspaceIntegrationClient struct {
	Credentials []string `json:"credentials,omitempty" yaml:"credentials,omitempty"`
}

// WorkspaceCredential declares a named credential and its secret source.
// Secret values are never stored in the Workspace manifest.
type WorkspaceCredential struct {
	Name        string                          `json:"name"                  yaml:"name"`
	SecretRef   string                          `json:"secretRef"             yaml:"secretRef"`
	VolumeMount *WorkspaceCredentialVolumeMount `json:"volumeMount,omitempty" yaml:"volumeMount,omitempty"`
}

// WorkspaceCredentialVolumeMount describes where a named credential mount is
// exposed inside a container.
type WorkspaceCredentialVolumeMount struct {
	Target   string `json:"target"             yaml:"target"`
	ReadOnly bool   `json:"readOnly,omitempty" yaml:"readOnly,omitempty"`
}

// WorkspaceTeam describes a named agent team. Sources are project-code
// checkouts; role-level sources select role definitions. Integrations and
// harnesses are scoped to this team, and CWD is passed to the harness.
type WorkspaceTeam struct {
	Name         string                                `json:"name"                   yaml:"name"`
	Sources      []WorkspaceTeamSource                 `json:"sources,omitempty"      yaml:"sources,omitempty"`
	CWD          string                                `json:"cwd"                    yaml:"cwd"`
	Integrations map[string]WorkspaceIntegrationClient `json:"integrations,omitempty" yaml:"integrations,omitempty"`
	Harnesses    []WorkspaceHarness                    `json:"harnesses"              yaml:"harnesses"`
	Agents       []WorkspaceAgent                      `json:"agents"                 yaml:"agents"`
}

// WorkspaceHarness selects a harness implementation from a workspace source.
type WorkspaceHarness struct {
	Name    string `json:"name"              yaml:"name"`
	Source  string `json:"source"            yaml:"source"`
	Path    string `json:"path"              yaml:"path"`
	Default bool   `json:"default,omitempty" yaml:"default,omitempty"`
}

// WorkspaceAgent selects an agent role from a workspace source.
type WorkspaceAgent struct {
	Name    string                      `json:"name"              yaml:"name"`
	Source  string                      `json:"source"            yaml:"source"`
	Path    string                      `json:"path"              yaml:"path"`
	Harness string                      `json:"harness,omitempty" yaml:"harness,omitempty"`
	Needs   *kuketeams.ProjectRoleNeeds `json:"needs,omitempty"   yaml:"needs,omitempty"`
}

// WorkspaceTeamSource references a workspace source and selects its checkout
// directory inside the team's container.
type WorkspaceTeamSource struct {
	Name string `json:"name" yaml:"name"`
	Path string `json:"path" yaml:"path"`
}
