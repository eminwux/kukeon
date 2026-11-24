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
	"github.com/eminwux/kukeon/internal/team/initialize"
	"github.com/spf13/cobra"
)

func newExportCommand(deps Deps) *cobra.Command {
	var conn connection
	var opts initialize.ExportOptions
	cmd := &cobra.Command{
		Use:   "export NAME",
		Short: "Print a stored team as a repository's kuketeam.yaml",
		Long: "Print the stored Team NAME as a kuketeam.yaml: its name, the project's address, each source " +
			"with its address and ref, and the declared roles, harnesses and connectors. Without " +
			"--host-fields it leaves out what only means something on this host: the realm, the stored names " +
			"of the Sources, the project's ref, the git identities and spec.secrets.\n" +
			"--explicit-defaults writes the lists the team inherits from the TeamDefaults out.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := conn.open(cmd, deps, false)
			if err != nil {
				return err
			}
			defer s.close()
			raw, err := initialize.Export(cmd.Context(), s.client, args[0], opts)
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(raw)
			return err
		},
	}
	conn.register(cmd)
	cmd.Flags().BoolVar(&opts.HostFields, "host-fields", false,
		"Keep the realm, the stored Source names, the project's ref, the git identities and spec.secrets")
	cmd.Flags().BoolVar(&opts.ExplicitDefaults, "explicit-defaults", false,
		"Write the role, harness and connector lists the team inherits from the TeamDefaults")
	return cmd
}
