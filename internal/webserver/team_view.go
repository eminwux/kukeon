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
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/eminwux/kukeon/internal/gitcred"
	"github.com/eminwux/kukeon/internal/team/initialize"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/config"
)

// The team view (#1644, AC3): a stored team as it composes, read without a
// fetch — its project and sources with their addresses, its roles and
// harnesses, each list marked declared or inherited from the defaults, its
// connectors and the task Configs it rendered with their schedules. It is
// built from the hydrated team (initialize.ResolveTeam), unlike the form,
// which is built from the stored one.

type teamDetailView struct {
	Project    *viewSource    `json:"project,omitempty"`
	Sources    viewSources    `json:"sources"`
	Roles      viewRoles      `json:"roles"`
	Harnesses  viewHarnesses  `json:"harnesses"`
	Connectors viewConnectors `json:"connectors"`
	Tasks      []viewTask     `json:"tasks"`
	Configs    []viewConfig   `json:"configs"`
	// Unresolved says why the team does not resolve (a source no Source
	// stores); the sources then show what is stored, not what composes.
	Unresolved string `json:"unresolved,omitempty"`
}

// viewSource is one source as the team reads it. Declared says the team
// lists it; a source it does not list is a stored Source a role or harness
// names.
type viewSource struct {
	Alias     string `json:"alias"`
	Stored    string `json:"stored,omitempty"`
	URL       string `json:"url"`
	Ref       string `json:"ref,omitempty"`
	ClonePath string `json:"clonePath,omitempty"`
	Identity  string `json:"identity,omitempty"`
	Declared  bool   `json:"declared"`
}

type viewSources struct {
	Inherited bool         `json:"inherited"`
	Items     []viewSource `json:"items"`
}

type viewRole struct {
	Name        string   `json:"name"`
	Source      string   `json:"source"`
	Path        string   `json:"path"`
	Harness     string   `json:"harness,omitempty"`
	InitialPath string   `json:"initialPath,omitempty"`
	Image       []string `json:"image"`
	Secrets     []string `json:"secrets"`
	// AllTasks says the role selects every task it declares; else
	// Selected lists the selections.
	AllTasks bool                `json:"allTasks"`
	Selected []viewTaskSelection `json:"selected"`
}

type viewTaskSelection struct {
	Name     string `json:"name"`
	Schedule string `json:"schedule,omitempty"`
	Suspend  *bool  `json:"suspend,omitempty"`
	Args     string `json:"args,omitempty"`
}

type viewRoles struct {
	Inherited bool       `json:"inherited"`
	Items     []viewRole `json:"items"`
}

type viewHarnesses struct {
	Inherited bool          `json:"inherited"`
	Items     []formHarness `json:"items"`
}

type teamConnectorView struct {
	Name     string `json:"name"`
	Method   string `json:"method,omitempty"`
	Identity string `json:"identity,omitempty"`
}

type viewConnectors struct {
	Inherited bool                `json:"inherited"`
	Items     []teamConnectorView `json:"items"`
}

// viewTask is one task Config the team rendered: its role and task, its
// schedule (spec.schedule) and what kukeond's scheduler last did
// (status.schedule).
type viewTask struct {
	Role     string                            `json:"role"`
	Task     string                            `json:"task"`
	Config   string                            `json:"config"`
	Scope    []string                          `json:"scope"`
	Schedule *v1beta1.CellConfigSchedule       `json:"schedule,omitempty"`
	Status   *v1beta1.CellConfigScheduleStatus `json:"status,omitempty"`
}

// viewConfig is one Config of the team, with its scope for a link.
type viewConfig struct {
	Name  string   `json:"name"`
	Scope []string `json:"scope"`
	Role  string   `json:"role,omitempty"`
	Task  string   `json:"task,omitempty"`
}

func refText(s config.Source) string {
	switch {
	case s.Branch != "":
		return "branch " + s.Branch
	case s.Tag != "":
		return "tag " + s.Tag
	case s.Commit != "":
		return "commit " + s.Commit
	}
	return ""
}

func identityName(g *config.SourceGit) string {
	if g == nil || g.IdentityRef == nil {
		return ""
	}
	return g.IdentityRef.Name
}

