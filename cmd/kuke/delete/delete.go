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

package deletecmd

import (
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/delete/cell"
	"github.com/eminwux/kukeon/cmd/kuke/delete/container"
	"github.com/eminwux/kukeon/cmd/kuke/delete/realm"
	"github.com/eminwux/kukeon/cmd/kuke/delete/space"
	"github.com/eminwux/kukeon/cmd/kuke/delete/stack"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// NewDeleteCmd builds the `kuke delete` parent command and registers all resource
// delete subcommands. Persistent flags defined on the root kuke command are
// inherited automatically via Cobra's command tree.
func NewDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "delete [name]",
		Aliases: []string{"d"},
		Short:   "Delete Kukeon resources (realm, space, stack, cell, container)",
		Run: func(cmd *cobra.Command, _ []string) {
			_ = cmd.Help()
		},
	}

	cmd.ValidArgsFunction = completeDeleteSubcommands

	// Add persistent --cascade flag
	cmd.PersistentFlags().
		Bool("cascade", false, "Automatically delete child resources recursively (does not apply to containers)")
	_ = viper.BindPFlag(config.KUKE_DELETE_CASCADE.ViperKey, cmd.PersistentFlags().Lookup("cascade"))

	// Add persistent --force flag
	cmd.PersistentFlags().Bool("force", false, "Skip validation and attempt deletion anyway")
	_ = viper.BindPFlag(config.KUKE_DELETE_FORCE.ViperKey, cmd.PersistentFlags().Lookup("force"))

	cmd.AddCommand(
		realm.NewRealmCmd(),
		space.NewSpaceCmd(),
		stack.NewStackCmd(),
		cell.NewCellCmd(),
		container.NewContainerCmd(),
	)

	return cmd
}

// completeDeleteSubcommands provides shell completion for delete subcommand names.
func completeDeleteSubcommands(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	subcommands := []string{"realm", "space", "stack", "cell", "container"}

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
