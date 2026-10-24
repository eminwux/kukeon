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
	integrations map[string]model.WorkspaceIntegrationClient,
	credentialNames map[string]struct{},
) error {
	for clientName, client := range integrations {
		clientPrefix := prefix + ".integrations." + clientName
		if strings.TrimSpace(clientName) == "" {
			return invalid(prefix+".integrations", "client names are required")
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
		if err := validateWorkingDirectory(prefix+".cwd", team.CWD); err != nil {
			return err
		}
		if len(team.Harnesses) == 0 {
			return invalid(prefix+".harnesses", "must contain at least one harness")
		}
		harnessNames := make(map[string]struct{}, len(team.Harnesses))
		projectTeamSpec := teammodel.ProjectTeamSpec{}
		defaultHarnesses := 0
		for j, harness := range team.Harnesses {
			harnessPrefix := fmt.Sprintf("%s.harnesses[%d]", prefix, j)
			if err := required(harnessPrefix, "name", harness.Name); err != nil {
				return err
			}
			if _, exists := harnessNames[harness.Name]; exists {
				return invalid(prefix+".harnesses", fmt.Sprintf("contains duplicate name %q", harness.Name))
			}
			harnessNames[harness.Name] = struct{}{}
			if err := required(harnessPrefix, "source", harness.Source); err != nil {
				return err
			}
			if err := required(harnessPrefix, "path", harness.Path); err != nil {
				return err
			}
			if err := validateRelativePath(harnessPrefix+".path", harness.Path); err != nil {
				return err
			}
			if _, ok := sources[harness.Source]; !ok {
				return invalid(harnessPrefix+".source", fmt.Sprintf("references unknown source %q", harness.Source))
			}
			if !teammodel.IsKnownHarness(harness.Name) {
				return invalid(harnessPrefix+".name", fmt.Sprintf("unknown harness %q", harness.Name))
			}
			if harness.Default {
				defaultHarnesses++
				projectTeamSpec.Defaults.Harnesses = append(projectTeamSpec.Defaults.Harnesses, harness.Name)
			}
		}
		if defaultHarnesses > 1 {
			return invalid(prefix+".harnesses", "must not contain more than one default harness")
		}
		for j, sourceRef := range team.Sources {
			sourcePrefix := fmt.Sprintf("%s.sources[%d]", prefix, j)
			if err := required(sourcePrefix, "name", sourceRef.Name); err != nil {
				return err
			}
			if err := required(sourcePrefix, "path", sourceRef.Path); err != nil {
				return err
			}
			if err := validateRelativePath(sourcePrefix+".path", sourceRef.Path); err != nil {
				return err
			}
			if _, ok := sources[sourceRef.Name]; !ok {
				return invalid(
					sourcePrefix+".name",
					fmt.Sprintf("references unknown source %q", sourceRef.Name),
				)
			}
		}
		if err := validateIntegrations(prefix, team.Integrations, credentialNames); err != nil {
			return err
		}
		if len(team.Agents) == 0 {
			return invalid(prefix+".agents", "must contain at least one agent")
		}
		seenAgents := make(map[string]struct{}, len(team.Agents))
		for j, agent := range team.Agents {
			agentPrefix := fmt.Sprintf("%s.agents[%d]", prefix, j)
			if err := required(agentPrefix, "name", agent.Name); err != nil {
				return err
			}
			if _, exists := seenAgents[agent.Name]; exists {
				return invalid(prefix+".agents", fmt.Sprintf("contains duplicate name %q", agent.Name))
			}
			seenAgents[agent.Name] = struct{}{}
			if agent.Source == "" || agent.Path == "" {
				return invalid(agentPrefix, "requires source and path")
			}
			source, ok := sources[agent.Source]
			if !ok {
				return invalid(agentPrefix+".source", fmt.Sprintf("references unknown source %q", agent.Source))
			}
			if err := validateRelativePath(agentPrefix+".path", agent.Path); err != nil {
				return err
			}
			if agent.Harness != "" {
				if _, configured := harnessNames[agent.Harness]; !configured {
					return invalid(agentPrefix+".harness", fmt.Sprintf("references unknown harness %q", agent.Harness))
				}
			}
			if j == 0 {
				projectTeamSpec.Source = teammodel.TeamSource{Repo: source.Repo, Ref: source.Ref}
			}
			projectTeamSpec.Roles = append(projectTeamSpec.Roles, teammodel.ProjectTeamRole{
				Name: agent.Name, Source: agent.Source, Path: agent.Path, Needs: agent.Needs,
			})
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

func validateWorkingDirectory(field, cwd string) error {
	if filepath.IsAbs(cwd) || cwd == "$HOME" || cwd == "~" {
		return nil
	}
	if strings.HasPrefix(cwd, "$HOME/") {
		return validateRelativePath(field, strings.TrimPrefix(cwd, "$HOME/"))
	}
	if strings.HasPrefix(cwd, "~/") {
		return validateRelativePath(field, strings.TrimPrefix(cwd, "~/"))
	}
	return validateRelativePath(field, cwd)
}

func validateRelativePath(field, value string) error {
	if filepath.IsAbs(value) {
		return invalid(field, "must be relative")
	}
	for _, segment := range strings.Split(filepath.ToSlash(value), "/") {
		if segment == ".." {
			return invalid(field, "must not traverse outside its base directory")
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
