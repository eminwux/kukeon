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
	"errors"
	"fmt"

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// CreateContainer creates a container in an existing cell by merging the container spec
// into the cell's containers list. The cell must already exist.
func (r *Exec) CreateContainer(cell intmodel.Cell, container intmodel.ContainerSpec) (intmodel.Cell, error) {
	// Get existing cell (returns internal model)
	existingCell, err := r.GetCell(cell)
	if err != nil {
		if errors.Is(err, errdefs.ErrCellNotFound) {
			return intmodel.Cell{}, fmt.Errorf("cell %q not found: %w", cell.Metadata.Name, errdefs.ErrCellNotFound)
		}
		return intmodel.Cell{}, fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
	}

	// Cell found, ensure container is merged
	ensuredCell, ensureErr := r.EnsureContainer(existingCell, container)
	if ensureErr != nil {
		return intmodel.Cell{}, ensureErr
	}

	// Populate container statuses after creating container and persist them
	if err = r.PopulateAndPersistCellContainerStatuses(&ensuredCell); err != nil {
		r.logger.WarnContext(r.ctx, "failed to populate and persist container statuses",
			"cell", ensuredCell.Metadata.Name,
			"error", err)
		// Continue anyway - status population is best-effort
	}

	return ensuredCell, nil
}

// EnsureContainer ensures that a container spec is merged into an existing cell.
// It merges the container into the cell's Spec.Containers list (avoiding duplicates by ID),
// ensures containers exist, and updates metadata.
func (r *Exec) EnsureContainer(cell intmodel.Cell, container intmodel.ContainerSpec) (intmodel.Cell, error) {
	// Check if container already exists in the cell
	existingContainerIDs := make(map[string]bool)
	for _, existingContainer := range cell.Spec.Containers {
		existingContainerIDs[existingContainer.ID] = true
	}

	// Merge container if it doesn't already exist
	if !existingContainerIDs[container.ID] {
		r.logger.DebugContext(
			r.ctx,
			"merging container into cell",
			"cell", cell.Metadata.Name,
			"containerID", container.ID,
			"existingContainerCount", len(cell.Spec.Containers),
		)

		cell.Spec.Containers = append(cell.Spec.Containers, container)

		r.logger.DebugContext(
			r.ctx,
			"merged container into cell",
			"cell", cell.Metadata.Name,
			"containerID", container.ID,
			"totalContainers", len(cell.Spec.Containers),
		)
	} else {
		r.logger.DebugContext(
			r.ctx,
			"container already exists in cell, skipping merge",
			"cell", cell.Metadata.Name,
			"containerID", container.ID,
		)
	}

	// Log final container count before ensuring containers
	r.logger.DebugContext(
		r.ctx,
		"calling ensureCellContainers",
		"cell", cell.Metadata.Name,
		"containerCount", len(cell.Spec.Containers),
	)

	// Ensure containers exist
	_, ensureErr := r.ensureCellContainers(&cell)
	if ensureErr != nil {
		return intmodel.Cell{}, ensureErr
	}

	// Update metadata to persist the merged container
	if err := r.UpdateCellMetadata(cell); err != nil {
		return intmodel.Cell{}, fmt.Errorf("%w: %w", errdefs.ErrUpdateCellMetadata, err)
	}

	return cell, nil
}
