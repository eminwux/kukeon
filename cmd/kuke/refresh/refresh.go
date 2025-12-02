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

package refresh

import (
	"strings"

	"github.com/eminwux/kukeon/cmd/kuke/get/shared"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/spf13/cobra"
)

// RefreshResult is an alias for controller.RefreshResult.
type RefreshResult = controller.RefreshResult

// RefreshController defines the interface for refresh operations.
type RefreshController interface {
	RefreshAll() (RefreshResult, error)
}

// MockControllerKey is used to inject mock controllers in tests via context.
type MockControllerKey struct{}

type controllerWrapper struct {
	ctrl *controller.Exec
}

func (w *controllerWrapper) RefreshAll() (RefreshResult, error) {
	return w.ctrl.RefreshAll()
}

func NewRefreshCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "refresh",
		Short:        "Refresh metadata status by introspecting containerd and CNI for all entities",
		Long:         "Refresh metadata status by introspecting containerd and CNI for all entities. Updates only .status fields to reflect current runtime state without modifying .spec or runtime state.",
		SilenceUsage: true,
		RunE:         runRefreshCmd,
	}

	return cmd
}

func runRefreshCmd(cmd *cobra.Command, _ []string) error {
	var ctrl RefreshController
	if mockCtrl, ok := cmd.Context().Value(MockControllerKey{}).(RefreshController); ok {
		ctrl = mockCtrl
	} else {
		realCtrl, err := shared.ControllerFromCmd(cmd)
		if err != nil {
			return err
		}
		ctrl = &controllerWrapper{ctrl: realCtrl}
	}

	result, err := ctrl.RefreshAll()
	if err != nil {
		return err
	}

	// Print summary
	cmd.Printf("Refreshed metadata status:\n")
	printEntityList(cmd, "  Realms", result.RealmsFound)
	printEntityList(cmd, "  Spaces", result.SpacesFound)
	printEntityList(cmd, "  Stacks", result.StacksFound)
	printEntityList(cmd, "  Cells", result.CellsFound)
	printEntityList(cmd, "  Containers", result.ContainersFound)
	cmd.Printf("\n")

	cmd.Printf("Updated:\n")
	hasUpdates := false
	if len(result.RealmsUpdated) > 0 {
		printEntityList(cmd, "  Realms", result.RealmsUpdated)
		hasUpdates = true
	}
	if len(result.SpacesUpdated) > 0 {
		printEntityList(cmd, "  Spaces", result.SpacesUpdated)
		hasUpdates = true
	}
	if len(result.StacksUpdated) > 0 {
		printEntityList(cmd, "  Stacks", result.StacksUpdated)
		hasUpdates = true
	}
	if len(result.CellsUpdated) > 0 {
		printEntityList(cmd, "  Cells", result.CellsUpdated)
		hasUpdates = true
	}
	if len(result.ContainersUpdated) > 0 {
		printEntityList(cmd, "  Containers", result.ContainersUpdated)
		hasUpdates = true
	}
	if !hasUpdates {
		cmd.Printf("  (none)\n")
	}
	cmd.Printf("\n")

	cmd.Printf("Errors:\n")
	if len(result.Errors) > 0 {
		for _, errMsg := range result.Errors {
			cmd.Printf("  %s\n", errMsg)
		}
	} else {
		cmd.Printf("  (none)\n")
	}

	return nil
}

// printEntityList prints a list of entities with count and names.
// Format: "Label: count (name1, name2, ...)" or "Label: count" if empty.
func printEntityList(cmd *cobra.Command, label string, entities []string) {
	count := len(entities)
	if count == 0 {
		cmd.Printf("%s: 0\n", label)
		return
	}

	const maxEntitiesToShow = 5
	if count <= maxEntitiesToShow {
		// For small lists, show all names
		cmd.Printf("%s: %d (%s)\n", label, count, strings.Join(entities, ", "))
	} else {
		// For large lists, show first few and count
		cmd.Printf("%s: %d (%s, ...)\n", label, count, strings.Join(entities[:maxEntitiesToShow], ", "))
	}
}
