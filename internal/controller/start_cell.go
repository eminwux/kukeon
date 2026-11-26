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
	"github.com/eminwux/kukeon/internal/errdefs"
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
// For a Config-lineage cell, StartCell first renders the cell's lineage Config
// + Blueprint and, when the render differs from the live cell, re-materialises
// the spec and rebuilds the containerd records before starting — on every
// start, from the render taken at start time, including the Failed / Error /
// Degraded recovery route (issues #983, #1569). The persisted Status.OutOfSync
// flag is the informational SYNC signal only and does not gate the reapply, so
// `kuke stop` followed by `kuke start` produces the same end state as `kuke
// restart`, straight after a Config edit. The reapply is daemon-side so every
// client that issues StartCell (CLI, future API consumers) gets the
// reconcile-on-start behaviour.
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
	// cell's existing Volume and preserves its contents (#1017). The lineage
	// reapply below re-materialises a fresh `desired` spec whose newly-added
	// ${CELL_NAME} mounts this pass cannot see; reapplyBreaking /
	// reapplyCompatibleInPlace / recoverCell provision `desired` separately
	// before handing it to the runner (#1294 review).
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
	// recovery could run (#1268). Centralising the decision here also keeps the
	// startable-state set in agreement across the three operator verbs (`kuke
	// start` reaches this method with no CLI-side state guard, while `kuke run
	// <cell>` and `kuke restart` pre-filter before calling StartCell):
	//
	//   - Pending / Unknown: genuinely unrecoverable / mid-transition. Refuse
	//     with the same delete-then-rerun pointer the CLI verbs print.
	//   - Failed (kukeon bring-up fault, container records may be half-created),
	//     Error (workload crash whose sticky root is still running), and Degraded
	//     (root/sandbox up but a non-root workload down or restarting): a plain
	//     StartCell cannot cleanly recover any of them, so all three route
	//     through RecreateCell (stop -> delete -> recreate containers -> start,
	//     including the leftover root). For a Config-lineage cell the recreate
	//     uses a fresh render of the lineage Config when that render differs
	//     from the on-disk spec, so a crashed cell whose Config was fixed comes
	//     back on the fix in one start instead of re-crashing on the old spec
	//     forever (issue #1569); with no lineage, a deleted Config or a render
	//     failure, it recreates from the on-disk spec as before. RecreateCell's
	//     start phase funnels through markCellReady, which clears the failure
	//     breadcrumb (Status.Reason / Message) on the Ready transition (#1268).
	//   - Ready (stale metadata, no live task) / Stopped / Exited: fall through
	//     to the running-container guard and the regular start path below — their
	//     container records are intact and not running, so runner.StartCell
	//     re-runs them without a recreate (after the lineage reapply below).
	switch internalCell.Status.State {
	case intmodel.CellStatePending, intmodel.CellStateUnknown:
		state := v1beta1.CellState(internalCell.Status.State)
		return res, fmt.Errorf(
			"%w: cell %q exists in %s state; delete it with `kuke delete cell %s` before restarting",
			errdefs.ErrInvalidCellState,
			internalCell.Metadata.Name,
			state.String(),
			internalCell.Metadata.Name,
		)
	case intmodel.CellStateFailed, intmodel.CellStateError, intmodel.CellStateDegraded:
		recovered, recoverErr := b.recoverCell(internalCell)
		if recoverErr != nil {
			return res, recoverErr
		}
		res.Cell = recovered
		res.Started = true
		return res, nil
	}

	// Check if containers are actually running by examining Status.Containers
	// which is freshly populated from containerd by validateAndGetCell -> GetCell -> populateCellContainerStatuses
	// This prevents blocking starts when containers have crashed externally but metadata still shows Ready state.
	// Only Ready / Stopped / Exited reach here — the recovery switch above
	// already claimed Pending / Unknown (refused) and Failed / Error / Degraded
	// (recreate). The guard blocks on live *workloads*, not the cell shell: on a
	// cell with a non-root workload spec the root container is excluded, because
	// an Exited cell can still carry a live root (one finished before the
	// reconciler's wind-down covered every restartPolicy, #1557, or whose
	// wind-down has not run yet), and that root must not wedge the cell against
	// `kuke run` / `start` / `restart` (#1316). Root-only cells (kukeond-style /
	// cgroup-only, no non-root spec) keep the root in the check — there the root
	// IS the workload, so a running root must still block a duplicate start. The
	// root/non-root split is the same one deriveCellStateFromNonRootContainer-
	// Statuses uses to derive the cell state in the first place.
	if len(internalCell.Status.Containers) > 0 {
		rootID := ""
		if hasNonRootContainerSpec(internalCell) {
			rootID = rootContainerID(internalCell)
		}
		// Check if any non-root container is actually running
		hasRunningContainer := false
		for _, containerStatus := range internalCell.Status.Containers {
			if rootID != "" && containerStatus.ID == rootID {
				continue
			}
			if containerStatus.State == intmodel.ContainerStateReady {
				hasRunningContainer = true
				break
			}
		}
		if hasRunningContainer {
			return res, fmt.Errorf(
				"%w: cell %q has running containers and must first be stopped",
				errdefs.ErrInvalidCellState,
				internalCell.Metadata.Name,
			)
		}
		// Containers are stopped (or unknown), allow start even if metadata says Ready
	} else if internalCell.Status.State == intmodel.CellStateReady {
		// No container statuses available, fall back to metadata state check
		// This preserves existing behavior for cells without containers or when status population fails
		return res, fmt.Errorf(
			"%w: cell %q is already in Ready state and must first be stopped",
			errdefs.ErrInvalidCellState,
			internalCell.Metadata.Name,
		)
	}

	// Reapply the lineage Config from a render taken now, so the start runs
	// against the freshly materialised spec whether or not the reconcile tick
	// has flagged the cell OutOfSync yet — see #983, #1569. A Breaking diff
	// recreates the cell; a Compatible diff is applied in place with the
	// container overlay preserved (epic:cell-identity P7, #1095). If the
	// reapply already brought the cell back to Ready, the caller is done;
	// otherwise fall through to the regular runner.StartCell.
	if reapplied, started, ok := b.reapplyLineageOnStart(internalCell); ok {
		internalCell = reapplied
		if started {
			res.Cell = internalCell
			res.Started = true
			return res, nil
		}
	}

	// Start all containers in the cell. The runner persists the started
	// cell under the per-cell lock and is the only writer of the verb's
	// outcome (issue #1569).
	internalCell, err = b.runner.StartCell(internalCell)
	if err != nil {
		return res, fmt.Errorf("failed to start cell containers: %w", err)
	}

	res.Cell = internalCell
	res.Started = true
	return res, nil
}

