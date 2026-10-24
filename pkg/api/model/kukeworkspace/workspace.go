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
	Sources  []WorkspaceSource  `json:"sources"  yaml:"sources"`
	Projects []WorkspaceProject `json:"projects" yaml:"projects"`
	Teams    []WorkspaceTeam    `json:"teams"    yaml:"teams"`
}

// WorkspaceSource identifies a repository and path containing role
// definitions. Ref is a pinned Git tag or commit; Path is repository-relative.
type WorkspaceSource struct {
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

// WorkspaceIntegration maps integration clients (such as GitHub's gh and git
// tools) to their credential sets. The map key names the integration, so no
// redundant type field is needed.
type WorkspaceIntegration map[string]WorkspaceIntegrationClient

// WorkspaceIntegrationClient contains credentials for one client of an
// integration, for example GitHub CLI (gh) or Git authentication (git).
type WorkspaceIntegrationClient struct {
	Credentials map[string]WorkspaceCredentialRef `json:"credentials,omitempty" yaml:"credentials,omitempty"`
}

// WorkspaceCredentialRef names a credential configured in a secret source.
// Secret values are never stored in the Workspace manifest.
type WorkspaceCredentialRef struct {
	SecretRef string `json:"secretRef" yaml:"secretRef"`
}

// WorkspaceTeam embeds a complete ProjectTeam document. The ProjectTeam GVK,
// metadata, and spec are declared inline in the Workspace document; project
// identifies the project checkout that owns this roster. WorkspaceCell marks
// a workspace-scoped team, for which Role is the selected role and AgentSource
// optionally selects a source declared in WorkspaceSpec.Sources.
type WorkspaceTeam struct {
	kuketeams.ProjectTeam `json:",inline" yaml:",inline"`

	Project string `json:"project,omitempty"       yaml:"project,omitempty"`
	// AgentSource is an optional source name for a workspace-scoped team.
	AgentSource   string         `json:"agentSource,omitempty"   yaml:"agentSource,omitempty"`
	Role          string         `json:"role,omitempty"          yaml:"role,omitempty"`
	WorkspaceCell *WorkspaceCell `json:"workspaceCell,omitempty" yaml:"workspaceCell,omitempty"`
}

// WorkspaceCell describes a cell owned by a workspace team.
type WorkspaceCell struct {
	Blueprint string `json:"blueprint" yaml:"blueprint"`
}
