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
	teamparser "github.com/eminwux/kukeon/internal/kuketeams"
	teammodel "github.com/eminwux/kukeon/pkg/api/model/kuketeams"
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
	if len(doc.Spec.Teams) == 0 {
		return invalid("spec.teams", "must contain at least one team")
	}
	if err := validateSources(doc.Spec.Sources); err != nil {
		return err
	}
	credentialNames, err := validateCredentials(doc.Spec.Credentials)
	if err != nil {
		return err
	}
	if projectErr := validateProjects(doc.Spec.Projects, credentialNames); projectErr != nil {
		return projectErr
	}
	if err := validateTeams(doc.Spec.Teams); err != nil {
		return err
	}
	return nil
}

func validateSources(sources []model.WorkspaceSource) error {
	if err := uniqueNames("spec.sources", sourceNames(sources)); err != nil {
		return err
	}
	for i, source := range sources {
		prefix := fmt.Sprintf("spec.sources[%d]", i)
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

func validateCredentials(credentials []model.WorkspaceCredential) (map[string]struct{}, error) {
	names := make(map[string]struct{}, len(credentials))
	for i, credential := range credentials {
		prefix := fmt.Sprintf("spec.credentials[%d]", i)
		if err := required(prefix, "name", credential.Name); err != nil {
			return nil, err
		}
		if err := required(prefix, "secretRef", credential.SecretRef); err != nil {
			return nil, err
		}
		if _, ok := names[credential.Name]; ok {
			return nil, invalid("spec.credentials", fmt.Sprintf("contains duplicate name %q", credential.Name))
		}
		names[credential.Name] = struct{}{}
		if credential.VolumeMount != nil {
			if err := required(prefix+".volumeMount", "target", credential.VolumeMount.Target); err != nil {
				return nil, err
			}
			if !filepath.IsAbs(credential.VolumeMount.Target) {
				return nil, invalid(prefix+".volumeMount.target", "must be an absolute container path")
			}
		}
	}
	return names, nil
}

func validateProjects(projects []model.WorkspaceProject, credentialNames map[string]struct{}) error {
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
			integrationPrefix := prefix + ".integrations." + name
			if strings.TrimSpace(name) == "" {
				return invalid(prefix+".integrations", "integration names are required")
			}
			if len(integration) == 0 {
				return invalid(integrationPrefix, "must declare at least one client")
			}
			for clientName, client := range integration {
				clientPrefix := integrationPrefix + "." + clientName
				if strings.TrimSpace(clientName) == "" {
					return invalid(integrationPrefix, "client names are required")
				}
				for _, credential := range client.Credentials {
					if _, ok := credentialNames[credential]; !ok {
						return invalid(
							clientPrefix+".credentials",
							fmt.Sprintf("references unknown credential %q", credential),
						)
					}
				}
			}
		}
	}
	return nil
}

func validateTeams(teams []model.WorkspaceTeam) error {
	if err := uniqueNames("spec.teams", teamNames(teams)); err != nil {
		return err
	}
	for i, team := range teams {
		prefix := fmt.Sprintf("spec.teams[%d]", i)
		projectTeam := teammodel.ProjectTeam{
			APIVersion: teammodel.APIVersionV1,
			Kind:       teammodel.KindProjectTeam,
			Metadata:   team.Metadata,
			Spec:       team.Spec,
		}
		teamYAML, marshalErr := yaml.Marshal(projectTeam)
		if marshalErr != nil {
			return invalid(prefix, "must contain a valid ProjectTeam document")
		}
		if _, parseErr := teamparser.Parse(teamYAML); parseErr != nil {
			return invalid(prefix, "must comply with the ProjectTeam document schema: "+parseErr.Error())
		}
		if fieldErr := required(prefix+".metadata", "name", team.Metadata.Name); fieldErr != nil {
			return fieldErr
		}
		if team.Project != "" {
			if team.Spec.WorkspaceCell != nil || team.AgentSource != "" {
				return invalid(prefix, "project teams use project and an inline ProjectTeam document")
			}
			continue
		}
		if team.Spec.WorkspaceCell == nil {
			return invalid(prefix, "requires project or workspaceCell")
		}
		if len(team.Spec.Roles) == 0 {
			return invalid(prefix+".spec.roles", "must contain at least one role for a workspace cell team")
		}
		if len(team.Spec.WorkspaceCell.Configs) == 0 {
			return invalid(prefix+".spec.workspaceCell.configs", "must contain at least one cell config")
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

func sourceNames(items []model.WorkspaceSource) []string {
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

func teamNames(items []model.WorkspaceTeam) []string {
	out := make([]string, len(items))
	for i := range items {
		out[i] = items[i].Metadata.Name
	}
	return out
}
