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
	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/naming"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type spaceController interface {
	CreateSpace(space intmodel.Space) (controller.CreateSpaceResult, error)
}

// MockControllerKey is used to inject mock controllers in tests via context.
type MockControllerKey struct{}

type controllerWrapper struct {
	ctrl *controller.Exec
}

func (w *controllerWrapper) CreateSpace(space intmodel.Space) (controller.CreateSpaceResult, error) {
	return w.ctrl.CreateSpace(space)
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
				realm = config.KUKE_CREATE_SPACE_REALM.ValueOrDefault()
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

			// Build v1beta1.SpaceDoc from command arguments
			doc := &v1beta1.SpaceDoc{
				Metadata: v1beta1.SpaceMetadata{
					Name: name,
				},
				Spec: v1beta1.SpaceSpec{
					RealmID: realm,
				},
			}

			// Convert at boundary before calling controller
			space, version, err := apischeme.NormalizeSpace(*doc)
			if err != nil {
				return fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
			}

			// Call controller with internal type
			result, err := ctrl.CreateSpace(space)
			if err != nil {
				return err
			}

			printSpaceResult(cmd, result, version)
			return nil
		},
	}

	cmd.Flags().String("realm", "", "Realm that will own the space")
	_ = viper.BindPFlag(config.KUKE_CREATE_SPACE_REALM.ViperKey, cmd.Flags().Lookup("realm"))

	// Register autocomplete function for --realm flag
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)

	return cmd
}

func printSpaceResult(cmd *cobra.Command, result controller.CreateSpaceResult, version v1beta1.Version) {
	// Convert result back to external for output
	resultDoc, err := apischeme.BuildSpaceExternalFromInternal(result.Space, version)
	if err != nil {
		// Fallback to internal type if conversion fails
		name := result.Space.Metadata.Name
		realm := result.Space.Spec.RealmName
		networkName, buildErr := naming.BuildSpaceNetworkName(realm, name)
		if buildErr != nil {
			networkName = "<unknown>"
		}
		cmd.Printf("Space %q (realm %q, network %q)\n", name, realm, networkName)
		cmd.Printf("Warning: failed to convert result for output: %v\n", err)
	} else {
		name := resultDoc.Metadata.Name
		realm := resultDoc.Spec.RealmID
		networkName, err := naming.BuildSpaceNetworkName(realm, name)
		if err != nil {
			networkName = "<unknown>"
		}
		cmd.Printf("Space %q (realm %q, network %q)\n", name, realm, networkName)
	}
	shared.PrintCreationOutcome(cmd, "metadata", result.MetadataExistsPost, result.Created)
	shared.PrintCreationOutcome(cmd, "network", result.CNINetworkExistsPost, result.CNINetworkCreated)
	shared.PrintCreationOutcome(cmd, "cgroup", result.CgroupExistsPost, result.CgroupCreated)
}

// PrintSpaceResult is exported for testing purposes.
func PrintSpaceResult(cmd *cobra.Command, result controller.CreateSpaceResult, version v1beta1.Version) {
	printSpaceResult(cmd, result, version)
}
