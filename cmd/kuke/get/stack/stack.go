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
	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// MockControllerKey is used to inject a mock kukeonv1.Client via context in tests.
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
			client, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()

			outputFormat, err := shared.ParseOutputFormat(cmd)
			if err != nil {
				return err
			}

			realm := strings.TrimSpace(viper.GetString(config.KUKE_GET_STACK_REALM.ViperKey))
			if realm == "" {
				realm = strings.TrimSpace(config.KUKE_GET_STACK_REALM.ValueOrDefault())
			}
			space := strings.TrimSpace(viper.GetString(config.KUKE_GET_STACK_SPACE.ViperKey))
			if space == "" {
				space = strings.TrimSpace(config.KUKE_GET_STACK_SPACE.ValueOrDefault())
			}

			var name string
			if len(args) > 0 {
				name = strings.TrimSpace(args[0])
			} else {
				name = strings.TrimSpace(viper.GetString(config.KUKE_GET_STACK_NAME.ViperKey))
			}

			if name != "" {
				if realm == "" {
					return fmt.Errorf("%w (--realm)", errdefs.ErrRealmNameRequired)
				}
				if space == "" {
					return fmt.Errorf("%w (--space)", errdefs.ErrSpaceNameRequired)
				}
				doc := v1beta1.StackDoc{
					Metadata: v1beta1.StackMetadata{Name: name},
					Spec: v1beta1.StackSpec{
						RealmID: realm,
						SpaceID: space,
					},
				}
				result, err := client.GetStack(cmd.Context(), doc)
				if err != nil {
					if errors.Is(err, errdefs.ErrStackNotFound) {
						return fmt.Errorf("stack %q not found in realm %q, space %q", name, realm, space)
					}
					return err
				}
				if !result.MetadataExists {
					return fmt.Errorf("stack %q not found in realm %q, space %q", name, realm, space)
				}
				return printStack(&result.Stack, outputFormat)
			}

			stacks, err := client.ListStacks(cmd.Context(), realm, space)
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

	cmd.ValidArgsFunction = config.CompleteStackNames
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
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

func printStack(stack *v1beta1.StackDoc, format shared.OutputFormat) error {
	switch format {
	case shared.OutputFormatJSON:
		return shared.PrintJSON(stack)
	default:
		return shared.PrintYAML(stack)
	}
}

func printStacks(cmd *cobra.Command, stacks []v1beta1.StackDoc, format shared.OutputFormat) error {
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
		for i := range stacks {
			s := &stacks[i]
			state := (&s.Status.State).String()
			cgroup := s.Status.CgroupPath
			if cgroup == "" {
				cgroup = "-"
			}
			rows = append(rows, []string{s.Metadata.Name, s.Spec.RealmID, s.Spec.SpaceID, state, cgroup})
		}
		shared.PrintTable(cmd, headers, rows)
		return nil
	default:
		return shared.PrintYAML(stacks)
	}
}
