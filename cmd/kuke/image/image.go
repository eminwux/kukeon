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

// Package image hosts the `kuke image` parent command and its subcommands.
// Phase 1 ships `load` only; `get` and `delete` arrive in #211 and #212.
package image

import (
	"github.com/spf13/cobra"
)

// NewImageCmd builds the `kuke image` parent command and registers its
// subcommands. Persistent flags on the root kuke command are inherited
// automatically.
func NewImageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "image",
		Short: "Manage container images in a realm's containerd namespace",
		Run: func(cmd *cobra.Command, _ []string) {
			_ = cmd.Help()
		},
	}

	cmd.AddCommand(NewLoadCmd())

	return cmd
}
