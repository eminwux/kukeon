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
	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// MockControllerKey is used to inject a mock kukeonv1.Client via context in tests.
type MockControllerKey struct{}

func NewSpaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "space [name]",
		Aliases:       []string{"sp"},
		Short:         "Purge a space with comprehensive cleanup",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			realm := strings.TrimSpace(viper.GetString(config.KUKE_PURGE_SPACE_REALM.ViperKey))
			if realm == "" {
				return fmt.Errorf("%w (--realm)", errdefs.ErrRealmNameRequired)
			}

			force := shared.ParseForceFlag(cmd)
			cascade := shared.ParseCascadeFlag(cmd)

			doc := v1beta1.SpaceDoc{
				Metadata: v1beta1.SpaceMetadata{Name: name},
				Spec:     v1beta1.SpaceSpec{RealmID: realm},
			}

			client, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()

			result, err := client.PurgeSpace(cmd.Context(), doc, force, cascade)
			if err != nil {
				return err
			}

			spaceName := result.Space.Metadata.Name
			if spaceName == "" {
				spaceName = name
			}
			realmName := result.Space.Spec.RealmID
			if realmName == "" {
				realmName = realm
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

	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	cmd.ValidArgsFunction = config.CompleteSpaceNames

	return cmd
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukeshared.ClientFromCmd(cmd)
}
