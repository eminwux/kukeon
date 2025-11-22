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

package cell

import (
	"errors"
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/get/shared"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type CellController interface {
	GetCell(name, realm, space, stack string) (*v1beta1.CellDoc, error)
	ListCells(realm, space, stack string) ([]*v1beta1.CellDoc, error)
}

type cellController = CellController // internal alias for backward compatibility

// MockControllerKey is used to inject mock controllers in tests via context.
type MockControllerKey struct{}

func NewCellCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "cell [name]",
		Aliases:       []string{"cells"},
		Short:         "Get or list cell information",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			var ctrl cellController
			if mockCtrl, ok := cmd.Context().Value(MockControllerKey{}).(CellController); ok {
				ctrl = mockCtrl
			} else {
				realCtrl, err := shared.ControllerFromCmd(cmd)
				if err != nil {
					return err
				}
				ctrl = &controllerWrapper{ctrl: realCtrl}
			}

			outputFormat, formatErr := shared.ParseOutputFormat(cmd)
			if formatErr != nil {
				return formatErr
			}

			realm := strings.TrimSpace(viper.GetString(config.KUKE_GET_CELL_REALM.ViperKey))
			if realm == "" {
				realm, _ = cmd.Flags().GetString("realm")
				realm = strings.TrimSpace(realm)
			}

			space := strings.TrimSpace(viper.GetString(config.KUKE_GET_CELL_SPACE.ViperKey))
			if space == "" {
				space, _ = cmd.Flags().GetString("space")
				space = strings.TrimSpace(space)
			}

			stack := strings.TrimSpace(viper.GetString(config.KUKE_GET_CELL_STACK.ViperKey))
			if stack == "" {
				stack, _ = cmd.Flags().GetString("stack")
				stack = strings.TrimSpace(stack)
			}

			var name string
			if len(args) > 0 {
				name = strings.TrimSpace(args[0])
			} else {
				name = strings.TrimSpace(viper.GetString(config.KUKE_GET_CELL_NAME.ViperKey))
			}

			if name != "" {
				// Get single cell (requires realm, space, and stack)
				if realm == "" {
					return fmt.Errorf("%w (--realm)", errdefs.ErrRealmNameRequired)
				}
				if space == "" {
					return fmt.Errorf("%w (--space)", errdefs.ErrSpaceNameRequired)
				}
				if stack == "" {
					return fmt.Errorf("%w (--stack)", errdefs.ErrStackNameRequired)
				}

				cell, getErr := ctrl.GetCell(name, realm, space, stack)
				if getErr != nil {
					if errors.Is(getErr, errdefs.ErrCellNotFound) {
						return fmt.Errorf(
							"cell %q not found in realm %q, space %q, stack %q",
							name,
							realm,
							space,
							stack,
						)
					}
					return getErr
				}

				return printCell(cmd, cell, outputFormat)
			}

			// List cells (optionally filtered by realm, space, and/or stack)
			cells, listErr := ctrl.ListCells(realm, space, stack)
			if listErr != nil {
				return listErr
			}

			return printCells(cmd, cells, outputFormat)
		},
	}

	cmd.Flags().String("realm", "", "Filter cells by realm name")
	_ = viper.BindPFlag(config.KUKE_GET_CELL_REALM.ViperKey, cmd.Flags().Lookup("realm"))

	cmd.Flags().String("space", "", "Filter cells by space name")
	_ = viper.BindPFlag(config.KUKE_GET_CELL_SPACE.ViperKey, cmd.Flags().Lookup("space"))

	cmd.Flags().String("stack", "", "Filter cells by stack name")
	_ = viper.BindPFlag(config.KUKE_GET_CELL_STACK.ViperKey, cmd.Flags().Lookup("stack"))

	cmd.Flags().
		StringP("output", "o", "", "Output format (yaml, json, table). Default: table for list, yaml for single resource")
	_ = viper.BindPFlag(config.KUKE_GET_OUTPUT.ViperKey, cmd.Flags().Lookup("output"))
	_ = viper.BindPFlag(config.KUKE_GET_OUTPUT.ViperKey, cmd.Flags().Lookup("o"))

	return cmd
}

func printCell(_ *cobra.Command, cell interface{}, format shared.OutputFormat) error {
	switch format {
	case shared.OutputFormatYAML:
		return shared.PrintYAML(cell)
	case shared.OutputFormatJSON:
		return shared.PrintJSON(cell)
	case shared.OutputFormatTable:
		// For single resource, show full YAML by default
		return shared.PrintYAML(cell)
	default:
		return shared.PrintYAML(cell)
	}
}

func printCells(cmd *cobra.Command, cells []*v1beta1.CellDoc, format shared.OutputFormat) error {
	switch format {
	case shared.OutputFormatYAML:
		return shared.PrintYAML(cells)
	case shared.OutputFormatJSON:
		return shared.PrintJSON(cells)
	case shared.OutputFormatTable:
		if len(cells) == 0 {
			cmd.Println("No cells found.")
			return nil
		}

		headers := []string{"NAME", "REALM", "SPACE", "STACK", "STATE", "CGROUP"}
		rows := make([][]string, 0, len(cells))

		for _, c := range cells {
			state := (&c.Status.State).String()
			cgroup := c.Status.CgroupPath
			if cgroup == "" {
				cgroup = "-"
			}

			rows = append(rows, []string{
				c.Metadata.Name,
				c.Spec.RealmID,
				c.Spec.SpaceID,
				c.Spec.StackID,
				state,
				cgroup,
			})
		}

		shared.PrintTable(cmd, headers, rows)
		return nil
	default:
		return shared.PrintYAML(cells)
	}
}

type controllerWrapper struct {
	ctrl *controller.Exec
}

func (w *controllerWrapper) GetCell(name, realm, space, stack string) (*v1beta1.CellDoc, error) {
	return w.ctrl.GetCell(name, realm, space, stack)
}

func (w *controllerWrapper) ListCells(realm, space, stack string) ([]*v1beta1.CellDoc, error) {
	return w.ctrl.ListCells(realm, space, stack)
}
