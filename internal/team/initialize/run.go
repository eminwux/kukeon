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

// Package initialize composes kuke teams and applies them to kukeond (issue
// #1618): kukeond stores the Team and TeamDefaults documents, this package
// fetches sources, composes and applies, without managing cells. The CLI
// (internal/team/cli), `kuke apply` and the web server all call it, with a
// client they own.
package initialize

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	"github.com/eminwux/kukeon/pkg/team/config"
	"gopkg.in/yaml.v3"
)

const (
	majordomoName = config.SystemTeamName
	rootName      = "root" // Reserved legacy identity.
)

// Options are what every team operation takes: the daemon client, which the
// caller dials and closes (an operation never dials), and optional injected
// definition trees that replace fetching (tests and embedders).
type Options struct {
	Client  Client
	Sources map[string]fs.FS
	// DryRun stops after the plan: nothing is created or applied and no
	// secret value is read.
	DryRun bool
}

// Outcome is what a team operation returns: the plan, and the result when it
// was committed (nil on a dry run).
type Outcome struct {
	Plan   *Plan
	Result *Result
	// Notes are the remarks of an operation that planned no team
	// application (kuke team init --upgrade with nothing to recompose);
	// Plan and Result are nil then.
	Notes []string
}

func (o Options) check() error {
	if o.Client == nil {
		return errors.New("team: no kukeond client")
	}
	return nil
}

// InitOptions select init's mode.
type InitOptions struct {
	Options

	// Force replaces the stored kuke team with the initial declaration.
	Force bool
	// Reapply recomposes the kuke team from its stored Team: kuke team apply
	// kuke. It excludes Force.
	Reapply bool
	// Upgrade re-pins the kukeon source of the stored kuke Team and of the
	// realm's TeamDefaults to the running binary's revision and recomposes
	// the kuke team (issue #1638). It excludes Force and Reapply.
	Upgrade bool
	// Pin replaces config.KukeonSource() as Upgrade's target revision
	// (tests).
	Pin *config.Source
}

// Init applies the kuke team (issue #1618, AC7). "Already initialized" is the
// stored kuke Team. When the realm has no TeamDefaults, init creates one in
// the same apply, with the kukeon source and its claude and codex harnesses,
// so a project team needs only roles; an existing TeamDefaults is never
// changed. Partial daemon application is not rolled back; a forced rerun
// converges the team.
func Init(ctx context.Context, opts InitOptions) (Outcome, error) {
	if err := opts.check(); err != nil {
		return Outcome{}, err
	}
	if opts.Upgrade {
		return upgrade(ctx, opts)
	}
	if opts.Reapply {
		if opts.Force {
			return Outcome{}, errors.New("init: --reapply and --force are mutually exclusive: " +
				"--reapply keeps the stored kuke team, --force replaces it with the initial declaration")
		}
		return ApplyStored(ctx, opts.Options, majordomoName, "kuke team init --reapply")
	}
	// --force replaces the stored team without consulting it, so a stored
	// declaration that no longer decodes is reset too.
	if !opts.Force {
		stored, err := StoredTeam(ctx, opts.Client, majordomoName)
		if err != nil {
			return Outcome{}, fmt.Errorf("init: %w; use --force to reset it", err)
		}
		if stored != nil {
			return Outcome{}, errors.New(
				"init: already initialized (team kuke is stored); use --force to reset or --reapply to recompose")
		}
	}
	defaults, found, err := readDefaults(ctx, opts.Client, config.DefaultRealm)
	if err != nil {
		return Outcome{}, fmt.Errorf("init: %w", err)
	}
	var defaultsDoc []byte
	if !found {
		defaults = InitialDefaults(config.DefaultRealm)
		if defaultsDoc, err = yaml.Marshal(defaults); err != nil {
			return Outcome{}, fmt.Errorf("init: encode TeamDefaults: %w", err)
		}
	}
	// The kukeon Source the kuke team reads is stored with it, unless one
	// is stored (or a legacy TeamDefaults still lists it).
	stored, err := Sources(ctx, opts.Client)
	if err != nil {
		return Outcome{}, fmt.Errorf("init: %w", err)
	}
	var sources map[string]config.SourceSpec
	if all, _ := withLegacy(defaults, stored); !hasSource(all, config.KukeonSourceName) {
		sources = map[string]config.SourceSpec{config.KukeonSourceName: config.KukeonSourceSpec()}
	}
	return run(ctx, opts.Options, request{
		command:     "kuke team init",
		origin:      Origin{Kind: OriginInitial},
		defaults:    defaults,
		team:        config.Majordomo(),
		have:        injectedSources(opts.Sources),
		defaultsDoc: defaultsDoc,
		sources:     sources,
	})
}

// InitialDefaults is the TeamDefaults init creates in a realm that has none:
// the codex and claude harnesses of the kukeon source. It has no sources
// (#1643: init stores the kukeon Source, at the ref the kuke team reads it
// from, beside it) and no default roles: those come from the Sources the
// operator stores.
func InitialDefaults(realm string) *config.TeamDefaults {
	defaults := config.EmptyDefaults(realm)
	defaults.Spec.Defaults = config.Defaults{Harnesses: config.MajordomoHarnesses()}
	return defaults
}

