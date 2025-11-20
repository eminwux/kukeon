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

package stop

import (
	cellcmd "github.com/eminwux/kukeon/cmd/kuke/stop/cell"
	containercmd "github.com/eminwux/kukeon/cmd/kuke/stop/container"
	"github.com/spf13/cobra"
)

// NewStopCmd builds the `kuke stop` parent command and registers all resource
// stop subcommands. Persistent flags defined on the root kuke command are
// inherited automatically via Cobra's command tree.
func NewStopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop Kukeon resources (cell, container)",
		Run: func(cmd *cobra.Command, _ []string) {
			_ = cmd.Help()
		},
	}

	cmd.AddCommand(
		cellcmd.NewCellCmd(),
		containercmd.NewContainerCmd(),
	)

	return cmd
}
