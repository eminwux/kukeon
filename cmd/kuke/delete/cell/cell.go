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
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// MockControllerKey is used to inject a mock kukeonv1.Client via context in tests.
type MockControllerKey struct{}

func NewCellCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "cell [name]",
		Aliases:       []string{"ce"},
		Short:         "Delete a cell",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			realm := strings.TrimSpace(viper.GetString(config.KUKE_DELETE_CELL_REALM.ViperKey))
			if realm == "" {
				realm = strings.TrimSpace(config.KUKE_DELETE_CELL_REALM.ValueOrDefault())
			}
			space := strings.TrimSpace(viper.GetString(config.KUKE_DELETE_CELL_SPACE.ViperKey))
			if space == "" {
				space = strings.TrimSpace(config.KUKE_DELETE_CELL_SPACE.ValueOrDefault())
			}
			stack := strings.TrimSpace(viper.GetString(config.KUKE_DELETE_CELL_STACK.ViperKey))
			if stack == "" {
				stack = strings.TrimSpace(config.KUKE_DELETE_CELL_STACK.ValueOrDefault())
			}

			doc := v1beta1.CellDoc{
				Metadata: v1beta1.CellMetadata{Name: name},
				Spec: v1beta1.CellSpec{
					RealmID: realm,
					SpaceID: space,
					StackID: stack,
				},
			}

			client, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()

			result, err := client.DeleteCell(cmd.Context(), doc)
			if err != nil {
				return err
			}

			cellName := result.Cell.Metadata.Name
			if cellName == "" {
				cellName = name
			}
			stackName := result.Cell.Spec.StackID
			if stackName == "" {
				stackName = stack
			}
			cmd.Printf("Deleted cell %q from stack %q\n", cellName, stackName)
			return nil
		},
	}

	cmd.Flags().String("realm", "", "Realm that owns the cell")
	_ = viper.BindPFlag(config.KUKE_DELETE_CELL_REALM.ViperKey, cmd.Flags().Lookup("realm"))
	cmd.Flags().String("space", "", "Space that owns the cell")
	_ = viper.BindPFlag(config.KUKE_DELETE_CELL_SPACE.ViperKey, cmd.Flags().Lookup("space"))
	cmd.Flags().String("stack", "", "Stack that owns the cell")
	_ = viper.BindPFlag(config.KUKE_DELETE_CELL_STACK.ViperKey, cmd.Flags().Lookup("stack"))

	cmd.ValidArgsFunction = config.CompleteCellNames
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)

	return cmd
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukeshared.ClientFromCmd(cmd)
}
