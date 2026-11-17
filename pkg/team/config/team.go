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

package config

import (
	"errors"
	"io"
	"slices"
)

const (
	KindTeam      = "Team"
	KindTeamEntry = "TeamEntry"
)

// Root returns a legacy root declaration inheriting global defaults.
// Init uses Majordomo instead; this helper does not migrate registrations.
func Root() *Team {
	return &Team{APIVersion: APIVersion, Kind: KindTeam, Metadata: Metadata{Name: "root"}}
}

// Majordomo returns the self-contained initial declaration of the kukeon team:
// the majordomo role on both shipped harnesses.
func Majordomo() *Team {
	return &Team{APIVersion: APIVersion, Kind: KindTeam, Metadata: Metadata{Name: "kukeon"}, Spec: Defaults{
		Sources: []Source{
			{Name: "kukeon", URL: "github.com/eminwux/kukeon", Branch: "main", ClonePath: "/home/user/kukeon-src"},
		},
		Roles: []Role{
			{
				Name:        "majordomo",
				Source:      "kukeon",
				Path:        "team/roles/majordomo",
				InitialPath: "$HOME",
				Needs:       &Needs{Image: []string{"go"}},
			},
		},
		Harnesses: []Harness{
			{Name: "codex", Source: "kukeon", Path: "team/harnesses/codex"},
			{Name: "claude", Source: "kukeon", Path: "team/harnesses/claude"},
		},
	}}
}

// Resolve applies whole-list inheritance, validates effective references, and
// retains the original declaration. Explicit empty slices select none.
func Resolve(cfg *TeamsConfig, team *Team) (Defaults, error) {
	if err := Validate(cfg); err != nil {
		return Defaults{}, err
	}
	if team == nil || team.APIVersion != APIVersion || team.Kind != KindTeam || !ValidName(team.Metadata.Name) {
		return Defaults{}, errors.New("team: require kuketeams.io/v1 Team and a valid metadata.name")
	}
	d := team.Spec
	if d.Sources == nil {
		d.Sources = cfg.Spec.Defaults.Sources
	}
	if d.Roles == nil {
		d.Roles = cfg.Spec.Defaults.Roles
	}
	if d.Harnesses == nil {
		d.Harnesses = cfg.Spec.Defaults.Harnesses
	}
	if team.ProjectSource != nil {
		if team.ProjectSource.Name != "project" {
			return Defaults{}, errors.New("projectSource: name must be project")
		}
		for _, source := range d.Sources {
			if source.Name == "project" {
				return Defaults{}, errors.New(
					"projectSource: source name project conflicts with resolved sources; rename the inherited/authored source",
				)
			}
		}
		d.Sources = append(slices.Clone(d.Sources), *team.ProjectSource)
	}
	return d, validateSelections(&d)
}

// DecodeTeamDeclaration decodes a strict Team document before caller-supplied
// naming defaults/overrides. Resolve validates its final name and selections.
func DecodeTeamDeclaration(r io.Reader) (*Team, error) {
	var team Team
	if err := decodeDocument(r, &team); err != nil {
		return nil, err
	}
	if team.APIVersion != APIVersion || team.Kind != KindTeam {
		return nil, errors.New("team: require kuketeams.io/v1 Team")
	}
	return &team, nil
}

func DecodeTeam(r io.Reader) (*Team, error) {
	team, err := DecodeTeamDeclaration(r)
	if err != nil {
		return nil, err
	}
	if !ValidName(team.Metadata.Name) {
		return nil, errors.New("team: require a valid metadata.name")
	}
	return team, nil
}

// Entry copies the authored declaration without materializing inherited fields.
// It must only be persisted after application has succeeded.
func Entry(team *Team) (*TeamEntry, error) {
	if team == nil || team.APIVersion != APIVersion || team.Kind != KindTeam || !ValidName(team.Metadata.Name) {
		return nil, errors.New("team: invalid declaration")
	}
	return &TeamEntry{
		APIVersion:    APIVersion,
		Kind:          KindTeamEntry,
		Metadata:      team.Metadata,
		ProjectSource: cloneSource(team.ProjectSource),
		Spec:          cloneSelections(team.Spec),
	}, nil
}

func DecodeEntry(r io.Reader) (*TeamEntry, error) {
	var entry TeamEntry
	if err := decodeDocument(r, &entry); err != nil {
		return nil, err
	}
	if entry.APIVersion != APIVersion || entry.Kind != KindTeamEntry || !ValidName(entry.Metadata.Name) {
		return nil, errors.New("entry: require kuketeams.io/v1 TeamEntry and a valid metadata.name")
	}
	if err := ValidateSecrets("secrets", entry.Secrets); err != nil {
		return nil, err
	}
	return &entry, nil
}

func (e TeamEntry) Team() *Team {
	return &Team{
		APIVersion:    e.APIVersion,
		Kind:          KindTeam,
		Metadata:      e.Metadata,
		ProjectSource: cloneSource(e.ProjectSource),
		Spec:          cloneSelections(e.Spec),
	}
}

func cloneSelections(d Defaults) Defaults {
	d.Sources = slices.Clone(d.Sources)
	for i := range d.Sources {
		d.Sources[i].Git = cloneSourceGit(d.Sources[i].Git)
	}
	d.Harnesses = slices.Clone(d.Harnesses)
	d.Roles = slices.Clone(d.Roles)
	for i := range d.Roles {
		if d.Roles[i].Needs != nil {
			needs := *d.Roles[i].Needs
			needs.Image = slices.Clone(needs.Image)
			d.Roles[i].Needs = &needs
		}
		if d.Roles[i].Tasks != nil {
			tasks := slices.Clone(*d.Roles[i].Tasks)
			if tasks == nil {
				tasks = []TaskSelection{}
			}
			for j := range tasks {
				if tasks[j].Suspend != nil {
					suspend := *tasks[j].Suspend
					tasks[j].Suspend = &suspend
				}
			}
			d.Roles[i].Tasks = &tasks
		}
	}
	return d
}

func cloneSource(source *Source) *Source {
	if source == nil {
		return nil
	}
	copySource := *source
	copySource.Git = cloneSourceGit(source.Git)
	return &copySource
}

func cloneSourceGit(g *SourceGit) *SourceGit {
	if g == nil {
		return nil
	}
	out := *g
	if g.Author != nil {
		author := *g.Author
		out.Author = &author
	}
	if g.Committer != nil {
		committer := *g.Committer
		out.Committer = &committer
	}
	if g.Credential != nil {
		credential := *g.Credential
		out.Credential = &credential
	}
	if g.Signing != nil {
		signing := *g.Signing
		if g.Signing.AllowedSignersRef != nil {
			ref := *g.Signing.AllowedSignersRef
			signing.AllowedSignersRef = &ref
		}
		out.Signing = &signing
	}
	out.Sign = slices.Clone(g.Sign)
	return &out
}
