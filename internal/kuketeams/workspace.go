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

package kuketeams

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"unicode"

	model "github.com/eminwux/kukeon/pkg/api/model/kuketeams"
	"gopkg.in/yaml.v3"
)

// reservedAgentsDir matches the existing renderer checkout slot.
const reservedAgentsDir = "agents"

// ParseWorkspace parses only the new desired Workspace kind. Legacy operator
// documents are rejected, never reinterpreted by shape.
func ParseWorkspace(raw []byte) (*model.Workspace, error) {
	if err := configurationKind(raw, model.KindWorkspace); err != nil {
		return nil, err
	}
	var w model.Workspace
	if err := decodeConfiguration(raw, &w); err != nil {
		return nil, err
	}
	if err := validateWorkspace(&w); err != nil {
		return nil, err
	}
	return &w, nil
}

// ParseTeam validates a Team declaration's local structure. Missing selections
// are inheritance requests; ResolveDefaults checks that they become explicit.
func ParseTeam(raw []byte) (*model.Team, error) {
	if err := configurationKind(raw, model.KindTeam); err != nil {
		return nil, err
	}
	var team model.Team
	if err := decodeConfiguration(raw, &team); err != nil {
		return nil, err
	}
	if err := validateTeam(&team); err != nil {
		return nil, err
	}
	return &team, nil
}

func configurationKind(raw []byte, kind string) error {
	var h header
	if err := yaml.Unmarshal(raw, &h); err != nil {
		return fieldError("document", "malformed header")
	}
	if h.APIVersion != model.APIVersionV1 {
		return fieldError(
			"apiVersion",
			"expected kuketeams.io/v1; aggregate kukeworkspace documents require explicit splitting into Teams, one project per Team",
		)
	}
	if h.Kind != kind {
		return fieldError(
			"kind",
			"expected "+kind+"; unsupported document kinds are not converted",
		)
	}
	return nil
}

func validateWorkspace(w *model.Workspace) error {
	if err := validateOperator(w.Spec); err != nil {
		return err
	}
	if err := validateSelections(w.Spec.Defaults, "spec.defaults"); err != nil {
		return err
	}
	seen := make(map[string]bool)
	for i, binding := range w.Spec.Credentials {
		field := fmt.Sprintf("spec.credentials[%d]", i)
		if err := uniqueName(binding.Name, field+".name", seen); err != nil {
			return err
		}
		if err := safeName(binding.SecretRef, field+".secretRef"); err != nil {
			return err
		}
		if binding.VolumeMount != nil {
			target := binding.VolumeMount.Target
			if !strings.HasPrefix(target, "/") || target == "/" || !safePath(target, true) {
				return fieldError(
					field+".volumeMount.target",
					"expected an absolute container path without traversal or expansion",
				)
			}
		}
	}
	return nil
}

func validateOperator(spec model.WorkspaceSpec) error {
	if spec.Git != nil {
		if err := validateOperatorGit(spec.Git); err != nil {
			return err
		}
	}
	for name, secret := range spec.Secrets {
		field := "spec.secrets." + name
		if err := safeName(name, field); err != nil {
			return err
		}
		if secret.From != model.SecretFromEnv && secret.From != model.SecretFromFile {
			return fieldError(field+".from", "expected env or file; inline values are unsupported")
		}
		if strings.TrimSpace(secret.Key) == "" {
			return fieldError(field+".key", "required")
		}
	}
	for key, transport := range spec.Sources {
		if err := validateRepo(key); err != nil {
			return fieldError("spec.sources."+key, "expected a repository identity")
		}
		if strings.TrimSpace(transport) == "" {
			return fieldError("spec.sources."+key, "transport clone URL is required")
		}
	}
	return nil
}

