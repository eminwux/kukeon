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

	"github.com/eminwux/kukeon/pkg/team/config"
)

const defaultName = config.DefaultTeamName

// userDefaultTeamNote is what init prints when the stored team default is a
// user's team, stored before default became a system team: init leaves it
// alone and says how to free the name.
const userDefaultTeamNote = "team default is a team you added before default became a system team; " +
	"init left it untouched and did not apply the system default team (the agent templates). " +
	"To rename yours: kuke team export default > team.yaml, set metadata.name in team.yaml, " +
	"kuke apply -f team.yaml, kuke delete team default, then kuke team init --reapply"

// initDefault applies the system default team after the kuke team (issue
// #1692), in the mode of the same init: kuke is the kuke team's outcome.
//
//   - A stored team default that is not the system one is left untouched,
//     with userDefaultTeamNote.
//   - --reapply recomposes a stored default team, as --upgrade does when it
//     recomposed the kuke team; either creates it when none is stored, so an
//     install that predates it gets it.
//   - --upgrade with nothing to recompose creates the team only when none is
//     stored, and never when the kuke team is not stored.
//   - A plain init creates it when none is stored, and keeps a stored one;
//     --force replaces it with the initial declaration.
func initDefault(ctx context.Context, opts InitOptions, kuke Outcome) (Outcome, error) {
	stored, err := StoredTeam(ctx, opts.Client, defaultName)
	if err != nil {
		return Outcome{}, err
	}
	if stored != nil && !config.IsSystemDefaultTeam(stored) {
		return Outcome{Notes: []string{userDefaultTeamNote}}, nil
	}
	command := "kuke team init"
	switch {
	case opts.Upgrade:
		command = upgradeCommand
		if kuke.Result == nil && kuke.Plan == nil {
			// Nothing was recomposed: the kuke team is not stored, or its
			// Source is already at this revision.
			kukeStored, kukeErr := StoredTeam(ctx, opts.Client, majordomoName)
			if kukeErr != nil {
				return Outcome{}, kukeErr
			}
			if kukeStored == nil {
				return Outcome{Notes: []string{"team default is not applied either; kuke team init applies both"}}, nil
			}
			if stored != nil {
				return Outcome{Notes: []string{"team default already reads that revision"}}, nil
			}
		}
	case opts.Reapply:
		command = "kuke team init --reapply"
	case !opts.Force && stored != nil:
		return Outcome{Notes: []string{
			"team default is stored; kept (kuke team init --reapply recomposes it, --force resets it)",
		}}, nil
	}
	if stored != nil && !opts.Force {
		return ApplyStored(ctx, opts.Options, defaultName, command)
	}
	defaults, _, err := readDefaults(ctx, opts.Client, config.DefaultRealm)
	if err != nil {
		return Outcome{}, fmt.Errorf("init: %w", err)
	}
	// The kuke team's apply stored the kukeon Source; a dry run did not,
	// so the plan states it as kuke's did.
	storedSources, err := Sources(ctx, opts.Client)
	if err != nil {
		return Outcome{}, fmt.Errorf("init: %w", err)
	}
	var sources map[string]config.SourceSpec
	if all, _ := withLegacy(defaults, storedSources); !hasSource(all, config.KukeonSourceName) {
		sources = map[string]config.SourceSpec{config.KukeonSourceName: config.KukeonSourceSpec()}
	}
	return run(ctx, opts.Options, request{
		command:  command,
		origin:   Origin{Kind: OriginInitial},
		defaults: defaults,
		team:     config.DefaultTeam(),
		have:     injectedSources(opts.Sources),
		sources:  sources,
	})
}
