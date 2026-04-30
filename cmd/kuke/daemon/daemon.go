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

// Package daemon hosts the `kuke daemon` subcommand group, which exposes
// daemon-lifecycle verbs (start, and later stop/kill/restart/reset). These
// commands operate on kukeond itself and therefore run in-process — they
// cannot route through the daemon they are managing.
package daemon

import (
	"strings"

	startcmd "github.com/eminwux/kukeon/cmd/kuke/daemon/start"
	"github.com/spf13/cobra"
)

// NewDaemonCmd builds the `kuke daemon` parent command and registers all
// daemon-lifecycle subcommands. Persistent flags defined on the root kuke
// command are inherited automatically via Cobra's command tree.
func NewDaemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Manage the kukeond daemon lifecycle (start, stop, ...)",
		Long: "Manage the kukeond daemon lifecycle.\n\n" +
			"These commands act on the kukeond cell provisioned by `kuke init`. " +
			"They run in-process because the daemon they manage may not be " +
			"running at the time the command is invoked.",
		Run: func(cmd *cobra.Command, _ []string) {
			_ = cmd.Help()
		},
	}

	cmd.ValidArgsFunction = completeDaemonSubcommands

	cmd.AddCommand(
		startcmd.NewStartCmd(),
	)

	return cmd
}

// completeDaemonSubcommands provides shell completion for daemon subcommand names.
func completeDaemonSubcommands(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	subcommands := []string{"start"}

	if toComplete == "" {
		return subcommands, cobra.ShellCompDirectiveNoFileComp
	}

	matches := make([]string, 0, len(subcommands))
	for _, subcmd := range subcommands {
		if strings.HasPrefix(subcmd, toComplete) {
			matches = append(matches, subcmd)
		}
	}

	return matches, cobra.ShellCompDirectiveNoFileComp
}
