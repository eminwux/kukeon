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

package webserver

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/team/initialize"
	"github.com/eminwux/kukeon/pkg/team/config"
	"github.com/eminwux/kukeon/pkg/team/connector"
)

// The team form (#1644, D2): the parts of a Team declaration the wizard and
// the Edit team screen show as fields — the project, the sources, the roles
// with their harness, image needs, initial path and tasks, the harnesses and
// the team's connectors. It is its own JSON model, never a JSON-encoded
// config.Team: every list that can inherit carries its flag, because JSON
// cannot tell a nil list from an empty one. What the form does not model —
// spec.secrets, a role's needs.secrets, a source's long-form git block — is
// kept from the base declaration by apply and edited as YAML. A role's
// per-connector switches (#1649, D7) are modelled: apply changes a role entry
// only where a switch or identity differs from the base, through the same
// functions as kuke team connector.

type teamForm struct {
	Name    string      `json:"name"`
	Project *formSource `json:"project,omitempty"`
	// Sources are the team's spec.sources; null declares none (the team
	// reads every stored Source), an array (even empty) lists them.
	Sources    []formSource   `json:"sources"`
	Roles      formRoles      `json:"roles"`
	Harnesses  formHarnesses  `json:"harnesses"`
	Connectors formConnectors `json:"connectors"`
}

// formSource is one source entry or the project. Stored names the stored
// Source the entry refers to when it is not the alias; URL makes it a
// definition, which creating the team stores. Identity is the git identity
// of a definition; a reference reads its stored Source's.
type formSource struct {
	Alias     string `json:"alias"`
	Stored    string `json:"stored,omitempty"`
	URL       string `json:"url,omitempty"`
	RefKind   string `json:"refKind,omitempty"`
	Ref       string `json:"ref,omitempty"`
	ClonePath string `json:"clonePath,omitempty"`
	Identity  string `json:"identity,omitempty"`
}

// formRoles is spec.roles: inherit leaves it out (the defaults' roles).
type formRoles struct {
	Inherit bool       `json:"inherit"`
	Items   []formRole `json:"items"`
}

type formRole struct {
	Name        string    `json:"name"`
	Source      string    `json:"source"`
	Path        string    `json:"path"`
	Harness     string    `json:"harness,omitempty"`
	InitialPath string    `json:"initialPath,omitempty"`
	Image       []string  `json:"image"`
	Tasks       formTasks `json:"tasks"`
	// Connectors has one item per connector a team can give a role
	// (initialize.Togglable), whether this entry enables it or not.
	Connectors []formRoleConnector `json:"connectors"`
}

// formRoleConnector is one per-role connector switch: Enabled says the role
// entry enables it (a connectors entry or a legacy needs.secrets name), and
// Identity is the entry's identity, empty for the scope defaults.
type formRoleConnector struct {
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
	Identity string `json:"identity,omitempty"`
}

// formTasks is a role's tasks: all selects every task the role declares.
type formTasks struct {
	All   bool       `json:"all"`
	Items []formTask `json:"items"`
}

// formTask is one selected task. Suspend is the schedule switch: false is
// on, true is off, null keeps the role's.
type formTask struct {
	Name     string `json:"name"`
	Schedule string `json:"schedule,omitempty"`
	Suspend  *bool  `json:"suspend"`
	Args     string `json:"args,omitempty"`
	// Harnesses are the task entry's per-harness overrides (issue #1668),
	// keyed by harness name. The form edits only their switch (D6), but
	// carries their schedule so Apply does not erase it.
	Harnesses map[string]formTaskHarness `json:"harnesses,omitempty"`
}

// formTaskHarness is one per-harness override of a task entry. Suspend is
// the switch for that harness: false is on, true is off, null keeps the
// entry's.
type formTaskHarness struct {
	Schedule string `json:"schedule,omitempty"`
	Suspend  *bool  `json:"suspend"`
}

// formTaskHarnessesOf copies a task entry's overrides into the form.
func formTaskHarnessesOf(in map[string]config.TaskHarness) map[string]formTaskHarness {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]formTaskHarness, len(in))
	for name, override := range in {
		out[name] = formTaskHarness{Schedule: override.Schedule, Suspend: cloneBool(override.Suspend)}
	}
	return out
}

