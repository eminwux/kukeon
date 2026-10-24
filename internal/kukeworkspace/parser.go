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
	credentialNames, err := validateCredentials(doc.Spec.Credentials)
	if err != nil {
		return err
	}
	sources, err := validateSources(doc.Spec.Sources)
	if err != nil {
		return err
	}
	if teamsErr := validateTeams(doc.Spec.Teams, sources, credentialNames); teamsErr != nil {
		return teamsErr
	}
	return nil
}

func validateSources(
	sources []model.WorkspaceSource,
) (map[string]model.WorkspaceSource, error) {
	if err := uniqueNames("spec.sources", sourceNames(sources)); err != nil {
		return nil, err
	}
	byName := make(map[string]model.WorkspaceSource, len(sources))
	for i, source := range sources {
		prefix := fmt.Sprintf("spec.sources[%d]", i)
		if err := required(prefix, "name", source.Name); err != nil {
			return nil, err
		}
		if err := required(prefix, "repo", source.Repo); err != nil {
			return nil, err
		}
		if err := required(prefix, "ref", source.Ref); err != nil {
			return nil, err
		}
		if source.Path != "" {
			clean := filepath.Clean(source.Path)
			if filepath.IsAbs(source.Path) || clean == ".." ||
				strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
				return nil, invalid(prefix+".path", "must be repository-relative and stay within the source")
			}
		}
		byName[source.Name] = source
	}
	return byName, nil
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

func validateIntegrations(
	prefix string,
	integrations map[string]model.WorkspaceIntegration,
	credentialNames map[string]struct{},
) error {
	for name, integration := range integrations {
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
	return nil
}

func validateTeams(
	teams []model.WorkspaceTeam,
	sources map[string]model.WorkspaceSource,
	credentialNames map[string]struct{},
) error {
	if err := uniqueNames("spec.teams", teamNames(teams)); err != nil {
		return err
	}
	for i, team := range teams {
		prefix := fmt.Sprintf("spec.teams[%d]", i)
		if err := required(prefix, "name", team.Name); err != nil {
			return err
		}
		if err := required(prefix, "cwd", team.CWD); err != nil {
			return err
		}
		if !filepath.IsAbs(team.CWD) {
			return invalid(prefix+".cwd", "must be an absolute working directory")
		}
		if team.Config == nil {
			return invalid(prefix+".config", "is required")
		}
		for j, sourceName := range team.Sources {
			if _, ok := sources[sourceName]; !ok {
				return invalid(
					fmt.Sprintf("%s.sources[%d]", prefix, j),
					fmt.Sprintf("references unknown source %q", sourceName),
				)
			}
		}
		if err := validateIntegrations(prefix+".config", team.Config.Integrations, credentialNames); err != nil {
			return err
		}
		projectTeamSpec := team.Spec
		for j, role := range team.Spec.Roles {
			rolePrefix := fmt.Sprintf("%s.spec.roles[%d]", prefix, j)
			if role.Source == "" || role.Path == "" {
				return invalid(rolePrefix, "requires source and path")
			}
			source, ok := sources[role.Source]
			if !ok {
				return invalid(rolePrefix+".source", fmt.Sprintf("references unknown source %q", role.Source))
			}
			if j == 0 {
				projectTeamSpec.Source = teammodel.TeamSource{Repo: source.Repo, Ref: source.Ref}
			}
		}
		if len(team.Sources) > 0 && team.Spec.WorkspaceCell != nil {
			return invalid(prefix, "project teams use sources and inline ProjectTeam spec")
		}
		if len(team.Sources) == 0 {
			if team.Spec.WorkspaceCell == nil {
				return invalid(prefix, "requires sources or workspaceCell")
			}
			if len(team.Spec.Roles) == 0 {
				return invalid(prefix+".spec.roles", "workspaceCell requires at least one role")
			}
			if len(team.Spec.WorkspaceCell.Configs) == 0 {
				return invalid(prefix+".spec.workspaceCell.configs", "must contain at least one cell config")
			}
		}
		metadata := teammodel.Metadata{Name: team.Name}
		projectTeam := teammodel.ProjectTeam{
			APIVersion: teammodel.APIVersionV1,
			Kind:       teammodel.KindProjectTeam,
			Metadata:   metadata,
			Spec:       projectTeamSpec,
		}
		teamYAML, marshalErr := yaml.Marshal(projectTeam)
		if marshalErr != nil {
			return invalid(prefix, "must contain a valid ProjectTeam document")
		}
		if _, parseErr := teamparser.Parse(teamYAML); parseErr != nil {
			return invalid(prefix, "must comply with the ProjectTeam document schema: "+parseErr.Error())
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

func teamNames(items []model.WorkspaceTeam) []string {
	out := make([]string, len(items))
	for i := range items {
		out[i] = items[i].Name
	}
	return out
}
