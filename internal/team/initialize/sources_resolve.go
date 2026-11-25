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
	"bytes"
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"

	"github.com/eminwux/kukeon/internal/gitcred"
	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/config"
	"gopkg.in/yaml.v3"
)

// Stored Sources and the shapes around them (issue #1643, D6). kukeond
// stores a Team whose sources are references to stored Sources and a
// TeamDefaults with none; compose reads the shape it always read, every
// source with its address. hydrate turns the stored shape into that one for
// a composition, and normalize turns a declaration into the stored shape.

// Sources reads the Sources stored in realm default, by name.
func Sources(ctx context.Context, client Client) (map[string]config.SourceSpec, error) {
	listed, err := client.ListSources(ctx, config.DefaultRealm)
	if err != nil {
		return nil, requestError("lookup", "sources", err)
	}
	out := make(map[string]config.SourceSpec, len(listed))
	for _, got := range listed {
		var doc api.SourceDoc
		if decodeErr := config.DecodeStrict(bytes.NewReader(got.Document), &doc); decodeErr != nil {
			return nil, fmt.Errorf("stored source %s: %w", got.Summary.Name, decodeErr)
		}
		out[doc.Metadata.Name] = doc.Spec
	}
	return out, nil
}

// withLegacy adds to stored the sources a TeamDefaults written before
// #1643 still lists (AC13): each entry reads as a stored Source when none of
// its name is stored. The second return is the legacy entries' clone paths.
func withLegacy(
	defaults *config.TeamDefaults, stored map[string]config.SourceSpec,
) (map[string]config.SourceSpec, map[string]string) {
	out := maps.Clone(stored)
	if out == nil {
		out = map[string]config.SourceSpec{}
	}
	clonePaths := map[string]string{}
	if defaults == nil {
		return out, clonePaths
	}
	for _, source := range defaults.Spec.Defaults.Sources {
		if source.URL == "" {
			continue
		}
		if _, ok := out[source.Name]; !ok {
			out[source.Name] = specOf(source)
		}
		if source.ClonePath != "" {
			clonePaths[source.Name] = source.ClonePath
		}
	}
	return out, clonePaths
}

func hasSource(all map[string]config.SourceSpec, name string) bool {
	_, ok := all[name]
	return ok
}

// convertDefaults converts a TeamDefaults written before #1643 (AC13): each
// listed source becomes a Source unless one of its name is stored, and the
// list is cleared. It returns nil when defaults lists no sources; else the
// converted copy, the Sources to create and the notes saying so, one per
// clonePath the conversion drops because it is not the one composition
// gives a role's source.
func convertDefaults(
	defaults *config.TeamDefaults, stored map[string]config.SourceSpec,
) (*config.TeamDefaults, map[string]config.SourceSpec, []string) {
	if defaults == nil || defaults.Spec.Defaults.Sources == nil {
		return nil, nil, nil
	}
	out := *defaults
	out.Spec.Defaults.Sources = nil
	created := map[string]config.SourceSpec{}
	var notes []string
	for _, source := range defaults.Spec.Defaults.Sources {
		if _, ok := stored[source.Name]; !ok && source.URL != "" {
			created[source.Name] = specOf(source)
		}
		if source.ClonePath != "" && source.ClonePath != "/home/user/"+source.Name {
			notes = append(notes, fmt.Sprintf(
				"source %s was cloned at %s; a Source has no clone path, so set it per team with "+
					"spec.sources[].clonePath", source.Name, source.ClonePath))
		}
	}
	notes = append([]string{fmt.Sprintf("converted %d registered sources to Source documents",
		len(defaults.Spec.Defaults.Sources))}, notes...)
	return &out, created, notes
}

// defaultsDocument validates and encodes a TeamDefaults in the stored form.
func defaultsDocument(defaults *config.TeamDefaults) ([]byte, error) {
	if err := config.ValidateStoredDefaults(defaults); err != nil {
		return nil, fmt.Errorf("TeamDefaults: %w", err)
	}
	raw, err := yaml.Marshal(defaults)
	if err != nil {
		return nil, fmt.Errorf("encode TeamDefaults: %w", err)
	}
	return raw, nil
}

