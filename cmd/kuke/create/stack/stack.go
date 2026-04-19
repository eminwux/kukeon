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
	"github.com/eminwux/kukeon/cmd/kuke/create/shared"
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
		Short:         "Create or reconcile a stack within a space",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runCreateStack,
	}

	cmd.Flags().String("realm", "", "Realm that owns the stack")
	_ = viper.BindPFlag(config.KUKE_CREATE_STACK_REALM.ViperKey, cmd.Flags().Lookup("realm"))

	cmd.Flags().String("space", "", "Space that owns the stack")
	_ = viper.BindPFlag(config.KUKE_CREATE_STACK_SPACE.ViperKey, cmd.Flags().Lookup("space"))

	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)

	return cmd
}

func runCreateStack(cmd *cobra.Command, args []string) error {
	name, err := shared.RequireNameArgOrDefault(
		cmd,
		args,
		"stack",
		viper.GetString(config.KUKE_CREATE_STACK_NAME.ViperKey),
	)
	if err != nil {
		return err
	}

	realm := strings.TrimSpace(viper.GetString(config.KUKE_CREATE_STACK_REALM.ViperKey))
	if realm == "" {
		realm = strings.TrimSpace(config.KUKE_CREATE_STACK_REALM.ValueOrDefault())
	}

	space := strings.TrimSpace(viper.GetString(config.KUKE_CREATE_STACK_SPACE.ViperKey))
	if space == "" {
		space = strings.TrimSpace(config.KUKE_CREATE_STACK_SPACE.ValueOrDefault())
	}

	doc := v1beta1.StackDoc{
		Metadata: v1beta1.StackMetadata{Name: name},
		Spec: v1beta1.StackSpec{
			ID:      name,
			RealmID: realm,
			SpaceID: space,
		},
	}

	client, err := resolveClient(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	result, err := client.CreateStack(cmd.Context(), doc)
	if err != nil {
		return err
	}

	printStackResult(cmd, result)
	return nil
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukeshared.ClientFromCmd(cmd)
}

func printStackResult(cmd *cobra.Command, result kukeonv1.CreateStackResult) {
	cmd.Printf(
		"Stack %q (realm %q, space %q)\n",
		result.Stack.Metadata.Name,
		result.Stack.Spec.RealmID,
		result.Stack.Spec.SpaceID,
	)
	shared.PrintCreationOutcome(cmd, "metadata", result.MetadataExistsPost, result.Created)
	shared.PrintCreationOutcome(cmd, "cgroup", result.CgroupExistsPost, result.CgroupCreated)
}

// PrintStackResult is exported for testing purposes.
func PrintStackResult(cmd *cobra.Command, result kukeonv1.CreateStackResult) {
	printStackResult(cmd, result)
}
