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

package realm

import (
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/delete/shared"
	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/controller"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
)

// RealmController defines the interface for realm deletion operations.
// It is exported for use in tests.
type RealmController interface {
	DeleteRealm(realm intmodel.Realm, force, cascade bool) (controller.DeleteRealmResult, error)
}

// MockControllerKey is used to inject mock controllers in tests via context.
type MockControllerKey struct{}

func NewRealmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "realm [name]",
		Aliases:       []string{"r"},
		Short:         "Delete a realm",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Check for mock controller in context (for testing)
			var ctrl RealmController
			if mockCtrl, ok := cmd.Context().Value(MockControllerKey{}).(RealmController); ok {
				ctrl = mockCtrl
			} else {
				realCtrl, err := shared.ControllerFromCmd(cmd)
				if err != nil {
					return err
				}
				ctrl = &controllerWrapper{ctrl: realCtrl}
			}

			name := strings.TrimSpace(args[0])

			force := shared.ParseForceFlag(cmd)
			cascade := shared.ParseCascadeFlag(cmd)

			realmDoc := &v1beta1.RealmDoc{
				Metadata: v1beta1.RealmMetadata{
					Name: name,
				},
				Spec: v1beta1.RealmSpec{
					Namespace: name,
				},
			}

			// Convert at boundary before calling controller
			realmInternal, _, err := apischeme.NormalizeRealm(*realmDoc)
			if err != nil {
				return err
			}

			result, err := ctrl.DeleteRealm(realmInternal, force, cascade)
			if err != nil {
				return err
			}

			realmName := name
			if result.Realm.Metadata.Name != "" {
				realmName = result.Realm.Metadata.Name
			}
			cmd.Printf("Deleted realm %q\n", realmName)
			return nil
		},
	}

	// Register autocomplete for positional argument
	cmd.ValidArgsFunction = config.CompleteRealmNames

	return cmd
}

type controllerWrapper struct {
	ctrl *controller.Exec
}

func (w *controllerWrapper) DeleteRealm(
	realm intmodel.Realm,
	force, cascade bool,
) (controller.DeleteRealmResult, error) {
	return w.ctrl.DeleteRealm(realm, force, cascade)
}