// taskHarnessesOf writes the form's overrides back into a task entry,
// dropping an override that sets neither a schedule nor a switch, and
// returning nil when none is left.
func taskHarnessesOf(in map[string]formTaskHarness) map[string]config.TaskHarness {
	var out map[string]config.TaskHarness
	for name, override := range in {
		schedule := strings.TrimSpace(override.Schedule)
		if schedule == "" && override.Suspend == nil {
			continue
		}
		if out == nil {
			out = map[string]config.TaskHarness{}
		}
		out[strings.TrimSpace(name)] = config.TaskHarness{Schedule: schedule, Suspend: cloneBool(override.Suspend)}
	}
	return out
}

type formHarnesses struct {
	Inherit bool          `json:"inherit"`
	Items   []formHarness `json:"items"`
}

type formHarness struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Path   string `json:"path"`
}

type formConnectors struct {
	Inherit bool                     `json:"inherit"`
	Items   map[string]formConnector `json:"items"`
}

type formConnector struct {
	Method   string `json:"method,omitempty"`
	Identity string `json:"identity,omitempty"`
}

// Ref kinds of a formSource.
const (
	refBranch = "branch"
	refTag    = "tag"
	refCommit = "commit"
)

// formOf is the form of a Team: the stored one for an edit, a draft's
// declaration for a new team. catalog gives each role its connector
// switches; nil gives none.
func formOf(team *config.Team, catalog *connector.Catalog) teamForm {
	s := team.Spec
	out := teamForm{
		Name:       team.Metadata.Name,
		Roles:      formRoles{Inherit: s.Roles == nil, Items: []formRole{}},
		Harnesses:  formHarnesses{Inherit: s.Harnesses == nil, Items: []formHarness{}},
		Connectors: formConnectors{Inherit: s.Connectors == nil, Items: map[string]formConnector{}},
	}
	if s.ProjectSource != nil {
		project := formSourceOf(*s.ProjectSource)
		if project.Alias == "" {
			project.Alias = config.ProjectSourceName
		}
		out.Project = &project
	}
	if s.Sources != nil {
		out.Sources = make([]formSource, 0, len(s.Sources))
		for _, entry := range s.Sources {
			out.Sources = append(out.Sources, formSourceOf(entry))
		}
	}
	for _, role := range s.Roles {
		item := formRole{
			Name: role.Name, Source: role.Source, Path: role.Path, Harness: role.Harness,
			InitialPath: role.InitialPath, Image: []string{},
			Tasks:      formTasks{All: role.Tasks == nil, Items: []formTask{}},
			Connectors: roleConnectorsOf(&role, catalog),
		}
		if role.Needs != nil {
			item.Image = append(item.Image, role.Needs.Image...)
		}
		if role.Tasks != nil {
			for _, task := range *role.Tasks {
				item.Tasks.Items = append(item.Tasks.Items, formTask{
					Name: task.Name, Schedule: task.Schedule, Suspend: cloneBool(task.Suspend), Args: task.Args,
					Harnesses: formTaskHarnessesOf(task.Harnesses),
				})
			}
		}
		out.Roles.Items = append(out.Roles.Items, item)
	}
	for _, h := range s.Harnesses {
		out.Harnesses.Items = append(out.Harnesses.Items, formHarness{Name: h.Name, Source: h.Source, Path: h.Path})
	}
	for name, choice := range s.Connectors {
		out.Connectors.Items[name] = formConnector{Method: choice.Method, Identity: choice.Identity}
	}
	return out
}

// roleConnectorsOf is a role entry's connector switches. A legacy
// needs.secrets opt-in shows as on and is not rewritten (D7).
func roleConnectorsOf(role *config.Role, catalog *connector.Catalog) []formRoleConnector {
	out := []formRoleConnector{}
	for _, conn := range initialize.TogglableConnectors(catalog) {
		out = append(out, formRoleConnector{
			Name: conn.Metadata.Name, Enabled: initialize.RoleConnectorSet(role, conn),
			Identity: role.Connectors[conn.Metadata.Name].Identity,
		})
	}
	return out
}

func formSourceOf(entry config.Source) formSource {
	out := formSource{Alias: entry.Name, Stored: entry.Stored, URL: entry.URL, ClonePath: entry.ClonePath}
	switch {
	case entry.Branch != "":
		out.RefKind, out.Ref = refBranch, entry.Branch
	case entry.Tag != "":
		out.RefKind, out.Ref = refTag, entry.Tag
	case entry.Commit != "":
		out.RefKind, out.Ref = refCommit, entry.Commit
	}
	if entry.Git != nil && entry.Git.IdentityRef != nil {
		out.Identity = entry.Git.IdentityRef.Name
	}
	return out
}

