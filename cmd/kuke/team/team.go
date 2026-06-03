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

// Package team hosts the `kuke team` parent command and its subcommands. The
// team-distribution epic (#792) lands `init` across four steps: skeleton +
// drop-in lifecycle (#796), pinned-source resolve + cache + role/harness/image
// load (#1041), render pipeline (needs-merge / image-select / bind) (#1042),
// and apply-with-prune to kukeond (#1043). After step 4, `kuke team init`
// reads the project's committed kuketeam.yaml roster, scaffolds the operator-
// global facts file (~/.kuke/kuketeams.yaml) on first run, renders the per-
// (role × harness) CellBlueprint/CellConfig pairs labeled with the project,
// applies that labeled set to kukeond (per-team prune via #1029 — re-running
// in one project prunes only that project's stale objects and leaves every
// other project untouched), and writes a per-project drop-in entry
// (~/.kuke/kuketeam.d/<project>.yaml). Nothing is written under
// ~/.kuke/rendered/ — the daemon owns the persisted blueprints/configs and
// the drop-in entry is the only host-side record of an applied team.
package team

import (
	"github.com/spf13/cobra"
)

// NewTeamCmd builds the `kuke team` parent command and registers its
// subcommands. Persistent flags on the root kuke command are inherited
// automatically.
func NewTeamCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "team",
		Short: "Compose per-project agent teams from the in-repo kuketeam.yaml roster",
		Run: func(cmd *cobra.Command, _ []string) {
			_ = cmd.Help()
		},
	}

	cmd.AddCommand(NewInitCmd())

	return cmd
}
