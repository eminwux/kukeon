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
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type SpaceController interface {
	GetSpace(name, realm string) (*v1beta1.SpaceDoc, error)
	ListSpaces(realm string) ([]*v1beta1.SpaceDoc, error)
}

type spaceController = SpaceController // internal alias for backward compatibility

// MockControllerKey is used to inject mock controllers in tests via context.
type MockControllerKey struct{}

type printObjectFunc func(interface{}) error

var (
	// YAMLPrinter is exported for testing.
	YAMLPrinter printObjectFunc = shared.PrintYAML
	// JSONPrinter is exported for testing.
	JSONPrinter printObjectFunc = shared.PrintJSON
)

var (
	printYAML printObjectFunc = YAMLPrinter
	printJSON printObjectFunc = JSONPrinter
)

type tablePrinterFunc func(*cobra.Command, []string, [][]string)

// TablePrinter is exported for testing.
var TablePrinter tablePrinterFunc = shared.PrintTable

var printTable tablePrinterFunc = TablePrinter

func NewSpaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "space [name]",
		Aliases:       []string{"spaces"},
		Short:         "Get or list space information",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			var ctrl spaceController
			if mockCtrl, ok := cmd.Context().Value(MockControllerKey{}).(SpaceController); ok {
				ctrl = mockCtrl
			} else {
				realCtrl, err := shared.ControllerFromCmd(cmd)
				if err != nil {
					return err
				}
				ctrl = &controllerWrapper{ctrl: realCtrl}
			}

			outputFormat, parseErr := shared.ParseOutputFormat(cmd)
			if parseErr != nil {
				return parseErr
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

				space, getErr := ctrl.GetSpace(name, realm)
				if getErr != nil {
					if errors.Is(getErr, errdefs.ErrSpaceNotFound) {
						return fmt.Errorf("space %q not found in realm %q", name, realm)
					}
					return getErr
				}

				return printSpace(space, outputFormat)
			}

			// List spaces (optionally filtered by realm)
			spaces, listErr := ctrl.ListSpaces(realm)
			if listErr != nil {
				return listErr
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

	// Register autocomplete for positional argument
	cmd.ValidArgsFunction = config.CompleteSpaceNames

	// Register autocomplete function for --realm flag
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)

	// Register autocomplete function for --output flag
	_ = cmd.RegisterFlagCompletionFunc("output", config.CompleteOutputFormat)

	// Register autocomplete function for -o flag
	_ = cmd.RegisterFlagCompletionFunc("o", config.CompleteOutputFormat)

	return cmd
}

func printSpace(space interface{}, format shared.OutputFormat) error {
	switch format {
	case shared.OutputFormatYAML:
		return YAMLPrinter(space)
	case shared.OutputFormatJSON:
		return JSONPrinter(space)
	case shared.OutputFormatTable:
		// For single resource, show full YAML by default
		return YAMLPrinter(space)
	default:
		return YAMLPrinter(space)
	}
}

func printSpaces(cmd *cobra.Command, spaces []*v1beta1.SpaceDoc, format shared.OutputFormat) error {
	switch format {
	case shared.OutputFormatYAML:
		return YAMLPrinter(spaces)
	case shared.OutputFormatJSON:
		return JSONPrinter(spaces)
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

		TablePrinter(cmd, headers, rows)
		return nil
	default:
		return YAMLPrinter(spaces)
	}
}

type controllerWrapper struct {
	ctrl *controller.Exec
}

func (w *controllerWrapper) GetSpace(name, realm string) (*v1beta1.SpaceDoc, error) {
	return w.ctrl.GetSpace(name, realm)
}

func (w *controllerWrapper) ListSpaces(realm string) ([]*v1beta1.SpaceDoc, error) {
	return w.ctrl.ListSpaces(realm)
}
