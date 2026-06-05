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

package controller

import (
	"fmt"

	"github.com/eminwux/kukeon/internal/controller/apply"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// StartCellResult reports the outcome of starting a cell.
type StartCellResult struct {
	Cell    intmodel.Cell
	Started bool
}

// StartCell starts all containers in a cell and updates the cell metadata state.
//
// For a Config-lineage cell whose persisted Status.OutOfSync is true, StartCell
// first re-materialises the cell's spec from its lineage Config + Blueprint and
// rebuilds the containerd records, so `kuke stop` followed by `kuke start`
// produces the same end state as `kuke restart` on an OutOfSync
// cell — issue #983. The reapply is daemon-side so every client that issues
// StartCell (CLI, future API consumers) gets the reconcile-on-start behaviour.
func (b *Exec) StartCell(cell intmodel.Cell) (StartCellResult, error) {
	var res StartCellResult

	internalCell, err := b.validateAndGetCell(cell)
	if err != nil {
		return res, err
	}

	// Carry `kuke run --env` runtime entries from the inbound RPC cell onto
	// the disk-read internalCell so the runner's OCI build path sees them
	// (issue #834). v1beta1.CellSpec.RuntimeEnv is yaml:"-", so the disk
	// read above strips the field — without this copy the --reuse +
	// --env cron driver use case from #840 silently drops the per-tick
	// env. Empty input is a no-op (the bare `kuke run <cfg>` path through
	// existing-Stopped → StartCell never sets RuntimeEnv).
	internalCell.Spec.RuntimeEnv = cell.Spec.RuntimeEnv

	// Check if containers are actually running by examining Status.Containers
	// which is freshly populated from containerd by validateAndGetCell -> GetCell -> populateCellContainerStatuses
	// This prevents blocking starts when containers have crashed externally but metadata still shows Ready state
	if len(internalCell.Status.Containers) > 0 {
		// Check if any container is actually running
		hasRunningContainer := false
		for _, containerStatus := range internalCell.Status.Containers {
			if containerStatus.State == intmodel.ContainerStateReady {
				hasRunningContainer = true
				break
			}
		}
		if hasRunningContainer {
			return res, fmt.Errorf(
				"cell %q has running containers and must first be stopped",
				internalCell.Metadata.Name,
			)
		}
		// Containers are stopped (or unknown), allow start even if metadata says Ready
	} else if internalCell.Status.State == intmodel.CellStateReady {
		// No container statuses available, fall back to metadata state check
		// This preserves existing behavior for cells without containers or when status population fails
		return res, fmt.Errorf(
			"cell %q is already in Ready state and must first be stopped",
			internalCell.Metadata.Name,
		)
	}

	// Reapply the lineage Config when the persisted OutOfSync flag is set, so
	// the start runs against the freshly materialised spec — see #983. If the
	// reapply already brought the cell back to Ready (RecreateCell path), the
	// caller is done; otherwise fall through to the regular runner.StartCell.
	if reapplied, started, ok := b.reapplyOutOfSyncFromConfig(internalCell); ok {
		internalCell = reapplied
		if started {
			res.Cell = internalCell
			res.Started = true
			return res, nil
		}
	}

	// Start all containers in the cell
	internalCell, err = b.runner.StartCell(internalCell)
	if err != nil {
		return res, fmt.Errorf("failed to start cell containers: %w", err)
	}

	// Update cell metadata state to Ready
	if err = b.runner.UpdateCellMetadata(internalCell); err != nil {
		return res, fmt.Errorf("failed to update cell metadata: %w", err)
	}

	res.Cell = internalCell
	res.Started = true
	return res, nil
}

// reapplyOutOfSyncFromConfig re-materialises the cell from its lineage Config
// and rebuilds the on-disk + containerd state when the persisted OutOfSync
// flag is set on a Config-lineage cell. Returns:
//
//   - (cell, true,  true)  : reapply rebuilt the cell; runner.RecreateCell
//     already brought it to Ready, the caller should
//     skip its runner.StartCell pass.
//   - (cell, false, true)  : reapply ran, but the materialised spec matched
//     the on-disk spec or only metadata diverged — the
//     caller continues with the regular start pass.
//   - (zero, false, false) : reapply did not run (no lineage label, OutOfSync
//     flag clear, OutOfSyncError set, lineage Config /
//     Blueprint missing, materialise error). Caller
//     continues with the on-disk spec — the runtime is
//     still bounced as the operator asked, matching the
//     CLI restart fall-through (cmd/kuke/restart).
func (b *Exec) reapplyOutOfSyncFromConfig(cell intmodel.Cell) (intmodel.Cell, bool, bool) {
	if !cell.Status.OutOfSync || cell.Status.OutOfSyncError != "" {
		return intmodel.Cell{}, false, false
	}

	configName, hasLineage := configLineage(cell)
	if !hasLineage {
		return intmodel.Cell{}, false, false
	}

	cfg, found, err := lookupLineageConfig(b.runner, cell, configName)
	if err != nil {
		b.logger.WarnContext(b.ctx,
			"OutOfSync reapply lookup failed; starting cell with on-disk spec",
			"cell", cell.Metadata.Name,
			"config", configName,
			"error", err,
		)
		return intmodel.Cell{}, false, false
	}
	if !found {
		// Lineage Config deleted — nothing to materialise from.
		b.logger.InfoContext(b.ctx,
			"OutOfSync reapply skipped: lineage config deleted",
			"cell", cell.Metadata.Name,
			"config", configName,
		)
		return intmodel.Cell{}, false, false
	}

	desired, err := materializeCellFromConfig(b.runner, cfg, cell.Metadata.Name)
	if err != nil {
		b.logger.WarnContext(b.ctx,
			"OutOfSync reapply materialise failed; starting cell with on-disk spec",
			"cell", cell.Metadata.Name,
			"config", configName,
			"error", err,
		)
		return intmodel.Cell{}, false, false
	}

	// The materialised cell carries the Config's view of identity; the live
	// cell's persisted identity is authoritative for the on-disk lookup
	// (RecreateCell's GetCell + UpdateCellMetadata both key on it).
	desired.Metadata.Name = cell.Metadata.Name
	desired.Spec.ID = cell.Spec.ID
	desired.Spec.RealmName = cell.Spec.RealmName
	desired.Spec.SpaceName = cell.Spec.SpaceName
	desired.Spec.StackName = cell.Spec.StackName
	desired.Spec.RuntimeEnv = cell.Spec.RuntimeEnv

	diff := apply.DiffCell(desired, cell)
	if !diff.HasChanges {
		// Reconciler stamped OutOfSync but the live spec already matches
		// the materialised one — nothing to rebuild. The next reconciler
		// tick clears the stale OutOfSync flag.
		return intmodel.Cell{}, false, false
	}

	// Rebuild the cell from the materialised spec. RecreateCell tears down the
	// containerd records (post-#867 stop leaves them in place with the old
	// spec-hash, which would otherwise make startCellLocked refuse the new
	// spec), recreates fresh containers, and ends by calling startCellLocked
	// so the cell lands in Ready. Mirrors the restart CLI's
	// ApplyDocuments+StopCell+StartCell sequence for a Stopped cell.
	recreated, err := b.runner.RecreateCell(desired)
	if err != nil {
		b.logger.WarnContext(b.ctx,
			"OutOfSync reapply RecreateCell failed; falling back to on-disk spec",
			"cell", cell.Metadata.Name,
			"config", configName,
			"error", err,
		)
		return intmodel.Cell{}, false, false
	}

	b.logger.InfoContext(b.ctx,
		"reapplied OutOfSync cell from lineage config on start",
		"cell", cell.Metadata.Name,
		"config", configName,
	)
	return recreated, true, true
}
