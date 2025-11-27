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
	"github.com/eminwux/kukeon/cmd/kuke/create/shared"
	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type cellController interface {
	CreateCell(cell intmodel.Cell) (controller.CreateCellResult, error)
}

// MockControllerKey is used to inject mock controllers in tests via context.
type MockControllerKey struct{}

func NewCellCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "cell [name]",
		Aliases:       []string{"ce"},
		Short:         "Create or reconcile a cell within a stack",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runCreateCell,
	}

	cmd.Flags().String("realm", "", "Realm that owns the cell")
	_ = viper.BindPFlag(config.KUKE_CREATE_CELL_REALM.ViperKey, cmd.Flags().Lookup("realm"))

	cmd.Flags().String("space", "", "Space that owns the cell")
	_ = viper.BindPFlag(config.KUKE_CREATE_CELL_SPACE.ViperKey, cmd.Flags().Lookup("space"))

	cmd.Flags().String("stack", "", "Stack that owns the cell")
	_ = viper.BindPFlag(config.KUKE_CREATE_CELL_STACK.ViperKey, cmd.Flags().Lookup("stack"))

	// Register autocomplete functions for flags
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)

	return cmd
}

func runCreateCell(cmd *cobra.Command, args []string) error {
	name, err := shared.RequireNameArgOrDefault(
		cmd,
		args,
		"cell",
		viper.GetString(config.KUKE_CREATE_CELL_NAME.ViperKey),
	)
	if err != nil {
		return err
	}

	realm := strings.TrimSpace(viper.GetString(config.KUKE_CREATE_CELL_REALM.ViperKey))
	if realm == "" {
		return fmt.Errorf("%w (--realm)", errdefs.ErrRealmNameRequired)
	}

	space := strings.TrimSpace(viper.GetString(config.KUKE_CREATE_CELL_SPACE.ViperKey))
	if space == "" {
		return fmt.Errorf("%w (--space)", errdefs.ErrSpaceNameRequired)
	}

	stack := strings.TrimSpace(viper.GetString(config.KUKE_CREATE_CELL_STACK.ViperKey))
	if stack == "" {
		return fmt.Errorf("%w (--stack)", errdefs.ErrStackNameRequired)
	}

	// Build v1beta1.CellDoc from command arguments
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

	// Ensure all nested structs are initialized
	doc = v1beta1.NewCellDoc(doc)

	// Convert at boundary before calling controller
	cell, version, err := apischeme.NormalizeCell(*doc)
	if err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	// Check for mock controller in context (for testing)
	var ctrl cellController
	if mockCtrl, ok := cmd.Context().Value(MockControllerKey{}).(cellController); ok {
		ctrl = mockCtrl
	} else {
		realCtrl, ctrlErr := shared.ControllerFromCmd(cmd)
		if ctrlErr != nil {
			return ctrlErr
		}
		ctrl = realCtrl
	}

	// Call controller with internal type
	result, err := ctrl.CreateCell(cell)
	if err != nil {
		return err
	}

	printCellResult(cmd, result, version)
	return nil
}

func printCellResult(cmd *cobra.Command, result controller.CreateCellResult, version v1beta1.Version) {
	// Convert result back to external for output
	resultDoc, err := apischeme.BuildCellExternalFromInternal(result.Cell, version)
	if err != nil {
		// Fallback to internal type if conversion fails
		cmd.Printf(
			"Cell %q (realm %q, space %q, stack %q)\n",
			result.Cell.Metadata.Name,
			result.Cell.Spec.RealmName,
			result.Cell.Spec.SpaceName,
			result.Cell.Spec.StackName,
		)
		cmd.Printf("Warning: failed to convert result for output: %v\n", err)
	} else {
		cmd.Printf(
			"Cell %q (realm %q, space %q, stack %q)\n",
			resultDoc.Metadata.Name,
			resultDoc.Spec.RealmID,
			resultDoc.Spec.SpaceID,
			resultDoc.Spec.StackID,
		)
	}
	shared.PrintCreationOutcome(cmd, "metadata", result.MetadataExistsPost, result.Created)
	shared.PrintCreationOutcome(cmd, "cgroup", result.CgroupExistsPost, result.CgroupCreated)
	shared.PrintCreationOutcome(cmd, "root container", result.RootContainerExistsPost, result.RootContainerCreated)

	if len(result.Containers) == 0 {
		cmd.Println("  - containers: none defined")
	} else {
		for _, container := range result.Containers {
			label := fmt.Sprintf("container %q", container.Name)
			shared.PrintCreationOutcome(cmd, label, container.ExistsPost, container.Created)
		}
	}

	if result.Started {
		cmd.Println("  - containers: started")
	} else {
		cmd.Println("  - containers: not started")
	}
}

// PrintCellResult is exported for testing purposes.
func PrintCellResult(cmd *cobra.Command, result controller.CreateCellResult, version v1beta1.Version) {
	printCellResult(cmd, result, version)
}
