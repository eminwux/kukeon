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
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/eminwux/kukeon/internal/gitcred"
	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/config"
)

// Source states of a draft (#1643 AC6): a definition whose stored name holds
// the same repository, or a reference to a stored Source, is stored; a
// definition no Source of its name holds is new; one whose stored name holds
// another repository conflicts. A reference to a Source that is not stored is
// missing.
const (
	DraftStored   = "stored"
	DraftNew      = "new"
	DraftConflict = "conflict"
	DraftMissing  = "missing"
)

// Draft is what kuke create team would start from, read without composing
// or writing anything (#1644): the declaration with its project, and the
// state of each source it names.
type Draft struct {
	// Origin says where the declaration came from: the repository's
	// kuketeam.yaml, a given file, or the defaults.
	Origin Origin
	// Name is the team: the one asked for, else the file's metadata.name,
	// else the repository's base name.
	Name string
	// Team is the declaration create team would compose, its project set.
	Team *config.Team
	// Sources are the project, then each spec.sources entry.
	Sources []DraftSource
	Notes   []string
}

// DraftSource is one source of a draft.
type DraftSource struct {
	// Alias is the name the team's roles and harnesses use; project for
	// the project.
	Alias string
	// Stored is the stored Source the entry is, or would be stored as.
	Stored string
	// URL and Ref are the address and ref the entry is read at.
	URL, Ref string
	// Identity names the git identity it is fetched with, when one is set.
	Identity string
	// State is one of the Draft* states.
	State string
	// Have is a conflicting stored Source's address.
	Have string
	// Contents is what the source offers, when it was fetched (the project).
	Contents *SourceContents
}

// DraftTeam reads what kuke create team NAME --source ... would create: it
// fetches the project as Add does, reads its kuketeam.yaml from the fetched
// revision unless File or NoRepoFile, and reports the state of each source.
// It composes nothing and writes nothing; a stored team of the name is not
// an error here (Add asks for Force).
//
//nolint:gocyclo,cyclop,funlen // addRemote's steps up to composition, kept in its order
func DraftTeam(ctx context.Context, opts AddOptions) (Draft, error) {
	if err := opts.check(); err != nil {
		return Draft{}, err
	}
	if opts.Directory != "" {
		return Draft{}, errors.New("draft: a directory is read by kuke create team on its host")
	}
	if err := validateRevision(opts); err != nil {
		return Draft{}, err
	}
	var file *config.Team
	if opts.File != nil {
		decoded, err := config.DecodeTeamDeclaration(bytes.NewReader(opts.File))
		if err != nil {
			return Draft{}, fmt.Errorf("draft %s: %w", opts.FileName, err)
		}
		file = decoded
	}
	project := &config.Source{
		Name: config.ProjectSourceName, URL: opts.Source, Branch: opts.Branch, Tag: opts.Tag, Commit: opts.Commit,
	}
	if opts.Source == "" {
		var declared *config.Source
		if file != nil {
			declared = file.Spec.ProjectSource
		}
		if declared == nil || (declared.URL == "" && declared.Stored == "") {
			return Draft{}, errors.New("draft: name the project by its address or a stored Source, " +
				"or give a file with spec.projectSource.url")
		}
		project.URL, project.Stored, project.Git = declared.URL, declared.Stored, declared.Git.Clone()
		project.ClonePath = declared.ClonePath
		if !hasRef(*project) {
			project.Branch, project.Tag, project.Commit = declared.Branch, declared.Tag, declared.Commit
		}
	}
	name := opts.Name
	if name == "" && file != nil {
		name = file.Metadata.Name
	}
	provisional := name
	if provisional == "" {
		provisional = repositoryName(project)
	}
	fetch, err := projectSource(ctx, opts, provisional, project)
	if err != nil {
		return Draft{}, err
	}
	if !hasRef(fetch) {
		branch, branchErr := defaultBranch(ctx, identityOf(opts.Client, config.DefaultRealm), fetch)
		if branchErr != nil {
			return Draft{}, branchErr
		}
		project.Branch, fetch.Branch = branch, branch
	}
	have, cleanup, err := withProject(ctx, opts.Client, opts.Sources, fetch)
	if err != nil {
		return Draft{}, err
	}
	defer cleanup()
	tree := have.trees[config.ProjectSourceName]
	var repo *config.Team
	if tree != nil && opts.File == nil && !opts.NoRepoFile {
		if repo, err = readTreeTeam(tree); err != nil {
			return Draft{}, err
		}
	}
	origin := Origin{Kind: OriginDefaults}
	if repo != nil {
		origin = Origin{
			Kind:   OriginRepository,
			Detail: teamFileName + " at " + shortCommit(have.commits[config.ProjectSourceName]),
		}
		if name == "" {
			name = repo.Metadata.Name
		}
	}
	if name == "" {
		name = provisional
	}
	if err = validateAddName(name); err != nil {
		return Draft{}, err
	}
	if project.ClonePath == "" {
		project.ClonePath = "/home/user/" + name
	}
	team, origin, err := declaration(opts, name, repo, origin)
	if err != nil {
		return Draft{}, err
	}
	var notes []string
	team.Spec.ProjectSource, notes = mergeProject(team.Spec.ProjectSource, project, fetch.URL)
	defaults, _, err := readDefaults(ctx, opts.Client, config.DefaultRealm)
	if err != nil {
		return Draft{}, err
	}
	sources, err := draftSources(ctx, opts.Client, team, defaults)
	if err != nil {
		return Draft{}, err
	}
	if tree != nil {
		contents, discoverErr := Discover(tree, config.ProjectSourceName, config.EmptyDefaults(config.DefaultRealm))
		if discoverErr != nil {
			return Draft{}, discoverErr
		}
		contents.Commit = have.commits[config.ProjectSourceName]
		contents.Source = fetch
		sources[0].Contents = contents
	}
	if stored, lookupErr := StoredTeam(ctx, opts.Client, name); lookupErr != nil {
		return Draft{}, lookupErr
	} else if stored != nil {
		notes = append(notes, fmt.Sprintf("team %s is stored: adding it replaces its declaration", name))
	}
	return Draft{Origin: origin, Name: name, Team: team, Sources: sources, Notes: notes}, nil
}

