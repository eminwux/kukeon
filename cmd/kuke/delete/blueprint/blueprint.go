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

package blueprint

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

// NewBlueprintCmd builds `kuke delete blueprint <name>` (issue #643). It removes
// the daemon-stored document for a single named, scoped CellBlueprint. The
// blueprint is identified by its name plus its binding scope
// (--realm/--space/--stack). A Blueprint is never cell-scoped, so there is no
// --cell flag. Unlike `kuke delete secret` there is no live-reference gate:
// cells materialized from a blueprint are independent copies (#620).
func NewBlueprintCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "blueprint [name]",
		Aliases:       []string{"bp"},
		Short:         "Delete a kind: CellBlueprint",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])

			realm := strings.TrimSpace(config.KUKE_DELETE_BLUEPRINT_REALM.ValueOrDefault())
			space := config.KUKE_DELETE_BLUEPRINT_SPACE.ValueOrDefault()
			stack := config.KUKE_DELETE_BLUEPRINT_STACK.ValueOrDefault()
			if realm == "" {
				return fmt.Errorf("%w (--realm)", errdefs.ErrRealmNameRequired)
			}

			doc := v1beta1.CellBlueprintDoc{
				APIVersion: v1beta1.APIVersionV1Beta1,
				Kind:       v1beta1.KindCellBlueprint,
				Metadata: v1beta1.CellBlueprintMetadata{
					Name:  name,
					Realm: realm,
					Space: strings.TrimSpace(space),
					Stack: strings.TrimSpace(stack),
				},
			}

			client, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()

			result, err := client.DeleteBlueprint(cmd.Context(), doc)
			if err != nil {
				return err
			}

			blueprintName := name
			if result.Blueprint.Metadata.Name != "" {
				blueprintName = result.Blueprint.Metadata.Name
			}
			cmd.Printf("Deleted blueprint %q\n", blueprintName)
			return nil
		},
	}

	cmd.Flags().String("realm", "", "Realm the blueprint is bound to")
	_ = viper.BindPFlag(config.KUKE_DELETE_BLUEPRINT_REALM.ViperKey, cmd.Flags().Lookup("realm"))
	cmd.Flags().String("space", "", "Space the blueprint is bound to")
	_ = viper.BindPFlag(config.KUKE_DELETE_BLUEPRINT_SPACE.ViperKey, cmd.Flags().Lookup("space"))
	cmd.Flags().String("stack", "", "Stack the blueprint is bound to")
	_ = viper.BindPFlag(config.KUKE_DELETE_BLUEPRINT_STACK.ViperKey, cmd.Flags().Lookup("stack"))

	cmd.ValidArgsFunction = config.CompleteBlueprintNames
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)

	return cmd
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukeshared.DaemonClientFromCmd(cmd)
}
