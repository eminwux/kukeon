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

package kill

import (
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/kill/shared"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type killController interface {
	KillCell(name, realmName, spaceName, stackName string) (*controller.KillCellResult, error)
	KillContainer(name, realmName, spaceName, stackName, cellName string) (*controller.KillContainerResult, error)
}

// MockControllerKey is used to inject mock controllers in tests via context.
type MockControllerKey struct{}

// NewKillCmd builds the `kuke kill` command that handles killing cells or containers.
func NewKillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "kill --realm <realm> --space <space> --stack <stack> [--cell <cell>] <resource-type> <resource-name>",
		Short:         "Kill Kukeon resources (cell, container)",
		Long:          "Kill a cell or container. For cells, kills all containers in the cell. For containers, requires --cell flag.",
		Args:          cobra.ExactArgs(2),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Check for mock controller in context (for testing)
			var ctrl killController
			if mockCtrl, ok := cmd.Context().Value(MockControllerKey{}).(killController); ok {
				ctrl = mockCtrl
			} else {
				realCtrl, err := shared.ControllerFromCmd(cmd)
				if err != nil {
					return err
				}
				ctrl = realCtrl
			}

			resourceType := strings.ToLower(strings.TrimSpace(args[0]))
			resourceName := strings.TrimSpace(args[1])

			realm := strings.TrimSpace(viper.GetString(config.KUKE_KILL_REALM.ViperKey))
			space := strings.TrimSpace(viper.GetString(config.KUKE_KILL_SPACE.ViperKey))
			stack := strings.TrimSpace(viper.GetString(config.KUKE_KILL_STACK.ViperKey))
			cell := strings.TrimSpace(viper.GetString(config.KUKE_KILL_CELL.ViperKey))

			if realm == "" {
				return fmt.Errorf("%w (--realm)", errdefs.ErrRealmNameRequired)
			}
			if space == "" {
				return fmt.Errorf("%w (--space)", errdefs.ErrSpaceNameRequired)
			}
			if stack == "" {
				return fmt.Errorf("%w (--stack)", errdefs.ErrStackNameRequired)
			}

			switch resourceType {
			case "cell":
				result, err := ctrl.KillCell(resourceName, realm, space, stack)
				if err != nil {
					return err
				}
				cmd.Printf("Killed cell %q from stack %q\n", result.CellName, result.StackName)
				return nil

			case "container":
				if cell == "" {
					return fmt.Errorf("%w (--cell)", errdefs.ErrCellNameRequired)
				}
				result, err := ctrl.KillContainer(resourceName, realm, space, stack, cell)
				if err != nil {
					return err
				}
				cmd.Printf("Killed container %q from cell %q\n", result.ContainerName, result.CellName)
				return nil

			default:
				return fmt.Errorf("invalid resource type %q, must be 'cell' or 'container'", resourceType)
			}
		},
	}

	cmd.Flags().String("realm", "", "Realm that owns the resource")
	_ = viper.BindPFlag(config.KUKE_KILL_REALM.ViperKey, cmd.Flags().Lookup("realm"))

	cmd.Flags().String("space", "", "Space that owns the resource")
	_ = viper.BindPFlag(config.KUKE_KILL_SPACE.ViperKey, cmd.Flags().Lookup("space"))

	cmd.Flags().String("stack", "", "Stack that owns the resource")
	_ = viper.BindPFlag(config.KUKE_KILL_STACK.ViperKey, cmd.Flags().Lookup("stack"))

	cmd.Flags().String("cell", "", "Cell that owns the container (required for container resource type)")
	_ = viper.BindPFlag(config.KUKE_KILL_CELL.ViperKey, cmd.Flags().Lookup("cell"))

	// Register autocomplete functions for flags and positional arguments
	cmd.ValidArgsFunction = completeKillArgs
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)
	_ = cmd.RegisterFlagCompletionFunc("cell", config.CompleteCellNames)

	return cmd
}

// completeKillArgs provides shell completion for kill command positional arguments.
// When len(args) == 0, completes resource-type ("cell" or "container").
// When len(args) == 1, completes resource-name based on the resource-type.
func completeKillArgs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	// First argument: resource-type
	if len(args) == 0 {
		resourceTypes := []string{"cell", "container"}
		matches := make([]string, 0, len(resourceTypes))
		for _, rt := range resourceTypes {
			if toComplete == "" || strings.HasPrefix(rt, toComplete) {
				matches = append(matches, rt)
			}
		}
		return matches, cobra.ShellCompDirectiveNoFileComp
	}

	// Second argument: resource-name (depends on resource-type)
	if len(args) == 1 {
		resourceType := strings.ToLower(strings.TrimSpace(args[0]))
		switch resourceType {
		case "cell":
			return config.CompleteCellNames(cmd, args, toComplete)
		case "container":
			return config.CompleteContainerNames(cmd, args, toComplete)
		default:
			// Unknown resource type, return empty
			return []string{}, cobra.ShellCompDirectiveNoFileComp
		}
	}

	// More than 2 args, return empty
	return []string{}, cobra.ShellCompDirectiveNoFileComp
}
