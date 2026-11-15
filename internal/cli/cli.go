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

// Package cli owns the `kuke team` command construction and output,
// independently of process exit.
package cli

import "github.com/spf13/cobra"

// NewTeamCommand returns the `kuke team` command: the composer's init and add
// commands under a parent that kuke's root command mounts.
//
// The parent's no-op PersistentPreRunE replaces the kuke root's hook for this
// subtree, so init and add resolve the operator home, the kukeond endpoint
// and the ClientConfiguration themselves instead of going through kuke's
// runtime configuration loading. The subcommands' --host and --configuration
// flags shadow kuke's persistent flags of the same names.
func NewTeamCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "team",
		Short:             "Compose agent teams and register them with kukeond",
		Args:              cobra.NoArgs,
		SilenceUsage:      true,
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
		RunE:              func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	// Cobra honors SilenceUsage only on the executing command or the root, so
	// set it on each subcommand: a failed init/add reports its error alone.
	for _, sub := range []*cobra.Command{defaultInitCommand(), defaultAddCommand()} {
		sub.SilenceUsage = true
		cmd.AddCommand(sub)
	}
	return cmd
}
