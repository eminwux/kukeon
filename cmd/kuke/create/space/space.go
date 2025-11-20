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

package space

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

func NewSpaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "space [name]",
		Short:         "Create or reconcile a space within a realm",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := shared.RequireNameArgOrDefault(
				cmd,
				args,
				"space",
				viper.GetString(config.KUKE_CREATE_SPACE_NAME.ViperKey),
			)
			if err != nil {
				return err
			}

			realm := strings.TrimSpace(viper.GetString(config.KUKE_CREATE_SPACE_REALM.ViperKey))
			if realm == "" {
				return fmt.Errorf("%w (--realm)", errdefs.ErrRealmNameRequired)
			}

			ctrl, err := shared.ControllerFromCmd(cmd)
			if err != nil {
				return err
			}

			result, err := ctrl.CreateSpace(controller.CreateSpaceOptions{
				Name:      name,
				RealmName: realm,
			})
			if err != nil {
				return err
			}

			printSpaceResult(cmd, result)
			return nil
		},
	}

	cmd.Flags().String("realm", "", "Realm that will own the space")
	_ = viper.BindPFlag(config.KUKE_CREATE_SPACE_REALM.ViperKey, cmd.Flags().Lookup("realm"))

	return cmd
}

func printSpaceResult(cmd *cobra.Command, result controller.CreateSpaceResult) {
	cmd.Printf("Space %q (realm %q, network %q)\n", result.Name, result.RealmName, result.NetworkName)
	shared.PrintCreationOutcome(cmd, "metadata", result.MetadataExistsPost, result.Created)
	shared.PrintCreationOutcome(cmd, "network", result.CNINetworkExistsPost, result.CNINetworkCreated)
	shared.PrintCreationOutcome(cmd, "cgroup", result.CgroupExistsPost, result.CgroupCreated)
}
