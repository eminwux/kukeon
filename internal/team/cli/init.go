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
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/eminwux/kukeon/internal/team/initialize"
	"github.com/spf13/cobra"
)

// newInitCommand keeps home resolution and result formatting in the CLI. The
// injected functions let tests exercise outcomes without changing operator HOME.
func newInitCommand(
	homeDir func() (string, error),
	run func(context.Context, initialize.Options) (initialize.Result, error),
) *cobra.Command {
	var host, configuration string
	var force, reapply bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Apply and register the kuke team and its majordomo role",
		Long: "Fetch selected definitions, apply the kuke team (the majordomo role) to kukeond and register it at " +
			"~/.kuke/kuketeam.d/kuke.yaml.\nAn existing registration stops init; --force replaces it with the initial " +
			"majordomo declaration, migrating a host initialized under the old kukeon team name.\n" +
			"~/.kuke/kuketeams.yaml is optional and hand-written: init reads it when present and never writes it.\n" +
			"No cells are created or started. The daemon and parent runtime scope must already exist.\n\n" +
			"Init does not read or store a git identity. Team cells clone, push and sign with the git connector " +
			"identity a Realm, Space or Stack default names; store yours with `kuke connector import git --default`.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if reapply && force {
				return errors.New("--reapply and --force are mutually exclusive: " +
					"--reapply keeps the stored kuke entry, --force replaces it with the initial declaration")
			}
			home, err := homeDir()
			if err != nil {
				return fmt.Errorf("resolve operator home directory: %w", err)
			}
			result, err := run(
				cmd.Context(),
				initialize.Options{
					Root:          filepath.Join(home, ".kuke"),
					Home:          home,
					Host:          host,
					Configuration: configuration,
					Force:         force,
					Reapply:       reapply,
				},
			)
			if err != nil {
				return err
			}
			if result.ConfigPath != "" {
				if _, err = fmt.Fprintf(cmd.OutOrStdout(), "Read configuration: %s\n", result.ConfigPath); err != nil {
					return err
				}
			}
			_, err = fmt.Fprintf(
				cmd.OutOrStdout(),
				"Applied %d resources; registered kuke team: %s\nNo cells created or started.\n",
				result.Resources, result.EntryPath,
			)
			if err == nil {
				err = printMissingSecrets(cmd.OutOrStdout(), result.MissingSecrets)
			}
			if err == nil {
				err = printNotes(cmd.OutOrStdout(), result.Notes)
			}
			return err
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "kukeond Unix endpoint (overrides KUKEON_HOST and kuke.yaml)")
	cmd.Flags().
		BoolVar(&force, "force", false, "Replace the registered kuke team with the initial majordomo declaration")
	cmd.Flags().
		BoolVar(&reapply, "reapply", false, "Recompose and apply the kuke team from its stored entry (~/.kuke/kuketeam.d/kuke.yaml), pruning obsolete configs")
	cmd.Flags().
		StringVar(&configuration, "configuration", "", "kuke ClientConfiguration path (default KUKE_CONFIGURATION or ~/.kuke/kuke.yaml)")
	return cmd
}

func defaultInitCommand() *cobra.Command {
	return newInitCommand(os.UserHomeDir, initialize.Run)
}
