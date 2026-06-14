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
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
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
	// read above strips the field — without this copy the cron driver use
	// case from #840 (`kuke run <cfg> --name X --env`, where X is an
	// existing-Stopped cell receiving per-invocation env on restart-and-
	// attach) silently drops the per-tick env. Empty input is a no-op (a
	// bare `kuke start <cell>` never sets RuntimeEnv).
	internalCell.Spec.RuntimeEnv = cell.Spec.RuntimeEnv

	// Auto-provision the on-disk spec's per-cell (ensure) volumes before any
	// start/recreate path rebuilds a container OCI spec, so the volume-reference
	// resolver finds the on-disk directory. Idempotent, so a restart re-binds the
	// cell's existing Volume and preserves its contents (#1017). The OutOfSync
	// reapply below re-materialises a fresh `desired` spec whose newly-added
	// ${CELL_NAME} mounts this pass cannot see; reapplyBreaking /
	// reapplyCompatibleInPlace provision `desired` separately before handing it to
	// the runner (#1294 review).
	if err = b.ensurePerCellVolumes(internalCell); err != nil {
		return res, err
	}

	// Recovery routing for the terminal-by-derivation states, evaluated BEFORE
	// the "already running" guard below. The ordering is load-bearing (#1274): a
	// sticky Error/Failed cell keeps its root container alive — cell state is
	// derived from non-root containers only (deriveCellStateFromNonRootContainer-
	// Statuses ignores the root) and neither Error nor Failed is wound down (only
	// Exited fires shouldWindDownCell) — so the leftover root reads back
	// ContainerStateReady. If the running-container guard ran first it would
	// refuse with "has running containers and must first be stopped" before
	// recovery could run, which is the exact #1268 acceptance criterion that
	// stayed unmet after #1272. Centralising the decision here also keeps the
	// startable-state set in agreement across the three operator verbs (`kuke
	// start` reaches this method with no CLI-side state guard, while `kuke run
	// <cell>` and `kuke restart` pre-filter before calling StartCell):
	//
	//   - Pending / Unknown: genuinely unrecoverable / mid-transition. Refuse
	//     with the same delete-then-rerun pointer the CLI verbs print.
	//   - Failed (kukeon bring-up fault, container records may be half-created)
	//     and Error (workload crash whose sticky root is still running): a plain
	//     StartCell cannot recover either — Failed's records may be incomplete,
	//     and Error's live root trips the running-container guard. Both route
	//     through RecreateCell (stop -> delete -> recreate containers -> start,
	//     including the leftover root), the same recovery the OutOfSync
	//     breaking-diff reapply already uses. Routed here (before the OutOfSync
	//     reapply) so the cell is recovered from its on-disk spec regardless of
	//     lineage; the next reconcile re-flags OutOfSync if it still diverges
	//     from a lineage Config. RecreateCell's start phase funnels through
	//     markCellReady, which clears the failure breadcrumb (Status.Reason /
	//     Message) on the Ready transition (#1268).
	//   - Ready (stale metadata, no live task) / Stopped / Exited: fall through
	//     to the running-container guard and the regular start path below — their
	//     container records are intact and not running, so runner.StartCell
	//     re-runs them without a recreate.
	switch internalCell.Status.State {
	case intmodel.CellStatePending, intmodel.CellStateUnknown:
		state := v1beta1.CellState(internalCell.Status.State)
		return res, fmt.Errorf(
			"cell %q exists in %s state; delete it with `kuke delete cell %s` before restarting",
			internalCell.Metadata.Name,
			state.String(),
			internalCell.Metadata.Name,
		)
	case intmodel.CellStateFailed, intmodel.CellStateError:
		state := v1beta1.CellState(internalCell.Status.State)
		recreated, recreateErr := b.runner.RecreateCell(internalCell)
		if recreateErr != nil {
			return res, fmt.Errorf("failed to recover %s cell: %w", state.String(), recreateErr)
		}
		res.Cell = recreated
		res.Started = true
		return res, nil
	}

	// Check if containers are actually running by examining Status.Containers
	// which is freshly populated from containerd by validateAndGetCell -> GetCell -> populateCellContainerStatuses
	// This prevents blocking starts when containers have crashed externally but metadata still shows Ready state.
	// Only Ready / Stopped / Exited reach here — the recovery switch above already
	// claimed Pending / Unknown (refused) and Failed / Error (recreate), so a
	// leftover-running root on a recoverable terminal cell no longer trips this guard.
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
	// the start runs against the freshly materialised spec — see #983. A
	// Breaking diff recreates the cell; a Compatible diff is applied in place
	// with the container overlay preserved (epic:cell-identity P7, #1095). If
	// the reapply already brought the cell back to Ready, the caller is done;
	// otherwise fall through to the regular runner.StartCell.
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
// and reconciles the on-disk + containerd state when the persisted OutOfSync
// flag is set on a Config-lineage cell. The materialised diff is routed by
// change class (epic:cell-identity P7, #1095):
//
//   - Breaking diff → runner.RecreateCell. The diverged field is baked into
//     the cell's OCI runtime spec at create (root image/command/args, a
//     host-namespace toggle, …), so the cell must be torn down and rebuilt;
//     the container overlay is wiped (volume-backed memory survives once
//     #1015 lands).
//   - Compatible / Additive diff → in-place apply with the root overlay
//     preserved: runner.StartCell restarts the cell on its existing
//     containerd snapshot (issue #867), then runner.UpdateCell re-persists the
//     compatible metadata (env/ports/volumes) and stop-remove-recreate-starts
//     any non-root child whose image/command/args changed. UpdateCell cannot
//     run on a stopped cell — non-root children join the root's pid/net/ipc
//     namespaces — so the StartCell-then-UpdateCell ordering is load-bearing.
//
// Returns:
//
//   - (cell, true,  true)  : reapply rebuilt or in-place-reconciled the cell
//     and brought it to Ready; the caller should skip
//     its runner.StartCell pass.
//   - (zero, false, false) : reapply did not run or could not complete its
//     runtime step (no lineage label, OutOfSync flag
//     clear, OutOfSyncError set, lineage Config /
//     Blueprint missing, materialise error, no diff, or
//     a RecreateCell/StartCell failure). Caller continues
//     with the on-disk spec — the runtime is still bounced
//     as the operator asked, matching the CLI restart
//     fall-through (cmd/kuke/restart).
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

	desired, err := materializeCellFromConfig(b.runner, cfg, cell.Metadata.Name, provenanceEnvOverrides(cell))
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

	// Route by change class. A Breaking diff (the RecreateCell domain
	// apply.ReconcileCell uses — root-container baked fields, host-namespace
	// toggles) sets diff.ChangeType to Breaking; anything else (Compatible or
	// Additive) is applied in place with the overlay preserved.
	if diff.ChangeType == apply.ChangeTypeBreaking {
		return b.reapplyBreaking(cell, desired, configName)
	}
	return b.reapplyCompatibleInPlace(cell, desired, configName)
}

