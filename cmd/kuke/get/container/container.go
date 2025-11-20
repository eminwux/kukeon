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

package container

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

func NewContainerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "container [name]",
		Aliases:       []string{"containers"},
		Short:         "Get or list container information",
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

			realm := strings.TrimSpace(viper.GetString(config.KUKE_GET_CONTAINER_REALM.ViperKey))
			if realm == "" {
				realm, _ = cmd.Flags().GetString("realm")
				realm = strings.TrimSpace(realm)
			}

			space := strings.TrimSpace(viper.GetString(config.KUKE_GET_CONTAINER_SPACE.ViperKey))
			if space == "" {
				space, _ = cmd.Flags().GetString("space")
				space = strings.TrimSpace(space)
			}

			stack := strings.TrimSpace(viper.GetString(config.KUKE_GET_CONTAINER_STACK.ViperKey))
			if stack == "" {
				stack, _ = cmd.Flags().GetString("stack")
				stack = strings.TrimSpace(stack)
			}

			cell := strings.TrimSpace(viper.GetString(config.KUKE_GET_CONTAINER_CELL.ViperKey))
			if cell == "" {
				cell, _ = cmd.Flags().GetString("cell")
				cell = strings.TrimSpace(cell)
			}

			var name string
			if len(args) > 0 {
				name = strings.TrimSpace(args[0])
			} else {
				name = strings.TrimSpace(viper.GetString(config.KUKE_GET_CONTAINER_NAME.ViperKey))
			}

			if name != "" {
				// Get single container (requires realm, space, stack, and cell)
				if realm == "" {
					return fmt.Errorf("%w (--realm)", errdefs.ErrRealmNameRequired)
				}
				if space == "" {
					return fmt.Errorf("%w (--space)", errdefs.ErrSpaceNameRequired)
				}
				if stack == "" {
					return fmt.Errorf("%w (--stack)", errdefs.ErrStackNameRequired)
				}
				if cell == "" {
					return fmt.Errorf("%w (--cell)", errdefs.ErrCellNameRequired)
				}

				container, err := ctrl.GetContainer(name, realm, space, stack, cell)
				if err != nil {
					return err
				}

				return printContainer(cmd, container, outputFormat)
			}

			// List containers (optionally filtered by realm, space, stack, and/or cell)
			containers, err := ctrl.ListContainers(realm, space, stack, cell)
			if err != nil {
				return err
			}

			return printContainers(cmd, containers, outputFormat)
		},
	}

	cmd.Flags().String("realm", "", "Filter containers by realm name")
	_ = viper.BindPFlag(config.KUKE_GET_CONTAINER_REALM.ViperKey, cmd.Flags().Lookup("realm"))

	cmd.Flags().String("space", "", "Filter containers by space name")
	_ = viper.BindPFlag(config.KUKE_GET_CONTAINER_SPACE.ViperKey, cmd.Flags().Lookup("space"))

	cmd.Flags().String("stack", "", "Filter containers by stack name")
	_ = viper.BindPFlag(config.KUKE_GET_CONTAINER_STACK.ViperKey, cmd.Flags().Lookup("stack"))

	cmd.Flags().String("cell", "", "Filter containers by cell name")
	_ = viper.BindPFlag(config.KUKE_GET_CONTAINER_CELL.ViperKey, cmd.Flags().Lookup("cell"))

	cmd.Flags().
		StringP("output", "o", "", "Output format (yaml, json, table). Default: table for list, yaml for single resource")
	_ = viper.BindPFlag(config.KUKE_GET_OUTPUT.ViperKey, cmd.Flags().Lookup("output"))
	_ = viper.BindPFlag(config.KUKE_GET_OUTPUT.ViperKey, cmd.Flags().Lookup("o"))

	return cmd
}

func printContainer(cmd *cobra.Command, container interface{}, format shared.OutputFormat) error {
	switch format {
	case shared.OutputFormatYAML:
		return shared.PrintYAML(container)
	case shared.OutputFormatJSON:
		return shared.PrintJSON(container)
	case shared.OutputFormatTable:
		// For single resource, show full YAML by default
		return shared.PrintYAML(container)
	default:
		return shared.PrintYAML(container)
	}
}

func printContainers(cmd *cobra.Command, containers []*v1beta1.ContainerSpec, format shared.OutputFormat) error {
	switch format {
	case shared.OutputFormatYAML:
		return shared.PrintYAML(containers)
	case shared.OutputFormatJSON:
		return shared.PrintJSON(containers)
	case shared.OutputFormatTable:
		if len(containers) == 0 {
			cmd.Println("No containers found.")
			return nil
		}

		headers := []string{"NAME", "REALM", "SPACE", "STACK", "CELL", "IMAGE", "STATE"}
		rows := make([][]string, 0, len(containers))

		for _, c := range containers {
			// Container ID now stores just the container name
			containerName := c.ID

			state := "Unknown"
			// ContainerSpec doesn't have a State field, so we'll use "-"
			// The actual state would need to be retrieved from containerd

			rows = append(rows, []string{
				containerName,
				c.RealmID,
				c.SpaceID,
				c.StackID,
				c.CellID,
				c.Image,
				state,
			})
		}

		shared.PrintTable(cmd, headers, rows)
		return nil
	default:
		return shared.PrintYAML(containers)
	}
}
