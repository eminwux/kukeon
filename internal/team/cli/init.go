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
	var force, reapply bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Apply the kuke team and its majordomo role",
		Long: "Fetch the kukeon source, compose the kuke team (the majordomo role) and apply it to kukeond, " +
			"which stores the Team with the Configs composed from it.\n" +
			"A stored kuke team stops init; --force replaces it with the initial majordomo declaration and " +
			"--reapply recomposes it from the stored Team (kuke team apply kuke).\n" +
			"When the realm has no TeamDefaults, init creates one with the kukeon source and its claude and codex " +
			"harnesses, so a project team needs only roles. An existing TeamDefaults is never changed.\n" +
			"No cells are created or started. The daemon and parent runtime scope must already exist.\n\n" +
			"Init does not read or store a git identity. Team cells clone, push and sign with the git connector " +
			"identity a Realm, Space or Stack default names; store yours with `kuke connector import git --default`.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if reapply && force {
				return errors.New("--reapply and --force are mutually exclusive: " +
					"--reapply keeps the stored kuke team, --force replaces it with the initial declaration")
			}
			s, err := conn.open(cmd, deps, true)
			if err != nil {
				return err
			}
			defer s.close()
			out, err := initialize.Init(cmd.Context(), initialize.InitOptions{
				Options: s.options(false), Force: force, Reapply: reapply,
			})
			if err != nil {
				return err
			}
			return PrintOutcome(cmd.OutOrStdout(), out)
		},
	}
	conn.register(cmd)
	cmd.Flags().BoolVar(&force, "force", false, "Replace the stored kuke team with the initial majordomo declaration")
	cmd.Flags().BoolVar(&reapply, "reapply", false,
		"Recompose and apply the kuke team from its stored Team, pruning obsolete configs")
	return cmd
}
