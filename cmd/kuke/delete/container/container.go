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

package container

import (
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/delete/shared"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type containerController interface {
	DeleteContainer(name, realm, space, stack, cell string) (*controller.DeleteContainerResult, error)
}

// MockControllerKey is used to inject mock controllers in tests via context.
type MockControllerKey struct{}

func NewContainerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "container [name]",
		Short:         "Delete a container",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Check for mock controller in context (for testing)
			var ctrl containerController
			if mockCtrl, ok := cmd.Context().Value(MockControllerKey{}).(containerController); ok {
				ctrl = mockCtrl
			} else {
				realCtrl, err := shared.ControllerFromCmd(cmd)
				if err != nil {
					return err
				}
				ctrl = realCtrl
			}

			name := strings.TrimSpace(args[0])
			realm := strings.TrimSpace(viper.GetString(config.KUKE_DELETE_CONTAINER_REALM.ViperKey))
			space := strings.TrimSpace(viper.GetString(config.KUKE_DELETE_CONTAINER_SPACE.ViperKey))
			stack := strings.TrimSpace(viper.GetString(config.KUKE_DELETE_CONTAINER_STACK.ViperKey))
			cell := strings.TrimSpace(viper.GetString(config.KUKE_DELETE_CONTAINER_CELL.ViperKey))

			if realm == "" {
				return fmt.Errorf("%w (--realm)", errdefs.ErrRealmNameRequired)
			}
			if space == "" {
				return fmt.Errorf("%w (--space)", errdefs.ErrSpaceNameRequired)
			}
			if stack == "" {
				return fmt.Errorf("%w (--stack)", errdefs.ErrStackNameRequired)
			}
			if cell == "" {
				return fmt.Errorf("%w (--cell)", errdefs.ErrCellNameRequired)
			}

			result, err := ctrl.DeleteContainer(name, realm, space, stack, cell)
			if err != nil {
				return err
			}

			cmd.Printf("Deleted container %q from cell %q\n", result.ContainerName, result.CellName)
			return nil
		},
	}

	cmd.Flags().String("realm", "", "Realm that owns the container")
	_ = viper.BindPFlag(config.KUKE_DELETE_CONTAINER_REALM.ViperKey, cmd.Flags().Lookup("realm"))

	cmd.Flags().String("space", "", "Space that owns the container")
	_ = viper.BindPFlag(config.KUKE_DELETE_CONTAINER_SPACE.ViperKey, cmd.Flags().Lookup("space"))

	cmd.Flags().String("stack", "", "Stack that owns the container")
	_ = viper.BindPFlag(config.KUKE_DELETE_CONTAINER_STACK.ViperKey, cmd.Flags().Lookup("stack"))

	cmd.Flags().String("cell", "", "Cell that owns the container")
	_ = viper.BindPFlag(config.KUKE_DELETE_CONTAINER_CELL.ViperKey, cmd.Flags().Lookup("cell"))

	// Register autocomplete functions for flags and positional argument
	cmd.ValidArgsFunction = config.CompleteContainerNames
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)
	_ = cmd.RegisterFlagCompletionFunc("cell", config.CompleteCellNames)

	return cmd
}
