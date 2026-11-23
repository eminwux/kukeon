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

// Package config defines and validates the team documents: Team, one team's
// declaration, and TeamDefaults, the settings every team in a realm inherits.
// It performs no filesystem, environment, Git, daemon, or network discovery.
package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

const (
	// APIVersion is the apiVersion of every team document, the one the rest
	// of kukeon uses (#1618).
	APIVersion = "v1beta1"
	// KindTeamDefaults is the settings every team of a realm inherits.
	KindTeamDefaults = "TeamDefaults"
	// DefaultsName is the only name a TeamDefaults may have: a realm holds at
	// most one.
	DefaultsName = "default"
)

// TeamDefaults is a realm's team defaults; it contains selections, not
// fetched definitions.
type TeamDefaults struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Metadata   Metadata `yaml:"metadata"`
	Spec       Spec     `yaml:"spec"`
}

// EmptyDefaults is the TeamDefaults of a realm that stores none.
func EmptyDefaults(realm string) *TeamDefaults {
	return &TeamDefaults{
		APIVersion: APIVersion, Kind: KindTeamDefaults,
		Metadata: Metadata{Name: DefaultsName, Realm: realm},
	}
}

// Spec has no git block: a git identity is a git connector identity, stored
// with `kuke connector import git` (issue #1591). A leftover `spec.git` fails
// to decode with an error that says so.
type Spec struct {
	// Defaults are the sources every team may use and the selections a
	// team that omits a list inherits. Its sources are the registered
	// sources (`kuke team source`).
	Defaults   Defaults          `yaml:"defaults"`
	Secrets    []SecretBinding   `yaml:"secrets,omitempty"`
	Parameters map[string]string `yaml:"parameters,omitempty"`
}

// MarshalYAML keeps an explicit empty list or map instead of erasing it with
// omitempty, so a stored document reads back as it was written.
func (s Spec) MarshalYAML() (any, error) {
	var out struct {
		Defaults   Defaults           `yaml:"defaults"`
		Secrets    *[]SecretBinding   `yaml:"secrets,omitempty"`
		Parameters *map[string]string `yaml:"parameters,omitempty"`
	}
	out.Defaults = s.Defaults
	if s.Secrets != nil {
		out.Secrets = &s.Secrets
	}
	if s.Parameters != nil {
		out.Parameters = &s.Parameters
	}
	return out, nil
}

// GitSigning names the stored ssh-key secret to sign with and, optionally, the
// secret holding the allowed-signers file used to verify signatures.
type GitSigning struct {
	SecretRef         SecretRef  `yaml:"secretRef"`
	AllowedSignersRef *SecretRef `yaml:"allowedSignersRef,omitempty"`
}

// SourceGit is the git identity one source states for itself. Unset fields
// fall back to the identity the git connector's scope default names.
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
	// Connectors are the team's connector choices, keyed by connector name
	// (issue #1548): which method a connector is delivered by and which stored
	// identity fills it. A connector left out — or an entry with no identity —
	// uses the default method and inherits its identity from the Stack, Space
	// and Realm defaults. Nil inherits the global defaults' map.
	Connectors map[string]ConnectorChoice `yaml:"connectors,omitempty"`
	Sources    []Source                   `yaml:"sources,omitempty"`
	Roles      []Role                     `yaml:"roles,omitempty"`
	Harnesses  []Harness                  `yaml:"harnesses,omitempty"`
}

// ConnectorChoice is what a team (or one of its roles) decides for one
// connector. Both fields are optional.
type ConnectorChoice struct {
	// Method picks one of the connector's methods (for example `subscription`,
	// `api-key` or `login` for claude). Empty selects the connector's default.
	Method string `yaml:"method,omitempty"`
	// Identity names the stored Secret that fills the connector, written
	// `name[@realm[/space[/stack]]]`; without a scope it is looked up in the
	// team's realm. Empty leaves the identity to the scope defaults, so a
	// later change of a default reaches the team's cells without a re-render.
	Identity string `yaml:"identity,omitempty"`
}

