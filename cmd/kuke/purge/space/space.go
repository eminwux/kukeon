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
	"github.com/eminwux/kukeon/cmd/kuke/purge/shared"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type spaceController interface {
	PurgeSpace(doc *v1beta1.SpaceDoc, force, cascade bool) (controller.PurgeSpaceResult, error)
}

// MockControllerKey is used to inject mock controllers in tests via context.
type MockControllerKey struct{}

func NewSpaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "space [name]",
		Short:         "Purge a space with comprehensive cleanup",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Check for mock controller in context (for testing)
			var ctrl spaceController
			if mockCtrl, ok := cmd.Context().Value(MockControllerKey{}).(spaceController); ok {
				ctrl = mockCtrl
			} else {
				realCtrl, err := shared.ControllerFromCmd(cmd)
				if err != nil {
					return err
				}
				ctrl = &controllerWrapper{ctrl: realCtrl}
			}

			name := strings.TrimSpace(args[0])
			realm := strings.TrimSpace(viper.GetString(config.KUKE_PURGE_SPACE_REALM.ViperKey))

			if realm == "" {
				return fmt.Errorf("%w (--realm)", errdefs.ErrRealmNameRequired)
			}

			force := shared.ParseForceFlag(cmd)
			cascade := shared.ParseCascadeFlag(cmd)

			doc := &v1beta1.SpaceDoc{
				Metadata: v1beta1.SpaceMetadata{
					Name: name,
				},
				Spec: v1beta1.SpaceSpec{
					RealmID: realm,
				},
			}

			result, err := ctrl.PurgeSpace(doc, force, cascade)
			if err != nil {
				return err
			}

			spaceName := name
			realmName := realm
			if result.SpaceDoc != nil {
				if trimmed := strings.TrimSpace(result.SpaceDoc.Metadata.Name); trimmed != "" {
					spaceName = trimmed
				}
				if trimmed := strings.TrimSpace(result.SpaceDoc.Spec.RealmID); trimmed != "" {
					realmName = trimmed
				}
			}

			cmd.Printf("Purged space %q from realm %q\n", spaceName, realmName)
			cmd.Printf(
				"Deleted resources -> metadata:%t cgroup:%t cni:%t\n",
				result.MetadataDeleted,
				result.CgroupDeleted,
				result.CNINetworkDeleted,
			)
			if len(result.Purged) > 0 {
				cmd.Printf("Additional resources purged: %v\n", result.Purged)
			}
			return nil
		},
	}

	cmd.Flags().String("realm", "", "Realm that owns the space")
	_ = viper.BindPFlag(config.KUKE_PURGE_SPACE_REALM.ViperKey, cmd.Flags().Lookup("realm"))

	// Register autocomplete function for --realm flag
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)

	// Register autocomplete function for positional argument (space name)
	cmd.ValidArgsFunction = config.CompleteSpaceNames

	return cmd
}

type controllerWrapper struct {
	ctrl *controller.Exec
}

func (w *controllerWrapper) PurgeSpace(
	doc *v1beta1.SpaceDoc,
	force, cascade bool,
) (controller.PurgeSpaceResult, error) {
	return w.ctrl.PurgeSpace(doc, force, cascade)
}
