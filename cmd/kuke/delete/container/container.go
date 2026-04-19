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
	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// MockControllerKey is used to inject a mock kukeonv1.Client via context in tests.
type MockControllerKey struct{}

func NewContainerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "container [name]",
		Aliases:       []string{"co"},
		Short:         "Delete a container",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			realm := strings.TrimSpace(viper.GetString(config.KUKE_DELETE_CONTAINER_REALM.ViperKey))
			if realm == "" {
				realm = strings.TrimSpace(config.KUKE_DELETE_CONTAINER_REALM.ValueOrDefault())
			}
			space := strings.TrimSpace(viper.GetString(config.KUKE_DELETE_CONTAINER_SPACE.ViperKey))
			if space == "" {
				space = strings.TrimSpace(config.KUKE_DELETE_CONTAINER_SPACE.ValueOrDefault())
			}
			stack := strings.TrimSpace(viper.GetString(config.KUKE_DELETE_CONTAINER_STACK.ViperKey))
			if stack == "" {
				stack = strings.TrimSpace(config.KUKE_DELETE_CONTAINER_STACK.ValueOrDefault())
			}
			cell := strings.TrimSpace(viper.GetString(config.KUKE_DELETE_CONTAINER_CELL.ViperKey))
			if cell == "" {
				return fmt.Errorf("%w (--cell)", errdefs.ErrCellNameRequired)
			}

			doc := v1beta1.ContainerDoc{
				Metadata: v1beta1.ContainerMetadata{Name: name},
				Spec: v1beta1.ContainerSpec{
					RealmID: realm,
					SpaceID: space,
					StackID: stack,
					CellID:  cell,
				},
			}

			client, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()

			_, err = client.DeleteContainer(cmd.Context(), doc)
			if err != nil {
				return err
			}

			cmd.Printf("Deleted container %q from cell %q\n", name, cell)
			return nil
		},
	}

	cmd.Flags().String("realm", "", "Realm that owns the container")
	_ = viper.BindPFlag(config.KUKE_DELETE_CONTAINER_REALM.ViperKey, cmd.Flags().Lookup("realm"))
	cmd.Flags().String("space", "", "Space that owns the container")
	_ = viper.BindPFlag(config.KUKE_DELETE_CONTAINER_SPACE.ViperKey, cmd.Flags().Lookup("space"))
	cmd.Flags().String("stack", "", "Stack that owns the container")
	_ = viper.BindPFlag(config.KUKE_DELETE_CONTAINER_STACK.ViperKey, cmd.Flags().Lookup("stack"))
	cmd.Flags().String("cell", "", "Cell that owns the container")
	_ = viper.BindPFlag(config.KUKE_DELETE_CONTAINER_CELL.ViperKey, cmd.Flags().Lookup("cell"))

	cmd.ValidArgsFunction = config.CompleteContainerNames
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)
	_ = cmd.RegisterFlagCompletionFunc("cell", config.CompleteCellNames)

	return cmd
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukeshared.ClientFromCmd(cmd)
}
