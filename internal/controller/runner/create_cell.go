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

	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

func (r *Exec) CreateCell(cell intmodel.Cell) (intmodel.Cell, error) {
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}

	// Get existing cell (returns internal model)
	existingCell, err := r.GetCell(cell)
	if err != nil && !errors.Is(err, errdefs.ErrCellNotFound) {
		return intmodel.Cell{}, fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
	}

	// Cell found, merge containers and ensure resources exist
	if !errors.Is(err, errdefs.ErrCellNotFound) {
		// Merge containers from the new cell into the existing cell
		// This ensures containers specified in the new cell are created even if
		// they weren't in the stored cell document
		if len(cell.Spec.Containers) > 0 {
			// Log containers being merged
			r.logger.DebugContext(
				r.ctx,
				"merging containers into existing cell",
				"cell", existingCell.Metadata.Name,
				"existingContainerCount", len(existingCell.Spec.Containers),
				"newContainerCount", len(cell.Spec.Containers),
			)

			// Create a map of existing container IDs to avoid duplicates
			existingContainerIDs := make(map[string]bool)
			for _, container := range existingCell.Spec.Containers {
				existingContainerIDs[container.ID] = true
				r.logger.DebugContext(
					r.ctx,
					"existing container in cell",
					"cell", existingCell.Metadata.Name,
					"containerID", container.ID,
				)
			}
			// Add containers from the new cell that don't already exist
			for _, container := range cell.Spec.Containers {
				r.logger.DebugContext(
					r.ctx,
					"checking if container should be merged",
					"cell", existingCell.Metadata.Name,
					"containerID", container.ID,
					"alreadyExists", existingContainerIDs[container.ID],
				)
				if !existingContainerIDs[container.ID] {
					existingCell.Spec.Containers = append(existingCell.Spec.Containers, container)
					r.logger.DebugContext(
						r.ctx,
						"merged new container into cell",
						"cell", existingCell.Metadata.Name,
						"containerID", container.ID,
						"totalContainers", len(existingCell.Spec.Containers),
					)
				}
			}
		}

		ensuredCell, ensureErr := r.EnsureCell(existingCell)
		if ensureErr != nil {
			return intmodel.Cell{}, ensureErr
		}

		return ensuredCell, nil
	}

	// Cell not found, create new cell
	resultCell, err := r.provisionNewCell(cell)
	if err != nil {
		return intmodel.Cell{}, err
	}

	return resultCell, nil
}

// EnsureCell ensures that all required resources for a cell exist.
// It ensures the cgroup exists, ensures cell containers exist, and updates metadata.
func (r *Exec) EnsureCell(cell intmodel.Cell) (intmodel.Cell, error) {
	// Ensure cgroup exists
	ensuredCell, ensureErr := r.ensureCellCgroup(cell)
	if ensureErr != nil {
		return intmodel.Cell{}, ensureErr
	}

	// Log final container count before ensuring containers
	r.logger.DebugContext(
		r.ctx,
		"calling ensureCellContainers",
		"cell", ensuredCell.Metadata.Name,
		"containerCount", len(ensuredCell.Spec.Containers),
	)

	_, ensureErr = r.ensureCellContainers(ensuredCell)
	if ensureErr != nil {
		return intmodel.Cell{}, ensureErr
	}

	// Update metadata to persist the containers
	if err := r.UpdateCellMetadata(ensuredCell); err != nil {
		return intmodel.Cell{}, fmt.Errorf("%w: %w", errdefs.ErrUpdateCellMetadata, err)
	}

	return ensuredCell, nil
}
