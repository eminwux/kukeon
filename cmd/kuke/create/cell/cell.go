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

package cell

import (
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/create/shared"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func NewCellCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "cell [name]",
		Short:         "Create or reconcile a cell within a stack",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := shared.RequireNameArgOrDefault(
				cmd,
				args,
				"cell",
				viper.GetString(config.KUKE_CREATE_CELL_NAME.ViperKey),
			)
			if err != nil {
				return err
			}

			realm := strings.TrimSpace(viper.GetString(config.KUKE_CREATE_CELL_REALM.ViperKey))
			if realm == "" {
				return fmt.Errorf("%w (--realm)", errdefs.ErrRealmNameRequired)
			}

			space := strings.TrimSpace(viper.GetString(config.KUKE_CREATE_CELL_SPACE.ViperKey))
			if space == "" {
				return fmt.Errorf("%w (--space)", errdefs.ErrSpaceNameRequired)
			}

			stack := strings.TrimSpace(viper.GetString(config.KUKE_CREATE_CELL_STACK.ViperKey))
			if stack == "" {
				return fmt.Errorf("%w (--stack)", errdefs.ErrStackNameRequired)
			}

			ctrl, err := shared.ControllerFromCmd(cmd)
			if err != nil {
				return err
			}

			result, err := ctrl.CreateCell(controller.CreateCellOptions{
				Name:      name,
				RealmName: realm,
				SpaceName: space,
				StackName: stack,
			})
			if err != nil {
				return err
			}

			printCellResult(cmd, result)
			return nil
		},
	}

	cmd.Flags().String("realm", "", "Realm that owns the cell")
	_ = viper.BindPFlag(config.KUKE_CREATE_CELL_REALM.ViperKey, cmd.Flags().Lookup("realm"))

	cmd.Flags().String("space", "", "Space that owns the cell")
	_ = viper.BindPFlag(config.KUKE_CREATE_CELL_SPACE.ViperKey, cmd.Flags().Lookup("space"))

	cmd.Flags().String("stack", "", "Stack that owns the cell")
	_ = viper.BindPFlag(config.KUKE_CREATE_CELL_STACK.ViperKey, cmd.Flags().Lookup("stack"))

	return cmd
}

func printCellResult(cmd *cobra.Command, result controller.CreateCellResult) {
	cmd.Printf(
		"Cell %q (realm %q, space %q, stack %q)\n",
		result.Name,
		result.RealmName,
		result.SpaceName,
		result.StackName,
	)
	shared.PrintCreationOutcome(cmd, "metadata", result.MetadataExistsPost, result.Created)
	shared.PrintCreationOutcome(cmd, "cgroup", result.CgroupExistsPost, result.CgroupCreated)
	shared.PrintCreationOutcome(cmd, "root container", result.RootContainerExistsPost, result.RootContainerCreated)

	if len(result.Containers) == 0 {
		cmd.Println("  - containers: none defined")
	} else {
		for _, container := range result.Containers {
			label := fmt.Sprintf("container %q", container.Name)
			shared.PrintCreationOutcome(cmd, label, container.ExistsPost, container.Created)
		}
	}

	if result.Started {
		cmd.Println("  - containers: started")
	} else {
		cmd.Println("  - containers: not started")
	}
}
