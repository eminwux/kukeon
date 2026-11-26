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

package cli

import (
	"errors"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/team/initialize"
	"github.com/spf13/cobra"
)

func newInitCommand(deps Deps) *cobra.Command {
	var conn connection
	var force, reapply, upgrade bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Apply the kuke team (majordomo) and the default team (agent templates)",
		Long: "Fetch the kukeon source, compose the kuke team (the majordomo role) and apply it to kukeond, " +
			"which stores the Team with the Configs composed from it. Then apply the default team the same " +
			"way: the agent role on claude and codex, composed as the agent templates default-agent-claude " +
			"and default-agent-codex (label kukeon.io/template=true) that kuke create agent copies. " +
			"--force, --reapply and --upgrade cover both teams; a team named default you added before it " +
			"became a system team is left untouched, with how to rename it.\n" +
			"A stored kuke team stops init; --force replaces it with the initial majordomo declaration and " +
			"--reapply recomposes it from the stored Team (kuke team apply kuke).\n" +
			"Init stores the Source kukeon (kuke get sources) when none is stored. When the realm has no " +
			"TeamDefaults, init creates one with the claude and codex harnesses of that Source, so a project " +
			"team needs only roles. An existing TeamDefaults is never changed, except by --upgrade.\n" +
			"--upgrade re-pins the stored Source kukeon to the revision this kuke was built from, converts a " +
			"TeamDefaults or kuke team stored before Sources were a kind, and recomposes the kuke team, keeping " +
			"every other field (spec.secrets included). It never creates the kuke team, leaves a Source kukeon " +
			"with another address alone, and recomposes no other team. It does not merge a newer majordomo declaration (a new role need, a new " +
			"harness) into the stored team; --force does that. kuke update and the installer run it.\n" +
			"No cells are created or started. The daemon and parent runtime scope must already exist.\n\n" +
			"Init does not read or store a git identity. Team cells clone, push and sign with the git connector " +
			"identity a Realm, Space or Stack default names; store yours with `kuke connector import git --default`.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if reapply && force {
				return errors.New("--reapply and --force are mutually exclusive: " +
					"--reapply keeps the stored kuke team, --force replaces it with the initial declaration")
			}
			if upgrade && (force || reapply) {
				return errors.New("--upgrade is mutually exclusive with --force and --reapply: " +
					"--upgrade re-pins the stored Source kukeon, --force replaces the team, " +
					"--reapply recomposes it at its stored revision")
			}
			s, err := conn.open(cmd, deps, true)
			if err != nil {
				return err
			}
			defer s.close()
			out, err := initialize.Init(cmd.Context(), initialize.InitOptions{
				Options: s.options(false), Force: force, Reapply: reapply, Upgrade: upgrade,
			})
			if err != nil {
				// The kuke team was applied before the default team failed.
				if errors.Is(err, errdefs.ErrDefaultTeamInit) {
					if printErr := PrintOutcome(cmd.OutOrStdout(), out); printErr != nil {
						return printErr
					}
				}
				return err
			}
			return PrintOutcome(cmd.OutOrStdout(), out)
		},
	}
	conn.register(cmd)
	cmd.Flags().BoolVar(&force, "force", false,
		"Replace the stored kuke team with the initial majordomo declaration (picks up a newer majordomo "+
			"declaration; drops spec.secrets)")
	cmd.Flags().BoolVar(&reapply, "reapply", false,
		"Recompose and apply the kuke team from its stored Team, pruning obsolete configs")
	cmd.Flags().BoolVar(&upgrade, "upgrade", false,
		"Re-pin the stored Source kukeon to this kuke's revision, convert state stored before Sources were "+
			"a kind, and recompose the kuke team")
	return cmd
}