// applySources applies the Sources in writes, and defaults when withDefaults
// is set, in one plain apply.
func applySources(
	ctx context.Context, client Client, writes map[string]config.SourceSpec,
	defaults *config.TeamDefaults, withDefaults bool,
) error {
	var docs [][]byte
	for _, name := range slices.Sorted(maps.Keys(writes)) {
		doc, err := sourceDocument(newSource{name: name, spec: writes[name]})
		if err != nil {
			return err
		}
		docs = append(docs, doc)
	}
	if withDefaults {
		doc, err := defaultsDocument(defaults)
		if err != nil {
			return err
		}
		docs = append(docs, doc)
	}
	if len(docs) == 0 {
		return nil
	}
	stream, expected := bytes.Join(docs, []byte("---\n")), len(docs)
	response, err := client.ApplyDocuments(ctx, stream)
	if err != nil {
		return fmt.Errorf("apply Sources and TeamDefaults: %w", err)
	}
	return checkResults(response, expected, "the command")
}

// specOf is the stored Source a definition entry describes.
func specOf(source config.Source) config.SourceSpec {
	return config.SourceSpec{
		URL: source.URL, Branch: source.Branch, Tag: source.Tag, Commit: source.Commit,
		Git: source.Git.Clone(),
	}
}

// storedName is the stored Source an entry refers to: its source field,
// else its alias; for the project, else the team's name.
func storedName(entry config.Source, team string, project bool) string {
	switch {
	case entry.Stored != "":
		return entry.Stored
	case project:
		return team
	default:
		return entry.Name
	}
}

func hasRef(s config.Source) bool { return s.Branch != "" || s.Tag != "" || s.Commit != "" }

// fill is entry with the address and git of spec, and spec's ref when the
// entry states none (D4).
func fill(entry config.Source, spec config.SourceSpec) config.Source {
	out := config.Source{Name: entry.Name, URL: spec.URL, ClonePath: entry.ClonePath, Git: spec.Git.Clone()}
	if hasRef(entry) {
		out.Branch, out.Tag, out.Commit = entry.Branch, entry.Tag, entry.Commit
	} else {
		out.Branch, out.Tag, out.Commit = spec.Branch, spec.Tag, spec.Commit
	}
	return out
}

// resolveEntry is an entry in the shape compose reads: a definition (or a
// legacy entry) reads its own address, a reference its stored Source's.
func resolveEntry(
	entry config.Source, field, team string, project bool, stored map[string]config.SourceSpec,
) (config.Source, error) {
	if entry.URL != "" {
		out := entry
		out.Stored, out.Git = "", entry.Git.Clone()
		return out, nil
	}
	name := storedName(entry, team, project)
	spec, ok := stored[name]
	if !ok {
		if project {
			return config.Source{}, fmt.Errorf(
				"%s: no Source %s is stored for team %s's project; pass --source ADDRESS, set "+
					"spec.projectSource.url, or store it with kuke create source %s --url ADDRESS",
				field, name, team, name)
		}
		return config.Source{}, fmt.Errorf(
			"%s (%s): no Source %s is stored; store it with kuke create source %s --url ADDRESS",
			field, entry.Name, name, name)
	}
	return fill(entry, spec), nil
}

