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
	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/create/shared"
	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// MockControllerKey is used to inject a mock kukeonv1.Client via context in tests.
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

			doc := v1beta1.RealmDoc{
				Metadata: v1beta1.RealmMetadata{Name: name},
				Spec:     v1beta1.RealmSpec{Namespace: namespace},
			}

			client, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()

			result, err := client.CreateRealm(cmd.Context(), doc)
			if err != nil {
				return err
			}

			printRealmResult(cmd, result)
			return nil
		},
	}

	cmd.Flags().String("namespace", "", "Containerd namespace for the realm (defaults to the realm name)")

	return cmd
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukeshared.ClientFromCmd(cmd)
}

func printRealmResult(cmd *cobra.Command, result kukeonv1.CreateRealmResult) {
	cmd.Printf("Realm %q (namespace %q)\n", result.Realm.Metadata.Name, result.Realm.Spec.Namespace)
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
func PrintRealmResult(cmd *cobra.Command, result kukeonv1.CreateRealmResult) {
	printRealmResult(cmd, result)
}