// reapplyBreaking rebuilds the cell from the materialised spec via
// RecreateCell. RecreateCell tears down the containerd records (post-#867 stop
// leaves them in place with the old spec-hash, which would otherwise make
// startCellLocked refuse the new spec), recreates fresh containers — wiping the
// container overlay — and ends by calling startCellLocked so the cell lands in
// Ready. Mirrors the restart CLI's ApplyDocuments+StopCell+StartCell sequence
// for a Stopped cell. On failure the caller falls back to the on-disk spec.
func (b *Exec) reapplyBreaking(cell, desired intmodel.Cell, configName string) (intmodel.Cell, bool, bool) {
	// Provision the re-materialised spec's per-cell (ensure) volumes before
	// RecreateCell rebuilds containers against `desired`. The StartCell-level
	// ensurePerCellVolumes pass ran against the pre-reapply on-disk spec, so a
	// Config edit that *adds* a new ${CELL_NAME} mount has no provisioned Volume
	// yet — RecreateCell would then build a container against a Volume step 4's
	// resolver hard-errors on, and OutOfSync never converges (#1017, #1294
	// review). Idempotent, so an already-bound cell re-binds in place. On
	// failure, fall back to the on-disk spec like the RecreateCell path below.
	if err := b.ensurePerCellVolumes(desired); err != nil {
		b.logger.WarnContext(b.ctx,
			"OutOfSync reapply ensure-volumes failed; falling back to on-disk spec",
			"cell", cell.Metadata.Name,
			"config", configName,
			"error", err,
		)
		return intmodel.Cell{}, false, false
	}

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
		"reapplied OutOfSync cell from lineage config on start (breaking diff; recreated)",
		"cell", cell.Metadata.Name,
		"config", configName,
	)
	return recreated, true, true
}

