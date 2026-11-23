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

	"github.com/eminwux/kukeon/internal/team/initialize"
	"github.com/spf13/cobra"
)

func newInitCommand(deps Deps) *cobra.Command {
	var conn connection
	var force, reapply, upgrade bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Apply the kuke team and its majordomo role",
		Long: "Fetch the kukeon source, compose the kuke team (the majordomo role) and apply it to kukeond, " +
			"which stores the Team with the Configs composed from it.\n" +
			"A stored kuke team stops init; --force replaces it with the initial majordomo declaration and " +
			"--reapply recomposes it from the stored Team (kuke team apply kuke).\n" +
			"When the realm has no TeamDefaults, init creates one with the kukeon source and its claude and codex " +
			"harnesses, so a project team needs only roles. An existing TeamDefaults is never changed, except " +
			"by --upgrade.\n" +
			"--upgrade moves the kukeon source of the stored kuke team and of the TeamDefaults to the revision " +
			"this kuke was built from and recomposes the kuke team, keeping every other field (spec.secrets " +
			"included). It never creates the kuke team, leaves a kukeon source with another URL alone, and " +
			"recomposes no other team. It does not merge a newer majordomo declaration (a new role need, a new " +
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
					"--upgrade re-pins the stored kuke team's kukeon source, --force replaces the team, " +
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
		"Re-pin the kukeon source of the stored kuke team and the TeamDefaults to this kuke's revision and "+
			"recompose the kuke team")
	return cmd
}
