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
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/gitcred"
	"github.com/eminwux/kukeon/internal/team/gitremote"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/config"
)

// SourceOptions store a Source (kuke create source, #1643).
type SourceOptions struct {
	Options

	Name, URL, Branch, Tag, Commit string
	// Identity names the stored git identity the Source is fetched and
	// cloned with (spec.git.identityRef); nil sets none.
	Identity *config.SecretRef
	// Force replaces a stored Source of the name.
	Force bool
}

// SourceChange reports a stored Source.
type SourceChange struct {
	// Action is created or updated.
	Action string
	// Source is the stored spec, its ref discovered when none was given.
	Source config.SourceSpec
	// Discovered is what the Source offers, read from the fetched tree.
	Discovered *SourceContents
	// Users are the teams and defaults that use a replaced Source: a
	// change reaches each team when it is recomposed.
	Users []SourceUse
}

// SourceUse is one use of a stored Source.
type SourceUse struct {
	// Team is the team that uses it; empty for the TeamDefaults.
	Team string `json:"team,omitempty"`
	// How is project, declared (a team's sources entry), role, harness,
	// default role or default harness.
	How string `json:"how"`
	// Name is the alias, role or harness that uses it.
	Name string `json:"name"`
}

func (u SourceUse) String() string {
	if u.Team == "" {
		return u.How + " " + u.Name
	}
	return "team " + u.Team + " (" + u.How + " " + u.Name + ")"
}

// Uses of a Source, by how a document reaches it.
const (
	UseProject        = "project"
	UseDeclared       = "declared"
	UseRole           = "role"
	UseHarness        = "harness"
	UseDefaultRole    = "default role"
	UseDefaultHarness = "default harness"
)

// CreateSource stores a Source (kuke create source, #1643 Step 6): it checks
// the address, discovers the default branch when no ref is given, fetches the
// Source with its git identity (AC9) and applies it. A stored name is
// ErrSourceExists unless Force; a replacement returns the teams that use it.
func CreateSource(ctx context.Context, opts SourceOptions) (SourceChange, error) {
	if err := opts.check(); err != nil {
		return SourceChange{}, err
	}
	if !config.ValidName(opts.Name) || opts.Name == config.ProjectSourceName {
		return SourceChange{}, fmt.Errorf(
			"create source: invalid source name %q (lowercase letters, digits, dots, hyphens; project is reserved)",
			opts.Name)
	}
	if _, err := gitremote.URL(opts.URL); err != nil {
		return SourceChange{}, err
	}
	if err := validateRevision(AddOptions{Branch: opts.Branch, Tag: opts.Tag, Commit: opts.Commit}); err != nil {
		return SourceChange{}, err
	}
	got, err := opts.Client.GetSource(ctx, kukeonv1.SourceRef{Name: opts.Name, Realm: config.DefaultRealm})
	if err != nil {
		return SourceChange{}, requestError("lookup", "source "+opts.Name, err)
	}
	if got.Exists && !opts.Force {
		return SourceChange{}, fmt.Errorf("%w: source %s is stored (%s, %s); use --force to replace it",
			errdefs.ErrSourceExists, opts.Name, gitcred.ShortForm(got.Summary.URL), got.Summary.Ref)
	}
	spec := config.SourceSpec{URL: opts.URL, Branch: opts.Branch, Tag: opts.Tag, Commit: opts.Commit}
	if opts.Identity != nil {
		ref := *opts.Identity
		if ref.Realm == "" {
			ref.Realm = config.DefaultRealm
		}
		spec.Git = &config.SourceGit{IdentityRef: &ref}
	}
	source := sourceOf(opts.Name, spec)
	stage := identityOf(opts.Client, config.DefaultRealm)
	if !hasRef(source) {
		branch, branchErr := defaultBranch(ctx, stage, source)
		if branchErr != nil {
			return SourceChange{}, branchErr
		}
		spec.Branch, source.Branch = branch, branch
	}
	if err = config.ValidateSource(opts.Name, spec); err != nil {
		return SourceChange{}, fmt.Errorf("create source: %w", err)
	}
	defaults, err := Defaults(ctx, opts.Client)
	if err != nil {
		return SourceChange{}, err
	}
	contents, err := inspect(ctx, opts.Options, source, defaults)
	if err != nil {
		return SourceChange{}, err
	}
	contents.Source = source
	change := SourceChange{Action: actionCreated, Source: spec, Discovered: contents}
	if got.Exists {
		change.Action = actionUpdated
		users, usersErr := SourceUsers(ctx, opts.Client)
		if usersErr != nil {
			return SourceChange{}, usersErr
		}
		change.Users = users[opts.Name]
	}
	if opts.DryRun {
		return change, nil
	}
	if err = applySources(ctx, opts.Client, map[string]config.SourceSpec{opts.Name: spec}, nil, false); err != nil {
		return SourceChange{}, err
	}
	return change, nil
}

