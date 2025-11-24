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
	"github.com/eminwux/kukeon/cmd/kuke/delete/shared"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// SpaceController defines the interface for space deletion operations.
// It is exported for use in tests.
type SpaceController interface {
	DeleteSpace(doc *v1beta1.SpaceDoc, force, cascade bool) (controller.DeleteSpaceResult, error)
}

// MockControllerKey is used to inject mock controllers in tests via context.
type MockControllerKey struct{}

func NewSpaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "space [name]",
		Aliases:       []string{"sp"},
		Short:         "Delete a space",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Check for mock controller in context (for testing)
			var ctrl SpaceController
			if mockCtrl, ok := cmd.Context().Value(MockControllerKey{}).(SpaceController); ok {
				ctrl = mockCtrl
			} else {
				realCtrl, err := shared.ControllerFromCmd(cmd)
				if err != nil {
					return err
				}
				ctrl = &controllerWrapper{ctrl: realCtrl}
			}

			name := strings.TrimSpace(args[0])
			realm := strings.TrimSpace(viper.GetString(config.KUKE_DELETE_SPACE_REALM.ViperKey))

			if realm == "" {
				return fmt.Errorf("%w (--realm)", errdefs.ErrRealmNameRequired)
			}

			force := shared.ParseForceFlag(cmd)
			cascade := shared.ParseCascadeFlag(cmd)

			spaceDoc := &v1beta1.SpaceDoc{
				Metadata: v1beta1.SpaceMetadata{
					Name: name,
				},
				Spec: v1beta1.SpaceSpec{
					RealmID: realm,
				},
			}

			result, err := ctrl.DeleteSpace(spaceDoc, force, cascade)
			if err != nil {
				return err
			}

			spaceName := result.SpaceName
			if spaceName == "" && result.SpaceDoc != nil {
				spaceName = result.SpaceDoc.Metadata.Name
			}
			realmName := result.RealmName
			if realmName == "" && result.SpaceDoc != nil {
				realmName = result.SpaceDoc.Spec.RealmID
			}

			cmd.Printf("Deleted space %q from realm %q\n", spaceName, realmName)
			return nil
		},
	}

	cmd.Flags().String("realm", "", "Realm that owns the space")
	_ = viper.BindPFlag(config.KUKE_DELETE_SPACE_REALM.ViperKey, cmd.Flags().Lookup("realm"))

	// Register autocomplete function for --realm flag
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)

	// Register autocomplete function for positional argument (space name)
	cmd.ValidArgsFunction = config.CompleteSpaceNames

	return cmd
}

type controllerWrapper struct {
	ctrl *controller.Exec
}

func (w *controllerWrapper) DeleteSpace(
	doc *v1beta1.SpaceDoc,
	force, cascade bool,
) (controller.DeleteSpaceResult, error) {
	return w.ctrl.DeleteSpace(doc, force, cascade)
}