// MarshalYAML keeps explicit empty lists and an explicit empty connectors
// map instead of erasing them with omitempty: empty selects nothing, omitted
// inherits.
func (d Defaults) MarshalYAML() (any, error) {
	var out struct {
		Connectors *map[string]ConnectorChoice `yaml:"connectors,omitempty"`
		Sources    *[]Source                   `yaml:"sources,omitempty"`
		Roles      *[]Role                     `yaml:"roles,omitempty"`
		Harnesses  *[]Harness                  `yaml:"harnesses,omitempty"`
	}
	if d.Connectors != nil {
		out.Connectors = &d.Connectors
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
	// Connectors are this role's connector choices, keyed by connector name.
	// An entry wins over the team's spec.connectors entry for the same
	// connector, and gives the role's cells the connector even when the role
	// itself does not ask for it. An entry that names no method keeps the
	// team's method (and its identity, unless the entry names one); an entry
	// that names a method states the whole choice.
	Connectors map[string]ConnectorChoice `yaml:"connectors,omitempty"`
	// Tasks selects which of the role's declared tasks (role.yaml
	// spec.tasks) the team renders as headless task CellConfigs. Nil (the
	// field omitted) selects every task the role declares; a non-nil empty
	// list selects none. A pointer so an authored `tasks: []` survives a
	// Team round-trip instead of being erased by omitempty. An entry is
	// a task name, or `{ name, schedule?, suspend? }` to set the task's
	// cadence for this team (issue #1503).
	Tasks *[]TaskSelection `yaml:"tasks,omitempty"`
}

// TaskSelection selects one of a role's declared tasks and, optionally, sets
// its schedule for this team. In YAML it is either the task's name or a
// mapping:
//
//	tasks:
//	  - pick-issue
//	  - { name: pick-bug, schedule: "*/15 * * * *" }
//	  - { name: pick-nits, schedule: "@daily", suspend: true }
//
// A task's cadence is a deployment decision, so each value here overrides the
// corresponding default the role declares for the task: Args replaces the
// role's argument text, Schedule replaces the role's schedule, and Suspend — a pointer, so an explicit `false` is distinct
// from omitted — replaces the role's suspend (`suspend: false` turns on a
// schedule the role ships switched off).
type TaskSelection struct {
	// Name is the name of a task the role declares.
	Name string `yaml:"name"`
	// Schedule is a five-field cron expression or
	// @hourly/@daily/@weekly/@monthly, read in UTC. Empty keeps the role's.
	Schedule string `yaml:"schedule,omitempty"`
	// Suspend switches the task's schedule off (true) or on (false). Nil
	// keeps the role's.
	Suspend *bool `yaml:"suspend,omitempty"`
	// Args replaces the role's argument text for the task: the free text the
	// rendered prompt appends after the skill name. Empty keeps the role's.
	Args string `yaml:"args,omitempty"`
}

// taskSelectionFields is TaskSelection without its YAML methods, for the
// mapping form.
type taskSelectionFields TaskSelection

// UnmarshalYAML accepts a bare task name or a mapping.
func (t *TaskSelection) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		*t = TaskSelection{Name: node.Value}
		return nil
	}
	var fields taskSelectionFields
	if err := node.Decode(&fields); err != nil {
		return fmt.Errorf("task entry: %w", err)
	}
	*t = TaskSelection(fields)
	return nil
}

// MarshalYAML writes a selection that sets no schedule as the bare task name,
// so an entry authored as a name is recorded as one.
func (t TaskSelection) MarshalYAML() (any, error) {
	if t.Schedule == "" && t.Suspend == nil && t.Args == "" {
		return t.Name, nil
	}
	return taskSelectionFields(t), nil
}

// SelectsTask reports whether the role entry selects the named task: every
// task when Tasks is omitted, otherwise only the listed ones.
func (r Role) SelectsTask(name string) bool {
	if r.Tasks == nil {
		return true
	}
	_, ok := r.TaskSelection(name)
	return ok
}

// TaskSelection returns the role entry's selection of the named task — the
// place its schedule overrides live — and whether the entry lists it. An entry
// that omits Tasks selects every task with no overrides, and reports false.
func (r Role) TaskSelection(name string) (TaskSelection, bool) {
	if r.Tasks == nil {
		return TaskSelection{}, false
	}
	for _, task := range *r.Tasks {
		if task.Name == name {
			return task, true
		}
	}
	return TaskSelection{}, false
}

// Needs are what a team adds to a role's own needs for this deployment.
type Needs struct {
	Image []string `yaml:"image,omitempty"`
	// Secrets names runtime secrets the team gives the role's cells on top of
	// the ones the role declares. A role that can use a credential without
	// requiring it (majordomo's health sweep filing issues, issue #1509)
	// leaves it out of role.yaml, and an install opts in by naming it here.
	Secrets []string `yaml:"secrets,omitempty"`
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
	Spec       TeamSpec `yaml:"spec"`
}

// TeamSpec is a team's declaration: the project source, the recorded secret
// bindings and the four selection fields of Defaults, inlined so a
// declaration reads `spec.roles` like the defaults it inherits from.
type TeamSpec struct {
	Defaults `yaml:",inline"`

	// ProjectSource is additive to resolved sources; authored lists still replace defaults.
	ProjectSource *Source `yaml:"projectSource,omitempty"`
	// Secrets records every binding the selected pairs used. Composition
	// prefers these over the defaults' spec.secrets, so edits survive a
	// recomposition.
	Secrets []SecretBinding `yaml:"secrets,omitempty"`
}

// MarshalYAML writes the spec with explicit empty lists kept: Defaults'
// own MarshalYAML would otherwise be promoted and drop the other fields.
func (s TeamSpec) MarshalYAML() (any, error) {
	var out struct {
		ProjectSource *Source                     `yaml:"projectSource,omitempty"`
		Secrets       *[]SecretBinding            `yaml:"secrets,omitempty"`
		Connectors    *map[string]ConnectorChoice `yaml:"connectors,omitempty"`
		Sources       *[]Source                   `yaml:"sources,omitempty"`
		Roles         *[]Role                     `yaml:"roles,omitempty"`
		Harnesses     *[]Harness                  `yaml:"harnesses,omitempty"`
	}
	out.ProjectSource = s.ProjectSource
	if s.Connectors != nil {
		out.Connectors = &s.Connectors
	}
	if s.Secrets != nil {
		out.Secrets = &s.Secrets
	}
	if s.Sources != nil {
		out.Sources = &s.Sources
	}
	if s.Roles != nil {
		out.Roles = &s.Roles
	}
	if s.Harnesses != nil {
		out.Harnesses = &s.Harnesses
	}
	return out, nil
}

// Metadata names a Team or a TeamDefaults and the realm it lives in. A
// repository's kuketeam.yaml may leave both out.
type Metadata struct {
	Name  string `yaml:"name,omitempty"`
	Realm string `yaml:"realm,omitempty"`
}