// hydrate returns copies of defaults and team in the shape compose reads
// (D6). The team's sources become its entries, each with the address and git
// of the Source it names and its own ref or the Source's, followed by every
// stored Source no alias shadows, under its own name; a source a selected
// role reads with no clonePath is cloned at /home/user/<alias>. The project
// becomes a full source named project, cloned at /home/user/<team> unless
// its entry says otherwise. The defaults' sources become every stored Source.
//
//nolint:funlen // one pass per input list; splitting it hides the order
func hydrate(
	defaults *config.TeamDefaults, team *config.Team, stored map[string]config.SourceSpec,
) (*config.TeamDefaults, *config.Team, error) {
	all, legacyClones := withLegacy(defaults, stored)
	names := slices.Sorted(maps.Keys(all))

	outDefaults := *defaults
	outDefaults.Spec.Defaults.Sources = make([]config.Source, 0, len(names))
	for _, name := range names {
		outDefaults.Spec.Defaults.Sources = append(outDefaults.Spec.Defaults.Sources,
			fill(config.Source{Name: name}, all[name]))
	}

	outTeam := cloneTeam(team)
	name := team.Metadata.Name
	inherits := team.Spec.Sources == nil
	sources := make([]config.Source, 0, len(team.Spec.Sources)+len(names))
	aliases := map[string]bool{}
	for i, entry := range team.Spec.Sources {
		source, err := resolveEntry(entry, fmt.Sprintf("spec.sources[%d]", i), name, false, all)
		if err != nil {
			return nil, nil, err
		}
		sources = append(sources, source)
		aliases[entry.Name] = true
	}
	for _, stored := range names {
		if aliases[stored] || stored == config.ProjectSourceName {
			continue
		}
		source := fill(config.Source{Name: stored}, all[stored])
		if inherits {
			// A team that inherited the legacy defaults' list cloned what
			// it listed.
			source.ClonePath = legacyClones[stored]
		}
		sources = append(sources, source)
	}
	if project := team.Spec.ProjectSource; project != nil {
		source, err := resolveEntry(*project, "spec.projectSource", name, true, all)
		if err != nil {
			return nil, nil, err
		}
		source.Name = config.ProjectSourceName
		if source.ClonePath == "" {
			source.ClonePath = "/home/user/" + name
		}
		outTeam.Spec.ProjectSource = &source
	}

	roles := team.Spec.Roles
	if roles == nil {
		roles = defaults.Spec.Defaults.Roles
	}
	harnesses := team.Spec.Harnesses
	if harnesses == nil {
		harnesses = defaults.Spec.Defaults.Harnesses
	}
	known := map[string]int{}
	taken := map[string]bool{}
	for i, source := range sources {
		known[source.Name] = i
		taken[source.ClonePath] = true
	}
	if project := outTeam.Spec.ProjectSource; project != nil {
		known[config.ProjectSourceName] = -1
		taken[project.ClonePath] = true
	}
	for _, role := range roles {
		i, ok := known[role.Source]
		if !ok {
			return nil, nil, unknownSource("role "+role.Name, role.Source)
		}
		// AC4: a role's source is cloned where the team says, else at
		// /home/user/<alias>.
		if i < 0 || sources[i].ClonePath != "" {
			continue
		}
		path := "/home/user/" + role.Source
		if taken[path] {
			return nil, nil, fmt.Errorf("role %s reads source %s, which would be cloned at %s, where another "+
				"source of team %s is cloned; list it with its own clonePath in spec.sources "+
				"(- {name: %s, clonePath: /home/user/%s-src})", role.Name, role.Source, path, name,
				role.Source, role.Source)
		}
		sources[i].ClonePath = path
		taken[path] = true
	}
	for _, harness := range harnesses {
		if _, ok := known[harness.Source]; !ok {
			return nil, nil, unknownSource("harness "+harness.Name, harness.Source)
		}
	}
	if err := checkDefaultsSources(defaults, all); err != nil {
		return nil, nil, err
	}
	outTeam.Spec.Sources = sources
	return &outDefaults, outTeam, nil
}

// checkDefaultsSources names a default role or harness whose source no
// Source stores: kukeond accepts it (D8), composition cannot.
func checkDefaultsSources(defaults *config.TeamDefaults, all map[string]config.SourceSpec) error {
	for _, role := range defaults.Spec.Defaults.Roles {
		if _, ok := all[role.Source]; !ok {
			return unknownSource("default role "+role.Name, role.Source)
		}
	}
	for _, harness := range defaults.Spec.Defaults.Harnesses {
		if _, ok := all[harness.Source]; !ok {
			return unknownSource("default harness "+harness.Name, harness.Source)
		}
	}
	return nil
}

func unknownSource(who, source string) error {
	if source == config.ProjectSourceName {
		return fmt.Errorf("%s reads source project, and the team has no project source; "+
			"set spec.projectSource or pass --source ADDRESS", who)
	}
	return fmt.Errorf("%s names source %q, which the team does not declare and no Source stores; "+
		"store it with kuke create source %s --url ADDRESS", who, source, source)
}

// cloneTeam copies team deeply enough that hydrate and normalize can rewrite
// its sources.
func cloneTeam(team *config.Team) *config.Team {
	out := *team
	out.Spec.Sources = slices.Clone(team.Spec.Sources)
	for i := range out.Spec.Sources {
		out.Spec.Sources[i].Git = out.Spec.Sources[i].Git.Clone()
	}
	if team.Spec.ProjectSource != nil {
		project := *team.Spec.ProjectSource
		project.Git = project.Git.Clone()
		out.Spec.ProjectSource = &project
	}
	out.Spec.Secrets = slices.Clone(team.Spec.Secrets)
	return &out
}

// newSource is a Source a team declaration defines that is not stored yet.
type newSource struct {
	name string
	spec config.SourceSpec
	// field is where the declaration defines it, for diagnostics.
	field string
}

