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
	"sort"
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

// NewSecretCmd builds `kuke get secret(s)` (issue #622). With a name and
// `-o yaml|json` it prints the whole document, material included, through
// ReadSecret (issue #1652), so the output re-applies with `kuke apply -f -`.
// Every other shape is metadata-only: a named table or wide row, and every
// list in any format, carry the scope coordinates, name, type and readable
// identity facts but never the material. Without a name it lists every secret
// bound to the filter scope or any scope nested within it (subtree-filter
// semantics, matching `kuke get cells`).
func NewSecretCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "secret [name]",
		Aliases: []string{"secrets", "sec"},
		Short:   "Get or list kind: Secret (a named -o yaml|json prints the material)",
		Long: `Get or list kind: Secret.

A named secret with -o yaml or -o json prints the whole document, material
included (spec.data, spec.basicAuth or spec.sshKey), and re-applies unchanged
with "kuke apply -f -". A named table or wide row and every list, in any
format, show metadata only: name, type, connector, scope and, with -o wide,
the readable identity fields.

Anyone who can reach kukeond (root and the kukeon group) can read every
secret's material this way.`,
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
				read := client.GetSecret
				if outputFormat == shared.OutputFormatYAML || outputFormat == shared.OutputFormatJSON {
					read = client.ReadSecret
				}
				result, getErr := read(cmd.Context(), doc)
				if getErr != nil {
					if errors.Is(getErr, errdefs.ErrDaemonMethodUnsupported) {
						return fmt.Errorf(
							"%w: printing a secret's material needs a newer kukeond (run `sudo kuke init`)",
							errdefs.ErrDaemonMethodUnsupported,
						)
					}
					if errors.Is(getErr, errdefs.ErrSecretNotFound) {
						return fmt.Errorf("secret %q not found", name)
					}
					return getErr
				}
				if !result.MetadataExists {
					return fmt.Errorf("secret %q not found", name)
				}
				return printSecret(cmd, &result.Secret, outputFormat)
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
		StringP("output", "o", "", "Output format (yaml, json, table, wide). Default: table for list, table for single resource")
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

func printSecret(cmd *cobra.Command, secret *v1beta1.SecretDoc, format shared.OutputFormat) error {
	switch format {
	case shared.OutputFormatJSON:
		return shared.PrintJSON(cmd, secret)
	case shared.OutputFormatYAML:
		return shared.PrintYAML(cmd, secret)
	default:
		// table / wide: render the single found element as a one-row table
		// with the same columns as the list view (kubectl parity).
		return printSecrets(cmd, []v1beta1.SecretDoc{*secret}, format)
	}
}

func printSecrets(cmd *cobra.Command, secrets []v1beta1.SecretDoc, format shared.OutputFormat) error {
	switch format {
	case shared.OutputFormatYAML:
		return shared.PrintYAML(cmd, secrets)
	case shared.OutputFormatJSON:
		// A nil slice encodes as JSON null; normalize so an empty list
		// emits [] like cells/containers and the YAML path already do.
		if secrets == nil {
			secrets = []v1beta1.SecretDoc{}
		}
		return shared.PrintJSON(cmd, secrets)
	case shared.OutputFormatTable, shared.OutputFormatWide:
		if len(secrets) == 0 {
			cmd.Println("No secrets found.")
			return nil
		}
		// CONNECTOR is the connector the secret is an identity of; -o wide
		// adds its readable identity fields (issue #1548). The material is
		// never part of a get.
		wide := format == shared.OutputFormatWide
		headers := []string{"NAME", "TYPE", "CONNECTOR", "REALM", "SPACE", "STACK", "CELL"}
		if wide {
			headers = append(headers, "IDENTITY")
		}
		rows := make([][]string, 0, len(secrets))
		for i := range secrets {
			s := &secrets[i]
			secretType := s.Spec.Type
			if secretType == "" {
				secretType = v1beta1.SecretTypeOpaque
			}
			row := []string{
				s.Metadata.Name,
				secretType,
				dashIfEmpty(s.Spec.Connector),
				dashIfEmpty(s.Metadata.Realm),
				dashIfEmpty(s.Metadata.Space),
				dashIfEmpty(s.Metadata.Stack),
				dashIfEmpty(s.Metadata.Cell),
			}
			if wide {
				row = append(row, dashIfEmpty(identitySummary(s.Spec.Identity)))
			}
			rows = append(rows, row)
		}
		shared.PrintTable(cmd, headers, rows)
		return nil
	default:
		return shared.PrintYAML(cmd, secrets)
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

// identitySummary renders a secret's readable identity fields as
// `field=value; field=value`, sorted by field.
func identitySummary(identity map[string]string) string {
	fields := make([]string, 0, len(identity))
	for field := range identity {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		parts = append(parts, field+"="+identity[field])
	}
	return strings.Join(parts, "; ")
}
