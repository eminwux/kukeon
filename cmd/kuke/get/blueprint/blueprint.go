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

package blueprint

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

// NewBlueprintCmd builds `kuke get blueprint(s)` (issue #643). With a name it
// shows one Blueprint's full document (parameters, slot declarations, cell
// template); without one it lists every Blueprint bound to the filter scope or
// any scope nested within it (subtree-filter semantics, matching
// `kuke get secrets`). A Blueprint is never cell-scoped, so there is no --cell
// flag — the scope bottoms out at stack.
func NewBlueprintCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "blueprint [name]",
		Aliases:       []string{"blueprints", "bp"},
		Short:         "Get or list kind: CellBlueprint",
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
			// so `kuke get blueprints` with no flags lists the whole subtree.
			realm := shared.ExplicitFlag(cmd, "realm", config.KUKE_GET_BLUEPRINT_REALM.ViperKey)
			space := shared.ExplicitFlag(cmd, "space", config.KUKE_GET_BLUEPRINT_SPACE.ViperKey)
			stack := shared.ExplicitFlag(cmd, "stack", config.KUKE_GET_BLUEPRINT_STACK.ViperKey)

			var name string
			if len(args) > 0 {
				name = strings.TrimSpace(args[0])
			} else {
				name = strings.TrimSpace(viper.GetString(config.KUKE_GET_BLUEPRINT_NAME.ViperKey))
			}

			if name != "" {
				// A single blueprint is identified by its name plus its exact
				// binding scope. Default every coordinate to the operator's
				// full default scope (realm/space/stack = "default") so a
				// no-flag `kuke get blueprint <name>` finds resources stored at
				// the full default coordinate — how `kuke create blueprint` and
				// the team renderer actually store them. Mirrors `get cell`
				// (issue #1156). A realm-scoped Blueprint (space/stack unset) is
				// reachable by passing an explicit `--space "" --stack ""`.
				if realm == "" {
					realm = strings.TrimSpace(config.KUKE_GET_BLUEPRINT_REALM.ValueOrDefault())
				}
				if space == "" {
					space = strings.TrimSpace(config.KUKE_GET_BLUEPRINT_SPACE.ValueOrDefault())
				}
				if stack == "" {
					stack = strings.TrimSpace(config.KUKE_GET_BLUEPRINT_STACK.ValueOrDefault())
				}
				if realm == "" {
					return fmt.Errorf("%w (--realm)", errdefs.ErrRealmNameRequired)
				}
				lookup := v1beta1.CellBlueprintDoc{
					APIVersion: v1beta1.APIVersionV1Beta1,
					Kind:       v1beta1.KindCellBlueprint,
					Metadata: v1beta1.CellBlueprintMetadata{
						Name:  name,
						Realm: realm,
						Space: space,
						Stack: stack,
					},
				}
				result, getErr := client.GetBlueprint(cmd.Context(), lookup)
				if getErr != nil {
					if errors.Is(getErr, errdefs.ErrBlueprintNotFound) {
						return blueprintNotFoundErr(cmd, client, name, realm, space, stack)
					}
					return getErr
				}
				if !result.MetadataExists {
					return blueprintNotFoundErr(cmd, client, name, realm, space, stack)
				}
				return printBlueprint(cmd, &result.Blueprint, outputFormat)
			}

			blueprints, err := client.ListBlueprints(cmd.Context(), realm, space, stack)
			if err != nil {
				return err
			}
			return printBlueprints(cmd, blueprints, outputFormat)
		},
	}

	cmd.Flags().String("realm", "", "Filter blueprints by realm name")
	_ = viper.BindPFlag(config.KUKE_GET_BLUEPRINT_REALM.ViperKey, cmd.Flags().Lookup("realm"))
	cmd.Flags().String("space", "", "Filter blueprints by space name")
	_ = viper.BindPFlag(config.KUKE_GET_BLUEPRINT_SPACE.ViperKey, cmd.Flags().Lookup("space"))
	cmd.Flags().String("stack", "", "Filter blueprints by stack name")
	_ = viper.BindPFlag(config.KUKE_GET_BLUEPRINT_STACK.ViperKey, cmd.Flags().Lookup("stack"))
	cmd.Flags().
		StringP("output", "o", "", "Output format (yaml, json, table, wide). Default: table for list, yaml for single resource")
	_ = viper.BindPFlag(config.KUKE_GET_OUTPUT.ViperKey, cmd.Flags().Lookup("output"))
	_ = viper.BindPFlag(config.KUKE_GET_OUTPUT.ViperKey, cmd.Flags().Lookup("o"))

	cmd.ValidArgsFunction = config.CompleteBlueprintNames
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

// blueprintNotFoundErr builds the single-get miss error. It surfaces the exact
// scope searched and — best-effort — hints the coordinate where a Blueprint of
// the same name does live (probed realm-wide), so an operator who stored it at a
// non-default space/stack sees where to look instead of a bare "not found"
// (issue #1156). The realm-wide list is advisory: a list error is swallowed and
// the base error returned unadorned.
func blueprintNotFoundErr(
	cmd *cobra.Command, client kukeonv1.Client, name, realm, space, stack string,
) error {
	base := fmt.Sprintf(
		"blueprint %q not found (searched realm=%q space=%q stack=%q)", name, realm, space, stack,
	)
	blueprints, err := client.ListBlueprints(cmd.Context(), realm, "", "")
	if err != nil {
		return errors.New(base)
	}
	for i := range blueprints {
		m := &blueprints[i].Metadata
		if m.Name == name && (m.Realm != realm || m.Space != space || m.Stack != stack) {
			return fmt.Errorf(
				"%s; a blueprint %q exists at realm=%q space=%q stack=%q",
				base, name, m.Realm, m.Space, m.Stack,
			)
		}
	}
	return errors.New(base)
}

func printBlueprint(cmd *cobra.Command, blueprint *v1beta1.CellBlueprintDoc, format shared.OutputFormat) error {
	switch format {
	case shared.OutputFormatJSON:
		return shared.PrintJSON(cmd, blueprint)
	case shared.OutputFormatYAML, shared.OutputFormatTable:
		return shared.PrintYAML(cmd, blueprint)
	default:
		return shared.PrintYAML(cmd, blueprint)
	}
}

func printBlueprints(cmd *cobra.Command, blueprints []v1beta1.CellBlueprintDoc, format shared.OutputFormat) error {
	switch format {
	case shared.OutputFormatYAML:
		return shared.PrintYAML(cmd, blueprints)
	case shared.OutputFormatJSON:
		return shared.PrintJSON(cmd, blueprints)
	case shared.OutputFormatTable, shared.OutputFormatWide:
		if len(blueprints) == 0 {
			cmd.Println("No blueprints found.")
			return nil
		}
		headers := []string{"NAME", "REALM", "SPACE", "STACK"}
		rows := make([][]string, 0, len(blueprints))
		for i := range blueprints {
			b := &blueprints[i]
			rows = append(rows, []string{
				b.Metadata.Name,
				dashIfEmpty(b.Metadata.Realm),
				dashIfEmpty(b.Metadata.Space),
				dashIfEmpty(b.Metadata.Stack),
			})
		}
		shared.PrintTable(cmd, headers, rows)
		return nil
	default:
		return shared.PrintYAML(cmd, blueprints)
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
