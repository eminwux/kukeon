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
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/diskpressure"
	"github.com/eminwux/kukeon/internal/util/fs"
	"github.com/eminwux/kukeon/internal/util/naming"
)

func (r *Exec) CreateCell(cell intmodel.Cell) (intmodel.Cell, error) {
	defer r.lockCell(cell)()

	if err := r.ensureClientConnected(); err != nil {
		return intmodel.Cell{}, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}

	trimmedName := strings.TrimSpace(cell.Metadata.Name)
	if err := naming.ValidateHierarchyName("cell", trimmedName); err != nil {
		return intmodel.Cell{}, err
	}
	cell.Metadata.Name = trimmedName

	// Validate any container IDs embedded in the cell spec, mirroring the
	// controller-side check so a malformed container ID is rejected here too.
	for i := range cell.Spec.Containers {
		id := strings.TrimSpace(cell.Spec.Containers[i].ID)
		if err := naming.ValidateHierarchyName("container", id); err != nil {
			return intmodel.Cell{}, err
		}
		cell.Spec.Containers[i].ID = id
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

		// Populate container statuses after ensuring cell and persist them
		if err = r.PopulateAndPersistCellContainerStatuses(&ensuredCell); err != nil {
			r.logger.WarnContext(r.ctx, "failed to populate container statuses",
				"cell", ensuredCell.Metadata.Name,
				"error", err)
			// If UpdateCellMetadata failed, return error; otherwise continue (populate is best-effort)
			if errors.Is(err, errdefs.ErrWriteMetadata) || errors.Is(err, errdefs.ErrConversionFailed) {
				return intmodel.Cell{}, fmt.Errorf("%w: %w", errdefs.ErrUpdateCellMetadata, err)
			}
			// Continue anyway - status population is best-effort
		}

		return ensuredCell, nil
	}

	// Cell not found — this is a genuinely new cell. Guard against creating it
	// when the realm's data volume is already under hard disk pressure, so a
	// fresh snapshot does not push the volume to 100% (issue #1035). The guard
	// is observation-only: it never deletes anything, and `--ignore-disk-pressure`
	// (cell.Spec.IgnoreDiskPressure) bypasses it. The existing-cell branch above
	// (EnsureCell) is deliberately not guarded — ensuring resources for a cell
	// that already exists is not "digging the hole deeper".
	if err = r.guardDiskPressure(cell); err != nil {
		return intmodel.Cell{}, err
	}

	// Cell not found, create new cell
	resultCell, err := r.provisionNewCell(cell)
	if err != nil {
		return intmodel.Cell{}, err
	}

	// Populate container statuses after creating cell and persist them
	if err = r.PopulateAndPersistCellContainerStatuses(&resultCell); err != nil {
		r.logger.WarnContext(r.ctx, "failed to populate container statuses",
			"cell", resultCell.Metadata.Name,
			"error", err)
		// If UpdateCellMetadata failed, return error; otherwise continue (populate is best-effort)
		if errors.Is(err, errdefs.ErrWriteMetadata) || errors.Is(err, errdefs.ErrConversionFailed) {
			return intmodel.Cell{}, fmt.Errorf("%w: %w", errdefs.ErrUpdateCellMetadata, err)
		}
		// Continue anyway - status population is best-effort
	}

	return resultCell, nil
}

// guardDiskPressure fails fast with errdefs.ErrDiskPressure when the data
// volume backing the cell's realm is at or above the configured block
// threshold, unless the threshold is disabled (<= 0) or the cell carries the
// IgnoreDiskPressure override (`--ignore-disk-pressure`). It never deletes
// anything. A statfs failure is logged and treated as "allow" — a monitoring
// hiccup must never wedge all cell creation. Issue #1035.
func (r *Exec) guardDiskPressure(cell intmodel.Cell) error {
	if r.opts.DiskPressureBlockPercent <= 0 {
		return nil
	}
	if cell.Spec.IgnoreDiskPressure {
		r.logger.DebugContext(r.ctx, "disk-pressure guard bypassed via --ignore-disk-pressure",
			"cell", cell.Metadata.Name, "realm", cell.Spec.RealmName)
		return nil
	}

	sample := r.diskSampler
	if sample == nil {
		sample = diskpressure.Sample
	}
	dir := fs.RealmMetadataDir(r.opts.RunPath, cell.Spec.RealmName)
	usage, err := sample(dir)
	if err != nil {
		r.logger.WarnContext(r.ctx, "disk-pressure guard: statfs failed; allowing creation",
			"cell", cell.Metadata.Name, "realm", cell.Spec.RealmName, "path", dir, "error", err)
		return nil
	}

	if usage.UsedPercent >= float64(r.opts.DiskPressureBlockPercent) {
		return fmt.Errorf(
			"%w: data volume %s is at %.1f%% (block threshold %d%%); "+
				"free space or pass --ignore-disk-pressure",
			errdefs.ErrDiskPressure, dir, usage.UsedPercent, r.opts.DiskPressureBlockPercent)
	}
	return nil
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

	_, ensureErr = r.ensureCellContainers(&ensuredCell)
	if ensureErr != nil {
		return intmodel.Cell{}, ensureErr
	}

	// Backfill the bridge name on cells that pre-date the field. Same
	// best-effort posture as in provisionNewCell: if we cannot derive it,
	// leave the field empty rather than fail the ensure.
	if ensuredCell.Status.Network.BridgeName == "" {
		if bridge, brErr := cellSpaceBridgeName(ensuredCell); brErr == nil {
			ensuredCell.Status.Network.BridgeName = bridge
		} else {
			r.logger.WarnContext(r.ctx, "could not derive cell bridge name on ensure",
				"cell", ensuredCell.Metadata.Name, "error", brErr)
		}
	}

	// Update metadata to persist the containers
	if err := r.UpdateCellMetadata(ensuredCell); err != nil {
		return intmodel.Cell{}, fmt.Errorf("%w: %w", errdefs.ErrUpdateCellMetadata, err)
	}
	// The write above may have bumped the generation (a merged container is
	// a spec change). Sync the in-memory token so the caller's follow-up
	// status persist (PopulateAndPersistCellContainerStatuses) is not
	// rejected as stale against its own write.
	r.refreshCellGeneration(&ensuredCell)

	return ensuredCell, nil
}