func validateOperatorGit(git *model.WorkspaceGit) error {
	if git.Author != nil {
		if strings.TrimSpace(git.Author.Name) == "" {
			return fieldError("spec.git.author.name", "required")
		}
		if strings.TrimSpace(git.Author.Email) == "" {
			return fieldError("spec.git.author.email", "required")
		}
	}
	if git.Committer != nil {
		if strings.TrimSpace(git.Committer.Name) == "" {
			return fieldError("spec.git.committer.name", "required")
		}
		if strings.TrimSpace(git.Committer.Email) == "" {
			return fieldError("spec.git.committer.email", "required")
		}
	}
	if len(git.Sign) > 0 && strings.TrimSpace(git.SigningKey) == "" {
		return fieldError("spec.git.signingKey", "required when signing is enabled")
	}
	for i, target := range git.Sign {
		if target != model.GitSignCommits && target != model.GitSignTags {
			return fieldError(fmt.Sprintf("spec.git.sign[%d]", i), "expected commits or tags")
		}
	}
	return nil
}

func validateTeam(t *model.Team) error {
	if err := safeName(t.Metadata.Name, "metadata.name"); err != nil {
		return err
	}
	if t.Spec.Repo != nil {
		if err := validateRepository(*t.Spec.Repo, "spec.repo", false); err != nil {
			return err
		}
	}
	if t.Spec.CloneDir != "" {
		if t.Spec.Repo == nil {
			return fieldError("spec.cloneDir", "requires spec.repo")
		}
		if err := safeName(t.Spec.CloneDir, "spec.cloneDir"); err != nil {
			return err
		}
		if t.Spec.CloneDir == reservedAgentsDir {
			return fieldError("spec.cloneDir", "collides with reserved agents checkout; choose another basename")
		}
	} else if t.Spec.Repo != nil && t.Metadata.Name == reservedAgentsDir {
		return fieldError("spec.cloneDir", "required when the default Team name collides with the agents checkout")
	}
	if t.Spec.Cwd != "" && !safeHomePath(t.Spec.Cwd) {
		return fieldError("spec.cwd", "expected a relative, absolute, ~/ or $HOME/ container path without traversal")
	}
	if t.Spec.StateDir != "" && (!safeHomePath(t.Spec.StateDir) ||
		(!strings.HasPrefix(t.Spec.StateDir, "/") && !strings.HasPrefix(t.Spec.StateDir, "~/") && !strings.HasPrefix(t.Spec.StateDir, "$HOME/"))) {
		return fieldError("spec.stateDir", "expected an absolute, ~/ or $HOME/ host state path without traversal")
	}
	for _, coordinate := range []struct{ field, value string }{
		{"realm", t.Spec.Realm}, {"space", t.Spec.Space}, {"stack", t.Spec.Stack},
	} {
		if coordinate.value != "" {
			if err := safeName(coordinate.value, "spec."+coordinate.field); err != nil {
				return err
			}
		}
	}
	return validateSelections(t.Spec.TeamSelections, "spec")
}

