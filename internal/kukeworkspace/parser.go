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

// Package kukeworkspace parses Workspace manifests. Parsing and validation are
// pure operations; they do not read repositories, resolve credentials, or
// initialize cells.
package kukeworkspace

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	model "github.com/eminwux/kukeon/pkg/api/model/kukeworkspace"
	"gopkg.in/yaml.v3"
)

// Parse decodes and structurally validates one Workspace manifest.
func Parse(raw []byte) (*model.Workspace, error) {
	var doc model.Workspace
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%w: decode YAML: %w", errdefs.ErrWorkspaceManifestInvalid, err)
	}
	if doc.APIVersion != model.APIVersionV1 {
		return nil, fmt.Errorf(
			"%w: apiVersion %q; expected %q",
			errdefs.ErrWorkspaceManifestInvalid,
			doc.APIVersion,
			model.APIVersionV1,
		)
	}
	if doc.Kind != model.KindWorkspace {
		return nil, fmt.Errorf(
			"%w: kind %q; expected %q",
			errdefs.ErrWorkspaceManifestInvalid,
			doc.Kind,
			model.KindWorkspace,
		)
	}
	if err := Validate(&doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

// Validate checks fields that can be checked without resolving repositories,
// credentials, project identities, or role definitions.
func Validate(doc *model.Workspace) error {
	if doc == nil {
		return invalid("document", "is required")
	}
	if strings.TrimSpace(doc.Metadata.Name) == "" {
		return invalid("metadata.name", "is required")
	}
	if len(doc.Spec.AgentSources) == 0 {
		return invalid("spec.agentSources", "must contain at least one source")
	}
	if len(doc.Spec.Projects) == 0 {
		return invalid("spec.projects", "must contain at least one project")
	}
	if len(doc.Spec.ProjectTeams) == 0 {
		return invalid("spec.projectTeams", "must contain at least one team")
	}
	if err := validateAgentSources(doc.Spec.AgentSources); err != nil {
		return err
	}
	if err := validateProjects(doc.Spec.Projects); err != nil {
		return err
	}
	if err := validateProjectTeams(doc.Spec.ProjectTeams); err != nil {
		return err
	}
	return validateWorkspaceCells(doc.Spec.WorkspaceCells)
}

func validateAgentSources(sources []model.WorkspaceAgentSource) error {
	if err := uniqueNames("spec.agentSources", sourceNames(sources)); err != nil {
		return err
	}
	for i, source := range sources {
		prefix := fmt.Sprintf("spec.agentSources[%d]", i)
		if err := required(prefix, "name", source.Name); err != nil {
			return err
		}
		if err := required(prefix, "repo", source.Repo); err != nil {
			return err
		}
		if err := required(prefix, "ref", source.Ref); err != nil {
			return err
		}
		if err := required(prefix, "path", source.Path); err != nil {
			return err
		}
		clean := filepath.Clean(source.Path)
		if filepath.IsAbs(source.Path) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return invalid(prefix+".path", "must be repository-relative and stay within the source")
		}
	}
	return nil
}

func validateProjects(projects []model.WorkspaceProject) error {
	if err := uniqueNames("spec.projects", projectNames(projects)); err != nil {
		return err
	}
	for i, project := range projects {
		prefix := fmt.Sprintf("spec.projects[%d]", i)
		if err := required(prefix, "name", project.Name); err != nil {
			return err
		}
		if err := required(prefix, "repo", project.Repo); err != nil {
			return err
		}
		if err := required(prefix, "path", project.Path); err != nil {
			return err
		}
		if !filepath.IsAbs(project.Path) {
			return invalid(prefix+".path", "must be an absolute host-local checkout path")
		}
		for name, integration := range project.Integrations {
			if strings.TrimSpace(name) == "" || strings.TrimSpace(integration.Type) == "" {
				return invalid(prefix+".integrations", "each integration requires a name and type")
			}
			for credential, reference := range integration.Credentials {
				if strings.TrimSpace(credential) == "" || strings.TrimSpace(reference) == "" {
					return invalid(
						prefix+".integrations."+name+".credentials",
						"credential names and references must be non-empty",
					)
				}
			}
		}
	}
	return nil
}

func validateProjectTeams(teams []model.WorkspaceProjectTeam) error {
	if err := uniqueNames("spec.projectTeams", teamNames(teams)); err != nil {
		return err
	}
	for i, team := range teams {
		prefix := fmt.Sprintf("spec.projectTeams[%d]", i)
		if err := required(prefix, "name", team.Name); err != nil {
			return err
		}
		if err := required(prefix, "project", team.Project); err != nil {
			return err
		}
		if err := required(prefix, "agentSource", team.AgentSource); err != nil {
			return err
		}
		if len(team.Roles) == 0 {
			return invalid(prefix+".roles", "must explicitly select at least one role")
		}
		if err := uniqueNames(prefix+".roles", team.Roles); err != nil {
			return err
		}
		for j, role := range team.Roles {
			if strings.TrimSpace(role) == "" {
				return invalid(fmt.Sprintf("%s.roles[%d]", prefix, j), "is required")
			}
		}
	}
	return nil
}

func validateWorkspaceCells(cells []model.WorkspaceCell) error {
	if err := uniqueNames("spec.workspaceCells", cellNames(cells)); err != nil {
		return err
	}
	for i, cell := range cells {
		prefix := fmt.Sprintf("spec.workspaceCells[%d]", i)
		if err := required(prefix, "name", cell.Name); err != nil {
			return err
		}
		if err := required(prefix, "blueprint", cell.Blueprint); err != nil {
			return err
		}
	}
	return nil
}

func invalid(field, reason string) error {
	return fmt.Errorf("%w: %s %s", errdefs.ErrWorkspaceManifestInvalid, field, reason)
}

func required(prefix, field, value string) error {
	if strings.TrimSpace(value) == "" {
		return invalid(prefix+"."+field, "is required")
	}
	return nil
}

func uniqueNames(field string, names []string) error {
	seen := make(map[string]struct{}, len(names))
	for i, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			return invalid(fmt.Sprintf("%s[%d]", field, i), "name is required")
		}
		if _, ok := seen[name]; ok {
			return invalid(field, fmt.Sprintf("contains duplicate name %q", name))
		}
		seen[name] = struct{}{}
	}
	return nil
}

func sourceNames(items []model.WorkspaceAgentSource) []string {
	out := make([]string, len(items))
	for i := range items {
		out[i] = items[i].Name
	}
	return out
}

func projectNames(items []model.WorkspaceProject) []string {
	out := make([]string, len(items))
	for i := range items {
		out[i] = items[i].Name
	}
	return out
}

func teamNames(items []model.WorkspaceProjectTeam) []string {
	out := make([]string, len(items))
	for i := range items {
		out[i] = items[i].Name
	}
	return out
}

func cellNames(items []model.WorkspaceCell) []string {
	out := make([]string, len(items))
	for i := range items {
		out[i] = items[i].Name
	}
	return out
}
