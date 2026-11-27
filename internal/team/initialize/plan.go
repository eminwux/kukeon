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
	"io/fs"
	"maps"
	"slices"

	"github.com/eminwux/kukeon/internal/errdefs"
	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/compose"
	"github.com/eminwux/kukeon/pkg/team/config"
)

// Origin kinds: where the declaration a team command applies came from.
const (
	OriginRepository = "repository file"
	OriginDirectory  = "directory file"
	OriginDefaults   = "defaults"
	OriginFile       = "file"
	OriginStored     = "stored"
	OriginInitial    = "initial"
)

// Origin says where a declaration came from: Kind is one of the Origin*
// constants and Detail adds the path or commit.
type Origin struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail,omitempty"`
}

func (o Origin) String() string {
	if o.Detail == "" {
		return o.Kind
	}
	return o.Kind + " " + o.Detail
}

// PlannedSource is one source the composition read definitions from.
type PlannedSource struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Ref    string `json:"ref"`
	Commit string `json:"commit,omitempty"`
}

// PlannedTask is one headless task Config of a pair.
type PlannedTask struct {
	Name     string `json:"name"`
	Config   string `json:"config"`
	Schedule string `json:"schedule,omitempty"`
	Suspend  bool   `json:"suspend,omitempty"`
}

// PlannedPair is one role × harness and the Config composed for it.
type PlannedPair struct {
	Role       string        `json:"role"`
	Harness    string        `json:"harness"`
	Config     string        `json:"config"`
	Image      string        `json:"image"`
	Components []string      `json:"components,omitempty"`
	Tasks      []PlannedTask `json:"tasks,omitempty"`
}

// Plan is what a team application would do, computed without writing
// anything (issue #1618, AC10): the CLI prints it for --dry-run and the web
// server returns it as a preview. Commit applies it.
type Plan struct {
	Command string `json:"command"`
	Origin  Origin `json:"origin"`
	// Team is the declaration as it will be stored: the authored one, its
	// source definitions turned into references (#1643), with the secret
	// bindings composition used.
	Team    *config.Team    `json:"-"`
	Sources []PlannedSource `json:"sources"`
	Pairs   []PlannedPair   `json:"pairs"`
	// Create, Rerender and Prune are the Configs the application creates,
	// re-renders and removes, by name.
	Create   []string `json:"create"`
	Rerender []string `json:"rerender"`
	Prune    []string `json:"prune"`
	// MissingSecrets lists one remediation line per runtime secret that does
	// not exist and has no creation source; cells using it fail to start.
	MissingSecrets []string `json:"missingSecrets"`
	// CreateSecrets names the secrets the application creates from their
	// declared source.
	CreateSecrets []string `json:"createSecrets"`
	Notes         []string `json:"notes"`

	composed    *compose.Result
	realmDoc    []byte
	defaultsDoc []byte
	// sourceDocs are the Sources the team's definitions create (D7).
	sourceDocs [][]byte
	pending    []pendingSecret
	superseded []supersededCopy
}

// Blueprints names the shared blueprints the plan's Configs reference, in
// first-use order. After a commit they are the names the daemon stored.
func (p *Plan) Blueprints() []string {
	if p == nil || p.composed == nil {
		return nil
	}
	names := make([]string, 0, len(p.composed.Blueprints))
	for _, bp := range p.composed.Blueprints {
		names = append(names, bp.Metadata.Name)
	}
	return names
}

// Result reports a committed application.
type Result struct {
	Team      string
	Resources int
	// Pruned names the Configs the application removed.
	Pruned []string
	// MissingSecrets lists one remediation line per required runtime secret that
	// does not exist yet; cells using it fail to start until it is created.
	MissingSecrets []string
	// Notes are operator-facing remarks: settings that no longer have an
	// effect, a missing git identity, renamed blueprints.
	Notes []string
	// Plan is the plan that was committed.
	Plan *Plan
}