func validateSelections(s model.TeamSelections, field string) error {
	if s.Source != nil {
		if err := validateDefinition(*s.Source, field+".source"); err != nil {
			return err
		}
	}
	if s.Roles != nil {
		seen := make(map[string]bool)
		for i, role := range *s.Roles {
			f := fmt.Sprintf("%s.roles[%d]", field, i)
			if err := validateSelectedRole(role, f, seen); err != nil {
				return err
			}
		}
	}
	if s.Harnesses != nil {
		if err := validateSelectedHarnesses(*s.Harnesses, field+".harnesses"); err != nil {
			return err
		}
	}
	if s.Modes != nil {
		seen := make(map[string]bool)
		for i, mode := range *s.Modes {
			f := fmt.Sprintf("%s.modes[%d]", field, i)
			if mode != "interactive" && mode != "headless" {
				return fieldError(f, "expected interactive or headless")
			}
			if err := uniqueName(mode, f, seen); err != nil {
				return err
			}
		}
	}
	if s.Integrations != nil {
		for client, grant := range *s.Integrations {
			f := field + ".integrations." + client
			if err := safeName(client, f); err != nil {
				return err
			}
			if grant.Credentials == nil {
				return fieldError(f+".credentials", "required; use [] to disable grants")
			}
			seen := make(map[string]bool)
			for i, name := range grant.Credentials {
				if err := uniqueName(name, fmt.Sprintf("%s.credentials[%d]", f, i), seen); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validateSelectedRole(role model.TeamRole, field string, seen map[string]bool) error {
	if err := safeName(role.Ref, field+".ref"); err != nil {
		return err
	}
	name := role.Name
	if name == "" {
		name = role.Ref
	}
	if err := uniqueName(name, field+".name", seen); err != nil {
		return err
	}
	if role.Source != nil {
		if err := validateDefinition(*role.Source, field+".source"); err != nil {
			return err
		}
	}
	if err := definitionPath(role.Path, field+".path"); err != nil {
		return err
	}
	if role.Harness != "" {
		if err := safeName(role.Harness, field+".harness"); err != nil {
			return err
		}
	}
	if role.Needs != nil {
		if err := validateCapabilityNames(role.Needs.Image, field+".needs.image"); err != nil {
			return err
		}
	}
	return nil
}

func validateSelectedHarnesses(harnesses []model.TeamHarness, field string) error {
	seen := make(map[string]bool)
	hasDefault := false
	for i, harness := range harnesses {
		f := fmt.Sprintf("%s[%d]", field, i)
		if err := uniqueName(harness.Name, f+".name", seen); err != nil {
			return err
		}
		isDefault := harness.Default != nil && *harness.Default
		if isDefault && hasDefault {
			return fieldError(f+".default", "only one default harness is allowed")
		}
		hasDefault = hasDefault || isDefault
		if harness.Source != nil {
			if err := validateDefinition(*harness.Source, f+".source"); err != nil {
				return err
			}
		}
		if err := definitionPath(harness.Path, f+".path"); err != nil {
			return err
		}
	}
	return nil
}

func validateDefinition(source model.DefinitionSource, field string) error {
	if err := validateRepository(source.TeamSource, field, true); err != nil {
		return err
	}
	return definitionPath(source.Path, field+".path")
}

func validateRepository(source model.TeamSource, field string, requireRef bool) error {
	if source.Repo != strings.TrimSpace(source.Repo) {
		return fieldError(field+".repo", "unexpected surrounding whitespace")
	}
	if err := validateRepo(source.Repo); err != nil {
		return fieldError(
			field+".repo",
			"expected remote identity owner/repo or host/owner/repo, never a local path or URL",
		)
	}
	count := 0
	for _, selector := range []struct{ name, value string }{
		{"tag", source.Tag}, {"branch", source.Branch}, {"commit", source.Commit},
	} {
		if selector.value == "" {
			continue
		}
		count++
		if !safeRevision(selector.value) {
			return fieldError(field+"."+selector.name, "unsafe or malformed revision")
		}
		if err := validateRef(selector.value, selector.name); err != nil {
			return fieldError(field+"."+selector.name, "unsafe revision")
		}
	}
	if count > 1 || (requireRef && count != 1) {
		return fieldError(field, "select exactly one of tag, branch or commit (project repo may omit all)")
	}
	return nil
}

func safeRevision(value string) bool {
	if strings.ContainsAny(value, " \t\r\n\\~^:?*[") || strings.Contains(value, "..") ||
		strings.Contains(value, "@{") ||
		strings.HasPrefix(value, "-") ||
		strings.HasSuffix(value, ".") ||
		strings.HasSuffix(value, ".lock") {
		return false
	}
	for _, seg := range strings.Split(value, "/") {
		if seg == "" || strings.HasPrefix(seg, ".") {
			return false
		}
	}
	return !strings.ContainsFunc(value, unicode.IsControl)
}

func definitionPath(value, field string) error {
	if value != "" && !safePath(value, false) {
		return fieldError(field, "expected a repository-relative path without traversal or expansion")
	}
	return nil
}

func safeName(value, field string) error {
	if value == "" || !nameSegPattern.MatchString(value) || strings.HasPrefix(value, ".") ||
		strings.Contains(value, "..") {
		return fieldError(field, "expected a nonempty safe name (letters, digits, dot, underscore or hyphen)")
	}
	return nil
}

func uniqueName(value, field string, seen map[string]bool) error {
	if err := safeName(value, field); err != nil {
		return err
	}
	if seen[value] {
		return fieldError(field, "duplicate name")
	}
	seen[value] = true
	return nil
}

func safePath(value string, absolute bool) bool {
	if value == "" || strings.ContainsAny(value, "\\$~") || strings.ContainsFunc(value, unicode.IsControl) ||
		value != strings.TrimSpace(value) || strings.HasPrefix(value, "/") != absolute {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == ".." {
			return false
		}
	}
	return path.Clean(value) == value
}

func safeHomePath(value string) bool {
	if value == "~" || value == "$HOME" {
		return true
	}
	for _, prefix := range []string{"~/", "$HOME/"} {
		if strings.HasPrefix(value, prefix) {
			return safePath(strings.TrimPrefix(value, prefix), false)
		}
	}
	return safePath(value, strings.HasPrefix(value, "/"))
}

// ResolveDefaults returns an independent Team with whole-field replacement of
// explicit selections. It performs only structural completeness checks, never
// source/credential lookup, environment expansion, filesystem or daemon IO.
func ResolveDefaults(w model.Workspace, team model.Team) (*model.Team, error) {
	if w.APIVersion != model.APIVersionV1 || team.APIVersion != model.APIVersionV1 {
		return nil, fieldError("apiVersion", "Workspace and Team must use kuketeams.io/v1")
	}
	if w.Kind != model.KindWorkspace {
		return nil, fieldError("kind", "expected a v1 Workspace")
	}
	if team.Kind != model.KindTeam {
		return nil, fieldError("kind", "expected a v1 Team")
	}
	if err := validateWorkspace(&w); err != nil {
		return nil, err
	}
	if err := validateTeam(&team); err != nil {
		return nil, err
	}
	s := &team.Spec.TeamSelections
	d := w.Spec.Defaults
	if s.Source == nil {
		s.Source = d.Source
	}
	if s.Roles == nil {
		s.Roles = d.Roles
	}
	if s.Harnesses == nil {
		s.Harnesses = d.Harnesses
	}
	if s.Modes == nil {
		s.Modes = d.Modes
	}
	if s.Integrations == nil {
		s.Integrations = d.Integrations
	}
	if err := validateCompleteSelections(*s); err != nil {
		return nil, err
	}
	// All model fields are JSON values. A serialization copy keeps nested pointers,
	// maps and slices independent of both inputs, including explicit empty lists.
	raw, err := json.Marshal(team)
	if err != nil {
		return nil, fmt.Errorf("copy Team: %w", err)
	}
	var result model.Team
	if err = json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("copy Team: %w", err)
	}
	return &result, nil
}

func validateCompleteSelections(s model.TeamSelections) error {
	if s.Roles == nil || len(*s.Roles) == 0 {
		return fieldError("spec.roles", "select at least one role explicitly in Team or Workspace defaults")
	}
	if s.Harnesses == nil || len(*s.Harnesses) == 0 {
		return fieldError("spec.harnesses", "select at least one harness explicitly in Team or Workspace defaults")
	}
	for i, role := range *s.Roles {
		if s.Source == nil && role.Source == nil {
			return fieldError(fmt.Sprintf("spec.roles[%d].source", i), "explicit source or inherited default required")
		}
	}
	for i, harness := range *s.Harnesses {
		if s.Source == nil && harness.Source == nil {
			return fieldError(
				fmt.Sprintf("spec.harnesses[%d].source", i),
				"explicit source or inherited default required",
			)
		}
	}
	return nil
}
