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

package stack

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

func NewStackCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "stack [name]",
		Short:         "Create or reconcile a stack within a space",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := shared.RequireNameArgOrDefault(
				cmd,
				args,
				"stack",
				viper.GetString(config.KUKE_CREATE_STACK_NAME.ViperKey),
			)
			if err != nil {
				return err
			}

			realm := strings.TrimSpace(viper.GetString(config.KUKE_CREATE_STACK_REALM.ViperKey))
			if realm == "" {
				return fmt.Errorf("%w (--realm)", errdefs.ErrRealmNameRequired)
			}

			space := strings.TrimSpace(viper.GetString(config.KUKE_CREATE_STACK_SPACE.ViperKey))
			if space == "" {
				return fmt.Errorf("%w (--space)", errdefs.ErrSpaceNameRequired)
			}

			ctrl, err := shared.ControllerFromCmd(cmd)
			if err != nil {
				return err
			}

			result, err := ctrl.CreateStack(controller.CreateStackOptions{
				Name:      name,
				RealmName: realm,
				SpaceName: space,
			})
			if err != nil {
				return err
			}

			printStackResult(cmd, result)
			return nil
		},
	}

	cmd.Flags().String("realm", "", "Realm that owns the stack")
	_ = viper.BindPFlag(config.KUKE_CREATE_STACK_REALM.ViperKey, cmd.Flags().Lookup("realm"))

	cmd.Flags().String("space", "", "Space that owns the stack")
	_ = viper.BindPFlag(config.KUKE_CREATE_STACK_SPACE.ViperKey, cmd.Flags().Lookup("space"))

	return cmd
}

func printStackResult(cmd *cobra.Command, result controller.CreateStackResult) {
	cmd.Printf("Stack %q (realm %q, space %q)\n", result.Name, result.RealmName, result.SpaceName)
	shared.PrintCreationOutcome(cmd, "metadata", result.MetadataExistsPost, result.Created)
	shared.PrintCreationOutcome(cmd, "cgroup", result.CgroupExistsPost, result.CgroupCreated)
}