// request is one team application: a declaration, where it came from, and
// the defaults it inherits.
type request struct {
	command  string
	origin   Origin
	defaults *config.TeamDefaults
	team     *config.Team
	// recorded are bindings kept from a stored Team the declaration replaces.
	recorded []config.SecretBinding
	// have are definition trees fetched already (the project source of an
	// add), and injected trees in tests.
	have *fetched
	// emptyCause explains an empty role selection (AC11).
	emptyCause string
	// defaultsDoc, when set, is a TeamDefaults document applied with the team
	// (init's first defaults).
	defaultsDoc []byte
	// notes are the command's own remarks, printed with the plan's.
	notes []string
	// sources are Sources the command stores with the team, replacing a
	// stored one of the same name (init's kukeon Source, upgrade's re-pinned
	// one and the Sources it converts).
	sources map[string]config.SourceSpec
}

// plan composes req and runs the read-only preflight.
func plan(ctx context.Context, client Client, req request) (*Plan, error) {
	team := req.team
	if team.Metadata.Realm == "" {
		team.Metadata.Realm = config.DefaultRealm
	}
	stored, err := Sources(ctx, client)
	if err != nil {
		return nil, err
	}
	var created []newSource
	for _, name := range slices.Sorted(maps.Keys(req.sources)) {
		stored[name] = req.sources[name]
		created = append(created, newSource{name: name, spec: req.sources[name]})
	}
	// The stored shape is settled first, so a name clash (AC6) fails before
	// anything is fetched or written.
	all, _ := withLegacy(req.defaults, stored)
	normalized, defined, sourceNotes, err := normalize(team, all)
	if err != nil {
		return nil, err
	}
	created = append(created, defined...)
	defaults, team, err := hydrate(req.defaults, team, stored)
	if err != nil {
		return nil, err
	}
	effective, err := config.Resolve(defaults, team)
	if err != nil {
		return nil, err
	}
	if err = checkSelection(effective, req); err != nil {
		return nil, err
	}
	for _, source := range effective.Sources {
		if err = checkSourceURL(source); err != nil {
			return nil, err
		}
	}
	trees := req.have
	if trees == nil || !trees.injected {
		var fetchedSources *fetched
		fetchedSources, err = fetchSources(
			ctx,
			identityOf(client, team.Metadata.Realm),
			effective,
			neededSources(effective, team.Spec.ProjectSource != nil),
			trees,
		)
		if err != nil {
			return nil, err
		}
		defer fetchedSources.cleanup()
		trees = fetchedSources
	}
	if trees == nil || !trees.injected {
		if err = discoverCreatedRefs(ctx, identityOf(client, team.Metadata.Realm), created); err != nil {
			return nil, err
		}
	}
	composed, err := compose.Compose(defaults, team, compose.Options{
		Sources: trees.trees,
		Secrets: req.recorded,
		Scope:   compose.Scope{Realm: team.Metadata.Realm},
	})
	if err != nil {
		return nil, compositionError(err, effective.Sources, trees.trees)
	}
	if len(composed.Pairs) == 0 {
		return nil, emptySelection(req)
	}
	// The Team is stored as declared, its definitions turned into
	// references, with the secret bindings composition used.
	normalized.Spec.Secrets = composed.Team.Spec.Secrets
	out := &Plan{
		Command:     req.command,
		Origin:      req.origin,
		Team:        normalized,
		Notes:       slices.Concat(req.notes, composed.Notes, sourceNotes),
		composed:    composed,
		defaultsDoc: req.defaultsDoc,
	}
	for _, source := range created {
		doc, docErr := sourceDocument(source)
		if docErr != nil {
			return nil, docErr
		}
		out.sourceDocs = append(out.sourceDocs, doc)
	}
	for _, source := range effective.Sources {
		if _, read := trees.trees[source.Name]; !read {
			continue
		}
		out.Sources = append(out.Sources, PlannedSource{
			Name: source.Name, URL: source.URL, Ref: sourceRef(source), Commit: trees.commits[source.Name],
		})
	}
	if err = preflight(ctx, client, out); err != nil {
		return nil, err
	}
	for _, secret := range out.pending {
		out.CreateSecrets = append(out.CreateSecrets, secret.doc.Metadata.Name)
	}
	describePairs(out)
	if err = diffConfigs(ctx, client, out); err != nil {
		return nil, err
	}
	return out, nil
}