// sourceOf is a stored Source as a source of that name.
func sourceOf(name string, spec config.SourceSpec) config.Source {
	return fill(config.Source{Name: name}, spec)
}

// storedSourceSpec reads the stored Source name.
func storedSourceSpec(ctx context.Context, client Client, name string) (config.SourceSpec, error) {
	got, err := client.GetSource(ctx, kukeonv1.SourceRef{Name: name, Realm: config.DefaultRealm})
	if err != nil {
		return config.SourceSpec{}, requestError("lookup", "source "+name, err)
	}
	if !got.Exists {
		return config.SourceSpec{}, fmt.Errorf("%w: source %s is not stored; store it with "+
			"kuke create source %s --url ADDRESS", errdefs.ErrSourceNotFound, name, name)
	}
	var doc api.SourceDoc
	if err = config.DecodeStrict(bytes.NewReader(got.Document), &doc); err != nil {
		return config.SourceSpec{}, fmt.Errorf("stored source %s: %w", name, err)
	}
	return doc.Spec, nil
}

// ShowSource fetches a stored Source with its git identity and lists the
// roles and harnesses it offers, marking the defaults.
func ShowSource(ctx context.Context, opts Options, name string) (*SourceContents, error) {
	if err := opts.check(); err != nil {
		return nil, err
	}
	spec, err := storedSourceSpec(ctx, opts.Client, name)
	if err != nil {
		return nil, err
	}
	defaults, err := Defaults(ctx, opts.Client)
	if err != nil {
		return nil, err
	}
	source := sourceOf(name, spec)
	contents, err := inspect(ctx, opts, source, defaults)
	if err != nil {
		return nil, err
	}
	contents.Source = source
	return contents, nil
}

// InspectSource fetches a Source that is not stored — an address, an optional
// ref and an optional git identity — with the same fetch and the same
// identity order as ShowSource, and lists the roles and harnesses it offers
// (#1644 Step 1). Nothing is stored. No role or harness is marked a default:
// the defaults name stored Sources only. A source given no ref is read at its
// remote's default branch, which the result's Source carries.
func InspectSource(ctx context.Context, opts Options, spec config.SourceSpec) (*SourceContents, error) {
	if err := opts.check(); err != nil {
		return nil, err
	}
	if _, err := gitremote.URL(spec.URL); err != nil {
		return nil, err
	}
	if err := validateRevision(AddOptions{Branch: spec.Branch, Tag: spec.Tag, Commit: spec.Commit}); err != nil {
		return nil, err
	}
	if spec.Git != nil && spec.Git.IdentityRef != nil && spec.Git.IdentityRef.Realm == "" {
		ref := *spec.Git.IdentityRef
		ref.Realm = config.DefaultRealm
		spec.Git = &config.SourceGit{IdentityRef: &ref}
	}
	source := sourceOf(inspectName(spec.URL), spec)
	if !hasRef(source) && opts.Sources[source.Name] == nil {
		branch, err := defaultBranch(ctx, identityOf(opts.Client, config.DefaultRealm), source)
		if err != nil {
			return nil, err
		}
		source.Branch = branch
	}
	if err := config.ValidateSource(source.Name, specOf(source)); err != nil {
		return nil, fmt.Errorf("inspect source: %w", err)
	}
	contents, err := inspect(ctx, opts, source, config.EmptyDefaults(config.DefaultRealm))
	if err != nil {
		return nil, err
	}
	contents.Source = source
	return contents, nil
}

