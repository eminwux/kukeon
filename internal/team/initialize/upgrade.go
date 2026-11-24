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
	"gopkg.in/yaml.v3"
)

const upgradeCommand = "kuke team init --upgrade"

// upgrade is `kuke team init --upgrade` (issue #1638): it moves the kukeon
// source of the stored kuke Team and of realm default's TeamDefaults to the
// revision the running kuke was built from, and recomposes the kuke team. Only
// the source's branch, tag and commit change; every other field of both
// documents, spec.secrets included, is kept. A kukeon source with another URL
// is the operator's and is left alone. It never creates the kuke team, and
// recomposes no other team: the teams that inherit the defaults are named in
// the notes.
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

	var notes []string
	var teamChanged, defaultsChanged bool
	if stored != nil {
		var note []string
		teamChanged, note = repinKukeon(stored.Spec.Sources, pin, "kuke team")
		notes = append(notes, note...)
	}
	if found {
		var note []string
		defaultsChanged, note = repinKukeon(defaults.Spec.Defaults.Sources, pin, "team defaults")
		notes = append(notes, note...)
	}

	if stored == nil {
		if defaultsChanged && !opts.DryRun {
			if _, err = writeDefaults(ctx, opts.Client, defaults); err != nil {
				return Outcome{}, fmt.Errorf("init: %w", err)
			}
		}
		if defaultsChanged {
			inheriting, inhErr := inheritingNotes(ctx, opts.Client)
			if inhErr != nil {
				return Outcome{}, fmt.Errorf("init: %w", inhErr)
			}
			notes = append(notes, inheriting...)
		} else if found {
			notes = append(notes, "team defaults already at "+pin.Revision())
		}
		notes = append(notes, "team kuke is not stored; run kuke team init to create it")
		return Outcome{Notes: notes}, nil
	}

	if !teamChanged && !defaultsChanged {
		notes = append(notes, "kuke team and defaults already at "+pin.Revision())
		return Outcome{Notes: notes}, nil
	}

	var defaultsDoc []byte
	if defaultsChanged {
		if err = config.Validate(defaults); err != nil {
			return Outcome{}, fmt.Errorf("init: TeamDefaults: %w", err)
		}
		if defaultsDoc, err = yaml.Marshal(defaults); err != nil {
			return Outcome{}, fmt.Errorf("init: encode TeamDefaults: %w", err)
		}
	}
	out, err := run(ctx, opts.Options, request{
		command:     upgradeCommand,
		origin:      Origin{Kind: OriginStored},
		defaults:    defaults,
		team:        stored,
		have:        injectedSources(opts.Sources),
		defaultsDoc: defaultsDoc,
	})
	if err != nil {
		return out, err
	}
	if defaultsChanged {
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

// repinKukeon moves the built-in kukeon source in sources to pin's revision,
// in place. It reports whether a source changed and one note per change or
// per kukeon source it leaves alone because its URL is not the built-in one.
func repinKukeon(sources []config.Source, pin config.Source, what string) (bool, []string) {
	var changed bool
	var notes []string
	for i := range sources {
		s := &sources[i]
		if s.Name != config.KukeonSourceName {
			continue
		}
		if !gitcred.SameRepository(s.URL, config.KukeonSourceURL) {
			notes = append(notes, fmt.Sprintf(
				"%s: source kukeon points at %s, not %s; left at %s",
				what, s.URL, config.KukeonSourceURL, s.Revision()))
			continue
		}
		if s.Branch == pin.Branch && s.Tag == pin.Tag && s.Commit == pin.Commit {
			continue
		}
		old := s.Revision()
		s.Branch, s.Tag, s.Commit = pin.Branch, pin.Tag, pin.Commit
		notes = append(notes, fmt.Sprintf("%s: kukeon source %s -> %s", what, old, s.Revision()))
		changed = true
	}
	return changed, notes
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
