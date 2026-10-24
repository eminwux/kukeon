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

// Team is a user-owned declaration at ~/.kuke/kuketeam.d/<name>.yaml.
// It is a configuration input, not a daemon resource or applied-state record.
type Team struct {
	APIVersion string   `json:"apiVersion" yaml:"apiVersion"`
	Kind       string   `json:"kind"       yaml:"kind"`
	Metadata   Metadata `json:"metadata"   yaml:"metadata"`
	Spec       TeamSpec `json:"spec"       yaml:"spec"`
}

// TeamSpec carries the roster and optional remote project selection.
type TeamSpec struct {
	TeamSelections `yaml:",inline"`

	// Repo uses the existing repository identity and tag/branch/commit syntax.
	// Unlike an agent source, an omitted revision selects the remote default branch.
	Repo     *TeamSource `json:"repo,omitempty"     yaml:"repo,omitempty"`
	CloneDir string      `json:"cloneDir,omitempty" yaml:"cloneDir,omitempty"`
	Cwd      string      `json:"cwd,omitempty"      yaml:"cwd,omitempty"`
	StateDir string      `json:"stateDir,omitempty" yaml:"stateDir,omitempty"`
	Realm    string      `json:"realm,omitempty"    yaml:"realm,omitempty"`
	Space    string      `json:"space,omitempty"    yaml:"space,omitempty"`
	Stack    string      `json:"stack,omitempty"    yaml:"stack,omitempty"`
}

// TeamSelections is shared by Workspace defaults and Team overrides. Nil means
// inherit; a present list/map replaces the whole inherited list/map, including
// an explicitly empty value. Pointers preserve that distinction on the wire.
type TeamSelections struct {
	Source       *DefinitionSource           `json:"source,omitempty"       yaml:"source,omitempty"`
	Roles        *[]TeamRole                 `json:"roles,omitempty"        yaml:"roles,omitempty"`
	Harnesses    *[]TeamHarness              `json:"harnesses,omitempty"    yaml:"harnesses,omitempty"`
	Modes        *[]string                   `json:"modes,omitempty"        yaml:"modes,omitempty"`
	Integrations *map[string]CredentialGrant `json:"integrations,omitempty" yaml:"integrations,omitempty"`
}

// TeamRole keeps the existing ref/needs composition and adds instance identity
// and definition provenance. Omitted Name uses Ref as the instance identity.
type TeamRole struct {
	Ref   string         `json:"ref"             yaml:"ref"`
	Needs *TeamRoleNeeds `json:"needs,omitempty" yaml:"needs,omitempty"`

	Name    string            `json:"name,omitempty"    yaml:"name,omitempty"`
	Source  *DefinitionSource `json:"source,omitempty"  yaml:"source,omitempty"`
	Path    string            `json:"path,omitempty"    yaml:"path,omitempty"`
	Harness string            `json:"harness,omitempty" yaml:"harness,omitempty"`
}

// TeamHarness selects an enabled harness. Omitted source/path delegates to
// agent-source metadata; Default chooses a harness without selecting all of them.
type TeamHarness struct {
	Name    string            `json:"name"              yaml:"name"`
	Source  *DefinitionSource `json:"source,omitempty"  yaml:"source,omitempty"`
	Path    string            `json:"path,omitempty"    yaml:"path,omitempty"`
	Default *bool             `json:"default,omitempty" yaml:"default,omitempty"`
}

// CredentialGrant explicitly grants named credentials to one named client.
// An empty list disables that client's grants; global declarations grant nothing.
type CredentialGrant struct {
	Credentials []string `json:"credentials" yaml:"credentials"`
}

// SelectedRoles returns the explicit roster, or no roles when it is absent.
func (t *Team) SelectedRoles() []TeamRole {
	if t.Spec.Roles == nil {
		return nil
	}
	return *t.Spec.Roles
}

// HarnessNames returns the enabled harness names in declaration order.
func (t *Team) HarnessNames() []string {
	if t.Spec.Harnesses == nil {
		return nil
	}
	names := make([]string, 0, len(*t.Spec.Harnesses))
	for _, h := range *t.Spec.Harnesses {
		names = append(names, h.Name)
	}
	return names
}

// AgentSource returns the selected default agent repository. An absent source
// returns an empty reference so source validation reports it without a panic.
func (t *Team) AgentSource() TeamSource {
	if t.Spec.Source == nil {
		return TeamSource{}
	}
	return t.Spec.Source.TeamSource
}

// TeamRoleNeeds is the project-side capability override for a role. Only the
// image-capability dimension is project-tunable; repos/mounts/params/secrets are
// declared by the role itself in the agents source.
type TeamRoleNeeds struct {
	// Image lists additional capability names this project's instance of the
	// role requires (e.g. [go]). Capability names, never image tags/digests.
	Image []string `json:"image,omitempty" yaml:"image,omitempty"`
}