// ApplyStored recomposes a team from its stored Team without consulting its
// repository's kuketeam.yaml (AC12): the generalized init --reapply. The
// daemon prunes the Configs the new composition no longer renders.
func ApplyStored(ctx context.Context, opts Options, name, command string) (Outcome, error) {
	if err := opts.check(); err != nil {
		return Outcome{}, err
	}
	if command == "" {
		command = "kuke team apply " + name
	}
	stored, err := StoredTeam(ctx, opts.Client, name)
	if err != nil {
		return Outcome{}, err
	}
	if stored == nil {
		if name == majordomoName {
			return Outcome{}, errors.New(
				"team kuke is not stored; run kuke team init first (or kuke team init --force to start over)")
		}
		return Outcome{}, notStored(name, config.DefaultRealm)
	}
	defaults, _, err := readDefaults(ctx, opts.Client, stored.Metadata.Realm)
	if err != nil {
		return Outcome{}, err
	}
	return run(ctx, opts, request{
		command: command, origin: Origin{Kind: OriginStored}, defaults: defaults, team: stored,
		have: injectedSources(opts.Sources),
	})
}

// ApplyFile composes the Team document in raw and stores it, creating the
// team when it is new (AC12). raw is a Team document, which is also what a
// repository's kuketeam.yaml is; when it has no spec.projectSource the stored
// team's is kept, and the stored team's secret bindings are kept beneath the
// document's own. `kuke apply -f` calls it for each Team document (D7).
func ApplyFile(ctx context.Context, opts Options, raw []byte, path string) (Outcome, error) {
	if err := opts.check(); err != nil {
		return Outcome{}, err
	}
	team, err := config.DecodeTeam(bytes.NewReader(raw))
	if err != nil {
		return Outcome{}, fmt.Errorf("team file %s: %w", path, err)
	}
	if team.Metadata.Name == rootName {
		return Outcome{}, fmt.Errorf("team file %s: team name %q is reserved", path, rootName)
	}
	if team.Metadata.Realm == "" {
		team.Metadata.Realm = config.DefaultRealm
	}
	if team.Metadata.Realm != config.DefaultRealm {
		return Outcome{}, fmt.Errorf("team file %s: teams live in realm %s; remove metadata.realm %q",
			path, config.DefaultRealm, team.Metadata.Realm)
	}
	stored, err := StoredTeam(ctx, opts.Client, team.Metadata.Name)
	if err != nil {
		return Outcome{}, err
	}
	var recorded []config.SecretBinding
	if stored != nil {
		if team.Spec.ProjectSource == nil {
			team.Spec.ProjectSource = stored.Spec.ProjectSource
		}
		recorded = stored.Spec.Secrets
	}
	if project := team.Spec.ProjectSource; project != nil && project.Name == "" {
		project.Name = config.ProjectSourceName
	}
	defaults, _, err := readDefaults(ctx, opts.Client, team.Metadata.Realm)
	if err != nil {
		return Outcome{}, err
	}
	return run(ctx, opts, request{
		command:  "kuke team apply -f " + path,
		origin:   Origin{Kind: OriginFile, Detail: path},
		defaults: defaults,
		team:     team,
		recorded: recorded,
		have:     injectedSources(opts.Sources),
	})
}

// run plans req and, unless this is a dry run, commits the plan.
func run(ctx context.Context, opts Options, req request) (Outcome, error) {
	p, err := plan(ctx, opts.Client, req)
	if err != nil {
		return Outcome{}, withRerun(err, req.command)
	}
	if opts.DryRun {
		return Outcome{Plan: p}, nil
	}
	if err = ctx.Err(); err != nil {
		return Outcome{Plan: p}, err
	}
	result, err := commit(ctx, opts.Client, p)
	if err != nil {
		return Outcome{Plan: p}, withRerun(err, req.command)
	}
	return Outcome{Plan: p, Result: &result}, nil
}

func withRerun(err error, command string) error {
	if errors.Is(err, errNotStored) {
		return err
	}
	// A document fetched from a source is not a local prerequisite (#1631).
	var skew *sourceSkewError
	if errors.As(err, &skew) {
		return fmt.Errorf("%w, then rerun %s", err, command)
	}
	return fmt.Errorf("%w; after fixing the prerequisite, rerun %s", err, command)
}

// StoredTeam reads the stored Team of name in realm default, or nil when none
// is stored.
func StoredTeam(ctx context.Context, client Client, name string) (*config.Team, error) {
	got, err := client.GetTeam(ctx, kukeonv1.TeamRef{Name: name, Realm: config.DefaultRealm})
	if err != nil {
		return nil, requestError("lookup", "team "+name, err)
	}
	if !got.Exists {
		return nil, nil //nolint:nilnil // an unstored team is an answer, not an error
	}
	team, err := config.DecodeTeam(bytes.NewReader(got.Document))
	if err != nil {
		return nil, fmt.Errorf("stored team %s: %w", name, err)
	}
	return team, nil
}

// readDefaults reads realm's stored TeamDefaults; found is false and the
// empty defaults are returned when the realm stores none.
func readDefaults(ctx context.Context, client Client, realm string) (*config.TeamDefaults, bool, error) {
	got, err := client.GetTeamDefaults(ctx, realm)
	if err != nil {
		return nil, false, requestError("lookup", "team defaults", err)
	}
	if !got.Exists {
		return config.EmptyDefaults(realm), false, nil
	}
	defaults, err := config.DecodeStoredDefaults(bytes.NewReader(got.Document))
	if err != nil {
		return nil, false, fmt.Errorf("stored team defaults of realm %s: %w", realm, err)
	}
	return defaults, true, nil
}
