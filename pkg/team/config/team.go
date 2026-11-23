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
	"maps"
	"slices"

	"github.com/eminwux/kukeon/pkg/api/version"
)

const (
	// ProjectSourceName is the name of a team's spec.projectSource: the
	// repository the team works on.
	ProjectSourceName = "project"

	// KindTeam is one team's declaration: what kukeond stores and what a
	// repository's kuketeam.yaml is.
	KindTeam = "Team"
	// DefaultRealm is the realm a team, and the defaults it inherits, live in
	// when a document names none. Every team is in realm default (#1618 D14).
	DefaultRealm = "default"
)

// SystemTeamName is the team kuke team init registers: the operator's own
// system team, named after the kuke- system prefix (cf. kuke-system) so it can
// never share an identity with a project team such as kukeon's own (#1527).
const SystemTeamName = "kuke"

// MajordomoSource is the source the kuke team reads its majordomo role and
// harnesses from, and the one kuke team init registers in a realm's first
// TeamDefaults. It has no clonePath there: only the kuke team clones it. It is
// pinned to this binary's own revision (KukeonSource, #1631).
func MajordomoSource() Source {
	return KukeonSource()
}

// MajordomoHarnesses are the shipped harnesses of the kukeon source, the ones
// the kuke team runs on and the defaults init registers.
func MajordomoHarnesses() []Harness {
	return []Harness{
		{Name: "codex", Source: "kukeon", Path: "team/harnesses/codex"},
		{Name: "claude", Source: "kukeon", Path: "team/harnesses/claude"},
	}
}

// Majordomo returns the self-contained initial declaration of the kuke team:
// the majordomo role on both shipped harnesses.
func Majordomo() *Team {
	source := MajordomoSource()
	source.ClonePath = "/home/user/kukeon-src"
	return &Team{
		APIVersion: APIVersion, Kind: KindTeam,
		Metadata: Metadata{Name: SystemTeamName, Realm: DefaultRealm},
		Spec: TeamSpec{Defaults: Defaults{
			Sources: []Source{source},
			Roles: []Role{
				{
					Name:        "majordomo",
					Source:      "kukeon",
					Path:        "team/roles/majordomo",
					InitialPath: "$HOME",
					Needs:       &Needs{Image: []string{"go"}},
				},
			},
			Harnesses: MajordomoHarnesses(),
		}},
	}
}

// Resolve applies whole-list inheritance, validates effective references, and
// retains the original declaration. Explicit empty slices select none.
func Resolve(cfg *TeamDefaults, team *Team) (Defaults, error) {
	if err := Validate(cfg); err != nil {
		return Defaults{}, err
	}
	if team == nil || version.Check(team.APIVersion) != nil || team.Kind != KindTeam || !ValidName(team.Metadata.Name) {
		return Defaults{}, errors.New("team: require v1beta1 Team and a valid metadata.name")
	}
	d := team.Spec.Defaults
	if d.Sources == nil {
		d.Sources = cfg.Spec.Defaults.Sources
	}
	if d.Roles == nil {
		d.Roles = cfg.Spec.Defaults.Roles
	}
	if d.Harnesses == nil {
		d.Harnesses = cfg.Spec.Defaults.Harnesses
	}
	if d.Connectors == nil {
		d.Connectors = cfg.Spec.Defaults.Connectors
	}
	if project := team.Spec.ProjectSource; project != nil {
		if project.Name != ProjectSourceName {
			return Defaults{}, errors.New("spec.projectSource: name must be project")
		}
		for _, source := range d.Sources {
			if source.Name == ProjectSourceName {
				return Defaults{}, errors.New(
					"spec.projectSource: source name project conflicts with resolved sources; rename the inherited/authored source",
				)
			}
		}
		d.Sources = append(slices.Clone(d.Sources), *project)
	}
	return d, validateSelections(&d)
}

// DecodeTeamDeclaration decodes a strict Team document before caller-supplied
// naming defaults/overrides: metadata.name and metadata.realm may be left
// out, as in a repository's kuketeam.yaml. Resolve validates its final name
// and selections.
func DecodeTeamDeclaration(r io.Reader) (*Team, error) {
	var team Team
	if err := decodeDocument(r, &team); err != nil {
		return nil, err
	}
	if err := validateTeamBody(&team); err != nil {
		return nil, err
	}
	return &team, nil
}

// DecodeTeam decodes a strict, named Team document.
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

// ValidateTeam checks a Team document as kukeond stores it: the header, a
// valid name and realm, and the body rules DecodeTeamDeclaration applies.
// The selections are checked against the defaults only at composition, as a
// role may name an inherited source.
func ValidateTeam(team *Team) error {
	if team == nil {
		return errors.New("team: must not be nil")
	}
	if err := validateTeamBody(team); err != nil {
		return err
	}
	if !ValidName(team.Metadata.Name) {
		return errors.New("metadata.name: require a valid team name")
	}
	if !ValidName(team.Metadata.Realm) {
		return errors.New("metadata.realm: require a valid realm name")
	}
	return nil
}

func validateTeamBody(team *Team) error {
	if version.Check(team.APIVersion) != nil || team.Kind != KindTeam {
		return errors.New("team: require v1beta1 Team")
	}
	if team.Metadata.Realm != "" && !ValidName(team.Metadata.Realm) {
		return errors.New("metadata.realm: require a valid realm name")
	}
	if project := team.Spec.ProjectSource; project != nil {
		if project.Name != "" && project.Name != ProjectSourceName {
			return errors.New("spec.projectSource: name must be project")
		}
		if project.ClonePath != "" && !SafePath(project.ClonePath) {
			return errors.New("spec.projectSource.clonePath: require an absolute in-cell path")
		}
	}
	if err := validateConnectors("spec.connectors", team.Spec.Connectors); err != nil {
		return err
	}
	return ValidateSecrets("spec.secrets", team.Spec.Secrets)
}

// Declaration returns an independent copy of the authored declaration, with
// the realm defaulted: the document kukeond stores once application has
// succeeded. It does not materialize inherited fields.
func Declaration(team *Team) (*Team, error) {
	if team == nil || version.Check(team.APIVersion) != nil || team.Kind != KindTeam || !ValidName(team.Metadata.Name) {
		return nil, errors.New("team: invalid declaration")
	}
	out := &Team{
		APIVersion: APIVersion,
		Kind:       KindTeam,
		Metadata:   team.Metadata,
		Spec: TeamSpec{
			ProjectSource: cloneSource(team.Spec.ProjectSource),
			Secrets:       slices.Clone(team.Spec.Secrets),
			Defaults:      cloneSelections(team.Spec.Defaults),
		},
	}
	if out.Metadata.Realm == "" {
		out.Metadata.Realm = DefaultRealm
	}
	if team.Spec.Secrets != nil && out.Spec.Secrets == nil {
		out.Spec.Secrets = []SecretBinding{}
	}
	return out, nil
}

func cloneSelections(d Defaults) Defaults {
	d.Connectors = maps.Clone(d.Connectors)
	d.Sources = slices.Clone(d.Sources)
	for i := range d.Sources {
		d.Sources[i].Git = cloneSourceGit(d.Sources[i].Git)
	}
	d.Harnesses = slices.Clone(d.Harnesses)
	d.Roles = slices.Clone(d.Roles)
	for i := range d.Roles {
		d.Roles[i].Connectors = maps.Clone(d.Roles[i].Connectors)
		if d.Roles[i].Needs != nil {
			needs := *d.Roles[i].Needs
			needs.Image = slices.Clone(needs.Image)
			needs.Secrets = slices.Clone(needs.Secrets)
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
