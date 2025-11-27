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
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/get/shared"
	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type StackController interface {
	GetStack(stack intmodel.Stack) (controller.GetStackResult, error)
	ListStacks(realmName, spaceName string) ([]*v1beta1.StackDoc, error)
}

type stackController = StackController // internal alias for backward compatibility

// MockControllerKey is used to inject mock controllers in tests via context.
type MockControllerKey struct{}

func NewStackCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "stack [name]",
		Aliases:       []string{"stacks", "st"},
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

			outputFormat, err := shared.ParseOutputFormat(cmd)
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

				doc := &v1beta1.StackDoc{
					Metadata: v1beta1.StackMetadata{
						Name: name,
					},
					Spec: v1beta1.StackSpec{
						RealmID: realm,
						SpaceID: space,
					},
				}

				// Convert at boundary before calling controller
				var stackInternal intmodel.Stack
				stackInternal, _, err = apischeme.NormalizeStack(*doc)
				if err != nil {
					return fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
				}

				var result controller.GetStackResult
				result, err = ctrl.GetStack(stackInternal)
				if err != nil {
					return err
				}
				if !result.MetadataExists {
					return fmt.Errorf("stack %q not found in realm %q, space %q", name, realm, space)
				}

				// Convert result back to external for printing
				var stackDoc v1beta1.StackDoc
				stackDoc, err = apischeme.BuildStackExternalFromInternal(result.Stack, apischeme.VersionV1Beta1)
				if err != nil {
					return fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
				}

				return printStack(cmd, &stackDoc, outputFormat)
			}

			// List stacks (optionally filtered by realm and/or space)
			stacks, err := ctrl.ListStacks(realm, space)
			if err != nil {
				return err
			}

			return printStacks(cmd, stacks, outputFormat)
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

	// Register autocomplete for positional argument
	cmd.ValidArgsFunction = config.CompleteStackNames

	// Register autocomplete functions for flags
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("output", config.CompleteOutputFormat)
	_ = cmd.RegisterFlagCompletionFunc("o", config.CompleteOutputFormat)

	return cmd
}

func printStack(_ *cobra.Command, stack interface{}, format shared.OutputFormat) error {
	switch format {
	case shared.OutputFormatYAML:
		return shared.PrintYAML(stack)
	case shared.OutputFormatJSON:
		return shared.PrintJSON(stack)
	case shared.OutputFormatTable:
		// For single resource, show full YAML by default
		return shared.PrintYAML(stack)
	default:
		return shared.PrintYAML(stack)
	}
}

func printStacks(cmd *cobra.Command, stacks []*v1beta1.StackDoc, format shared.OutputFormat) error {
	switch format {
	case shared.OutputFormatYAML:
		return shared.PrintYAML(stacks)
	case shared.OutputFormatJSON:
		return shared.PrintJSON(stacks)
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

		shared.PrintTable(cmd, headers, rows)
		return nil
	default:
		return shared.PrintYAML(stacks)
	}
}

type controllerWrapper struct {
	ctrl *controller.Exec
}

func (w *controllerWrapper) GetStack(stack intmodel.Stack) (controller.GetStackResult, error) {
	return w.ctrl.GetStack(stack)
}

func (w *controllerWrapper) ListStacks(realmName, spaceName string) ([]*v1beta1.StackDoc, error) {
	return w.ctrl.ListStacks(realmName, spaceName)
}