// lineageRender is a start-time render of a Config-lineage cell, compared
// against the live cell it would replace.
type lineageRender struct {
	configName string
	desired    intmodel.Cell
	diff       apply.CellDiffResult
	// drift is the render provenance drift (renderProvenanceDrift): inputs
	// that moved even when the rendered spec did not.
	drift []string
	// imageDrift reports a realm image rebuilt under the same reference,
	// which DiffCell cannot see but which needs fresh containers.
	imageDrift bool
}

// specChanged reports whether applying the render changes the cell's
// runtime: a spec diff or a rebuilt realm image.
func (lr lineageRender) specChanged() bool {
	return lr.diff.HasChanges || lr.imageDrift
}

// renderLineage renders a Config-lineage cell from its lineage Config and
// compares the result with the live cell. It returns false when there is
// nothing to render from — no lineage label, the lineage Config is gone, or
// the lookup or render failed — and the caller then works from the on-disk
// spec. Neither Status.OutOfSync nor Status.OutOfSyncError gates it (issue
// #1569): the decision is made from this render, so a start right after a
// Config edit applies it without waiting for the reconcile tick, and a Config
// fixed after a render error is picked up on the next start.
func (b *Exec) renderLineage(cell intmodel.Cell) (lineageRender, bool) {
	configName, hasLineage := configLineage(cell)
	if !hasLineage {
		return lineageRender{}, false
	}

	cfg, found, err := lookupLineageConfig(b.runner, cell, configName)
	if err != nil {
		b.logger.WarnContext(b.ctx,
			"lineage reapply lookup failed; starting cell with on-disk spec",
			"cell", cell.Metadata.Name,
			"config", configName,
			"error", err,
		)
		return lineageRender{}, false
	}
	if !found {
		// Lineage Config deleted — nothing to materialise from.
		b.logger.InfoContext(b.ctx,
			"lineage reapply skipped: lineage config deleted",
			"cell", cell.Metadata.Name,
			"config", configName,
		)
		return lineageRender{}, false
	}

	desired, err := materializeCellFromConfig(b.runner, cfg, bindingFromCell(cell))
	if err != nil {
		b.logger.WarnContext(b.ctx,
			"lineage reapply materialise failed; starting cell with on-disk spec",
			"cell", cell.Metadata.Name,
			"config", configName,
			"error", err,
		)
		return lineageRender{}, false
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

	return lineageRender{
		configName: configName,
		desired:    desired,
		diff:       apply.DiffCell(desired, cell),
		drift:      renderProvenanceDrift(desired, cell, legacyConfigRevision(cfg)),
		imageDrift: len(imageDigestDrift(desired, cell)) > 0,
	}, true
}

// recoverCell recreates a Failed / Error / Degraded cell (see the state
// switch in StartCell). When the lineage Config renders to a different spec
// (or a rebuilt realm image), the recreate uses that fresh spec, whatever
// the change class, since these states need a recreate anyway; if that
// recreate fails, or there is nothing to apply, or no render is available,
// the cell is recreated from its on-disk spec. A recreate that reflects the
// render finishes with recordReapply (issue #1569).
func (b *Exec) recoverCell(internalCell intmodel.Cell) (intmodel.Cell, error) {
	state := v1beta1.CellState(internalCell.Status.State)

	lr, rendered := b.renderLineage(internalCell)
	if rendered && lr.specChanged() {
		if volErr := b.ensurePerCellVolumes(lr.desired); volErr != nil {
			b.logger.WarnContext(b.ctx,
				"lineage reapply ensure-volumes failed; recovering cell from on-disk spec",
				"cell", internalCell.Metadata.Name,
				"config", lr.configName,
				"error", volErr,
			)
		} else {
			recreated, recreateErr := b.runner.RecreateCell(lr.desired)
			if recreateErr == nil {
				b.logger.InfoContext(b.ctx,
					"recovered cell from a fresh render of its lineage config",
					"cell", internalCell.Metadata.Name,
					"config", lr.configName,
					"state", state.String(),
				)
				return b.recordReapply(recreated, lr), nil
			}
			b.logger.WarnContext(b.ctx,
				"lineage reapply RecreateCell failed; recovering cell from on-disk spec",
				"cell", internalCell.Metadata.Name,
				"config", lr.configName,
				"error", recreateErr,
			)
		}
		// The fallback below runs the on-disk spec, which does not reflect
		// the render: leave the OutOfSync record to the tick.
		rendered = false
	}

	recreated, recreateErr := b.runner.RecreateCell(internalCell)
	if recreateErr != nil {
		return intmodel.Cell{}, fmt.Errorf("failed to recover %s cell: %w", state.String(), recreateErr)
	}
	if rendered && len(lr.drift) > 0 {
		// Only provenance moved; the on-disk spec already is the rendered
		// one, so record the fresh stamps.
		return b.recordReapply(recreated, lr), nil
	}
	return recreated, nil
}

// reapplyLineageOnStart re-materialises a Config-lineage cell from a render
// taken at start time and reconciles the on-disk + containerd state when the
// render differs from the live cell. It runs on every start of a cell with a
// lineage label (issue #1569); the persisted Status.OutOfSync flag is only the
// informational SYNC signal and does not gate it. The diff is routed by change
// class (epic:cell-identity P7, #1095):
//
//   - Breaking diff, or a rebuilt realm image → runner.RecreateCell. The
//     diverged field is baked into the cell's OCI runtime spec at create, so
//     the cell must be torn down and rebuilt; the container overlay is wiped.
//   - Compatible / Additive diff → in-place apply with the root overlay
//     preserved: runner.StartCell restarts the cell on its existing
//     containerd snapshot (issue #867), then runner.UpdateCell persists the
//     compatible fields (env/volumes, spec.autoDelete, spec.tty) and
//     stop-remove-recreate-starts any non-root child whose image/command/args
//     changed. UpdateCell cannot run on a stopped cell — non-root children
//     join the root's pid/net/ipc namespaces — so the StartCell-then-UpdateCell
//     ordering is load-bearing.
//   - Provenance-only drift (a level's defaults, the Config document, the
//     Blueprint or the fill values moved without changing the rendered spec)
//     → nothing to rebuild; the caller starts the cell on its on-disk spec.
//
// Every route that applied the render finishes with recordReapply, which
// records the fresh provenance and clears the OutOfSync fields.
//
// Returns:
//
//   - (cell, true,  true)  : reapply rebuilt or in-place-reconciled the cell
//     and brought it to Ready; the caller should skip
//     its runner.StartCell pass.
//   - (cell, false, true)  : provenance-only drift recorded; the caller starts
//     the cell on its on-disk spec.
//   - (zero, false, false) : reapply did not run or could not complete its
//     runtime step (no lineage label, lineage Config
//     missing, render error, no diff and no drift, or a
//     RecreateCell/StartCell failure). Caller continues
//     with the on-disk spec — the runtime is still bounced
//     as the operator asked, matching the CLI restart
//     fall-through (cmd/kuke/restart).
func (b *Exec) reapplyLineageOnStart(cell intmodel.Cell) (intmodel.Cell, bool, bool) {
	lr, ok := b.renderLineage(cell)
	if !ok {
		return intmodel.Cell{}, false, false
	}
	if !lr.diff.HasChanges && len(lr.drift) == 0 {
		// The live spec already matches the render — a pure bounce. Any
		// stale OutOfSync flag is left to the next tick.
		return intmodel.Cell{}, false, false
	}

	switch {
	case lr.diff.ChangeType == apply.ChangeTypeBreaking || lr.imageDrift:
		out, started, applied := b.reapplyBreaking(cell, lr.desired, lr.configName)
		if !applied {
			return out, started, false
		}
		return b.recordReapply(out, lr), started, true
	case lr.diff.HasChanges:
		out, started, applied := b.reapplyCompatibleInPlace(cell, lr.desired, lr.configName)
		if applied {
			out = b.recordReapply(out, lr)
		}
		return out, started, started || applied
	default:
		// Only provenance moved and the rendered spec is unchanged: nothing
		// to rebuild, just record the new provenance and let the caller
		// start the cell on its on-disk spec.
		return b.recordReapply(cell, lr), false, true
	}
}

// applyRenderRecord stamps a successful reapply's record onto cell: the
// fresh render's annotations merged over the live ones, Spec.Provenance from
// the render, and the three OutOfSync status fields cleared.
func applyRenderRecord(cell *intmodel.Cell, desired intmodel.Cell) {
	annotations := make(map[string]string, len(cell.Metadata.Annotations)+len(desired.Metadata.Annotations))
	for k, v := range cell.Metadata.Annotations {
		annotations[k] = v
	}
	for k, v := range desired.Metadata.Annotations {
		annotations[k] = v
	}
	cell.Metadata.Annotations = annotations
	if desired.Spec.Provenance != nil {
		cell.Spec.Provenance = intmodel.CloneCellProvenance(desired.Spec.Provenance)
	}
	cell.Status.OutOfSync = false
	cell.Status.OutOfSyncReason = ""
	cell.Status.OutOfSyncError = ""
}

// recordReapply finishes a successful reapply with one locked patch
// (runner.PatchCellMetadata, issue #1569): the fresh render's annotations and
// provenance replace the live ones and the OutOfSync fields are cleared, so
// `kuke get cell` reads Synced straight after the start instead of after the
// next tick. The patch works on a fresh read under the per-cell lock, so it
// never overwrites a write that landed after the reapply's runner calls. A
// failure is logged; the next tick then re-flags the cell and the next start
// retries. The returned cell is the caller's cell with the same record
// applied in memory.
func (b *Exec) recordReapply(cell intmodel.Cell, lr lineageRender) intmodel.Cell {
	err := b.runner.PatchCellMetadata(cell, func(fresh *intmodel.Cell) bool {
		applyRenderRecord(fresh, lr.desired)
		return true
	})
	if err != nil {
		b.logger.WarnContext(b.ctx,
			"lineage reapply could not record render provenance",
			"cell", cell.Metadata.Name,
			"config", lr.configName,
			"error", err,
		)
		return cell
	}
	applyRenderRecord(&cell, lr.desired)
	return cell
}

// reapplyBreaking rebuilds the cell from the materialised spec via
// RecreateCell. RecreateCell tears down the containerd records (post-#867 stop
// leaves them in place with the old spec-hash, which would otherwise make
// startCellLocked refuse the new spec), recreates fresh containers — wiping the
// container overlay — and ends by calling startCellLocked so the cell lands in
// Ready. Mirrors the restart CLI's ApplyDocuments+StopCell+StartCell sequence
// for a Stopped cell. On failure the caller falls back to the on-disk spec.
//
// Returns (cell, started, applied); a breaking reapply either applies and
// starts the cell, or does neither.
func (b *Exec) reapplyBreaking(cell, desired intmodel.Cell, configName string) (intmodel.Cell, bool, bool) {
	// Provision the re-materialised spec's per-cell (ensure) volumes before
	// RecreateCell rebuilds containers against `desired`. The StartCell-level
	// ensurePerCellVolumes pass ran against the pre-reapply on-disk spec, so a
	// Config edit that *adds* a new ${CELL_NAME} mount has no provisioned Volume
	// yet — RecreateCell would then build a container against a Volume step 4's
	// resolver hard-errors on, and the reapply never converges (#1017, #1294
	// review). Idempotent, so an already-bound cell re-binds in place. On
	// failure, fall back to the on-disk spec like the RecreateCell path below.
	if err := b.ensurePerCellVolumes(desired); err != nil {
		b.logger.WarnContext(b.ctx,
			"lineage reapply ensure-volumes failed; falling back to on-disk spec",
			"cell", cell.Metadata.Name,
			"config", configName,
			"error", err,
		)
		return intmodel.Cell{}, false, false
	}

	recreated, err := b.runner.RecreateCell(desired)
	if err != nil {
		b.logger.WarnContext(b.ctx,
			"lineage reapply RecreateCell failed; falling back to on-disk spec",
			"cell", cell.Metadata.Name,
			"config", configName,
			"error", err,
		)
		return intmodel.Cell{}, false, false
	}

	b.logger.InfoContext(b.ctx,
		"reapplied cell from lineage config on start (breaking diff; recreated)",
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
//
// Returns (cell, started, applied): started is true once the cell is running
// (on the on-disk spec at least), applied only when UpdateCell persisted the
// render as well.
func (b *Exec) reapplyCompatibleInPlace(
	cell, desired intmodel.Cell, configName string,
) (intmodel.Cell, bool, bool) {
	started, err := b.runner.StartCell(cell)
	if err != nil {
		b.logger.WarnContext(b.ctx,
			"lineage reapply StartCell failed; falling back to on-disk spec",
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
	// on-disk spec, the record is not updated, and the next start retries.
	if err := b.ensurePerCellVolumes(desired); err != nil {
		b.logger.WarnContext(b.ctx,
			"lineage reapply ensure-volumes failed; cell started on the on-disk spec but not reconciled",
			"cell", cell.Metadata.Name,
			"config", configName,
			"error", err,
		)
		return started, true, false
	}

	updated, err := b.runner.UpdateCell(desired)
	if err != nil {
		// The cell is already Ready from StartCell above; the next start
		// renders again and retries the in-place apply. Honour the
		// operator's start request with the running, not-yet-reconciled
		// cell rather than failing.
		b.logger.WarnContext(b.ctx,
			"lineage reapply UpdateCell failed; cell started on the on-disk spec but not reconciled",
			"cell", cell.Metadata.Name,
			"config", configName,
			"error", err,
		)
		return started, true, false
	}

	b.logger.InfoContext(b.ctx,
		"reapplied cell from lineage config on start (compatible diff; in-place, overlay preserved)",
		"cell", cell.Metadata.Name,
		"config", configName,
	)
	return updated, true, true
}
