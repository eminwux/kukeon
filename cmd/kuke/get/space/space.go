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
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/get/shared"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func NewSpaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "space [name]",
		Aliases:       []string{"spaces"},
		Short:         "Get or list space information",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctrl, err := shared.ControllerFromCmd(cmd)
			if err != nil {
				return err
			}

			outputFormat, err := shared.ParseOutputFormat(cmd)
			if err != nil {
				return err
			}

			realm := strings.TrimSpace(viper.GetString(config.KUKE_GET_SPACE_REALM.ViperKey))
			if realm == "" {
				realm, _ = cmd.Flags().GetString("realm")
				realm = strings.TrimSpace(realm)
			}

			var name string
			if len(args) > 0 {
				name = strings.TrimSpace(args[0])
			} else {
				name = strings.TrimSpace(viper.GetString(config.KUKE_GET_SPACE_NAME.ViperKey))
			}

			if name != "" {
				// Get single space (requires realm)
				if realm == "" {
					return fmt.Errorf("%w (--realm)", errdefs.ErrRealmNameRequired)
				}

				space, err := ctrl.GetSpace(name, realm)
				if err != nil {
					if errors.Is(err, errdefs.ErrSpaceNotFound) {
						return fmt.Errorf("space %q not found in realm %q", name, realm)
					}
					return err
				}

				return printSpace(cmd, space, outputFormat)
			}

			// List spaces (optionally filtered by realm)
			spaces, err := ctrl.ListSpaces(realm)
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

	return cmd
}

func printSpace(cmd *cobra.Command, space interface{}, format shared.OutputFormat) error {
	switch format {
	case shared.OutputFormatYAML:
		return shared.PrintYAML(space)
	case shared.OutputFormatJSON:
		return shared.PrintJSON(space)
	case shared.OutputFormatTable:
		// For single resource, show full YAML by default
		return shared.PrintYAML(space)
	default:
		return shared.PrintYAML(space)
	}
}

func printSpaces(cmd *cobra.Command, spaces []*v1beta1.SpaceDoc, format shared.OutputFormat) error {
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

		for _, s := range spaces {
			state := (&s.Status.State).String()
			cgroup := s.Status.CgroupPath
			if cgroup == "" {
				cgroup = "-"
			}

			rows = append(rows, []string{
				s.Metadata.Name,
				s.Spec.RealmID,
				state,
				cgroup,
			})
		}

		shared.PrintTable(cmd, headers, rows)
		return nil
	default:
		return shared.PrintYAML(spaces)
	}
}