// checkSelection fails early, before any fetch, when the team selects no
// role or no harness at all.
func checkSelection(effective config.Defaults, req request) error {
	if len(effective.Roles) == 0 || len(effective.Harnesses) == 0 {
		return emptySelection(req)
	}
	return nil
}

// emptySelection is AC11's error: which input was empty, and what fixes it.
func emptySelection(req request) error {
	cause := req.emptyCause
	switch {
	case cause != "":
	case req.team.Spec.Roles == nil && len(req.defaults.Spec.Defaults.Roles) == 0:
		cause = "the team selects no roles and the defaults have no default roles"
	case req.team.Spec.Roles != nil && len(req.team.Spec.Roles) == 0:
		cause = "the declaration's roles list is empty"
	case req.team.Spec.Harnesses == nil && len(req.defaults.Spec.Defaults.Harnesses) == 0:
		cause = "the team selects no harnesses and the defaults have no default harnesses"
	case req.team.Spec.Harnesses != nil && len(req.team.Spec.Harnesses) == 0:
		cause = "the declaration's harnesses list is empty"
	default:
		cause = "none of the selected roles declares a selected harness"
	}
	return fmt.Errorf(
		"preflight: team %s selects no role × harness pair: %s; name roles with --role "+
			"(a default role, or SOURCE:PATH in a Source stored with kuke create source)",
		req.team.Metadata.Name, cause,
	)
}

func sourceRef(source config.Source) string {
	switch {
	case source.Branch != "":
		return "branch " + source.Branch
	case source.Tag != "":
		return "tag " + source.Tag
	default:
		return "commit " + source.Commit
	}
}

func describePairs(p *Plan) {
	for _, pair := range p.composed.Pairs {
		planned := PlannedPair{
			Role: pair.Role, Harness: pair.Harness, Config: pair.Config.Metadata.Name, Image: pair.Image.Image,
			Components: slices.Clone(pair.Config.Spec.Components),
		}
		for _, task := range pair.Tasks {
			entry := PlannedTask{Name: task.Metadata.Labels[api.LabelTask], Config: task.Metadata.Name}
			if s := task.Spec.Schedule; s != nil {
				entry.Schedule, entry.Suspend = s.Cron, s.Suspend
			}
			planned.Tasks = append(planned.Tasks, entry)
		}
		p.Pairs = append(p.Pairs, planned)
	}
}

// diffConfigs compares the Configs composed with the ones the team has now.
func diffConfigs(ctx context.Context, client Client, p *Plan) error {
	listed, err := client.ListConfigs(ctx, "", "", "")
	if err != nil {
		return requestError("lookup", "configs", err)
	}
	current := map[string]bool{}
	for _, cfg := range listed {
		if cfg.Metadata.Labels[api.LabelTeam] == p.Team.Metadata.Name {
			current[cfg.Metadata.Name] = true
		}
	}
	composed := map[string]bool{}
	for _, pair := range p.composed.Pairs {
		for _, cfg := range append([]*api.CellConfigDoc{pair.Config}, pair.Tasks...) {
			name := cfg.Metadata.Name
			composed[name] = true
			if current[name] {
				p.Rerender = append(p.Rerender, name)
			} else {
				p.Create = append(p.Create, name)
			}
		}
	}
	for name := range current {
		if !composed[name] {
			p.Prune = append(p.Prune, name)
		}
	}
	slices.Sort(p.Create)
	slices.Sort(p.Rerender)
	slices.Sort(p.Prune)
	return nil
}

// injectedSources wraps definition trees a test or embedder supplies, which
// replace fetching entirely.
func injectedSources(trees map[string]fs.FS) *fetched {
	if trees == nil {
		return nil
	}
	return &fetched{trees: trees, commits: map[string]string{}, cleanup: func() {}, injected: true}
}

// errNotStored is returned when a command needs a stored Team that is not
// there.
var errNotStored = errdefs.ErrTeamNotFound

func notStored(name, realm string) error {
	return fmt.Errorf("%w: no team %s in realm %s; create it with kuke create team %s --source ADDRESS",
		errNotStored, name, realm, name)
}
