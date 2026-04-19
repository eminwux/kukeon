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
	"github.com/eminwux/kukeon/cmd/kuke/purge/shared"
	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
)

// MockControllerKey is used to inject a mock kukeonv1.Client via context in tests.
type MockControllerKey struct{}

func NewRealmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "realm [name]",
		Aliases:       []string{"r"},
		Short:         "Purge a realm with comprehensive cleanup",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			force := shared.ParseForceFlag(cmd)
			cascade := shared.ParseCascadeFlag(cmd)

			doc := v1beta1.RealmDoc{
				Metadata: v1beta1.RealmMetadata{Name: name},
			}

			client, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()

			result, err := client.PurgeRealm(cmd.Context(), doc, force, cascade)
			if err != nil {
				return err
			}

			realmName := result.Realm.Metadata.Name
			if realmName == "" {
				realmName = name
			}

			cmd.Printf("Purged realm %q\n", realmName)
			if len(result.Purged) > 0 {
				cmd.Printf("Additional resources purged: %v\n", result.Purged)
			}
			return nil
		},
	}

	cmd.ValidArgsFunction = config.CompleteRealmNames

	return cmd
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukeshared.ClientFromCmd(cmd)
}
