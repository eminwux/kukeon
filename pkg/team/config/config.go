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

// Package config defines and validates operator-owned TeamsConfig documents.
// It performs no filesystem, environment, Git, daemon, or network discovery.
package config

const (
	APIVersion = "kuketeams.io/v1"
	Kind       = "TeamsConfig"
)

// TeamsConfig is the global configuration; it contains selections, not fetched definitions.
type TeamsConfig struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Spec       Spec   `yaml:"spec"`
}

type Spec struct {
	Git        *Git              `yaml:"git,omitempty"`
	Defaults   Defaults          `yaml:"defaults"`
	Secrets    []SecretBinding   `yaml:"secrets,omitempty"`
	Parameters map[string]string `yaml:"parameters,omitempty"`
}

type Git struct {
	Author         *Identity `yaml:"author,omitempty"`
	Committer      *Identity `yaml:"committer,omitempty"`
	SigningKey     string    `yaml:"signingKey,omitempty"`
	Sign           []string  `yaml:"sign,omitempty"`
	AllowedSigners string    `yaml:"allowedSigners,omitempty"`
}

type Identity struct {
	Name  string `yaml:"name"`
	Email string `yaml:"email"`
}

// Defaults preserves nil (omitted/inherit) versus non-nil empty (select none)
// selection lists. Consumers must retain that distinction during inheritance.
type Defaults struct {
	Sources   []Source  `yaml:"sources,omitempty"`
	Roles     []Role    `yaml:"roles,omitempty"`
	Harnesses []Harness `yaml:"harnesses,omitempty"`
}

// MarshalYAML keeps explicit empty lists instead of erasing them with omitempty.
func (d Defaults) MarshalYAML() (any, error) {
	var out struct {
		Sources   *[]Source  `yaml:"sources,omitempty"`
		Roles     *[]Role    `yaml:"roles,omitempty"`
		Harnesses *[]Harness `yaml:"harnesses,omitempty"`
	}
	if d.Sources != nil {
		out.Sources = &d.Sources
	}
	if d.Roles != nil {
		out.Roles = &d.Roles
	}
	if d.Harnesses != nil {
		out.Harnesses = &d.Harnesses
	}
	return out, nil
}

type Source struct {
	Name   string `yaml:"name"`
	URL    string `yaml:"url"`
	Branch string `yaml:"branch,omitempty"`
	Tag    string `yaml:"tag,omitempty"`
	Commit string `yaml:"commit,omitempty"`
	// ClonePath opts this source into runtime cloning at an absolute in-cell path.
	ClonePath string `yaml:"clonePath,omitempty"`
}

type Role struct {
	Name        string `yaml:"name"`
	Source      string `yaml:"source"`
	Path        string `yaml:"path"`
	InitialPath string `yaml:"initialPath,omitempty"`
	Needs       *Needs `yaml:"needs,omitempty"`
	Harness     string `yaml:"harness,omitempty"`
}

type Needs struct {
	Image []string `yaml:"image,omitempty"`
}

type Harness struct {
	Name   string `yaml:"name"`
	Source string `yaml:"source"`
	Path   string `yaml:"path"`
}

// Selects reports whether a role composes with a harness from its effective
// list: every listed harness unless the role names one explicitly.
func (r Role) Selects(h Harness) bool {
	return r.Harness == "" || r.Harness == h.Name
}

// SecretBinding is a declaration, never a credential value. Name is the required
// role/harness identifier, independent of the runtime secret and env destination.
type SecretBinding struct {
	Name      string        `yaml:"name"`
	SecretRef SecretRef     `yaml:"secretRef"`
	EnvName   string        `yaml:"envName"`
	Source    *SecretSource `yaml:"source,omitempty"`
}

type SecretRef struct {
	Name  string `yaml:"name"`
	Realm string `yaml:"realm"`
	Space string `yaml:"space,omitempty"`
	Stack string `yaml:"stack,omitempty"`
	Cell  string `yaml:"cell,omitempty"`
}

// SecretSource declares where application may obtain bytes if the runtime secret
// is missing. Composition never reads either source or carries resolved bytes.
type SecretSource struct {
	Env  string `yaml:"env,omitempty"`
	File string `yaml:"file,omitempty"`
}

// Team is authored input. Omitted selections inherit global defaults.
type Team struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Metadata   Metadata `yaml:"metadata"`
	// ProjectSource is additive to resolved sources; authored lists still replace defaults.
	ProjectSource *Source  `yaml:"projectSource,omitempty"`
	Spec          Defaults `yaml:"spec"`
}

type Metadata struct {
	Name string `yaml:"name"`
}

// TeamEntry records the accepted authored declaration, not effective defaults or
// resolved image references. It can be recomposed without the original input.
type TeamEntry struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Metadata   Metadata `yaml:"metadata"`
	// ProjectSource is additive to resolved sources; authored lists still replace defaults.
	ProjectSource *Source `yaml:"projectSource,omitempty"`
	// Secrets records every binding the selected pairs used. Composition prefers
	// these over global spec.secrets, so operator edits survive repeated adds.
	Secrets []SecretBinding `yaml:"secrets,omitempty"`
	Spec    Defaults        `yaml:"spec"`
}
