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
	"fmt"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/create/shared"
	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type realmController interface {
	CreateRealm(realm intmodel.Realm) (controller.CreateRealmResult, error)
}

// MockControllerKey is used to inject mock controllers in tests via context.
type MockControllerKey struct{}

func NewRealmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "realm [name]",
		Aliases:       []string{"r"},
		Short:         "Create or reconcile a realm",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := shared.RequireNameArgOrDefault(
				cmd,
				args,
				"realm",
				viper.GetString(config.KUKE_CREATE_REALM_NAME.ViperKey),
			)
			if err != nil {
				return err
			}

			namespace, err := cmd.Flags().GetString("namespace")
			if err != nil {
				return err
			}

			// Build v1beta1.RealmDoc from command arguments
			doc := &v1beta1.RealmDoc{
				Metadata: v1beta1.RealmMetadata{
					Name: name,
				},
				Spec: v1beta1.RealmSpec{
					Namespace: namespace,
				},
			}

			// Convert at boundary before calling controller
			realm, version, err := apischeme.NormalizeRealm(*doc)
			if err != nil {
				return fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
			}

			// Check for mock controller in context (for testing)
			var ctrl realmController
			if mockCtrl, ok := cmd.Context().Value(MockControllerKey{}).(realmController); ok {
				ctrl = mockCtrl
			} else {
				var realCtrl *controller.Exec
				realCtrl, err = shared.ControllerFromCmd(cmd)
				if err != nil {
					return err
				}
				ctrl = realCtrl
			}

			// Call controller with internal type
			result, err := ctrl.CreateRealm(realm)
			if err != nil {
				return err
			}

			printRealmResult(cmd, result, version)
			return nil
		},
	}

	cmd.Flags().String("namespace", "", "Containerd namespace for the realm (defaults to the realm name)")

	return cmd
}

func printRealmResult(cmd *cobra.Command, result controller.CreateRealmResult, version v1beta1.Version) {
	// Convert result back to external for output
	resultDoc, err := apischeme.BuildRealmExternalFromInternal(result.Realm, version)
	if err != nil {
		// Fallback to internal type if conversion fails
		cmd.Printf("Realm %q (namespace %q)\n", result.Realm.Metadata.Name, result.Realm.Spec.Namespace)
		cmd.Printf("Warning: failed to convert result for output: %v\n", err)
	} else {
		cmd.Printf("Realm %q (namespace %q)\n", resultDoc.Metadata.Name, resultDoc.Spec.Namespace)
	}
	shared.PrintCreationOutcome(cmd, "metadata", result.MetadataExistsPost, result.Created)
	shared.PrintCreationOutcome(
		cmd,
		"containerd namespace",
		result.ContainerdNamespaceExistsPost,
		result.ContainerdNamespaceCreated,
	)
	shared.PrintCreationOutcome(cmd, "cgroup", result.CgroupExistsPost, result.CgroupCreated)
}

// PrintRealmResult is exported for testing purposes.
func PrintRealmResult(cmd *cobra.Command, result controller.CreateRealmResult, version v1beta1.Version) {
	printRealmResult(cmd, result, version)
}