func cloneBool(v *bool) *bool {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}

// apply returns a copy of base with what the form models replaced and
// everything else kept (D2): metadata.realm, spec.secrets, each role's
// connectors and needs.secrets (matched by name, else by source and path)
// except where a connector switch changed (#1649), and each source's git
// block besides its identity (matched by alias).
func (f teamForm) apply(base *config.Team, catalog *connector.Catalog) (*config.Team, error) {
	if base == nil {
		base = &config.Team{APIVersion: config.APIVersion, Kind: config.KindTeam}
	}
	out := *base
	out.Spec.Secrets = slices.Clone(base.Spec.Secrets)
	if name := strings.TrimSpace(f.Name); name != "" {
		out.Metadata.Name = name
	}
	if f.Project != nil {
		project, err := f.Project.entry("the project", base.Spec.ProjectSource)
		if err != nil {
			return nil, err
		}
		if project.Name == "" {
			project.Name = config.ProjectSourceName
		}
		out.Spec.ProjectSource = &project
	} else if base.Spec.ProjectSource != nil {
		project := *base.Spec.ProjectSource
		project.Git = project.Git.Clone()
		out.Spec.ProjectSource = &project
	}
	if f.Sources != nil {
		sources := make([]config.Source, 0, len(f.Sources))
		for i, item := range f.Sources {
			var have *config.Source
			for j := range base.Spec.Sources {
				if base.Spec.Sources[j].Name == strings.TrimSpace(item.Alias) {
					have = &base.Spec.Sources[j]
				}
			}
			entry, err := item.entry(fmt.Sprintf("source %d", i+1), have)
			if err != nil {
				return nil, err
			}
			if entry.Name == "" {
				return nil, fmt.Errorf("%w: source %d has no name", errdefs.ErrInvalidTeam, i+1)
			}
			sources = append(sources, entry)
		}
		out.Spec.Sources = sources
	} else {
		out.Spec.Sources = slices.Clone(base.Spec.Sources)
	}
	out.Spec.Roles = nil
	if !f.Roles.Inherit {
		out.Spec.Roles = make([]config.Role, 0, len(f.Roles.Items))
		for _, item := range f.Roles.Items {
			out.Spec.Roles = append(out.Spec.Roles, item.role(base.Spec.Roles))
		}
		for i, item := range f.Roles.Items {
			if err := item.applyConnectors(&out, &out.Spec.Roles[i], baseRole(item, base.Spec.Roles), catalog); err != nil {
				return nil, err
			}
		}
	}
	out.Spec.Harnesses = nil
	if !f.Harnesses.Inherit {
		out.Spec.Harnesses = make([]config.Harness, 0, len(f.Harnesses.Items))
		for _, item := range f.Harnesses.Items {
			out.Spec.Harnesses = append(out.Spec.Harnesses, config.Harness{
				Name: strings.TrimSpace(item.Name), Source: strings.TrimSpace(item.Source),
				Path: strings.TrimSpace(item.Path),
			})
		}
	}
	out.Spec.Connectors = nil
	if !f.Connectors.Inherit {
		out.Spec.Connectors = make(map[string]config.ConnectorChoice, len(f.Connectors.Items))
		for name, choice := range f.Connectors.Items {
			out.Spec.Connectors[name] = config.ConnectorChoice{
				Method: strings.TrimSpace(choice.Method), Identity: strings.TrimSpace(choice.Identity),
			}
		}
	}
	return &out, nil
}