// teamViewOf builds the view of a resolved team.
//
//nolint:funlen // one pass per list the page shows, in the page's order
func teamViewOf(r *initialize.ResolvedTeam) teamDetailView {
	stored, defaults := r.Stored, r.Defaults.Spec.Defaults
	out := teamDetailView{
		Sources:    viewSources{Inherited: stored.Spec.Sources == nil, Items: []viewSource{}},
		Roles:      viewRoles{Inherited: stored.Spec.Roles == nil, Items: []viewRole{}},
		Harnesses:  viewHarnesses{Inherited: stored.Spec.Harnesses == nil, Items: []formHarness{}},
		Connectors: viewConnectors{Inherited: stored.Spec.Connectors == nil, Items: []teamConnectorView{}},
		Tasks:      []viewTask{},
		Configs:    []viewConfig{},
		Unresolved: r.Unresolved,
	}
	roles, harnesses, connectors := stored.Spec.Roles, stored.Spec.Harnesses, stored.Spec.Connectors
	if roles == nil {
		roles = defaults.Roles
	}
	if harnesses == nil {
		harnesses = defaults.Harnesses
	}
	if connectors == nil {
		connectors = defaults.Connectors
	}
	for _, role := range roles {
		item := viewRole{
			Name: role.Name, Source: role.Source, Path: role.Path, Harness: role.Harness,
			InitialPath: role.InitialPath, Image: []string{}, Secrets: []string{}, AllTasks: role.Tasks == nil,
			Selected: []viewTaskSelection{},
		}
		if role.Needs != nil {
			item.Image = append(item.Image, role.Needs.Image...)
			item.Secrets = append(item.Secrets, role.Needs.Secrets...)
		}
		if role.Tasks != nil {
			for _, task := range *role.Tasks {
				item.Selected = append(item.Selected, viewTaskSelection{
					Name: task.Name, Schedule: task.Schedule, Suspend: task.Suspend, Args: task.Args,
				})
			}
		}
		out.Roles.Items = append(out.Roles.Items, item)
	}
	for _, h := range harnesses {
		out.Harnesses.Items = append(out.Harnesses.Items, formHarness{Name: h.Name, Source: h.Source, Path: h.Path})
	}
	for _, name := range slices.Sorted(maps.Keys(connectors)) {
		choice := connectors[name]
		out.Connectors.Items = append(out.Connectors.Items,
			teamConnectorView{Name: name, Method: choice.Method, Identity: choice.Identity})
	}

	// Sources: the project, every entry the team lists, then each stored
	// Source a role or harness reads that the team does not list.
	storedOf := func(entry config.Source, project bool) string {
		switch {
		case entry.Stored != "":
			return entry.Stored
		case project:
			return stored.Metadata.Name
		}
		return entry.Name
	}
	resolved := map[string]config.Source{}
	if r.Team != nil {
		for _, source := range r.Team.Spec.Sources {
			resolved[source.Name] = source
		}
		if p := r.Team.Spec.ProjectSource; p != nil {
			resolved[config.ProjectSourceName] = *p
		}
	}
	sourceView := func(entry config.Source, project, declared bool) viewSource {
		alias := entry.Name
		if project {
			alias = config.ProjectSourceName
		}
		item := viewSource{Alias: alias, Declared: declared, ClonePath: entry.ClonePath, URL: entry.URL}
		if entry.URL == "" {
			item.Stored = storedOf(entry, project)
			if spec, ok := r.Sources[item.Stored]; ok {
				item.URL, item.Identity = spec.URL, identityName(spec.Git)
			}
		} else {
			item.Identity = identityName(entry.Git)
		}
		if have, ok := resolved[alias]; ok {
			item.Ref, item.ClonePath = refText(have), have.ClonePath
			if item.URL == "" {
				item.URL = have.URL
			}
		} else {
			item.Ref = refText(entry)
		}
		item.URL = gitcred.ShortForm(item.URL)
		return item
	}
	if p := stored.Spec.ProjectSource; p != nil {
		project := sourceView(*p, true, true)
		out.Project = &project
	}
	listed := map[string]bool{config.ProjectSourceName: true}
	for _, entry := range stored.Spec.Sources {
		out.Sources.Items = append(out.Sources.Items, sourceView(entry, false, true))
		listed[entry.Name] = true
	}
	var read []string
	for _, role := range roles {
		read = append(read, role.Source)
	}
	for _, h := range harnesses {
		read = append(read, h.Source)
	}
	for _, name := range read {
		if listed[name] {
			continue
		}
		listed[name] = true
		out.Sources.Items = append(out.Sources.Items, sourceView(config.Source{Name: name}, false, false))
	}

	for _, doc := range r.Configs {
		m := doc.Metadata
		scope := []string{orDefault(m.Realm), orDefault(m.Space), orDefault(m.Stack)}
		role, task := m.Labels[v1beta1.LabelRole], m.Labels[v1beta1.LabelTask]
		out.Configs = append(out.Configs, viewConfig{Name: m.Name, Scope: scope, Role: role, Task: task})
		if task == "" {
			continue
		}
		item := viewTask{Role: role, Task: task, Config: m.Name, Scope: scope, Schedule: doc.Spec.Schedule}
		if doc.Status != nil {
			item.Status = doc.Status.Schedule
		}
		out.Tasks = append(out.Tasks, item)
	}
	sort.Slice(out.Configs, func(i, j int) bool { return out.Configs[i].Name < out.Configs[j].Name })
	sort.Slice(out.Tasks, func(i, j int) bool {
		return strings.Join([]string{out.Tasks[i].Role, out.Tasks[i].Task, out.Tasks[i].Config}, "/") <
			strings.Join([]string{out.Tasks[j].Role, out.Tasks[j].Task, out.Tasks[j].Config}, "/")
	})
	return out
}

func orDefault(value string) string {
	if value == "" {
		return config.DefaultRealm
	}
	return value
}
