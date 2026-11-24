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

package controller

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/pkg/team/config"
)

// SourceRecord is one stored Source with what kukeond reads out of it.
type SourceRecord struct {
	Source  intmodel.Source
	Summary SourceSummary
}

// SourceSummary mirrors kukeonv1.SourceSummary.
type SourceSummary struct {
	Name, Realm, URL, Ref, Identity string
}

func validateSourceRef(name, realm string) error {
	if !config.ValidName(name) {
		return fmt.Errorf("%w: invalid source name %q", errdefs.ErrInvalidSource, name)
	}
	if !config.ValidName(realm) {
		return fmt.Errorf("%w: invalid realm name %q", errdefs.ErrInvalidSource, realm)
	}
	return nil
}

// GetSource reads one stored Source (issue #1643). found is false, with no
// error, when none of the name is stored in the realm.
func (b *Exec) GetSource(name, realm string) (SourceRecord, bool, error) {
	realm = teamRealm(realm)
	if err := validateSourceRef(name, realm); err != nil {
		return SourceRecord{}, false, err
	}
	source, err := b.runner.GetSource(intmodel.Source{Metadata: intmodel.TeamMetadata{Name: name, Realm: realm}})
	if errors.Is(err, errdefs.ErrSourceNotFound) {
		return SourceRecord{}, false, nil
	}
	if err != nil {
		return SourceRecord{}, false, err
	}
	summary, err := summarizeSource(source)
	if err != nil {
		return SourceRecord{}, false, err
	}
	return SourceRecord{Source: source, Summary: summary}, true, nil
}

// ListSources lists the stored Sources of a realm, or of every realm when
// realm is empty, sorted by realm and name.
func (b *Exec) ListSources(realm string) ([]SourceRecord, error) {
	realm = strings.TrimSpace(realm)
	if realm != "" && !config.ValidName(realm) {
		return nil, fmt.Errorf("%w: invalid realm name %q", errdefs.ErrInvalidSource, realm)
	}
	sources, err := b.runner.ListSources(realm)
	if err != nil {
		return nil, err
	}
	out := make([]SourceRecord, 0, len(sources))
	for _, source := range sources {
		summary, sumErr := summarizeSource(source)
		if sumErr != nil {
			return nil, sumErr
		}
		out = append(out, SourceRecord{Source: source, Summary: summary})
	}
	slices.SortFunc(out, func(a, b SourceRecord) int {
		if c := strings.Compare(a.Summary.Realm, b.Summary.Realm); c != 0 {
			return c
		}
		return strings.Compare(a.Summary.Name, b.Summary.Name)
	})
	return out, nil
}

// DeleteSource deletes a stored Source (issue #1643, AC7). It is refused with
// errdefs.ErrSourceInUse while a default role or harness of the realm's
// TeamDefaults, or a stored team of the realm, references it; the error names
// every reference. Deleting a Source recomposes nothing.
func (b *Exec) DeleteSource(name, realm string) error {
	realm = teamRealm(realm)
	if err := validateSourceRef(name, realm); err != nil {
		return err
	}
	stored := intmodel.Source{Metadata: intmodel.TeamMetadata{Name: name, Realm: realm}}
	if _, err := b.runner.GetSource(stored); err != nil {
		if errors.Is(err, errdefs.ErrSourceNotFound) {
			return fmt.Errorf("%w: %q in realm %q", errdefs.ErrSourceNotFound, name, realm)
		}
		return err
	}
	users, err := b.sourceUsers(name, realm)
	if err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrDeleteSource, err)
	}
	if len(users) > 0 {
		return fmt.Errorf("%w: source %s is referenced by %s", errdefs.ErrSourceInUse, name, strings.Join(users, ", "))
	}
	if err = b.runner.DeleteSource(stored); err != nil && !errors.Is(err, errdefs.ErrSourceNotFound) {
		return err
	}
	return nil
}

// sourceUsers names what references the Source name in realm: the default
// roles and harnesses that name it, and the stored teams with an entry, a
// project or a role or harness of their own that resolves to it. A legacy
// entry that carries its own url defines its repository and does not count.
func (b *Exec) sourceUsers(name, realm string) ([]string, error) {
	var users []string
	defaults, err := b.runner.GetTeamDefaults(intmodel.TeamDefaults{
		Metadata: intmodel.TeamMetadata{Name: config.DefaultsName, Realm: realm},
	})
	switch {
	case err == nil:
		doc, convErr := apischeme.ConvertTeamDefaultsToExternal(defaults)
		if convErr != nil {
			return nil, convErr
		}
		for _, role := range doc.Spec.Defaults.Roles {
			if role.Source == name {
				users = append(users, "default role "+role.Name)
			}
		}
		for _, h := range doc.Spec.Defaults.Harnesses {
			if h.Source == name {
				users = append(users, "default harness "+h.Name)
			}
		}
	case !errors.Is(err, errdefs.ErrTeamDefaultsNotFound):
		return nil, err
	}
	teams, err := b.runner.ListTeams(realm)
	if err != nil {
		return nil, err
	}
	var teamUsers []string
	for _, team := range teams {
		doc, convErr := apischeme.ConvertTeamToExternal(team)
		if convErr != nil {
			return nil, convErr
		}
		if TeamReferencesSource(doc.Metadata.Name, doc.Spec, name) {
			teamUsers = append(teamUsers, "team "+team.Metadata.Name)
		}
	}
	slices.Sort(teamUsers)
	return append(users, teamUsers...), nil
}

// TeamReferencesSource reports whether a team declaration references the
// stored Source name: an entry or the project resolving to it (its source
// field, else its alias; the project's default is the team name), or a role
// or harness of its own naming it where no alias of that name exists.
func TeamReferencesSource(team string, spec config.TeamSpec, name string) bool {
	resolves := func(entry config.Source, fallback string) bool {
		if entry.URL != "" {
			return false // A legacy definition names its own repository.
		}
		stored := entry.Stored
		if stored == "" {
			stored = fallback
		}
		return stored == name
	}
	aliases := map[string]bool{}
	for _, entry := range spec.Sources {
		aliases[entry.Name] = true
		if resolves(entry, entry.Name) {
			return true
		}
	}
	if project := spec.ProjectSource; project != nil {
		aliases[config.ProjectSourceName] = true
		if resolves(*project, team) {
			return true
		}
	}
	if aliases[name] {
		return false
	}
	return slices.ContainsFunc(spec.Roles, func(r config.Role) bool { return r.Source == name }) ||
		slices.ContainsFunc(spec.Harnesses, func(h config.Harness) bool { return h.Source == name })
}

func summarizeSource(source intmodel.Source) (SourceSummary, error) {
	doc, err := apischeme.ConvertSourceToExternal(source)
	if err != nil {
		return SourceSummary{}, fmt.Errorf("%w: %w", errdefs.ErrGetSource, err)
	}
	summary := SourceSummary{
		Name:  source.Metadata.Name,
		Realm: source.Metadata.Realm,
		URL:   doc.Spec.URL,
		Ref:   doc.Spec.Revision(),
	}
	if git := doc.Spec.Git; git != nil && git.IdentityRef != nil {
		summary.Identity = git.IdentityRef.Name
		if git.IdentityRef.Realm != "" && git.IdentityRef.Realm != source.Metadata.Realm {
			summary.Identity += "@" + git.IdentityRef.Realm
		}
	}
	return summary, nil
}
