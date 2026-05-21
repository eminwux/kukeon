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

package secret

import (
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// MockControllerKey is used to inject a mock kukeonv1.Client via context in tests.
type MockControllerKey struct{}

// unsafeDeleteWarning documents the temporary window the AC of issue #622 calls
// out: until the secretRef referencing path ships in phase 3c, delete cannot
// know whether a live container still references the secret, so it removes the
// file unconditionally.
const unsafeDeleteWarning = "WARNING: until the secretRef referencing path ships (phase 3c), this verb " +
	"cannot detect a live container that references the secret and will delete it " +
	"unconditionally. Deleting a referenced secret may break running workloads."

// NewSecretCmd builds `kuke delete secret <name>` (issue #622). It removes the
// daemon-stored bytes file for a single named, scoped Secret. The secret is
// identified by its name plus its binding scope (--realm/--space/--stack/--cell).
func NewSecretCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "secret [name]",
		Aliases:       []string{"sec"},
		Short:         "Delete a kind: Secret",
		Long:          "Delete a kind: Secret's daemon-stored bytes.\n\n" + unsafeDeleteWarning,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])

			realm := strings.TrimSpace(config.KUKE_DELETE_SECRET_REALM.ValueOrDefault())
			space := config.KUKE_DELETE_SECRET_SPACE.ValueOrDefault()
			stack := config.KUKE_DELETE_SECRET_STACK.ValueOrDefault()
			cell := config.KUKE_DELETE_SECRET_CELL.ValueOrDefault()
			if realm == "" {
				return fmt.Errorf("%w (--realm)", errdefs.ErrRealmNameRequired)
			}

			doc := v1beta1.SecretDoc{
				APIVersion: v1beta1.APIVersionV1Beta1,
				Kind:       v1beta1.KindSecret,
				Metadata: v1beta1.SecretMetadata{
					Name:  name,
					Realm: realm,
					Space: strings.TrimSpace(space),
					Stack: strings.TrimSpace(stack),
					Cell:  strings.TrimSpace(cell),
				},
			}

			client, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()

			result, err := client.DeleteSecret(cmd.Context(), doc)
			if err != nil {
				return err
			}

			secretName := name
			if result.Secret.Metadata.Name != "" {
				secretName = result.Secret.Metadata.Name
			}
			cmd.Printf("Deleted secret %q\n", secretName)
			return nil
		},
	}

	cmd.Flags().String("realm", "", "Realm the secret is bound to")
	_ = viper.BindPFlag(config.KUKE_DELETE_SECRET_REALM.ViperKey, cmd.Flags().Lookup("realm"))
	cmd.Flags().String("space", "", "Space the secret is bound to")
	_ = viper.BindPFlag(config.KUKE_DELETE_SECRET_SPACE.ViperKey, cmd.Flags().Lookup("space"))
	cmd.Flags().String("stack", "", "Stack the secret is bound to")
	_ = viper.BindPFlag(config.KUKE_DELETE_SECRET_STACK.ViperKey, cmd.Flags().Lookup("stack"))
	cmd.Flags().String("cell", "", "Cell the secret is bound to")
	_ = viper.BindPFlag(config.KUKE_DELETE_SECRET_CELL.ViperKey, cmd.Flags().Lookup("cell"))

	cmd.ValidArgsFunction = config.CompleteSecretNames
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)
	_ = cmd.RegisterFlagCompletionFunc("cell", config.CompleteCellNames)

	return cmd
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukeshared.DaemonClientFromCmd(cmd)
}