// normalize returns the Team to store, the Sources to create with it and the
// notes saying so (D6, D7). Each definition (an entry with url) is stored as
// a Source when none of its stored name exists, and the entry keeps its
// alias, ref, clonePath and the source field when the stored name differs
// from the alias (always, for the project). A definition whose stored
// Source holds another repository is AC6's error; one that holds the same
// repository over another transport is a reference to it. A definition that
// becomes a reference drops its git block; when that block differs from the
// one used, a note says so (#1654).
func normalize(
	team *config.Team, stored map[string]config.SourceSpec,
) (*config.Team, []newSource, []string, error) {
	out := cloneTeam(team)
	var created []newSource
	var unused []string
	byName := map[string]int{}
	define := func(entry *config.Source, field string, project bool) error {
		name := storedName(*entry, team.Metadata.Name, project)
		if entry.URL == "" {
			if entry.Git != nil {
				return fmt.Errorf("%s (%s): git belongs to the stored Source %s; set it with "+
					"kuke create source %s --url ADDRESS --identity ID --force", field, entry.Name, name, name)
			}
		} else {
			spec := specOf(*entry)
			if have, ok := stored[name]; ok {
				if !gitcred.SameRepository(have.URL, spec.URL) {
					return clash(field, entry.Name, name, spec.URL, have.URL)
				}
				if entry.Git != nil && !reflect.DeepEqual(entry.Git, have.Git) {
					unused = append(unused, fmt.Sprintf("%s (%s): Source %s is already stored; its git "+
						"identity is used, not this entry's git block. Change it with kuke create source %s "+
						"--url ADDRESS --identity ID --force", field, entry.Name, name, name))
				}
			} else if i, seen := byName[name]; seen {
				if !gitcred.SameRepository(created[i].spec.URL, spec.URL) {
					return clash(field, entry.Name, name, spec.URL, created[i].spec.URL)
				}
				if entry.Git != nil && !reflect.DeepEqual(entry.Git, created[i].spec.Git) {
					unused = append(unused, fmt.Sprintf("%s (%s): Source %s is defined by %s; its git "+
						"identity is used, not this entry's git block", field, entry.Name, name, created[i].field))
				}
			} else {
				byName[name] = len(created)
				created = append(created, newSource{name: name, spec: spec, field: field})
			}
		}
		entry.URL, entry.Git = "", nil
		switch {
		case project:
			entry.Stored = name
		case name == entry.Name:
			entry.Stored = ""
		}
		return nil
	}
	for i := range out.Spec.Sources {
		if err := define(&out.Spec.Sources[i], fmt.Sprintf("spec.sources[%d]", i), false); err != nil {
			return nil, nil, nil, err
		}
	}
	if out.Spec.ProjectSource != nil {
		if err := define(out.Spec.ProjectSource, "spec.projectSource", true); err != nil {
			return nil, nil, nil, err
		}
	}
	notes := make([]string, 0, len(created)+len(unused))
	for _, source := range created {
		notes = append(notes, fmt.Sprintf("stores Source %s (%s, %s), defined by %s",
			source.name, gitcred.ShortForm(source.spec.URL), source.spec.Revision(), source.field))
	}
	notes = append(notes, unused...)
	return out, created, notes, nil
}

// clash is AC6's error: both addresses, and the source field that stores the
// definition under another name.
func clash(field, alias, name, defined, have string) error {
	return fmt.Errorf("%s (%s) defines Source %s at %s, but the stored Source %s is %s; "+
		"store this one under another name with source: NAME in the entry, or remove its url "+
		"to use the stored Source", field, alias, name, defined, name, have)
}

// discoverCreatedRefs gives each new Source that states no ref its remote's
// default branch: a stored Source has exactly one ref.
func discoverCreatedRefs(ctx context.Context, stage identity, created []newSource) error {
	for i := range created {
		spec := &created[i].spec
		if spec.Branch != "" || spec.Tag != "" || spec.Commit != "" {
			continue
		}
		branch, err := defaultBranch(ctx, stage, config.Source{Name: created[i].name, URL: spec.URL, Git: spec.Git})
		if err != nil {
			return fmt.Errorf("source %s: %w", created[i].name, err)
		}
		spec.Branch = branch
	}
	return nil
}

// sourceDocument is the Source document of a new source.
func sourceDocument(source newSource) ([]byte, error) {
	raw, err := yaml.Marshal(api.SourceDoc{
		APIVersion: api.APIVersionV1Beta1, Kind: api.KindSource,
		Metadata: api.TeamMetadata{Name: source.name, Realm: config.DefaultRealm},
		Spec:     source.spec,
	})
	if err != nil {
		return nil, fmt.Errorf("encode Source %s: %w", source.name, err)
	}
	return raw, nil
}
