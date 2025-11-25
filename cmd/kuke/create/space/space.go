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
	"github.com/eminwux/kukeon/internal/util/naming"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type spaceController interface {
	CreateSpace(doc *v1beta1.SpaceDoc) (controller.CreateSpaceResult, error)
}

// MockControllerKey is used to inject mock controllers in tests via context.
type MockControllerKey struct{}

type controllerWrapper struct {
	ctrl *controller.Exec
}

func (w *controllerWrapper) CreateSpace(doc *v1beta1.SpaceDoc) (controller.CreateSpaceResult, error) {
	return w.ctrl.CreateSpace(doc)
}

func NewSpaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "space [name]",
		Aliases:       []string{"sp"},
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

			// Check for mock controller in context (for testing)
			var ctrl spaceController
			if mockCtrl, ok := cmd.Context().Value(MockControllerKey{}).(spaceController); ok {
				ctrl = mockCtrl
			} else {
				var realCtrl *controller.Exec
				realCtrl, err = shared.ControllerFromCmd(cmd)
				if err != nil {
					return err
				}
				ctrl = &controllerWrapper{ctrl: realCtrl}
			}

			doc := &v1beta1.SpaceDoc{
				Metadata: v1beta1.SpaceMetadata{
					Name: name,
				},
				Spec: v1beta1.SpaceSpec{
					RealmID: realm,
				},
			}

			result, err := ctrl.CreateSpace(doc)
			if err != nil {
				return err
			}

			printSpaceResult(cmd, result)
			return nil
		},
	}

	cmd.Flags().String("realm", "", "Realm that will own the space")
	_ = viper.BindPFlag(config.KUKE_CREATE_SPACE_REALM.ViperKey, cmd.Flags().Lookup("realm"))

	// Register autocomplete function for --realm flag
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)

	return cmd
}

func printSpaceResult(cmd *cobra.Command, result controller.CreateSpaceResult) {
	if result.SpaceDoc == nil {
		cmd.Printf("Space (metadata missing)\n")
		shared.PrintCreationOutcome(cmd, "metadata", result.MetadataExistsPost, result.Created)
		shared.PrintCreationOutcome(cmd, "network", result.CNINetworkExistsPost, result.CNINetworkCreated)
		shared.PrintCreationOutcome(cmd, "cgroup", result.CgroupExistsPost, result.CgroupCreated)
		return
	}

	name := result.SpaceDoc.Metadata.Name
	realm := result.SpaceDoc.Spec.RealmID
	networkName, err := naming.BuildSpaceNetworkName(realm, name)
	if err != nil {
		networkName = "<unknown>"
	}

	cmd.Printf("Space %q (realm %q, network %q)\n", name, realm, networkName)
	shared.PrintCreationOutcome(cmd, "metadata", result.MetadataExistsPost, result.Created)
	shared.PrintCreationOutcome(cmd, "network", result.CNINetworkExistsPost, result.CNINetworkCreated)
	shared.PrintCreationOutcome(cmd, "cgroup", result.CgroupExistsPost, result.CgroupCreated)
}

// PrintSpaceResult is exported for testing purposes.
func PrintSpaceResult(cmd *cobra.Command, result controller.CreateSpaceResult) {
	printSpaceResult(cmd, result)
}
