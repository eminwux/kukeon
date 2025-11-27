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
	"github.com/eminwux/kukeon/cmd/kuke/purge/shared"
	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type cellController interface {
	PurgeCell(cell intmodel.Cell, force, cascade bool) (controller.PurgeCellResult, error)
}

// MockControllerKey is used to inject mock controllers in tests via context.
type MockControllerKey struct{}

func NewCellCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "cell [name]",
		Aliases:       []string{"ce"},
		Short:         "Purge a cell with comprehensive cleanup",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Check for mock controller in context (for testing)
			var ctrl cellController
			if mockCtrl, ok := cmd.Context().Value(MockControllerKey{}).(cellController); ok {
				ctrl = mockCtrl
			} else {
				realCtrl, err := shared.ControllerFromCmd(cmd)
				if err != nil {
					return err
				}
				ctrl = &controllerWrapper{ctrl: realCtrl}
			}

			name := strings.TrimSpace(args[0])
			realm := strings.TrimSpace(viper.GetString(config.KUKE_PURGE_CELL_REALM.ViperKey))
			space := strings.TrimSpace(viper.GetString(config.KUKE_PURGE_CELL_SPACE.ViperKey))
			stack := strings.TrimSpace(viper.GetString(config.KUKE_PURGE_CELL_STACK.ViperKey))

			if realm == "" {
				return fmt.Errorf("%w (--realm)", errdefs.ErrRealmNameRequired)
			}
			if space == "" {
				return fmt.Errorf("%w (--space)", errdefs.ErrSpaceNameRequired)
			}
			if stack == "" {
				return fmt.Errorf("%w (--stack)", errdefs.ErrStackNameRequired)
			}

			force := shared.ParseForceFlag(cmd)
			cascade := shared.ParseCascadeFlag(cmd) // Ignored for cells, but parsed for consistency

			doc := &v1beta1.CellDoc{
				Metadata: v1beta1.CellMetadata{
					Name: name,
				},
				Spec: v1beta1.CellSpec{
					RealmID: realm,
					SpaceID: space,
					StackID: stack,
				},
			}

			// Convert at boundary before calling controller
			cellInternal, _, err := apischeme.NormalizeCell(*doc)
			if err != nil {
				return fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
			}

			result, err := ctrl.PurgeCell(cellInternal, force, cascade)
			if err != nil {
				return err
			}

			// Use cell from result for output
			cellName := result.Cell.Metadata.Name
			stackName := result.Cell.Spec.StackName
			if cellName == "" {
				cellName = name
			}
			if stackName == "" {
				stackName = stack
			}

			cmd.Printf("Purged cell %q from stack %q\n", cellName, stackName)
			if len(result.Purged) > 0 {
				cmd.Printf("Additional resources purged: %v\n", result.Purged)
			}
			return nil
		},
	}

	cmd.Flags().String("realm", "", "Realm that owns the cell")
	_ = viper.BindPFlag(config.KUKE_PURGE_CELL_REALM.ViperKey, cmd.Flags().Lookup("realm"))

	cmd.Flags().String("space", "", "Space that owns the cell")
	_ = viper.BindPFlag(config.KUKE_PURGE_CELL_SPACE.ViperKey, cmd.Flags().Lookup("space"))

	cmd.Flags().String("stack", "", "Stack that owns the cell")
	_ = viper.BindPFlag(config.KUKE_PURGE_CELL_STACK.ViperKey, cmd.Flags().Lookup("stack"))

	// Register autocomplete for positional argument
	cmd.ValidArgsFunction = config.CompleteCellNames

	// Register autocomplete functions for flags
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)

	return cmd
}

type controllerWrapper struct {
	ctrl *controller.Exec
}

func (w *controllerWrapper) PurgeCell(cell intmodel.Cell, force, cascade bool) (controller.PurgeCellResult, error) {
	return w.ctrl.PurgeCell(cell, force, cascade)
}
