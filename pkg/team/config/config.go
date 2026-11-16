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

import "slices"

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

// Git is the operator's default git identity for every team cell. Credentials
// and signing name stored secrets — never host paths: kuke team writes this
// block as scope defaults, and cells clone, push and sign from the secrets
// alone, without the operator's ~/.ssh.
type Git struct {
	Author    *Identity `yaml:"author,omitempty"`
	Committer *Identity `yaml:"committer,omitempty"`
	// Credentials bind stored git credential secrets (type ssh-key or
	// basic-auth) to remote hosts. The remote transport follows the credential.
	Credentials []GitCredential `yaml:"credentials,omitempty"`
	// Signing names the stored ssh-key secret commits and tags are signed with.
	Signing *GitSigning `yaml:"signing,omitempty"`
	Sign    []string    `yaml:"sign,omitempty"`
	// SigningKey and AllowedSigners are the pre-stored-credential host paths.
	// They are still decoded so existing configurations load, but no host path
	// reaches a cell any more: composition ignores them and reports how to
	// import the key into a secret.
	SigningKey     string `yaml:"signingKey,omitempty"`
	AllowedSigners string `yaml:"allowedSigners,omitempty"`
}

// GitCredential binds a stored git credential secret to a host (or a
// host/path URL prefix).
type GitCredential struct {
	Host          string    `yaml:"host"`
	SecretRef     SecretRef `yaml:"secretRef"`
	User          string    `yaml:"user,omitempty"`
	Port          int       `yaml:"port,omitempty"`
	HostKeyPolicy string    `yaml:"hostKeyPolicy,omitempty"`
}

// GitSigning names the stored ssh-key secret to sign with and, optionally, the
// secret holding the allowed-signers file used to verify signatures.
type GitSigning struct {
	SecretRef         SecretRef  `yaml:"secretRef"`
	AllowedSignersRef *SecretRef `yaml:"allowedSignersRef,omitempty"`
}

// SourceGit is the git identity one source states for itself. Unset fields
// fall back to the default identity.
type SourceGit struct {
	Author     *Identity         `yaml:"author,omitempty"`
	Committer  *Identity         `yaml:"committer,omitempty"`
	Credential *SourceCredential `yaml:"credential,omitempty"`
	Signing    *GitSigning       `yaml:"signing,omitempty"`
	Sign       []string          `yaml:"sign,omitempty"`
}

// SourceCredential is the credential one source clones and pushes with.
type SourceCredential struct {
	SecretRef     SecretRef `yaml:"secretRef"`
	User          string    `yaml:"user,omitempty"`
	Port          int       `yaml:"port,omitempty"`
	HostKeyPolicy string    `yaml:"hostKeyPolicy,omitempty"`
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
	// Git states this source's own git identity; unset fields use the default.
	Git *SourceGit `yaml:"git,omitempty"`
}

type Role struct {
	Name        string `yaml:"name"`
	Source      string `yaml:"source"`
	Path        string `yaml:"path"`
	InitialPath string `yaml:"initialPath,omitempty"`
	Needs       *Needs `yaml:"needs,omitempty"`
	Harness     string `yaml:"harness,omitempty"`
	// Tasks selects which of the role's declared tasks (role.yaml
	// spec.tasks) the team renders as headless task CellConfigs. Nil (the
	// field omitted) selects every task the role declares; a non-nil empty
	// list selects none. A pointer so an authored `tasks: []` survives a
	// TeamEntry round-trip instead of being erased by omitempty.
	Tasks *[]string `yaml:"tasks,omitempty"`
}

// SelectsTask reports whether the role entry selects the named task: every
// task when Tasks is omitted, otherwise only the listed ones.
func (r Role) SelectsTask(name string) bool {
	return r.Tasks == nil || slices.Contains(*r.Tasks, name)
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