// inspectName names an unstored source in messages: its repository's base
// name when that is a valid Source name, else "source".
func inspectName(address string) string {
	base := strings.ToLower(strings.TrimSuffix(path.Base(gitcred.ShortForm(address)), ".git"))
	if config.ValidName(base) && base != config.ProjectSourceName {
		return base
	}
	return "source"
}

// DeleteSource deletes a stored Source. kukeond refuses it while a default
// role or harness or a stored team references it (ErrSourceInUse, AC7) and
// names them.
func DeleteSource(ctx context.Context, opts Options, name string) error {
	if err := opts.check(); err != nil {
		return err
	}
	got, err := opts.Client.DeleteSource(ctx, kukeonv1.SourceRef{Name: name, Realm: config.DefaultRealm})
	if err != nil {
		return fmt.Errorf("delete source %s: %w", name, err)
	}
	if !got.Deleted {
		return fmt.Errorf("%w: source %s is not stored", errdefs.ErrSourceNotFound, name)
	}
	return nil
}

// SourceUsers lists, per stored Source name, the teams and defaults that use
// it and how (Step 6): a team's project, its sources entries, its roles and
// harnesses (its own lists, else the defaults'), and the default roles and
// harnesses. A name no Source stores may appear: kukeond accepts a
// reference to it (D8).
func SourceUsers(ctx context.Context, client Client) (map[string][]SourceUse, error) {
	defaults, err := Defaults(ctx, client)
	if err != nil {
		return nil, err
	}
	teams, err := storedTeams(ctx, client)
	if err != nil {
		return nil, err
	}
	out := map[string][]SourceUse{}
	add := func(source string, use SourceUse) {
		if !slices.Contains(out[source], use) {
			out[source] = append(out[source], use)
		}
	}
	for _, role := range defaults.Spec.Defaults.Roles {
		add(role.Source, SourceUse{How: UseDefaultRole, Name: role.Name})
	}
	for _, harness := range defaults.Spec.Defaults.Harnesses {
		add(harness.Source, SourceUse{How: UseDefaultHarness, Name: harness.Name})
	}
	for _, team := range teams {
		name := team.Metadata.Name
		aliases := map[string]string{}
		if project := team.Spec.ProjectSource; project != nil {
			stored := storedName(*project, name, true)
			aliases[config.ProjectSourceName] = stored
			add(stored, SourceUse{Team: name, How: UseProject, Name: config.ProjectSourceName})
		}
		for _, entry := range team.Spec.Sources {
			stored := storedName(entry, name, false)
			aliases[entry.Name] = stored
			add(stored, SourceUse{Team: name, How: UseDeclared, Name: entry.Name})
		}
		resolve := func(source string) string {
			if stored, ok := aliases[source]; ok {
				return stored
			}
			return source
		}
		roles := team.Spec.Roles
		if roles == nil {
			roles = defaults.Spec.Defaults.Roles
		}
		for _, role := range roles {
			add(resolve(role.Source), SourceUse{Team: name, How: UseRole, Name: role.Name})
		}
		harnesses := team.Spec.Harnesses
		if harnesses == nil {
			harnesses = defaults.Spec.Defaults.Harnesses
		}
		for _, harness := range harnesses {
			add(resolve(harness.Source), SourceUse{Team: name, How: UseHarness, Name: harness.Name})
		}
	}
	for source := range out {
		sort.SliceStable(out[source], func(i, j int) bool { return out[source][i].Team < out[source][j].Team })
	}
	return out, nil
}
