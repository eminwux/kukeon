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

package stack

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

type StackController interface {
	GetStack(name, realmName, spaceName string) (*v1beta1.StackDoc, error)
	ListStacks(realmName, spaceName string) ([]*v1beta1.StackDoc, error)
}

type stackController = StackController // internal alias for backward compatibility

// MockControllerKey is used to inject mock controllers in tests via context.
type MockControllerKey struct{}

var (
	ParseOutputFormat = shared.ParseOutputFormat
	YAMLPrinter       = shared.PrintYAML
	JSONPrinter       = shared.PrintJSON
	TablePrinter      = shared.PrintTable
	RunPrintStack     = printStack
	RunPrintStacks    = printStacks
)

func NewStackCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "stack [name]",
		Aliases:       []string{"stacks"},
		Short:         "Get or list stack information",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			var ctrl stackController
			if mockCtrl, ok := cmd.Context().Value(MockControllerKey{}).(StackController); ok {
				ctrl = mockCtrl
			} else {
				realCtrl, err := shared.ControllerFromCmd(cmd)
				if err != nil {
					return err
				}
				ctrl = &controllerWrapper{ctrl: realCtrl}
			}

			outputFormat, err := ParseOutputFormat(cmd)
			if err != nil {
				return err
			}

			realm := strings.TrimSpace(viper.GetString(config.KUKE_GET_STACK_REALM.ViperKey))
			if realm == "" {
				realm, _ = cmd.Flags().GetString("realm")
				realm = strings.TrimSpace(realm)
			}

			space := strings.TrimSpace(viper.GetString(config.KUKE_GET_STACK_SPACE.ViperKey))
			if space == "" {
				space, _ = cmd.Flags().GetString("space")
				space = strings.TrimSpace(space)
			}

			var name string
			if len(args) > 0 {
				name = strings.TrimSpace(args[0])
			} else {
				name = strings.TrimSpace(viper.GetString(config.KUKE_GET_STACK_NAME.ViperKey))
			}

			if name != "" {
				// Get single stack (requires realm and space)
				if realm == "" {
					return fmt.Errorf("%w (--realm)", errdefs.ErrRealmNameRequired)
				}
				if space == "" {
					return fmt.Errorf("%w (--space)", errdefs.ErrSpaceNameRequired)
				}

				stack, err := ctrl.GetStack(name, realm, space)
				if err != nil {
					if errors.Is(err, errdefs.ErrStackNotFound) {
						return fmt.Errorf("stack %q not found in realm %q, space %q", name, realm, space)
					}
					return err
				}

				return RunPrintStack(cmd, stack, outputFormat)
			}

			// List stacks (optionally filtered by realm and/or space)
			stacks, err := ctrl.ListStacks(realm, space)
			if err != nil {
				return err
			}

			return RunPrintStacks(cmd, stacks, outputFormat)
		},
	}

	cmd.Flags().String("realm", "", "Filter stacks by realm name")
	_ = viper.BindPFlag(config.KUKE_GET_STACK_REALM.ViperKey, cmd.Flags().Lookup("realm"))

	cmd.Flags().String("space", "", "Filter stacks by space name")
	_ = viper.BindPFlag(config.KUKE_GET_STACK_SPACE.ViperKey, cmd.Flags().Lookup("space"))

	cmd.Flags().
		StringP("output", "o", "", "Output format (yaml, json, table). Default: table for list, yaml for single resource")
	_ = viper.BindPFlag(config.KUKE_GET_OUTPUT.ViperKey, cmd.Flags().Lookup("output"))
	_ = viper.BindPFlag(config.KUKE_GET_OUTPUT.ViperKey, cmd.Flags().Lookup("o"))

	return cmd
}

func printStack(_ *cobra.Command, stack interface{}, format shared.OutputFormat) error {
	switch format {
	case shared.OutputFormatYAML:
		return YAMLPrinter(stack)
	case shared.OutputFormatJSON:
		return JSONPrinter(stack)
	case shared.OutputFormatTable:
		// For single resource, show full YAML by default
		return YAMLPrinter(stack)
	default:
		return YAMLPrinter(stack)
	}
}

func printStacks(cmd *cobra.Command, stacks []*v1beta1.StackDoc, format shared.OutputFormat) error {
	switch format {
	case shared.OutputFormatYAML:
		return YAMLPrinter(stacks)
	case shared.OutputFormatJSON:
		return JSONPrinter(stacks)
	case shared.OutputFormatTable:
		if len(stacks) == 0 {
			cmd.Println("No stacks found.")
			return nil
		}

		headers := []string{"NAME", "REALM", "SPACE", "STATE", "CGROUP"}
		rows := make([][]string, 0, len(stacks))

		for _, s := range stacks {
			state := (&s.Status.State).String()
			cgroup := s.Status.CgroupPath
			if cgroup == "" {
				cgroup = "-"
			}

			rows = append(rows, []string{
				s.Metadata.Name,
				s.Spec.RealmID,
				s.Spec.SpaceID,
				state,
				cgroup,
			})
		}

		TablePrinter(cmd, headers, rows)
		return nil
	default:
		return YAMLPrinter(stacks)
	}
}

type controllerWrapper struct {
	ctrl *controller.Exec
}

func (w *controllerWrapper) GetStack(name, realmName, spaceName string) (*v1beta1.StackDoc, error) {
	return w.ctrl.GetStack(name, realmName, spaceName)
}

func (w *controllerWrapper) ListStacks(realmName, spaceName string) ([]*v1beta1.StackDoc, error) {
	return w.ctrl.ListStacks(realmName, spaceName)
}
