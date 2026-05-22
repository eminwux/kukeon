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

package config

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

// NewConfigCmd builds `kuke get config(s)` (issue #644). With a name it shows
// one Config's full document (blueprint ref, values, repo/secret slot fills);
// without one it lists every Config bound to the filter scope or any scope
// nested within it (subtree-filter semantics, matching `kuke get blueprints`).
// A Config is never cell-scoped, so there is no --cell flag — the scope bottoms
// out at stack.
func NewConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "config [name]",
		Aliases:       []string{"configs", "cfg"},
		Short:         "Get or list kind: CellConfig",
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
			// so `kuke get configs` with no flags lists the whole subtree.
			realm := shared.ExplicitFlag(cmd, "realm", config.KUKE_GET_CONFIG_REALM.ViperKey)
			space := shared.ExplicitFlag(cmd, "space", config.KUKE_GET_CONFIG_SPACE.ViperKey)
			stack := shared.ExplicitFlag(cmd, "stack", config.KUKE_GET_CONFIG_STACK.ViperKey)

			var name string
			if len(args) > 0 {
				name = strings.TrimSpace(args[0])
			} else {
				name = strings.TrimSpace(viper.GetString(config.KUKE_GET_CONFIG_NAME.ViperKey))
			}

			if name != "" {
				// A single config is identified by its name plus its exact
				// binding scope. Default the realm to the operator's current
				// realm; deeper coordinates stay unset unless provided.
				if realm == "" {
					realm = strings.TrimSpace(config.KUKE_GET_CONFIG_REALM.ValueOrDefault())
				}
				if realm == "" {
					return fmt.Errorf("%w (--realm)", errdefs.ErrRealmNameRequired)
				}
				lookup := v1beta1.CellConfigDoc{
					APIVersion: v1beta1.APIVersionV1Beta1,
					Kind:       v1beta1.KindCellConfig,
					Metadata: v1beta1.CellConfigMetadata{
						Name:  name,
						Realm: realm,
						Space: space,
						Stack: stack,
					},
				}
				result, getErr := client.GetConfig(cmd.Context(), lookup)
				if getErr != nil {
					if errors.Is(getErr, errdefs.ErrConfigNotFound) {
						return fmt.Errorf("config %q not found", name)
					}
					return getErr
				}
				if !result.MetadataExists {
					return fmt.Errorf("config %q not found", name)
				}
				return printConfig(&result.Config, outputFormat)
			}

			configs, err := client.ListConfigs(cmd.Context(), realm, space, stack)
			if err != nil {
				return err
			}
			return printConfigs(cmd, configs, outputFormat)
		},
	}

	cmd.Flags().String("realm", "", "Filter configs by realm name")
	_ = viper.BindPFlag(config.KUKE_GET_CONFIG_REALM.ViperKey, cmd.Flags().Lookup("realm"))
	cmd.Flags().String("space", "", "Filter configs by space name")
	_ = viper.BindPFlag(config.KUKE_GET_CONFIG_SPACE.ViperKey, cmd.Flags().Lookup("space"))
	cmd.Flags().String("stack", "", "Filter configs by stack name")
	_ = viper.BindPFlag(config.KUKE_GET_CONFIG_STACK.ViperKey, cmd.Flags().Lookup("stack"))
	cmd.Flags().
		StringP("output", "o", "", "Output format (yaml, json, table). Default: table for list, yaml for single resource")
	_ = viper.BindPFlag(config.KUKE_GET_OUTPUT.ViperKey, cmd.Flags().Lookup("output"))
	_ = viper.BindPFlag(config.KUKE_GET_OUTPUT.ViperKey, cmd.Flags().Lookup("o"))

	cmd.ValidArgsFunction = config.CompleteConfigNames
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)
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

func printConfig(cfg *v1beta1.CellConfigDoc, format shared.OutputFormat) error {
	switch format {
	case shared.OutputFormatJSON:
		return shared.PrintJSON(cfg)
	case shared.OutputFormatYAML, shared.OutputFormatTable:
		return shared.PrintYAML(cfg)
	default:
		return shared.PrintYAML(cfg)
	}
}

func printConfigs(cmd *cobra.Command, configs []v1beta1.CellConfigDoc, format shared.OutputFormat) error {
	switch format {
	case shared.OutputFormatYAML:
		return shared.PrintYAML(configs)
	case shared.OutputFormatJSON:
		return shared.PrintJSON(configs)
	case shared.OutputFormatTable:
		if len(configs) == 0 {
			cmd.Println("No configs found.")
			return nil
		}
		headers := []string{"NAME", "REALM", "SPACE", "STACK"}
		rows := make([][]string, 0, len(configs))
		for i := range configs {
			c := &configs[i]
			rows = append(rows, []string{
				c.Metadata.Name,
				dashIfEmpty(c.Metadata.Realm),
				dashIfEmpty(c.Metadata.Space),
				dashIfEmpty(c.Metadata.Stack),
			})
		}
		shared.PrintTable(cmd, headers, rows)
		return nil
	default:
		return shared.PrintYAML(configs)
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
