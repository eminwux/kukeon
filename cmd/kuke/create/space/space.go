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
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/create/shared"
	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/util/naming"
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
				realm = strings.TrimSpace(config.KUKE_CREATE_SPACE_REALM.ValueOrDefault())
			}

			doc := v1beta1.SpaceDoc{
				Metadata: v1beta1.SpaceMetadata{Name: name},
				Spec:     v1beta1.SpaceSpec{RealmID: realm},
			}

			client, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()

			result, err := client.CreateSpace(cmd.Context(), doc)
			if err != nil {
				return err
			}

			printSpaceResult(cmd, result)
			return nil
		},
	}

	cmd.Flags().String("realm", "", "Realm that will own the space")
	_ = viper.BindPFlag(config.KUKE_CREATE_SPACE_REALM.ViperKey, cmd.Flags().Lookup("realm"))

	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)

	return cmd
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukeshared.ClientFromCmd(cmd)
}

func printSpaceResult(cmd *cobra.Command, result kukeonv1.CreateSpaceResult) {
	name := result.Space.Metadata.Name
	realm := result.Space.Spec.RealmID
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
func PrintSpaceResult(cmd *cobra.Command, result kukeonv1.CreateSpaceResult) {
	printSpaceResult(cmd, result)
}
