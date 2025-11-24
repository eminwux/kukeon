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
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/kill/shared"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type KillCellResult = controller.KillCellResult

type cellController interface {
	KillCell(doc *v1beta1.CellDoc) (KillCellResult, error)
}

// MockControllerKey is used to inject mock controllers in tests via context.
type MockControllerKey struct{}

// NewCellCmd builds the `kuke kill cell` subcommand.
func NewCellCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "cell [name]",
		Short:         "Kill a cell",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			realm := strings.TrimSpace(viper.GetString(config.KUKE_KILL_CELL_REALM.ViperKey))
			space := strings.TrimSpace(viper.GetString(config.KUKE_KILL_CELL_SPACE.ViperKey))
			stack := strings.TrimSpace(viper.GetString(config.KUKE_KILL_CELL_STACK.ViperKey))

			if realm == "" {
				return fmt.Errorf("%w (--realm)", errdefs.ErrRealmNameRequired)
			}
			if space == "" {
				return fmt.Errorf("%w (--space)", errdefs.ErrSpaceNameRequired)
			}
			if stack == "" {
				return fmt.Errorf("%w (--stack)", errdefs.ErrStackNameRequired)
			}

			var ctrl cellController
			if mockCtrl, ok := cmd.Context().Value(MockControllerKey{}).(cellController); ok {
				ctrl = mockCtrl
			} else {
				realCtrl, err := shared.ControllerFromCmd(cmd)
				if err != nil {
					return err
				}
				ctrl = realCtrl
			}

			cellDoc := &v1beta1.CellDoc{
				APIVersion: v1beta1.APIVersionV1Beta1,
				Kind:       v1beta1.KindCell,
				Metadata: v1beta1.CellMetadata{
					Name: name,
				},
				Spec: v1beta1.CellSpec{
					RealmID: realm,
					SpaceID: space,
					StackID: stack,
				},
			}

			result, err := ctrl.KillCell(cellDoc)
			if err != nil {
				return err
			}

			targetDoc := result.CellDoc
			stackName := stack
			cellName := name
			if targetDoc != nil {
				if metaName := strings.TrimSpace(targetDoc.Metadata.Name); metaName != "" {
					cellName = metaName
				}
				if specStack := strings.TrimSpace(targetDoc.Spec.StackID); specStack != "" {
					stackName = specStack
				}
			}

			cmd.Printf("Killed cell %q from stack %q\n", cellName, stackName)
			return nil
		},
	}

	cmd.Flags().String("realm", "", "Realm that owns the cell")
	_ = viper.BindPFlag(config.KUKE_KILL_CELL_REALM.ViperKey, cmd.Flags().Lookup("realm"))

	cmd.Flags().String("space", "", "Space that owns the cell")
	_ = viper.BindPFlag(config.KUKE_KILL_CELL_SPACE.ViperKey, cmd.Flags().Lookup("space"))

	cmd.Flags().String("stack", "", "Stack that owns the cell")
	_ = viper.BindPFlag(config.KUKE_KILL_CELL_STACK.ViperKey, cmd.Flags().Lookup("stack"))

	cmd.ValidArgsFunction = config.CompleteCellNames
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)

	return cmd
}