// reapplyCompatibleInPlace applies a Compatible/Additive diff without wiping
// the container overlay. The cell is started on its existing (on-disk) spec
// first — StartCell's spec-hash reuse path (issue #867) restarts the root
// container on its existing containerd snapshot, so the workload's writable
// overlay survives. Starting from `desired` instead would trip
// ErrCellSpecHashDrift the moment a non-root child's image/command/args
// changed; that change is applied by UpdateCell afterwards, which stops,
// removes, recreates, and starts only the affected child (the root container —
// and its overlay — is never touched). UpdateCell needs the root running
// because non-root children join its namespaces, so the ordering is
// load-bearing.
func (b *Exec) reapplyCompatibleInPlace(
	cell, desired intmodel.Cell, configName string,
) (intmodel.Cell, bool, bool) {
	started, err := b.runner.StartCell(cell)
	if err != nil {
		b.logger.WarnContext(b.ctx,
			"OutOfSync reapply StartCell failed; falling back to on-disk spec",
			"cell", cell.Metadata.Name,
			"config", configName,
			"error", err,
		)
		return intmodel.Cell{}, false, false
	}

	// Provision the re-materialised spec's per-cell (ensure) volumes before
	// UpdateCell stop-removes-recreates any child whose spec changed against
	// `desired`. The StartCell-level ensurePerCellVolumes pass ran against the
	// pre-reapply on-disk spec, so a Config edit that *adds* a new ${CELL_NAME}
	// mount has no provisioned Volume yet — UpdateCell would rebuild the affected
	// child against a Volume step 4's resolver hard-errors on (#1017, #1294
	// review). Idempotent, so an already-bound cell re-binds in place. StartCell
	// above already restarted the root on its on-disk snapshot (no new mount), so
	// a failure here mirrors the UpdateCell-failure path: the cell is Ready on the
	// on-disk spec, OutOfSync stays set, and the next start retries.
	if err := b.ensurePerCellVolumes(desired); err != nil {
		b.logger.WarnContext(b.ctx,
			"OutOfSync reapply ensure-volumes failed; cell started on the on-disk spec but not reconciled",
			"cell", cell.Metadata.Name,
			"config", configName,
			"error", err,
		)
		return started, true, true
	}

	updated, err := b.runner.UpdateCell(desired)
	if err != nil {
		// The cell is already Ready from StartCell above; the persisted
		// OutOfSync flag stays set so the next start retries the in-place
		// apply. Honour the operator's start request with the running,
		// not-yet-reconciled cell rather than failing.
		b.logger.WarnContext(b.ctx,
			"OutOfSync reapply UpdateCell failed; cell started on the on-disk spec but not reconciled",
			"cell", cell.Metadata.Name,
			"config", configName,
			"error", err,
		)
		return started, true, true
	}

	b.logger.InfoContext(b.ctx,
		"reapplied OutOfSync cell from lineage config on start (compatible diff; in-place, overlay preserved)",
		"cell", cell.Metadata.Name,
		"config", configName,
	)
	return updated, true, true
}
