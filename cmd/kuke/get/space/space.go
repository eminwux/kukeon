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

func NewSpaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "space [name]",
		Aliases:       []string{"spaces", "sp"},
		Short:         "Get or list space information",
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

			realm := shared.ExplicitFlag(cmd, "realm", config.KUKE_GET_SPACE_REALM.ViperKey)

			var name string
			if len(args) > 0 {
				name = strings.TrimSpace(args[0])
			} else {
				name = strings.TrimSpace(viper.GetString(config.KUKE_GET_SPACE_NAME.ViperKey))
			}

			if name != "" {
				if realm == "" {
					realm = strings.TrimSpace(config.KUKE_GET_SPACE_REALM.ValueOrDefault())
				}
				if realm == "" {
					return fmt.Errorf("%w (--realm)", errdefs.ErrRealmNameRequired)
				}
				doc := v1beta1.SpaceDoc{
					Metadata: v1beta1.SpaceMetadata{Name: name},
					Spec:     v1beta1.SpaceSpec{RealmID: realm},
				}
				result, err := client.GetSpace(cmd.Context(), doc)
				if err != nil {
					if errors.Is(err, errdefs.ErrSpaceNotFound) {
						return fmt.Errorf("space %q not found in realm %q", name, realm)
					}
					return err
				}
				if !result.MetadataExists {
					return fmt.Errorf("space %q not found in realm %q", name, realm)
				}
				return printSpace(&result.Space, outputFormat)
			}

			spaces, err := client.ListSpaces(cmd.Context(), realm)
			if err != nil {
				return err
			}
			return printSpaces(cmd, spaces, outputFormat)
		},
	}

	cmd.Flags().String("realm", "", "Filter spaces by realm name")
	_ = viper.BindPFlag(config.KUKE_GET_SPACE_REALM.ViperKey, cmd.Flags().Lookup("realm"))

	cmd.Flags().
		StringP("output", "o", "", "Output format (yaml, json, table). Default: table for list, yaml for single resource")
	_ = viper.BindPFlag(config.KUKE_GET_OUTPUT.ViperKey, cmd.Flags().Lookup("output"))
	_ = viper.BindPFlag(config.KUKE_GET_OUTPUT.ViperKey, cmd.Flags().Lookup("o"))

	cmd.ValidArgsFunction = config.CompleteSpaceNames
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
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

func printSpace(space *v1beta1.SpaceDoc, format shared.OutputFormat) error {
	switch format {
	case shared.OutputFormatJSON:
		return shared.PrintJSON(space)
	default:
		return shared.PrintYAML(space)
	}
}

func printSpaces(cmd *cobra.Command, spaces []v1beta1.SpaceDoc, format shared.OutputFormat) error {
	switch format {
	case shared.OutputFormatYAML:
		return shared.PrintYAML(spaces)
	case shared.OutputFormatJSON:
		return shared.PrintJSON(spaces)
	case shared.OutputFormatTable:
		if len(spaces) == 0 {
			cmd.Println("No spaces found.")
			return nil
		}
		headers := []string{"NAME", "REALM", "STATE", "CGROUP"}
		rows := make([][]string, 0, len(spaces))
		for i := range spaces {
			s := &spaces[i]
			state := (&s.Status.State).String()
			cgroup := s.Status.CgroupPath
			if cgroup == "" {
				cgroup = "-"
			}
			rows = append(rows, []string{s.Metadata.Name, s.Spec.RealmID, state, cgroup})
		}
		shared.PrintTable(cmd, headers, rows)
		return nil
	default:
		return shared.PrintYAML(spaces)
	}
}
