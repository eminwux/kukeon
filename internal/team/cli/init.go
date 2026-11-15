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
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Apply and register the kukeon operator team",
		Long:  "Initialize ~/.kuke/kuketeams.yaml, fetch selected definitions, and apply the kukeon operator team to kukeond.\nAn existing configuration stops init; --force replaces it and reapplies the initial operator declaration.\nNo cells are created or started. The daemon and parent runtime scope must already exist.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := homeDir()
			if err != nil {
				return fmt.Errorf("resolve operator home directory: %w", err)
			}
			result, runErr := run(
				cmd.Context(),
				initialize.Options{
					Root:          filepath.Join(home, ".kuke"),
					Home:          home,
					Host:          host,
					Configuration: configuration,
					Force:         force,
				},
			)
			for _, diagnostic := range result.Bootstrap.Diagnostics {
				if _, err = fmt.Fprintln(cmd.ErrOrStderr(), diagnostic); err != nil {
					return err
				}
			}
			if runErr != nil {
				return runErr
			}
			status := "Created configuration"
			if force {
				status = "Reset configuration"
			}
			_, err = fmt.Fprintf(
				cmd.OutOrStdout(),
				"%s: %s\nApplied %d resources; registered kukeon team: %s\nNo cells created or started.\n",
				status,
				result.Bootstrap.Path,
				result.Resources, result.EntryPath,
			)
			return err
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "kukeond Unix endpoint (overrides KUKEON_HOST and kuke.yaml)")
	cmd.Flags().BoolVar(&force, "force", false, "Replace existing configuration and apply the initial operator declaration")
	cmd.Flags().
		StringVar(&configuration, "configuration", "", "kuke ClientConfiguration path (default KUKE_CONFIGURATION or ~/.kuke/kuke.yaml)")
	return cmd
}

func defaultInitCommand() *cobra.Command {
	return newInitCommand(os.UserHomeDir, initialize.Run)
}
