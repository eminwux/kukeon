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

package purge

import (
	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/purge/cell"
	"github.com/eminwux/kukeon/cmd/kuke/purge/container"
	"github.com/eminwux/kukeon/cmd/kuke/purge/realm"
	"github.com/eminwux/kukeon/cmd/kuke/purge/space"
	"github.com/eminwux/kukeon/cmd/kuke/purge/stack"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// NewPurgeCmd builds the `kuke purge` parent command and registers all resource
// purge subcommands. Persistent flags defined on the root kuke command are
// inherited automatically via Cobra's command tree.
func NewPurgeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "purge",
		Short: "Purge Kukeon resources with comprehensive cleanup (realm, space, stack, cell, container)",
		Run: func(cmd *cobra.Command, _ []string) {
			_ = cmd.Help()
		},
	}

	// Add persistent --cascade flag
	cmd.PersistentFlags().
		Bool("cascade", false, "Automatically purge child resources recursively (does not apply to containers)")
	_ = viper.BindPFlag(config.KUKE_PURGE_CASCADE.ViperKey, cmd.PersistentFlags().Lookup("cascade"))

	// Add persistent --force flag
	cmd.PersistentFlags().Bool("force", false, "Skip validation and attempt purge anyway")
	_ = viper.BindPFlag(config.KUKE_PURGE_FORCE.ViperKey, cmd.PersistentFlags().Lookup("force"))

	cmd.AddCommand(
		realm.NewRealmCmd(),
		space.NewSpaceCmd(),
		stack.NewStackCmd(),
		cell.NewCellCmd(),
		container.NewContainerCmd(),
	)

	return cmd
}
