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
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/delete/shared"
	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
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
		Aliases:       []string{"st"},
		Short:         "Delete a stack",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			realm := strings.TrimSpace(viper.GetString(config.KUKE_DELETE_STACK_REALM.ViperKey))
			if realm == "" {
				realm = strings.TrimSpace(config.KUKE_DELETE_STACK_REALM.ValueOrDefault())
			}
			space := strings.TrimSpace(viper.GetString(config.KUKE_DELETE_STACK_SPACE.ViperKey))
			if space == "" {
				space = strings.TrimSpace(config.KUKE_DELETE_STACK_SPACE.ValueOrDefault())
			}

			force := shared.ParseForceFlag(cmd)
			cascade := shared.ParseCascadeFlag(cmd)

			doc := v1beta1.StackDoc{
				Metadata: v1beta1.StackMetadata{Name: name},
				Spec: v1beta1.StackSpec{
					RealmID: realm,
					SpaceID: space,
				},
			}

			client, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()

			result, err := client.DeleteStack(cmd.Context(), doc, force, cascade)
			if err != nil {
				return err
			}

			stackName := result.StackName
			if stackName == "" {
				stackName = name
			}
			spaceName := result.SpaceName
			if spaceName == "" {
				spaceName = space
			}
			cmd.Printf("Deleted stack %q from space %q\n", stackName, spaceName)
			return nil
		},
	}

	cmd.Flags().String("realm", "", "Realm that owns the stack")
	_ = viper.BindPFlag(config.KUKE_DELETE_STACK_REALM.ViperKey, cmd.Flags().Lookup("realm"))
	cmd.Flags().String("space", "", "Space that owns the stack")
	_ = viper.BindPFlag(config.KUKE_DELETE_STACK_SPACE.ViperKey, cmd.Flags().Lookup("space"))

	cmd.ValidArgsFunction = config.CompleteStackNames
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)

	return cmd
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukeshared.ClientFromCmd(cmd)
}
