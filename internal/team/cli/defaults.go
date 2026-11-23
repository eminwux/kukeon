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

func newDefaultsCommand(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "defaults",
		Short: "Edit the roles and harnesses every team inherits",
		Long: "The defaults are the realm's TeamDefaults (kuke get teamdefaults). Connectors, parameters and " +
			"secret bindings are set by applying a TeamDefaults document with kuke apply -f.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newDefaultsSetCommand(deps))
	return cmd
}

func newDefaultsSetCommand(deps Deps) *cobra.Command {
	var conn connection
	var roles, harnesses []string
	cmd := &cobra.Command{
		Use:   "set [--role R]... [--harness H]...",
		Short: "Replace the default role or harness list",
		Long: "Each --role and --harness is a current default's NAME or [NAME=]SOURCE:PATH in a registered " +
			"source; the flags given replace their list whole. Teams that inherit the defaults change only " +
			"when recomposed with kuke team apply NAME.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !cmd.Flags().Changed("role") {
				roles = nil
			}
			if !cmd.Flags().Changed("harness") {
				harnesses = nil
			}
			s, err := conn.open(cmd, deps, false)
			if err != nil {
				return err
			}
			defer s.close()
			change, err := initialize.SetDefaults(cmd.Context(), s.options(false), roles, harnesses)
			if err != nil {
				return err
			}
			return PrintDefaultsChange(cmd.OutOrStdout(), "Defaults updated", change)
		},
	}
	conn.register(cmd)
	cmd.Flags().StringArrayVar(&roles, "role", nil, "A default role: NAME or [NAME=]SOURCE:PATH (repeatable)")
	cmd.Flags().StringArrayVar(&harnesses, "harness", nil,
		"A default harness: NAME or [NAME=]SOURCE:PATH (repeatable)")
	return cmd
}