// repositoryName is the base name of the project's repository, the team
// name a draft proposes when nothing names one.
func repositoryName(project *config.Source) string {
	if project.Stored != "" {
		return project.Stored
	}
	base := strings.ToLower(strings.TrimSuffix(path.Base(gitcred.ShortForm(project.URL)), ".git"))
	if config.ValidName(base) {
		return base
	}
	return project.URL
}

// draftSources reports the project and each spec.sources entry of team
// against the stored Sources.
func draftSources(
	ctx context.Context, client Client, team *config.Team, defaults *config.TeamDefaults,
) ([]DraftSource, error) {
	stored, err := Sources(ctx, client)
	if err != nil {
		return nil, err
	}
	all, _ := withLegacy(defaults, stored)
	name := team.Metadata.Name
	out := []DraftSource{}
	if project := team.Spec.ProjectSource; project != nil {
		out = append(out, draftSource(*project, name, true, all))
	}
	for _, entry := range team.Spec.Sources {
		out = append(out, draftSource(entry, name, false, all))
	}
	return out, nil
}

func draftSource(entry config.Source, team string, project bool, all map[string]config.SourceSpec) DraftSource {
	target := storedName(entry, team, project)
	out := DraftSource{Alias: entry.Name, Stored: target, URL: entry.URL}
	if project {
		out.Alias = config.ProjectSourceName
	}
	spec, ok := all[target]
	switch {
	case entry.URL == "" && ok:
		out.State, out.URL = DraftStored, spec.URL
	case entry.URL == "":
		out.State = DraftMissing
	case ok && gitcred.SameRepository(spec.URL, entry.URL):
		out.State = DraftStored
	case ok:
		out.State, out.Have = DraftConflict, spec.URL
	default:
		out.State = DraftNew
	}
	resolved := entry
	if out.State == DraftStored && entry.URL == "" {
		resolved = fill(entry, spec)
	}
	out.Ref = strings.TrimPrefix(sourceRef(resolved), " ")
	if !hasRef(resolved) {
		out.Ref = ""
	}
	git := resolved.Git
	if out.State == DraftStored {
		git = spec.Git
	}
	if git != nil && git.IdentityRef != nil {
		out.Identity = git.IdentityRef.Name
	}
	return out
}

// ResolvedTeam is a stored team as its page shows it (#1644): the stored
// declaration, the same team hydrated the way a composition reads it, the
// defaults it inherits from and the Configs it rendered. Nothing is fetched.
type ResolvedTeam struct {
	// Stored is the declaration as kukeond stores it: references only.
	Stored *config.Team
	// Team is Stored hydrated (D6): every source with its address, ref and
	// git identity, and every stored Source no alias shadows. Nil when the
	// team does not resolve; Unresolved then says why.
	Team       *config.Team
	Unresolved string
	Defaults   *config.TeamDefaults
	// Sources are the stored Sources, by name.
	Sources map[string]config.SourceSpec
	// Configs are the team's stored Configs (label kukeon.io/team).
	Configs []api.CellConfigDoc
}

// ResolveTeam reads the stored team name and resolves it without a fetch.
func ResolveTeam(ctx context.Context, client Client, name string) (*ResolvedTeam, error) {
	stored, err := StoredTeam(ctx, client, name)
	if err != nil {
		return nil, err
	}
	if stored == nil {
		return nil, notStored(name, config.DefaultRealm)
	}
	defaults, _, err := readDefaults(ctx, client, stored.Metadata.Realm)
	if err != nil {
		return nil, err
	}
	sources, err := Sources(ctx, client)
	if err != nil {
		return nil, err
	}
	configs, err := client.ListConfigs(ctx, "", "", "")
	if err != nil {
		return nil, requestError("lookup", "configs", err)
	}
	out := &ResolvedTeam{Stored: stored, Defaults: defaults, Sources: sources}
	for _, doc := range configs {
		if doc.Metadata.Labels[api.LabelTeam] == name {
			out.Configs = append(out.Configs, doc)
		}
	}
	if _, team, hydrateErr := hydrate(defaults, stored, sources); hydrateErr != nil {
		out.Unresolved = hydrateErr.Error()
	} else {
		out.Team = team
	}
	return out, nil
}
