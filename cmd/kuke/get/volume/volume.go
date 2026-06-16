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

package volume

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

// NewVolumeCmd builds `kuke get volume(s)` (issue #1236). With a name it shows
// one Volume's metadata; without one it lists every Volume bound to the filter
// scope or any scope nested within it (subtree-filter semantics, matching
// `kuke get blueprints`). A Volume is never cell-scoped, so there is no --cell
// flag — the scope bottoms out at stack.
func NewVolumeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "volume [name]",
		Aliases:       []string{"volumes", "vol"},
		Short:         "Get or list kind: Volume metadata",
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
			// so `kuke get volumes` with no flags lists the whole subtree.
			realm := shared.ExplicitFlag(cmd, "realm", config.KUKE_GET_VOLUME_REALM.ViperKey)
			space := shared.ExplicitFlag(cmd, "space", config.KUKE_GET_VOLUME_SPACE.ViperKey)
			stack := shared.ExplicitFlag(cmd, "stack", config.KUKE_GET_VOLUME_STACK.ViperKey)

			var name string
			if len(args) > 0 {
				name = strings.TrimSpace(args[0])
			} else {
				name = strings.TrimSpace(viper.GetString(config.KUKE_GET_VOLUME_NAME.ViperKey))
			}

			if name != "" {
				// A single volume is identified by its name plus its exact
				// binding scope. Default the realm to the operator's current
				// realm; deeper coordinates stay unset unless provided. A
				// realm-scoped Volume (space/stack unset) is reachable with no
				// extra flags.
				if realm == "" {
					realm = strings.TrimSpace(config.KUKE_GET_VOLUME_REALM.ValueOrDefault())
				}
				if realm == "" {
					return fmt.Errorf("%w (--realm)", errdefs.ErrRealmNameRequired)
				}
				lookup := v1beta1.VolumeDoc{
					APIVersion: v1beta1.APIVersionV1Beta1,
					Kind:       v1beta1.KindVolume,
					Metadata: v1beta1.VolumeMetadata{
						Name:  name,
						Realm: realm,
						Space: space,
						Stack: stack,
					},
				}
				result, getErr := client.GetVolume(cmd.Context(), lookup)
				if getErr != nil {
					if errors.Is(getErr, errdefs.ErrVolumeNotFound) {
						return fmt.Errorf("volume %q not found", name)
					}
					return getErr
				}
				if !result.MetadataExists {
					return fmt.Errorf("volume %q not found", name)
				}
				return printVolume(cmd, &result.Volume, outputFormat)
			}

			volumes, err := client.ListVolumes(cmd.Context(), realm, space, stack)
			if err != nil {
				return err
			}
			return printVolumes(cmd, volumes, outputFormat)
		},
	}

	cmd.Flags().String("realm", "", "Filter volumes by realm name")
	_ = viper.BindPFlag(config.KUKE_GET_VOLUME_REALM.ViperKey, cmd.Flags().Lookup("realm"))
	cmd.Flags().String("space", "", "Filter volumes by space name")
	_ = viper.BindPFlag(config.KUKE_GET_VOLUME_SPACE.ViperKey, cmd.Flags().Lookup("space"))
	cmd.Flags().String("stack", "", "Filter volumes by stack name")
	_ = viper.BindPFlag(config.KUKE_GET_VOLUME_STACK.ViperKey, cmd.Flags().Lookup("stack"))
	cmd.Flags().
		StringP("output", "o", "", "Output format (yaml, json, table, wide). Default: table for list, table for single resource")
	_ = viper.BindPFlag(config.KUKE_GET_OUTPUT.ViperKey, cmd.Flags().Lookup("output"))
	_ = viper.BindPFlag(config.KUKE_GET_OUTPUT.ViperKey, cmd.Flags().Lookup("o"))

	cmd.ValidArgsFunction = config.CompleteVolumeNames
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

func printVolume(cmd *cobra.Command, volume *v1beta1.VolumeDoc, format shared.OutputFormat) error {
	switch format {
	case shared.OutputFormatJSON:
		return shared.PrintJSON(cmd, volume)
	case shared.OutputFormatYAML:
		return shared.PrintYAML(cmd, volume)
	default:
		// table / wide: render the single found element as a one-row table
		// with the same columns as the list view (kubectl parity).
		return printVolumes(cmd, []v1beta1.VolumeDoc{*volume}, format)
	}
}

func printVolumes(cmd *cobra.Command, volumes []v1beta1.VolumeDoc, format shared.OutputFormat) error {
	switch format {
	case shared.OutputFormatYAML:
		return shared.PrintYAML(cmd, volumes)
	case shared.OutputFormatJSON:
		return shared.PrintJSON(cmd, volumes)
	case shared.OutputFormatTable, shared.OutputFormatWide:
		if len(volumes) == 0 {
			cmd.Println("No volumes found.")
			return nil
		}
		headers := []string{"NAME", "REALM", "SPACE", "STACK"}
		rows := make([][]string, 0, len(volumes))
		for i := range volumes {
			v := &volumes[i]
			rows = append(rows, []string{
				v.Metadata.Name,
				dashIfEmpty(v.Metadata.Realm),
				dashIfEmpty(v.Metadata.Space),
				dashIfEmpty(v.Metadata.Stack),
			})
		}
		shared.PrintTable(cmd, headers, rows)
		return nil
	default:
		return shared.PrintYAML(cmd, volumes)
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
