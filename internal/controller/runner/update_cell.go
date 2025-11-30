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

// UpdateCell updates an existing cell with new metadata and container changes.
// It handles:
// - Metadata updates (labels)
// - Container additions (containers in desired but not in actual)
// - Container updates (containers in both, with spec changes)
// - Container removals (orphans: containers in actual but not in desired)
//
// Breaking changes (root container spec changes, parent associations) should be rejected before calling this method.
func (r *Exec) UpdateCell(desired intmodel.Cell) (intmodel.Cell, error) {
	// Get existing cell
	existing, err := r.GetCell(desired)
	if err != nil {
		return intmodel.Cell{}, fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
	}

	// Update compatible metadata fields
	existing.Metadata.Labels = desired.Metadata.Labels

	// Build maps of desired and actual containers by ID
	desiredContainers := make(map[string]*intmodel.ContainerSpec)
	actualContainers := make(map[string]*intmodel.ContainerSpec)

	for i := range desired.Spec.Containers {
		container := &desired.Spec.Containers[i]
		if container.ID != "" {
			desiredContainers[container.ID] = container
		}
	}

	for i := range existing.Spec.Containers {
		container := &existing.Spec.Containers[i]
		if container.ID != "" {
			actualContainers[container.ID] = container
		}
	}

	// Handle orphan containers (in actual but not in desired)
	for id := range actualContainers {
		if _, exists := desiredContainers[id]; !exists {
			// Container should be removed
			if stopErr := r.StopContainer(existing, id); stopErr != nil {
				r.logger.WarnContext(
					r.ctx,
					"failed to stop container for removal, continuing",
					"cell", existing.Metadata.Name,
					"container", id,
					"error", stopErr,
				)
			}
			if deleteErr := r.DeleteContainer(existing, id); deleteErr != nil {
				r.logger.WarnContext(
					r.ctx,
					"failed to delete container for removal, continuing",
					"cell", existing.Metadata.Name,
					"container", id,
					"error", deleteErr,
				)
			}
		}
	}

	// Update existing cell's containers list to match desired
	// Preserve root container and existing containerd IDs where possible
	newContainers := make([]intmodel.ContainerSpec, 0, len(desired.Spec.Containers))
	for _, desiredContainer := range desired.Spec.Containers {
		if actualContainer, exists := actualContainers[desiredContainer.ID]; exists {
			// Container exists, preserve containerd ID but update spec
			desiredContainer.ContainerdID = actualContainer.ContainerdID
			// Check if container spec changed (image, command, args) - if so, recreate
			if containerSpecChanged(&desiredContainer, actualContainer) {
				// Stop and delete old container
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
				// Clear containerd ID so it will be recreated
				desiredContainer.ContainerdID = ""
			}
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
		newContainers = append(newContainers, desiredContainer)
	}

	// Update cell with new containers list
	existing.Spec.Containers = newContainers
	existing.Spec.RootContainerID = desired.Spec.RootContainerID

	// Ensure all containers exist (will create missing ones, update existing ones)
	if connectErr := r.ensureClientConnected(); connectErr != nil {
		return intmodel.Cell{}, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, connectErr)
	}

	_, ensureErr := r.ensureCellContainers(&existing)
	if ensureErr != nil {
		return intmodel.Cell{}, fmt.Errorf("failed to ensure cell containers: %w", ensureErr)
	}

	// Update metadata
	if updateErr := r.UpdateCellMetadata(existing); updateErr != nil {
		return intmodel.Cell{}, fmt.Errorf("%w: %w", errdefs.ErrUpdateCellMetadata, updateErr)
	}

	return existing, nil
}

// containerSpecChanged checks if a container spec has breaking changes (image, command, args).
func containerSpecChanged(desired, actual *intmodel.ContainerSpec) bool {
	return desired.Image != actual.Image ||
		desired.Command != actual.Command ||
		!stringSlicesEqual(desired.Args, actual.Args)
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return true // Different length means changed
	}
	for i := range a {
		if a[i] != b[i] {
			return true // Different content means changed
		}
	}
	return false // Same content means unchanged
}
