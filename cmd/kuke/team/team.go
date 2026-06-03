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

// Package team hosts the `kuke team` parent command and its subcommands. Step 1
// (#796, epic team-distribution #792) lands `init`: it reads the project's
// committed kuketeam.yaml roster, scaffolds the operator-global facts file
// (~/.kuke/kuketeams.yaml) on first run, and writes a per-project drop-in entry
// (~/.kuke/kuketeam.d/<project>.yaml). Source resolution, render, and apply land
// in steps 2–4 (#1041/#1042/#1043). Every `kuke team *` verb is host-local and
// daemon-independent — they manipulate ~/.kuke files, not /opt/kukeon state — so
// none require a live kukeond.
package team

import (
	"github.com/spf13/cobra"
)

// NewTeamCmd builds the `kuke team` parent command and registers its
// subcommands. Persistent flags on the root kuke command are inherited
// automatically.
func NewTeamCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "team",
		Short: "Compose per-project agent teams from the in-repo kuketeam.yaml roster",
		Run: func(cmd *cobra.Command, _ []string) {
			_ = cmd.Help()
		},
	}

	cmd.AddCommand(NewInitCmd())

	return cmd
}