// entry is the form's source over base, the entry of the same alias.
func (s formSource) entry(field string, base *config.Source) (config.Source, error) {
	var out config.Source
	if base != nil {
		out = *base
		out.Git = base.Git.Clone()
	}
	out.Name, out.Stored = strings.TrimSpace(s.Alias), strings.TrimSpace(s.Stored)
	out.URL, out.ClonePath = strings.TrimSpace(s.URL), strings.TrimSpace(s.ClonePath)
	out.Branch, out.Tag, out.Commit = "", "", ""
	ref := strings.TrimSpace(s.Ref)
	switch s.RefKind {
	case refBranch:
		out.Branch = ref
	case refTag:
		out.Tag = ref
	case refCommit:
		out.Commit = ref
	case "":
		if ref != "" {
			return config.Source{}, fmt.Errorf("%w: %s: a ref needs its kind (branch, tag or commit)",
				errdefs.ErrInvalidTeam, field)
		}
	default:
		return config.Source{}, fmt.Errorf("%w: %s: ref kind %q is not branch, tag or commit",
			errdefs.ErrInvalidTeam, field, s.RefKind)
	}
	identity := strings.TrimSpace(s.Identity)
	current := ""
	if out.Git != nil && out.Git.IdentityRef != nil {
		current = out.Git.IdentityRef.Name
	}
	if identity == current {
		return out, nil
	}
	if out.URL == "" {
		return config.Source{}, fmt.Errorf("%w: %s: the git identity belongs to the stored Source; "+
			"change it on the Sources page", errdefs.ErrInvalidTeam, field)
	}
	if identity == "" {
		out.Git.IdentityRef = nil
		if g := out.Git; g.Author == nil && g.Committer == nil && g.Credential == nil && g.Signing == nil &&
			len(g.Sign) == 0 {
			out.Git = nil
		}
		return out, nil
	}
	if out.Git == nil {
		out.Git = &config.SourceGit{}
	}
	out.Git.IdentityRef = &config.SecretRef{Name: identity, Realm: config.DefaultRealm}
	return out, nil
}

// baseRole is the base role a form role edits: the one of the same name,
// else of the same source and path; nil for a new role.
func baseRole(r formRole, base []config.Role) *config.Role {
	name := strings.TrimSpace(r.Name)
	for i := range base {
		if base[i].Name == name {
			return &base[i]
		}
	}
	for i := range base {
		if base[i].Source == r.Source && base[i].Path == r.Path {
			return &base[i]
		}
	}
	return nil
}

// applyConnectors changes the role entry entry of team where a switch
// differs from have, the base entry (nil for a new role): enabled where
// have does not enable the connector, disabled where it does, or a new
// identity. An untouched switch writes nothing, so a legacy needs.secrets
// opt-in keeps its spelling.
func (r formRole) applyConnectors(
	team *config.Team, entry, have *config.Role, catalog *connector.Catalog,
) error {
	for _, item := range r.Connectors {
		name := strings.TrimSpace(item.Name)
		conn, known := catalog.Find(name)
		was := known && initialize.RoleConnectorSet(have, conn)
		identity := strings.TrimSpace(item.Identity)
		var current string
		if have != nil {
			current = have.Connectors[name].Identity
		}
		var err error
		switch {
		case item.Enabled && (!was || identity != current):
			err = initialize.EnableRoleConnector(team, catalog, entry.Name, name, &identity)
		case !item.Enabled && was:
			err = initialize.DisableRoleConnector(team, catalog, entry.Name, name)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// role is the form's role over the base role of the same name, else of the
// same source and path.
func (r formRole) role(base []config.Role) config.Role {
	name := strings.TrimSpace(r.Name)
	have := baseRole(r, base)
	var out config.Role
	if have != nil {
		out = *have
		out.Connectors = maps.Clone(have.Connectors)
	}
	out.Name, out.Source, out.Path = name, strings.TrimSpace(r.Source), strings.TrimSpace(r.Path)
	out.Harness, out.InitialPath = strings.TrimSpace(r.Harness), strings.TrimSpace(r.InitialPath)
	var image []string
	for _, capability := range r.Image {
		if capability = strings.TrimSpace(capability); capability != "" {
			image = append(image, capability)
		}
	}
	var needs *config.Needs
	if have != nil && have.Needs != nil {
		kept := *have.Needs
		kept.Secrets = slices.Clone(have.Needs.Secrets)
		needs = &kept
	}
	switch {
	case len(image) > 0:
		if needs == nil {
			needs = &config.Needs{}
		}
		needs.Image = image
	case needs != nil && len(needs.Image) > 0:
		// The form cleared the image needs.
		needs.Image = nil
		if len(needs.Secrets) == 0 {
			needs = nil
		}
	}
	out.Needs = needs
	out.Tasks = nil
	if !r.Tasks.All {
		tasks := make([]config.TaskSelection, 0, len(r.Tasks.Items))
		for _, task := range r.Tasks.Items {
			tasks = append(tasks, config.TaskSelection{
				Name: strings.TrimSpace(task.Name), Schedule: strings.TrimSpace(task.Schedule),
				Suspend: cloneBool(task.Suspend), Args: task.Args, Harnesses: taskHarnessesOf(task.Harnesses),
			})
		}
		out.Tasks = &tasks
	}
	return out
}
