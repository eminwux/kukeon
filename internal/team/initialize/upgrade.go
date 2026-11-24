// Copyright 2025 Emiliano Spinella (eminwux)
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
	"errors"
	"fmt"
	"slices"

	"github.com/eminwux/kukeon/internal/gitcred"
	"github.com/eminwux/kukeon/pkg/team/config"
)

const upgradeCommand = "kuke team init --upgrade"

// upgrade is `kuke team init --upgrade` (issues #1638, #1643): it moves the
// stored kukeon Source to the revision the running kuke was built from and
// recomposes the kuke team. Only the Source's branch, tag and commit change.
// A kukeon Source with another URL is the operator's and is left alone.
// State written before #1643 is converted on the way: the TeamDefaults'
// sources become Sources, and the kuke team's kukeon entry becomes a
// reference to the stored Source, so the Source's revision is the one it
// reads. It never creates the kuke team, and recomposes no other team: the
// teams that inherit the defaults are named in the notes.
//
//nolint:gocyclo,cyclop,funlen // one decision per stored document
func upgrade(ctx context.Context, opts InitOptions) (Outcome, error) {
	if opts.Force || opts.Reapply {
		return Outcome{}, errors.New("init: --upgrade excludes --force and --reapply: " +
			"--upgrade re-pins the kukeon source of the stored kuke team, --force replaces the team, " +
			"--reapply recomposes it at its stored revision")
	}
	pin := config.KukeonSource()
	if opts.Pin != nil {
		pin = *opts.Pin
	}

	stored, err := StoredTeam(ctx, opts.Client, majordomoName)
	if err != nil {
		return Outcome{}, fmt.Errorf("init: %w", err)
	}
	defaults, found, err := readDefaults(ctx, opts.Client, config.DefaultRealm)
	if err != nil {
		return Outcome{}, fmt.Errorf("init: %w", err)
	}
	sources, err := Sources(ctx, opts.Client)
	if err != nil {
		return Outcome{}, fmt.Errorf("init: %w", err)
	}

	var notes []string
	// writes are the Sources this upgrade stores: the converted legacy
	// ones and the re-pinned kukeon Source.
	writes := map[string]config.SourceSpec{}
	defaultsChanged := false
	if found {
		converted, created, convertNotes := convertDefaults(defaults, sources)
		if converted != nil {
			defaults, defaultsChanged = converted, true
			notes = append(notes, convertNotes...)
			for name, spec := range created {
				writes[name] = spec
			}
		}
	}

	teamChanged := false
	if stored != nil {
		for i := range stored.Spec.Sources {
			entry := &stored.Spec.Sources[i]
			if entry.Name != config.KukeonSourceName || entry.URL == "" ||
				!gitcred.SameRepository(entry.URL, config.KukeonSourceURL) {
				continue
			}
			if _, ok := sources[config.KukeonSourceName]; !ok {
				if _, ok = writes[config.KukeonSourceName]; !ok {
					writes[config.KukeonSourceName] = specOf(*entry)
				}
			}
			*entry = config.Source{Name: entry.Name, ClonePath: entry.ClonePath}
			teamChanged = true
			notes = append(notes, "kuke team: its kukeon entry now reads the stored kukeon Source")
		}
	}

	kukeon, have := writes[config.KukeonSourceName]
	if !have {
		kukeon, have = sources[config.KukeonSourceName]
	}
	sourceChanged := false
	switch {
	case !have && stored == nil:
		// Nothing to upgrade: kuke team init creates both.
	case !have:
		// The kuke team reads a kukeon Source that is not stored.
		kukeon = config.SourceSpec{URL: config.KukeonSourceURL, Branch: pin.Branch, Tag: pin.Tag, Commit: pin.Commit}
		sourceChanged = true
		notes = append(notes, "Source kukeon: stored at "+kukeon.Revision())
	case !gitcred.SameRepository(kukeon.URL, config.KukeonSourceURL):
		notes = append(notes, fmt.Sprintf("Source kukeon points at %s, not %s; left at %s",
			kukeon.URL, config.KukeonSourceURL, kukeon.Revision()))
	case kukeon.Branch != pin.Branch || kukeon.Tag != pin.Tag || kukeon.Commit != pin.Commit:
		old := kukeon.Revision()
		kukeon.Branch, kukeon.Tag, kukeon.Commit = pin.Branch, pin.Tag, pin.Commit
		sourceChanged = true
		notes = append(notes, fmt.Sprintf("Source kukeon: %s -> %s", old, kukeon.Revision()))
	}
	if sourceChanged {
		writes[config.KukeonSourceName] = kukeon
	}
	changed := teamChanged || defaultsChanged || sourceChanged || len(writes) > 0

	if stored == nil {
		if changed && !opts.DryRun {
			if err = applySources(ctx, opts.Client, writes, defaults, defaultsChanged); err != nil {
				return Outcome{}, fmt.Errorf("init: %w", err)
			}
		}
		if defaultsChanged || sourceChanged {
			inheriting, inhErr := inheritingNotes(ctx, opts.Client)
			if inhErr != nil {
				return Outcome{}, fmt.Errorf("init: %w", inhErr)
			}
			notes = append(notes, inheriting...)
		}
		if !changed {
			notes = append(notes, "Source kukeon already at "+pin.Revision())
		}
		notes = append(notes, "team kuke is not stored; run kuke team init to create it")
		return Outcome{Notes: notes}, nil
	}

	if !changed {
		notes = append(notes, "Source kukeon already at "+pin.Revision())
		return Outcome{Notes: notes}, nil
	}

	var defaultsDoc []byte
	if defaultsChanged {
		if defaultsDoc, err = defaultsDocument(defaults); err != nil {
			return Outcome{}, fmt.Errorf("init: %w", err)
		}
	}
	out, err := run(ctx, opts.Options, request{
		command:     upgradeCommand,
		origin:      Origin{Kind: OriginStored},
		defaults:    defaults,
		team:        stored,
		have:        injectedSources(opts.Sources),
		defaultsDoc: defaultsDoc,
		sources:     writes,
	})
	if err != nil {
		return out, err
	}
	if defaultsChanged || sourceChanged {
		inheriting, inhErr := inheritingNotes(ctx, opts.Client)
		if inhErr != nil {
			return out, fmt.Errorf("init: %w", inhErr)
		}
		notes = append(notes, inheriting...)
	}
	if out.Plan != nil {
		out.Plan.Notes = append(slices.Clone(notes), out.Plan.Notes...)
	}
	if out.Result != nil {
		out.Result.Notes = append(slices.Clone(notes), out.Result.Notes...)
	}
	return out, nil
}

// inheritingNotes names the stored teams other than kuke that inherit the
// TeamDefaults, and how to recompose each: --upgrade recomposes only kuke.
func inheritingNotes(ctx context.Context, client Client) ([]string, error) {
	teams, err := InheritingTeams(ctx, client)
	if err != nil {
		return nil, err
	}
	var notes []string
	for _, team := range teams {
		if team == majordomoName {
			continue
		}
		notes = append(notes, fmt.Sprintf(
			"team %s inherits the defaults and was not recomposed; run kuke team apply %s", team, team))
	}
	return notes, nil
}
