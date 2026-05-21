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
	"errors"
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/get/shared"
	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// MockControllerKey is used to inject a mock kukeonv1.Client via context in tests.
type MockControllerKey struct{}

// NewSecretCmd builds `kuke get secret(s)` (issue #622). Secrets are
// metadata-only here: a Secret's bytes are never echoed by get — only its
// scope coordinates and name. With a name it shows one secret's metadata;
// without one it lists every secret bound to the filter scope or any scope
// nested within it (subtree-filter semantics, matching `kuke get cells`).
func NewSecretCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "secret [name]",
		Aliases:       []string{"secrets", "sec"},
		Short:         "Get or list kind: Secret metadata (spec.data is never echoed)",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()

			outputFormat, err := shared.ParseOutputFormat(cmd)
			if err != nil {
				return err
			}

			// List filters: an unset flag means "no filter" (ExplicitFlag),
			// so `kuke get secrets` with no flags lists the whole subtree.
			realm := shared.ExplicitFlag(cmd, "realm", config.KUKE_GET_SECRET_REALM.ViperKey)
			space := shared.ExplicitFlag(cmd, "space", config.KUKE_GET_SECRET_SPACE.ViperKey)
			stack := shared.ExplicitFlag(cmd, "stack", config.KUKE_GET_SECRET_STACK.ViperKey)
			cell := shared.ExplicitFlag(cmd, "cell", config.KUKE_GET_SECRET_CELL.ViperKey)

			var name string
			if len(args) > 0 {
				name = strings.TrimSpace(args[0])
			} else {
				name = strings.TrimSpace(viper.GetString(config.KUKE_GET_SECRET_NAME.ViperKey))
			}

			if name != "" {
				// A single secret is identified by its name plus its exact
				// binding scope. Default the realm to the operator's current
				// realm; deeper coordinates stay unset unless provided.
				if realm == "" {
					realm = strings.TrimSpace(config.KUKE_GET_SECRET_REALM.ValueOrDefault())
				}
				if realm == "" {
					return fmt.Errorf("%w (--realm)", errdefs.ErrRealmNameRequired)
				}
				doc := v1beta1.SecretDoc{
					APIVersion: v1beta1.APIVersionV1Beta1,
					Kind:       v1beta1.KindSecret,
					Metadata: v1beta1.SecretMetadata{
						Name:  name,
						Realm: realm,
						Space: space,
						Stack: stack,
						Cell:  cell,
					},
				}
				result, getErr := client.GetSecret(cmd.Context(), doc)
				if getErr != nil {
					if errors.Is(getErr, errdefs.ErrSecretNotFound) {
						return fmt.Errorf("secret %q not found", name)
					}
					return getErr
				}
				if !result.MetadataExists {
					return fmt.Errorf("secret %q not found", name)
				}
				return printSecret(&result.Secret, outputFormat)
			}

			secrets, err := client.ListSecrets(cmd.Context(), realm, space, stack, cell)
			if err != nil {
				return err
			}
			return printSecrets(cmd, secrets, outputFormat)
		},
	}

	cmd.Flags().String("realm", "", "Filter secrets by realm name")
	_ = viper.BindPFlag(config.KUKE_GET_SECRET_REALM.ViperKey, cmd.Flags().Lookup("realm"))
	cmd.Flags().String("space", "", "Filter secrets by space name")
	_ = viper.BindPFlag(config.KUKE_GET_SECRET_SPACE.ViperKey, cmd.Flags().Lookup("space"))
	cmd.Flags().String("stack", "", "Filter secrets by stack name")
	_ = viper.BindPFlag(config.KUKE_GET_SECRET_STACK.ViperKey, cmd.Flags().Lookup("stack"))
	cmd.Flags().String("cell", "", "Filter secrets by cell name")
	_ = viper.BindPFlag(config.KUKE_GET_SECRET_CELL.ViperKey, cmd.Flags().Lookup("cell"))
	cmd.Flags().
		StringP("output", "o", "", "Output format (yaml, json, table). Default: table for list, yaml for single resource")
	_ = viper.BindPFlag(config.KUKE_GET_OUTPUT.ViperKey, cmd.Flags().Lookup("output"))
	_ = viper.BindPFlag(config.KUKE_GET_OUTPUT.ViperKey, cmd.Flags().Lookup("o"))

	cmd.ValidArgsFunction = config.CompleteSecretNames
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)
	_ = cmd.RegisterFlagCompletionFunc("cell", config.CompleteCellNames)
	_ = cmd.RegisterFlagCompletionFunc("output", config.CompleteOutputFormat)
	_ = cmd.RegisterFlagCompletionFunc("o", config.CompleteOutputFormat)

	return cmd
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukeshared.ClientFromCmd(cmd)
}

func printSecret(secret *v1beta1.SecretDoc, format shared.OutputFormat) error {
	switch format {
	case shared.OutputFormatJSON:
		return shared.PrintJSON(secret)
	case shared.OutputFormatYAML, shared.OutputFormatTable:
		return shared.PrintYAML(secret)
	default:
		return shared.PrintYAML(secret)
	}
}

func printSecrets(cmd *cobra.Command, secrets []v1beta1.SecretDoc, format shared.OutputFormat) error {
	switch format {
	case shared.OutputFormatYAML:
		return shared.PrintYAML(secrets)
	case shared.OutputFormatJSON:
		return shared.PrintJSON(secrets)
	case shared.OutputFormatTable:
		if len(secrets) == 0 {
			cmd.Println("No secrets found.")
			return nil
		}
		headers := []string{"NAME", "REALM", "SPACE", "STACK", "CELL"}
		rows := make([][]string, 0, len(secrets))
		for i := range secrets {
			s := &secrets[i]
			rows = append(rows, []string{
				s.Metadata.Name,
				dashIfEmpty(s.Metadata.Realm),
				dashIfEmpty(s.Metadata.Space),
				dashIfEmpty(s.Metadata.Stack),
				dashIfEmpty(s.Metadata.Cell),
			})
		}
		shared.PrintTable(cmd, headers, rows)
		return nil
	default:
		return shared.PrintYAML(secrets)
	}
}

// dashIfEmpty renders an unset scope coordinate as "-" so the table column
// never collapses to an unaligned blank cell.
func dashIfEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
