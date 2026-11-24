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
	"fmt"
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
	// A reference to the stored kukeon Source (#1643), cloned where the
	// majordomo role works on kukeon.
	source := Source{Name: KukeonSourceName, ClonePath: "/home/user/kukeon-src"}
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
// valid name and realm, the body rules DecodeTeamDeclaration applies, and
// sources that are references only (#1643, AC3): an entry carrying url or git
// is a definition, which kuke stores as a Source before it stores the Team.
// The selections are checked against the defaults and the stored Sources only
// at composition, as a role may name an inherited or stored source.
func ValidateTeam(team *Team) error {
	if team == nil {
		return errors.New("team: must not be nil")
	}
	if err := validateTeamBody(team); err != nil {
		return err
	}
	if err := referencesOnly(&team.Spec); err != nil {
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
	if err := validateTeamSources(&team.Spec); err != nil {
		return err
	}
	if err := validateConnectors("spec.connectors", team.Spec.Connectors); err != nil {
		return err
	}
	if err := validateTaskOverrides(team.Spec.Roles); err != nil {
		return err
	}
	return ValidateSecrets("spec.secrets", team.Spec.Secrets)
}

// validateTaskOverrides checks the shape of every task entry's per-harness
// overrides as the document is decoded (issue #1668), so a Team with an empty
// override is refused before it is stored. The rest of a role entry is
// checked when the team composes.
func validateTaskOverrides(roles []Role) error {
	for i, role := range roles {
		if role.Tasks == nil {
			continue
		}
		for j, task := range *role.Tasks {
			field := fmt.Sprintf("spec.roles[%d].tasks[%d].harnesses", i, j)
			if err := checkTaskHarnesses(field, task.Harnesses); err != nil {
				return err
			}
		}
	}
	return nil
}

// referencesOnly rejects a source entry, or the projectSource, that carries
// url or git: a stored Team names its sources, the Sources hold them.
func referencesOnly(spec *TeamSpec) error {
	check := func(field string, source Source) error {
		switch {
		case source.URL != "":
			return fmt.Errorf("%s.url: a stored Team names its sources; store the repository with "+
				"kuke create source and name it in %s.source", field, field)
		case source.Git != nil:
			return fmt.Errorf("%s.git: a stored Team names its sources; set the git identity on the Source "+
				"(kuke create source NAME --identity ID --force)", field)
		}
		return nil
	}
	for i, source := range spec.Sources {
		if err := check(fmt.Sprintf("spec.sources[%d]", i), source); err != nil {
			return err
		}
	}
	if spec.ProjectSource != nil {
		return check("spec.projectSource", *spec.ProjectSource)
	}
	return nil
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
				tasks[j].Harnesses = cloneTaskHarnesses(tasks[j].Harnesses)
			}
			d.Roles[i].Tasks = &tasks
		}
	}
	return d
}

// cloneTaskHarnesses deep-copies a task entry's per-harness overrides.
func cloneTaskHarnesses(in map[string]TaskHarness) map[string]TaskHarness {
	if in == nil {
		return nil
	}
	out := make(map[string]TaskHarness, len(in))
	for name, override := range in {
		if override.Suspend != nil {
			suspend := *override.Suspend
			override.Suspend = &suspend
		}
		out[name] = override
	}
	return out
}

func cloneSource(source *Source) *Source {
	if source == nil {
		return nil
	}
	copySource := *source
	copySource.Git = cloneSourceGit(source.Git)
	return &copySource
}

// Clone returns a deep copy of g (nil for nil).
func (g *SourceGit) Clone() *SourceGit { return cloneSourceGit(g) }

func cloneSourceGit(g *SourceGit) *SourceGit {
	if g == nil {
		return nil
	}
	out := *g
	if g.IdentityRef != nil {
		ref := *g.IdentityRef
		out.IdentityRef = &ref
	}
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
