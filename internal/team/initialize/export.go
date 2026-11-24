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

package initialize

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/eminwux/kukeon/internal/gitcred"
	"github.com/eminwux/kukeon/pkg/team/config"
	"gopkg.in/yaml.v3"
)

// ExportOptions shape kuke team export (#1643, AC12).
type ExportOptions struct {
	// HostFields keeps what only means something on this host: the realm,
	// the stored Source each entry names, the refs the team's entries state
	// themselves, the git identities and spec.secrets. With it, the export
	// stores the same Team when created again here.
	HostFields bool
	// ExplicitDefaults writes the lists the team inherits from the
	// TeamDefaults out, with a definition for every source they read.
	ExplicitDefaults bool
}

// Export prints a stored team as a repository's kuketeam.yaml (AC12): its
// name, the project's address, each source with its address and ref, and the
// declared roles, harnesses and connectors. Addresses are written in their
// short form.
//
//nolint:funlen // one pass per field the export keeps or drops
func Export(ctx context.Context, client Client, name string, opts ExportOptions) ([]byte, error) {
	team, err := StoredTeam(ctx, client, name)
	if err != nil {
		return nil, err
	}
	if team == nil {
		return nil, notStored(name, config.DefaultRealm)
	}
	stored, err := Sources(ctx, client)
	if err != nil {
		return nil, err
	}
	defaults, err := Defaults(ctx, client)
	if err != nil {
		return nil, err
	}
	all, _ := withLegacy(defaults, stored)

	out := &config.Team{
		APIVersion: config.APIVersion, Kind: config.KindTeam,
		Metadata: config.Metadata{Name: team.Metadata.Name},
	}
	if opts.HostFields {
		out.Metadata.Realm = team.Metadata.Realm
		out.Spec.Secrets = slices.Clone(team.Spec.Secrets)
	}
	out.Spec.Roles = slices.Clone(team.Spec.Roles)
	out.Spec.Harnesses = slices.Clone(team.Spec.Harnesses)
	out.Spec.Connectors = maps.Clone(team.Spec.Connectors)
	if opts.ExplicitDefaults {
		if out.Spec.Roles == nil {
			out.Spec.Roles = slices.Clone(defaults.Spec.Defaults.Roles)
		}
		if out.Spec.Harnesses == nil {
			out.Spec.Harnesses = slices.Clone(defaults.Spec.Defaults.Harnesses)
		}
		if out.Spec.Connectors == nil {
			out.Spec.Connectors = maps.Clone(defaults.Spec.Defaults.Connectors)
		}
	}

	if project := team.Spec.ProjectSource; project != nil {
		entry, exportErr := exportEntry(*project, name, true, all, opts)
		if exportErr != nil {
			return nil, exportErr
		}
		entry.Name = config.ProjectSourceName
		if !opts.HostFields {
			// The project's ref is the checkout the team was created
			// from, not part of the declaration.
			entry.Branch, entry.Tag, entry.Commit = "", "", ""
			if entry.ClonePath == "/home/user/"+name {
				entry.ClonePath = ""
			}
		}
		out.Spec.ProjectSource = &entry
	}
	if team.Spec.Sources != nil {
		out.Spec.Sources = make([]config.Source, 0, len(team.Spec.Sources))
	}
	declared := map[string]bool{config.ProjectSourceName: true}
	for _, source := range team.Spec.Sources {
		entry, exportErr := exportEntry(source, name, false, all, opts)
		if exportErr != nil {
			return nil, exportErr
		}
		out.Spec.Sources = append(out.Spec.Sources, entry)
		declared[source.Name] = true
	}
	if opts.ExplicitDefaults {
		// A source the written lists read that the team does not declare
		// is defined too, so the file stands on its own.
		var read []string
		for _, role := range out.Spec.Roles {
			read = append(read, role.Source)
		}
		for _, harness := range out.Spec.Harnesses {
			read = append(read, harness.Source)
		}
		for _, source := range read {
			if declared[source] {
				continue
			}
			declared[source] = true
			entry, exportErr := exportEntry(config.Source{Name: source}, name, false, all, opts)
			if exportErr != nil {
				return nil, exportErr
			}
			out.Spec.Sources = append(out.Spec.Sources, entry)
		}
	}
	raw, err := yaml.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("encode team %s: %w", name, err)
	}
	return raw, nil
}

// exportEntry writes one entry as a definition: its stored Source's address
// in short form, and the entry's ref, else the Source's. With host fields the
// ref is the entry's own only, the stored name is kept when it differs from
// the alias (always for the project) and the Source's git identity is
// written.
func exportEntry(
	entry config.Source, team string, project bool, all map[string]config.SourceSpec, opts ExportOptions,
) (config.Source, error) {
	out := config.Source{Name: entry.Name, ClonePath: entry.ClonePath}
	spec := specOf(entry)
	if entry.URL == "" {
		stored := storedName(entry, team, project)
		have, ok := all[stored]
		if !ok {
			return config.Source{}, fmt.Errorf("team %s: %s names Source %s, which is not stored", team,
				entryField(entry, project), stored)
		}
		spec = have
		if opts.HostFields && (project || stored != entry.Name) {
			out.Stored = stored
		}
	}
	out.URL = gitcred.ShortForm(spec.URL)
	if hasRef(entry) || opts.HostFields {
		out.Branch, out.Tag, out.Commit = entry.Branch, entry.Tag, entry.Commit
	} else {
		out.Branch, out.Tag, out.Commit = spec.Branch, spec.Tag, spec.Commit
	}
	if opts.HostFields && spec.Git != nil && spec.Git.IdentityRef != nil {
		ref := *spec.Git.IdentityRef
		out.Git = &config.SourceGit{IdentityRef: &ref}
	}
	return out, nil
}

func entryField(entry config.Source, project bool) string {
	if project {
		return "spec.projectSource"
	}
	return "source " + entry.Name
}
