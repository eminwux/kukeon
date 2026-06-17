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
				// binding scope. Default every coordinate to the operator's
				// full default scope (realm/space/stack = "default") so a
				// no-flag `kuke get config <name>` finds resources stored at
				// the full default coordinate — how `kuke create config` and
				// the team renderer actually store them. Mirrors `get cell`
				// (issue #1156). A realm-scoped Config (space/stack unset) is
				// reachable by passing an explicit `--space "" --stack ""`.
				if realm == "" {
					realm = strings.TrimSpace(config.KUKE_GET_CONFIG_REALM.ValueOrDefault())
				}
				if space == "" {
					space = strings.TrimSpace(config.KUKE_GET_CONFIG_SPACE.ValueOrDefault())
				}
				if stack == "" {
					stack = strings.TrimSpace(config.KUKE_GET_CONFIG_STACK.ValueOrDefault())
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
						return configNotFoundErr(cmd, client, name, realm, space, stack)
					}
					return getErr
				}
				if !result.MetadataExists {
					return configNotFoundErr(cmd, client, name, realm, space, stack)
				}
				return printConfig(cmd, &result.Config, outputFormat)
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
		StringP("output", "o", "", "Output format (yaml, json, table, wide). Default: table for list, table for single resource")
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

// configNotFoundErr builds the single-get miss error. It surfaces the exact
// scope searched and — best-effort — hints the coordinate where a Config of the
// same name does live (probed realm-wide), so an operator who stored it at a
// non-default space/stack sees where to look instead of a bare "not found"
// (issue #1156). The realm-wide list is advisory: a list error is swallowed and
// the base error returned unadorned.
func configNotFoundErr(
	cmd *cobra.Command, client kukeonv1.Client, name, realm, space, stack string,
) error {
	base := fmt.Sprintf(
		"config %q not found (searched realm=%q space=%q stack=%q)", name, realm, space, stack,
	)
	configs, err := client.ListConfigs(cmd.Context(), realm, "", "")
	if err != nil {
		return errors.New(base)
	}
	for i := range configs {
		m := &configs[i].Metadata
		if m.Name == name && (m.Realm != realm || m.Space != space || m.Stack != stack) {
			return fmt.Errorf(
				"%s; a config %q exists at realm=%q space=%q stack=%q",
				base, name, m.Realm, m.Space, m.Stack,
			)
		}
	}
	return errors.New(base)
}

func printConfig(cmd *cobra.Command, cfg *v1beta1.CellConfigDoc, format shared.OutputFormat) error {
	switch format {
	case shared.OutputFormatJSON:
		return shared.PrintJSON(cmd, cfg)
	case shared.OutputFormatYAML:
		return shared.PrintYAML(cmd, cfg)
	default:
		// table / wide: render the single found element as a one-row table
		// with the same columns as the list view (kubectl parity).
		return printConfigs(cmd, []v1beta1.CellConfigDoc{*cfg}, format)
	}
}

func printConfigs(cmd *cobra.Command, configs []v1beta1.CellConfigDoc, format shared.OutputFormat) error {
	switch format {
	case shared.OutputFormatYAML:
		return shared.PrintYAML(cmd, configs)
	case shared.OutputFormatJSON:
		return shared.PrintJSON(cmd, configs)
	case shared.OutputFormatTable, shared.OutputFormatWide:
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
		return shared.PrintYAML(cmd, configs)
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
