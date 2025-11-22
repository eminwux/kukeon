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

package autocomplete

import (
	"errors"
	"os"

	"github.com/spf13/cobra"
)

func NewAutocompleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "autocomplete",
		Short: "Generate shell completion scripts",
		Long:  "Generate shell completion scripts for bash, zsh, or fish",
		Run: func(cmd *cobra.Command, _ []string) {
			_ = cmd.Help()
		},
	}

	cmd.AddCommand(newBashCmd())
	cmd.AddCommand(newZshCmd())
	cmd.AddCommand(newFishCmd())

	return cmd
}

func newBashCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "bash",
		Short: "Generate bash completion script",
		Long:  "Generate bash completion script. Output to stdout for redirecting to a file.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			rootCmd := cmd.Root()
			if rootCmd == nil {
				return errors.New("failed to get root command")
			}
			return rootCmd.GenBashCompletion(os.Stdout)
		},
	}
}

func newZshCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "zsh",
		Short: "Generate zsh completion script",
		Long:  "Generate zsh completion script. Output to stdout for redirecting to a file.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			rootCmd := cmd.Root()
			if rootCmd == nil {
				return errors.New("failed to get root command")
			}
			return rootCmd.GenZshCompletion(os.Stdout)
		},
	}
}

func newFishCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "fish",
		Short: "Generate fish completion script",
		Long:  "Generate fish completion script. Output to stdout for redirecting to a file.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			rootCmd := cmd.Root()
			if rootCmd == nil {
				return errors.New("failed to get root command")
			}
			return rootCmd.GenFishCompletion(os.Stdout, true)
		},
	}
}
