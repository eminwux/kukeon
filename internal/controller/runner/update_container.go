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

package runner

import (
	"fmt"

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// UpdateContainer updates an existing container within a cell.
// If the container spec has breaking changes (image, command, args), it will
// stop, delete, and recreate the container. Otherwise, it updates the container spec in metadata.
func (r *Exec) UpdateContainer(cell intmodel.Cell, desiredContainer intmodel.ContainerSpec) (intmodel.Cell, error) {
	// Get existing cell
	existing, err := r.GetCell(cell)
	if err != nil {
		return intmodel.Cell{}, fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
	}

	// Find the container in the cell
	var actualContainer *intmodel.ContainerSpec
	containerIndex := -1
	for i := range existing.Spec.Containers {
		if existing.Spec.Containers[i].ID == desiredContainer.ID {
			actualContainer = &existing.Spec.Containers[i]
			containerIndex = i
			break
		}
	}

	if actualContainer == nil {
		// Container doesn't exist, create it
		return r.CreateContainer(cell, desiredContainer)
	}

	// Check if container spec has breaking changes
	if containerSpecChanged(&desiredContainer, actualContainer) {
		// Breaking change: stop, delete, and recreate
		if stopErr := r.StopContainer(existing, desiredContainer.ID); stopErr != nil {
			r.logger.WarnContext(
				r.ctx,
				"failed to stop container for update, continuing",
				"cell", existing.Metadata.Name,
				"container", desiredContainer.ID,
				"error", stopErr,
			)
		}

		if deleteErr := r.DeleteContainer(existing, desiredContainer.ID); deleteErr != nil {
			r.logger.WarnContext(
				r.ctx,
				"failed to delete container for update, continuing",
				"cell", existing.Metadata.Name,
				"container", desiredContainer.ID,
				"error", deleteErr,
			)
		}

		// Clear containerd ID so container will be recreated
		desiredContainer.ContainerdID = ""
	}

	// Ensure container has proper parent references
	if desiredContainer.RealmName == "" {
		desiredContainer.RealmName = existing.Spec.RealmName
	}
	if desiredContainer.SpaceName == "" {
		desiredContainer.SpaceName = existing.Spec.SpaceName
	}
	if desiredContainer.StackName == "" {
		desiredContainer.StackName = existing.Spec.StackName
	}
	if desiredContainer.CellName == "" {
		desiredContainer.CellName = existing.Spec.ID
		if desiredContainer.CellName == "" {
			desiredContainer.CellName = existing.Metadata.Name
		}
	}

	// Update container in cell's containers list
	if containerIndex >= 0 {
		// Preserve containerd ID if not cleared
		if desiredContainer.ContainerdID == "" && actualContainer.ContainerdID != "" {
			desiredContainer.ContainerdID = actualContainer.ContainerdID
		}
		existing.Spec.Containers[containerIndex] = desiredContainer
	} else {
		// Add new container
		existing.Spec.Containers = append(existing.Spec.Containers, desiredContainer)
	}

	// Ensure container exists (will create if needed)
	if connectErr := r.ensureClientConnected(); connectErr != nil {
		return intmodel.Cell{}, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, connectErr)
	}

	_, ensureErr := r.ensureCellContainers(&existing)
	if ensureErr != nil {
		return intmodel.Cell{}, fmt.Errorf("failed to ensure container: %w", ensureErr)
	}

	// Update metadata
	if updateErr := r.UpdateCellMetadata(existing); updateErr != nil {
		return intmodel.Cell{}, fmt.Errorf("%w: %w", errdefs.ErrUpdateCellMetadata, updateErr)
	}

	return existing, nil
}
