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
	"time"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/naming"
)

// observedGenerationBehind reports whether the reconciler has not yet
// recorded, via Status.ObservedGeneration, that it acted on the cell's
// current Metadata.Generation. When true the reconciler persists even if no
// derived status field changed, so ObservedGeneration converges to
// Generation after a spec write and the AC5 stale-skip comparison stays
// meaningful (a cell that reached Ready synchronously, never via a
// reconcile persist, would otherwise sit at ObservedGeneration 0 forever).
func observedGenerationBehind(status intmodel.CellStatus, generation int64) bool {
	return status.ObservedGeneration != generation
}

// persistCellStatusGuarded stamps Status.ObservedGeneration with the
// generation the reconciler observed when the cell was listed and persists
// the cell — but only when no concurrent spec writer has advanced the
// on-disk Generation past that observation. The guard rides the writer's
// optimistic token: UpdateCellMetadata carries the observed generation, and
// the writer rejects with errdefs.ErrStaleResource when the on-disk
// generation has moved past it (or when a spec write lands in the CAS
// window). A stale observation is reported as a benign skip (persisted ==
// false, nil error): the next reconcile tick re-derives status from the
// fresher spec rather than clobbering it with a stale view.
func (r *Exec) persistCellStatusGuarded(cell intmodel.Cell) (bool, error) {
	cell.Status.ObservedGeneration = cell.Metadata.Generation
	if err := r.UpdateCellMetadata(cell); err != nil {
		if errors.Is(err, errdefs.ErrStaleResource) {
			r.logger.DebugContext(r.ctx,
				"reconcile: skipping stale cell persist; on-disk generation advanced",
				"cell", cell.Metadata.Name,
				"observed", cell.Metadata.Generation)
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// persistRealmStatusGuarded persists a realm whose status the refresh path
// just re-derived, treating a CAS rejection from a concurrent spec writer as
// a benign skip rather than a hard error. UpdateRealmMetadata rides the
// writer's optimistic token (phase 3 — #633) and returns
// errdefs.ErrStaleResource when the on-disk generation has advanced past the
// observation the refresh read with. Mirrors persistCellStatusGuarded so the
// realm/space/stack refresh paths match the cell path: a stale race
// self-heals on the next refresh tick instead of surfacing as a reported
// `Errors:` line in `kuke refresh` (issue #636).
func (r *Exec) persistRealmStatusGuarded(realm intmodel.Realm) (bool, error) {
	if err := r.UpdateRealmMetadata(realm); err != nil {
		if errors.Is(err, errdefs.ErrStaleResource) {
			r.logger.DebugContext(r.ctx,
				"refresh: skipping stale realm persist; on-disk generation advanced",
				"realm", realm.Metadata.Name,
				"observed", realm.Metadata.Generation)
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// persistSpaceStatusGuarded is the Space counterpart of
// persistRealmStatusGuarded.
func (r *Exec) persistSpaceStatusGuarded(space intmodel.Space) (bool, error) {
	if err := r.UpdateSpaceMetadata(space); err != nil {
		if errors.Is(err, errdefs.ErrStaleResource) {
			r.logger.DebugContext(r.ctx,
				"refresh: skipping stale space persist; on-disk generation advanced",
				"space", space.Metadata.Name,
				"observed", space.Metadata.Generation)
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// persistStackStatusGuarded is the Stack counterpart of
// persistRealmStatusGuarded.
func (r *Exec) persistStackStatusGuarded(stack intmodel.Stack) (bool, error) {
	if err := r.UpdateStackMetadata(stack); err != nil {
		if errors.Is(err, errdefs.ErrStaleResource) {
			r.logger.DebugContext(r.ctx,
				"refresh: skipping stale stack persist; on-disk generation advanced",
				"stack", stack.Metadata.Name,
				"observed", stack.Metadata.Generation)
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// RefreshRealm refreshes the status of a realm by checking cgroup and containerd namespace.
// Returns the updated realm, whether it was updated, and any error.
func (r *Exec) RefreshRealm(realm intmodel.Realm) (intmodel.Realm, bool, error) {
	originalStatus := realm.Status
	newStatus := intmodel.RealmStatus{
		State:      intmodel.RealmStateUnknown, // Default to Unknown - will be updated if checks succeed
		CgroupPath: originalStatus.CgroupPath,
	}

	// Check cgroup existence
	cgroupExists, err := r.ExistsCgroup(realm)
	switch {
	case err != nil:
		r.logger.DebugContext(r.ctx, "failed to check realm cgroup", "realm", realm.Metadata.Name, "error", err)
		// If check fails, state remains Unknown (already set). CgroupReady stays
		// false because we have no positive observation.
	case cgroupExists:
		newStatus.State = intmodel.RealmStateReady
		newStatus.CgroupReady = true
	default:
		// Cgroup doesn't exist - realm is unknown
		newStatus.State = intmodel.RealmStateUnknown
	}

	// Check containerd namespace (only if cgroup check succeeded without error)
	if err == nil {
		namespace := realm.Spec.Namespace
		if namespace == "" {
			namespace = realm.Metadata.Name
		}
		namespaceExists, namespaceErr := r.ExistsRealmContainerdNamespace(namespace)
		switch {
		case namespaceErr != nil:
			r.logger.DebugContext(
				r.ctx,
				"failed to check realm namespace",
				"realm",
				realm.Metadata.Name,
				"error",
				namespaceErr,
			)
			// If namespace check fails, set state to Unknown (cannot determine state)
			newStatus.State = intmodel.RealmStateUnknown
		case namespaceExists && newStatus.State == intmodel.RealmStateReady:
			// Namespace exists and cgroup exists - realm is ready
			newStatus.State = intmodel.RealmStateReady
			newStatus.ContainerdNamespaceReady = true
		case !namespaceExists && newStatus.State == intmodel.RealmStateReady:
			// Cgroup exists but namespace doesn't - realm is in inconsistent state
			newStatus.State = intmodel.RealmStateUnknown
		case namespaceExists:
			// Namespace exists but cgroup absent/Unknown above; record the
			// observation independently so callers can distinguish a missing
			// cgroup from a missing namespace.
			newStatus.ContainerdNamespaceReady = true
		}
	}

	// Carry lifecycle timestamps + Reason/Message through unchanged so the
	// stamping in UpdateRealmMetadata can apply its set-once invariants.
	// Without this, the locally constructed newStatus would erase CreatedAt
	// and ReadyAt every time the refresh path writes back.
	carryRealmLifecycle(originalStatus, &newStatus)

	// Update if anything changed
	updated := false
	if newStatus.State != originalStatus.State ||
		newStatus.CgroupPath != originalStatus.CgroupPath ||
		newStatus.CgroupReady != originalStatus.CgroupReady ||
		newStatus.ContainerdNamespaceReady != originalStatus.ContainerdNamespaceReady {
		realm.Status = newStatus
		persisted, updateErr := r.persistRealmStatusGuarded(realm)
		if updateErr != nil {
			return realm, false, fmt.Errorf("failed to update realm metadata: %w", updateErr)
		}
		// A concurrent spec write advanced the realm's generation past the
		// refreshed view: the derived status was skipped rather than
		// clobbering the newer spec, so report nothing updated.
		updated = persisted
	}

	return realm, updated, nil
}

// carryRealmLifecycle preserves the lifecycle/reason fields the refresh
// path does not derive from filesystem/containerd checks. CreatedAt and
// ReadyAt are set-once and must survive the locally-built newStatus;
// Reason/Message belong to whoever last set them (the reconciler in the
// future, or a Failed-path writer today). UpdatedAt is intentionally
// not carried — UpdateRealmMetadata stamps it on every persist.
func carryRealmLifecycle(orig intmodel.RealmStatus, next *intmodel.RealmStatus) {
	next.CreatedAt = orig.CreatedAt
	next.ReadyAt = orig.ReadyAt
	next.Reason = orig.Reason
	next.Message = orig.Message
}

// RefreshSpace refreshes the status of a space by checking CNI config.
// Returns the updated space, whether it was updated, and any error.
func (r *Exec) RefreshSpace(space intmodel.Space) (intmodel.Space, bool, error) {
	originalStatus := space.Status
	newStatus := intmodel.SpaceStatus{
		State:      intmodel.SpaceStateUnknown, // Default to Unknown - will be updated if checks succeed
		CgroupPath: originalStatus.CgroupPath,
	}

	// Check CNI config existence
	cniExists, err := r.ExistsSpaceCNIConfig(space)
	switch {
	case err != nil:
		r.logger.DebugContext(r.ctx, "failed to check space CNI config", "space", space.Metadata.Name, "error", err)
		// If check fails, state remains Unknown (already set)
	case cniExists:
		newStatus.State = intmodel.SpaceStateReady
	default:
		// CNI config doesn't exist - space is unknown
		newStatus.State = intmodel.SpaceStateUnknown
	}

	// Check cgroup existence (only if CNI check succeeded without error)
	if err == nil {
		cgroupExists, cgroupErr := r.ExistsCgroup(space)
		switch {
		case cgroupErr != nil:
			r.logger.DebugContext(
				r.ctx,
				"failed to check space cgroup",
				"space",
				space.Metadata.Name,
				"error",
				cgroupErr,
			)
			// If cgroup check fails, set state to Unknown (cannot determine state)
			newStatus.State = intmodel.SpaceStateUnknown
		case cgroupExists && newStatus.State == intmodel.SpaceStateReady:
			// Both CNI and cgroup exist - space is ready
			newStatus.State = intmodel.SpaceStateReady
			newStatus.CgroupReady = true
		case !cgroupExists && newStatus.State == intmodel.SpaceStateReady:
			// CNI exists but cgroup doesn't - space is in inconsistent state
			newStatus.State = intmodel.SpaceStateUnknown
		case cgroupExists:
			// Cgroup exists but CNI absent/Unknown above; record the cgroup
			// observation independently so the field tracks reality, not the
			// derived State.
			newStatus.CgroupReady = true
		}
	}

	carrySpaceLifecycle(originalStatus, &newStatus)

	// Update if anything changed
	updated := false
	if newStatus.State != originalStatus.State ||
		newStatus.CgroupPath != originalStatus.CgroupPath ||
		newStatus.CgroupReady != originalStatus.CgroupReady {
		space.Status = newStatus
		persisted, updateErr := r.persistSpaceStatusGuarded(space)
		if updateErr != nil {
			return space, false, fmt.Errorf("failed to update space metadata: %w", updateErr)
		}
		// A concurrent spec write advanced the space's generation past the
		// refreshed view: skip rather than clobber the newer spec.
		updated = persisted
	}

	return space, updated, nil
}

// carrySpaceLifecycle is the Space counterpart of carryRealmLifecycle.
func carrySpaceLifecycle(orig intmodel.SpaceStatus, next *intmodel.SpaceStatus) {
	next.CreatedAt = orig.CreatedAt
	next.ReadyAt = orig.ReadyAt
	next.Reason = orig.Reason
	next.Message = orig.Message
}

// RefreshStack refreshes the status of a stack by checking cgroup.
// Returns the updated stack, whether it was updated, and any error.
func (r *Exec) RefreshStack(stack intmodel.Stack) (intmodel.Stack, bool, error) {
	originalStatus := stack.Status
	newStatus := intmodel.StackStatus{
		State:      intmodel.StackStateUnknown, // Default to Unknown - will be updated if checks succeed
		CgroupPath: originalStatus.CgroupPath,
	}

	// Check cgroup existence
	cgroupExists, err := r.ExistsCgroup(stack)
	switch {
	case err != nil:
		r.logger.DebugContext(r.ctx, "failed to check stack cgroup", "stack", stack.Metadata.Name, "error", err)
		// If check fails, state remains Unknown (already set)
	case cgroupExists:
		newStatus.State = intmodel.StackStateReady
		newStatus.CgroupReady = true
	default:
		// Cgroup doesn't exist - stack is unknown
		newStatus.State = intmodel.StackStateUnknown
	}

	carryStackLifecycle(originalStatus, &newStatus)

	// Update if anything changed
	updated := false
	if newStatus.State != originalStatus.State ||
		newStatus.CgroupPath != originalStatus.CgroupPath ||
		newStatus.CgroupReady != originalStatus.CgroupReady {
		stack.Status = newStatus
		persisted, updateErr := r.persistStackStatusGuarded(stack)
		if updateErr != nil {
			return stack, false, fmt.Errorf("failed to update stack metadata: %w", updateErr)
		}
		// A concurrent spec write advanced the stack's generation past the
		// refreshed view: skip rather than clobber the newer spec.
		updated = persisted
	}

	return stack, updated, nil
}

// carryStackLifecycle is the Stack counterpart of carryRealmLifecycle.
func carryStackLifecycle(orig intmodel.StackStatus, next *intmodel.StackStatus) {
	next.CreatedAt = orig.CreatedAt
	next.ReadyAt = orig.ReadyAt
	next.Reason = orig.Reason
	next.Message = orig.Message
}

// RefreshCell refreshes the status of a cell and its containers.
// Returns the updated cell, number of containers updated, and any error.
//
// Mirrors the container-aware derivation ReconcileCell does (#543): the
// post-reboot scenario (cgroup wiped, container records survive, tasks
// gone) must transition cells out of Unknown so `kuke refresh` surfaces
// the same Cells / Containers updates the reconcile loop does. Pure
// "cgroup-only" cell-state derivation would re-park them at Unknown.
func (r *Exec) RefreshCell(cell intmodel.Cell) (intmodel.Cell, int, error) {
	originalStatus := cell.Status
	originalContainerStatuses := append(
		[]intmodel.ContainerStatus(nil), originalStatus.Containers...)

	newStatus := intmodel.CellStatus{
		State:      intmodel.CellStateUnknown, // Default to Unknown - will be updated if checks succeed
		CgroupPath: originalStatus.CgroupPath,
	}

	// Check cgroup existence. Used to set CgroupReady and to guard the
	// derivation pass below — when the filesystem check itself errors we
	// have no positive observation either way and stay at Unknown.
	cgroupExists, err := r.ExistsCgroup(cell)
	if err != nil {
		r.logger.DebugContext(r.ctx, "failed to check cell cgroup", "cell", cell.Metadata.Name, "error", err)
	} else if cgroupExists {
		newStatus.CgroupReady = true
	}

	carryCellLifecycle(originalStatus, &newStatus)

	// Populate container statuses so derivation reads from a fresh
	// snapshot and so containersUpdated reflects per-container state
	// changes (the post-reboot Unknown → Stopped transition that the
	// AC #3 calls out for `kuke refresh`).
	if populateErr := r.populateCellContainerStatuses(&cell); populateErr != nil {
		r.logger.DebugContext(r.ctx, "populate container statuses failed",
			"cell", cell.Metadata.Name, "error", populateErr)
	}
	newStatus.Containers = cell.Status.Containers

	// Derive cell state from the container snapshot when the cgroup
	// check didn't error. CreateCell-race protection comes from
	// latchReadyObserved below — a cell that has never been observed
	// Ready cannot be reaped regardless of what derivation reads back.
	if err == nil {
		newStatus.State = r.deriveCellState(cell)
	}

	// Stamp the runtime workload-failure breadcrumb on a fresh transition to
	// CellStateError (#1206, renamed from Failed in #1267), same contract as
	// ReconcileCell. carryCellLifecycle above already copied any prior
	// Reason/Message forward, so an already-Error cell keeps its original
	// reason; only a fresh Ready/Exited → Error transition restamps here.
	// cell's container snapshot was populated above, so the breadcrumb resolves
	// the failing container even though the final write target is newStatus.
	if newStatus.State == intmodel.CellStateError && originalStatus.State != intmodel.CellStateError {
		newStatus.Reason, newStatus.Message = cellFailureBreadcrumb(cell)
	}

	// Run ReadyObserved through the same one-way latch ReconcileCell uses.
	// Without this, `kuke refresh` racing a synchronous Ready write would
	// wipe the latched ReadyObserved to false (newStatus is constructed
	// without it and assigned wholesale below), defeating AutoDelete via
	// the same mechanism as #275 — see issue #278.
	newStatus.ReadyObserved = latchReadyObserved(
		originalStatus.ReadyObserved, originalStatus.State, newStatus.State)

	// Refresh all containers in the cell (always attempt, regardless of cgroup check result)
	containersUpdated := 0
	for i := range cell.Spec.Containers {
		containerSpec := &cell.Spec.Containers[i]
		updated, containerErr := r.refreshContainerStatus(cell, containerSpec)
		if containerErr != nil {
			r.logger.WarnContext(r.ctx, "failed to refresh container status",
				"cell", cell.Metadata.Name,
				"container", containerSpec.ID,
				"error", containerErr)
			// Continue with other containers
		} else if updated {
			containersUpdated++
		}
	}

	// Surface per-container state transitions to the operator: any
	// container whose snapshot State differs from the persisted record
	// counts toward containersUpdated, so `kuke refresh` lists them
	// under the Containers: line. Without this, refreshContainerStatus
	// alone only fires the counter on a missing-ContainerdID fill-in.
	if !containerStatusesEqual(originalContainerStatuses, newStatus.Containers) {
		containersUpdated += countContainerStatusChanges(
			originalContainerStatuses, newStatus.Containers)
	}

	// Update cell metadata if status changed or containers were updated
	cellUpdated := false
	if newStatus.State != originalStatus.State ||
		newStatus.CgroupPath != originalStatus.CgroupPath ||
		newStatus.CgroupReady != originalStatus.CgroupReady ||
		newStatus.ReadyObserved != originalStatus.ReadyObserved ||
		!containerStatusesEqual(originalContainerStatuses, newStatus.Containers) {
		cell.Status = newStatus
		cellUpdated = true
	}

	if cellUpdated || containersUpdated > 0 {
		persisted, updateErr := r.persistCellStatusGuarded(cell)
		if updateErr != nil {
			return cell, containersUpdated, fmt.Errorf("failed to update cell metadata: %w", updateErr)
		}
		// A concurrent spec write advanced the cell's generation past the
		// refreshed view: the derived status was discarded rather than
		// clobbering the newer spec, so report nothing updated.
		if !persisted {
			return cell, 0, nil
		}
		return cell, containersUpdated, nil
	}

	return cell, containersUpdated, nil
}

// countContainerStatusChanges counts containers whose State differs
// from the persisted record (or are newly present). Used by RefreshCell
// to size the Containers: line in `kuke refresh` summaries.
func countContainerStatusChanges(orig, next []intmodel.ContainerStatus) int {
	prevByID := make(map[string]intmodel.ContainerState, len(orig))
	for i := range orig {
		prevByID[orig[i].ID] = orig[i].State
	}
	changed := 0
	for i := range next {
		prev, existed := prevByID[next[i].ID]
		if !existed || prev != next[i].State {
			changed++
		}
	}
	return changed
}

// carryCellLifecycle is the Cell counterpart of carryRealmLifecycle.
// Network/Containers are not carried because the refresh path either
// rebuilds them (Containers) or leaves them on the live cell struct
// (Network). ReadyObserved is handled by the caller via
// latchReadyObserved — it needs both the original and the freshly
// derived State to apply the one-way latch, which carryCellLifecycle
// does not see — so the carry-through happens inline in RefreshCell.
func carryCellLifecycle(orig intmodel.CellStatus, next *intmodel.CellStatus) {
	next.CreatedAt = orig.CreatedAt
	next.ReadyAt = orig.ReadyAt
	next.Reason = orig.Reason
	next.Message = orig.Message
}

// refreshContainerStatus refreshes the status of a container by introspecting containerd.
func (r *Exec) refreshContainerStatus(cell intmodel.Cell, containerSpec *intmodel.ContainerSpec) (bool, error) {
	// Get realm to access namespace
	lookupRealm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: cell.Spec.RealmName,
		},
	}
	internalRealm, err := r.GetRealm(lookupRealm)
	if err != nil {
		return false, fmt.Errorf("failed to get realm: %w", err)
	}

	namespace := internalRealm.Spec.Namespace
	if namespace == "" {
		namespace = internalRealm.Metadata.Name
	}

	// Get containerd ID
	containerdID := containerSpec.ContainerdID
	if containerdID == "" {
		// Build containerd ID if not set
		if containerSpec.Root {
			containerdID, err = naming.BuildRootContainerdID(
				cell.Spec.SpaceName,
				cell.Spec.StackName,
				cell.Spec.ID,
			)
		} else {
			containerdID, err = naming.BuildContainerdID(
				cell.Spec.SpaceName,
				cell.Spec.StackName,
				cell.Spec.ID,
				containerSpec.ID,
			)
		}
		if err != nil {
			return false, fmt.Errorf("failed to build containerd ID: %w", err)
		}
	}

	// Check if container exists in containerd
	containerExists := false
	if r.ctrClient != nil {
		containerExists, err = r.ExistsContainer(namespace, containerdID)
		if err != nil {
			r.logger.DebugContext(r.ctx, "failed to check container existence",
				"container", containerSpec.ID,
				"containerdID", containerdID,
				"error", err)
			// Continue - assume container doesn't exist
		}
	}

	// Get task status if container exists
	var taskStatus containerd.Status
	taskExists := false
	if containerExists && r.ctrClient != nil {
		taskStatus, err = r.ctrClient.TaskStatus(namespace, containerdID)
		if err != nil {
			// Task might not exist even if container does
			r.logger.DebugContext(r.ctx, "failed to get task status",
				"container", containerSpec.ID,
				"containerdID", containerdID,
				"error", err)
		} else {
			taskExists = true
		}
	}

	// Log container state for debugging
	switch {
	case taskExists:
		r.logger.DebugContext(r.ctx, "container task status",
			"container", containerSpec.ID,
			"containerdID", containerdID,
			"status", taskStatus.Status)
	case containerExists:
		r.logger.DebugContext(r.ctx, "container exists but no task",
			"container", containerSpec.ID,
			"containerdID", containerdID)
	default:
		r.logger.DebugContext(r.ctx, "container not found in containerd",
			"container", containerSpec.ID,
			"containerdID", containerdID)
	}

	// Update container's ContainerdID if it was missing and we successfully built it
	updated := false
	if containerSpec.ContainerdID == "" && containerdID != "" {
		containerSpec.ContainerdID = containerdID
		updated = true
	}

	return updated, nil
}

// ReconcileCell is the daemon-side counterpart to RefreshCell. Where
// RefreshCell derives cell.Status.State from cgroup existence alone (so
// `kuke refresh` keeps its current shape), ReconcileCell additionally
// considers container task state — which is what flips a Ready cell to
// Stopped when an operator runs `ctr task kill` outside of kukeon, or
// when the cell's non-root workloads have all exited.
//
// State derivation (see deriveCellState):
//   - cgroup check error or cgroup absent → Unknown
//   - cgroup present, no non-root container in spec (kukeond-style or
//     cgroup-only cell) → derived from the root container's task state
//     (the legacy behavior; root *is* the workload here)
//   - cgroup present, at least one non-root container in spec → derived
//     from the union of non-root container task states (Ready if any
//     non-root is active; Exited if every non-root exited cleanly; Error
//     if any non-root exited non-zero; Unknown if any non-root reads back
//     Unknown — the defensive read-side check that pairs with #301). The
//     derivation never yields Stopped: that label is reserved for the
//     operator stop/kill verbs (#1267).
//
// Container statuses are populated up front so the derivation reads
// them straight from the snapshot instead of re-querying containerd
// (and so `kuke get cell` sees the same per-container view the
// reconciler decided on).
//
// Wind-down side-effects: when the derivation flips to Exited and the
// cell has at least one non-root container in spec and the root
// container task is still running, the reconciler kills the cell so the
// root container shell stops too (a crash → Error is sticky and left for
// the operator, so it does not wind down). Two flavors:
//
//   - AutoDelete=true: autoDeleteCell runs (KillCell + DeleteCell) and
//     the cell metadata is removed. Subsumes the per-cell
//     `kuke run --rm` watcher.
//   - AutoDelete=false: windDownCell runs (KillCell only). The cell
//     metadata is preserved in Exited state for the operator to
//     `kuke delete` explicitly — a long-lived `sleep infinity` root
//     does not get to hold the cell open after every workload has
//     exited.
//
// Both flavors are gated by the ReadyObserved latch so an in-flight
// CreateCell (cgroup created, non-root containers not yet registered
// → derivation reads Exited from "container does not exist") is not
// reaped before it ever reached Ready.
//
// Errors during kill/delete are returned to the caller so the loop's
// per-pass `Errors` slice records them and the cell is preserved for
// retry on the next tick (best-effort, like the watcher it replaces).
//
// Post-reboot cgroup healing (#855): when the cgroup check returns
// absent and the cell's persisted ReadyObserved latch is true (the cell
// was ever Ready), the reconciler re-runs ensureCellCgroup to
// re-create the cgroup directory and re-assert subtree controllers.
// Cells that never reached Ready (a half-CreateCell that crashed
// mid-way, gated by the same latch the wind-down path uses) are not
// promoted by the re-ensure.
func (r *Exec) ReconcileCell(cell intmodel.Cell) (intmodel.Cell, ReconcileOutcome, error) {
	defer r.lockCell(cell)()

	// Post-lock existence recheck (#1251). ReconcileCells snapshots the
	// cell list once per tick and iterates with the in-memory value
	// captured at list time. A `kuke delete cell` that completed while this
	// tick was blocked on the per-cell lock leaves us holding a pre-delete
	// snapshot whose ReadyObserved latch is still true — the cgroup-heal
	// branch below (#855) would then "heal" the missing cgroup and rewrite
	// the just-deleted metadata.json, silently resurrecting the cell. The
	// lock the #714 work added serializes the mutations but never re-read
	// disk after acquiring it; re-read now and short-circuit when the
	// metadata is gone. The post-reboot heal (#855) is unaffected: on a
	// genuine reboot the metadata survives, so GetCell succeeds here.
	if _, err := r.GetCell(cell); err != nil {
		if errors.Is(err, errdefs.ErrCellNotFound) {
			r.logger.DebugContext(r.ctx, "reconcile: cell metadata gone, skipping",
				"cell", cell.Metadata.Name,
				"realm", cell.Spec.RealmName,
				"space", cell.Spec.SpaceName,
				"stack", cell.Spec.StackName)
			return cell, ReconcileOutcome{Vanished: true}, nil
		}
		r.logger.DebugContext(r.ctx, "reconcile: failed to re-read cell after lock",
			"cell", cell.Metadata.Name, "error", err)
	}

	originalStatus := cell.Status

	// Sticky terminal states are preserved instead of re-derived
	// (cellStateIsSticky): CellStateFailed (a kukeon bring-up fault, #407) and
	// CellStateError (a workload crash, #1267). Keeping these sticky stops a
	// subsequent populate that reads back "no task" from quietly downgrading
	// them via a re-derivation tick — and so cellStateAutoDeleteTriggers (which
	// excludes both) cannot be bypassed. Container statuses are still refreshed
	// below so `kuke get cell -o yaml` shows the latest per-container view, but
	// the cell-level state is left alone until the operator runs
	// `kuke delete cell`.
	if cellStateIsSticky(originalStatus.State) {
		if err := r.populateCellContainerStatuses(&cell); err != nil {
			r.logger.DebugContext(r.ctx, "populate container statuses failed",
				"cell", cell.Metadata.Name, "error", err)
		}
		updated := !containerStatusesEqual(originalStatus.Containers, cell.Status.Containers) ||
			observedGenerationBehind(originalStatus, cell.Metadata.Generation)
		if updated {
			persisted, updateErr := r.persistCellStatusGuarded(cell)
			if updateErr != nil {
				return cell, ReconcileOutcome{}, fmt.Errorf("failed to update cell metadata: %w", updateErr)
			}
			updated = persisted
		}
		return cell, ReconcileOutcome{Updated: updated}, nil
	}

	cgroupExists, cgroupErr := r.ExistsCgroup(cell)
	if cgroupErr != nil {
		r.logger.DebugContext(r.ctx, "failed to check cell cgroup",
			"cell", cell.Metadata.Name, "error", cgroupErr)
	}
	// Mirror RefreshCell (refresh.go:369-374): the background reconcile
	// loop must close the same loop the on-demand `kuke refresh` path
	// already does, so cgroupReady falsifies on every cell whose
	// /sys/fs/cgroup/.../<cell> tree is gone (the post-reboot signature
	// for tmpfs-backed cgroups). Without this, `cgroupReady: true` stays
	// stamped after a host reboot wipes the cgroup, and `kuke status`'s
	// state-consistency check is silently bypassed (#853).
	cell.Status.CgroupReady = cgroupErr == nil && cgroupExists

	// Heal a wiped cell cgroup on the reconcile path so cells whose cgroup
	// the host reboot wiped (#854) recover without an operator running
	// `kuke start <name>` per cell. Gated on the persisted
	// ReadyObserved latch so a half-CreateCell that crashed before the
	// cell ever reached Ready is not promoted by the re-ensure — that
	// in-flight CreateCell will finish its own ensureCellCgroup path under
	// the per-cell lock. ensureCellCgroup is idempotent and already
	// re-asserts subtree controllers via enableCellControllers, covering
	// the second half of the post-#314/#328 contract. Heal failure is
	// logged and the tick continues so the next pass retries (#855).
	if cgroupErr == nil && !cgroupExists && originalStatus.ReadyObserved {
		healedCell, healErr := r.ensureCellCgroup(cell)
		if healErr != nil {
			r.logger.WarnContext(r.ctx, "reconcile: failed to heal missing cell cgroup",
				"cell", cell.Metadata.Name, "error", healErr)
		} else {
			cell = healedCell
			// ensureCellCgroup writes CgroupPath / SubtreeControllers but
			// not CgroupReady — pair the heal with the same truth the
			// probe above stamps so a healed cell ships cgroupReady:true
			// in the same tick instead of the false-then-true flip the
			// next pass would otherwise correct (#861's invariant).
			cell.Status.CgroupReady = true
			r.logger.InfoContext(r.ctx, "reconcile: healed missing cell cgroup",
				"cell", cell.Metadata.Name,
				"path", cell.Status.CgroupPath)
		}
	}

	// Populate container statuses up front so the non-root-driven
	// derivation can read them from the snapshot. The root container
	// path also benefits — populate already queried it, so a single
	// pass of GetContainerState calls covers both the per-container
	// status array and the cell-state derivation.
	if err := r.populateCellContainerStatuses(&cell); err != nil {
		r.logger.DebugContext(r.ctx, "populate container statuses failed",
			"cell", cell.Metadata.Name, "error", err)
	}

	// Run derivation whenever the filesystem check succeeded, even when the
	// cgroup is absent — a missing cgroup with surviving containerd
	// containers and no tasks is the post-reboot signature (#543), and
	// staying at Unknown there bypasses cellStateAutoDeleteTriggers /
	// shouldWindDownCell forever. The cgroupErr != nil path still parks at
	// Unknown because we have no positive observation of either side.
	// The CreateCell-race protection that used to come from the cgroup
	// gate is now carried entirely by latchReadyObserved: a cell that has
	// never been observed Ready cannot be reaped, regardless of what
	// derivation reads back.
	newState := intmodel.CellStateUnknown
	if cgroupErr == nil {
		newState = r.deriveCellState(cell)
	}

	cell.Status.State = newState
	cell.Status.ReadyObserved = latchReadyObserved(
		originalStatus.ReadyObserved, originalStatus.State, newState)

	// Surface the workload exit that drove a fresh runtime CellStateError
	// (#1206, renamed from Failed in #1267) so `kuke get cell -o yaml` carries
	// the failing container + exit code/signal, mirroring the startup-path
	// breadcrumb markCellFailed writes. Only stamp on the fresh transition: an
	// already-Error cell is short-circuited by the cellStateIsSticky guard
	// above and never reaches here, so its original Reason/Message is preserved.
	if newState == intmodel.CellStateError && originalStatus.State != intmodel.CellStateError {
		stampCellFailure(&cell)
	}

	// Restart-on-exit pass (#1233, phase 1 of the #1151 epic). Before the
	// wind-down / auto-delete reap gate, relaunch any non-root container whose
	// task exited in a way its RestartPolicy says must restart. Placed here so
	// it sees the freshly derived state and freshly populated container
	// statuses, but short-circuits the reap gate below for the cells it acts on.
	//
	// A non-zero exit derives CellStateError, which is sticky — once persisted,
	// the cellStateIsSticky short-circuit at the top of ReconcileCell would make
	// this pass unreachable. While a restart is owed (fired or backoff-deferred)
	// the cell is converging, not failed, so it is held at non-sticky
	// CellStateDegraded instead: the reap gate is suppressed, the crash
	// breadcrumb cleared, and a later tick re-derives Ready (workload back up) or
	// settles the sticky Error only once the restart budget exhausts (restartNone
	// from the on-failure cap). Failed cells (a kukeon bring-up fault) are already
	// excluded by the sticky short-circuit; never-Ready cells are excluded by the
	// ReadyObserved gate inside maybeRestartExitedContainers.
	cell, restartResult, restartErr := r.maybeRestartExitedContainers(cell)
	if restartErr != nil {
		// StartContainer already flipped the cell to Failed (sticky) via its
		// own markCellFailed defer (#407); surface the error so the reconcile
		// loop records it for this cell and retries on the next tick.
		return cell, ReconcileOutcome{}, restartErr
	}
	switch restartResult {
	case restartFired:
		// A fired restart suppresses wind-down / auto-delete for this tick. The
		// cell is mid-convergence (a workload crashed and was just relaunched),
		// so hold it at non-sticky Degraded rather than the sticky Error the
		// crash derived — Error would short-circuit this pass next tick and
		// strand the restart loop. The next tick re-derives Ready once the
		// relaunched container is observed up (or Degraded again if it re-crashed).
		// restartFired returns early, so persist the Degraded transition here.
		cell.Status.State = intmodel.CellStateDegraded
		cell.Status.Reason = originalStatus.Reason
		cell.Status.Message = originalStatus.Message
		persisted, persistErr := r.persistCellStatusGuarded(cell)
		if persistErr != nil {
			return cell, ReconcileOutcome{}, fmt.Errorf("failed to update cell metadata: %w", persistErr)
		}
		return cell, ReconcileOutcome{Updated: persisted}, nil
	case restartDeferred:
		// A restart is required but the per-container backoff has not elapsed.
		// Suppress the reap gate and hold the cell at non-sticky Degraded so the
		// (sticky) Error transition is not persisted — otherwise the
		// cellStateIsSticky short-circuit would strand the cell and this pass
		// could never fire once the backoff clears. Degraded persists honestly
		// (the workload is down with a relaunch pending); ReadyObserved stays
		// latched and the crash breadcrumb is cleared. Falls through to the
		// common persist below.
		cell.Status.State = intmodel.CellStateDegraded
		cell.Status.ReadyObserved = originalStatus.ReadyObserved
		cell.Status.Reason = originalStatus.Reason
		cell.Status.Message = originalStatus.Message
	case restartNone:
		// No restart is owed for this exit — the restartFired/restartDeferred
		// cases above already returned. Auto-delete (`--rm` / Spec.AutoDelete) is
		// an explicit "delete the cell once the workload exits" directive that
		// overrides the restartPolicy *preserve* gate: a `never` (or clean-exit
		// `on-failure`) container that restartPolicyPermitsCellReap would
		// otherwise keep in Stopped is reaped here instead. It does NOT override a
		// restart-requiring policy — `always` (any exit) and `on-failure`
		// (non-zero exit) fire the restart pass above and win this tick, so the
		// cell is relaunched, not deleted. The auto-delete branch is therefore NOT
		// gated on the RestartPolicy reap check, but it is reached only once no
		// restart is owed (cf. `docker run --rm` cleaning up a finished workload).
		if shouldAutoDeleteCell(cell.Spec.AutoDelete, newState, cell.Status.ReadyObserved) {
			return r.autoDeleteCell(cell)
		}

		// RestartPolicy gate (#1003): per-container restartPolicy vetoes the
		// wind-down kill so a workload authored with `restartPolicy: never`
		// (or `on-failure` that exited cleanly) is left in Stopped state instead
		// of disappearing under the next tick. Empty/unset defaults to `never` —
		// an exited non-root container preserves the cell, matching the
		// Kubernetes default restartPolicy. This governs only the non-auto-delete
		// path; a `--rm` cell is already handled above. See
		// restartPolicyPermitsCellReap.
		if restartPolicyPermitsCellReap(cell) && shouldWindDownCell(cell, newState) {
			return r.windDownCell(cell)
		}
	}

	updated := false
	if originalStatus.State != cell.Status.State {
		updated = true
	}
	if originalStatus.ReadyObserved != cell.Status.ReadyObserved {
		updated = true
	}
	if originalStatus.CgroupReady != cell.Status.CgroupReady {
		updated = true
	}
	if !containerStatusesEqual(originalStatus.Containers, cell.Status.Containers) {
		updated = true
	}
	if observedGenerationBehind(originalStatus, cell.Metadata.Generation) {
		updated = true
	}

	if updated {
		persisted, updateErr := r.persistCellStatusGuarded(cell)
		if updateErr != nil {
			return cell, ReconcileOutcome{}, fmt.Errorf("failed to update cell metadata: %w", updateErr)
		}
		updated = persisted
	}
	return cell, ReconcileOutcome{Updated: updated}, nil
}

// windDownCell is the non-AutoDelete counterpart to autoDeleteCell:
// when a cell's non-root workloads have all exited, kill the cell
// (KillCell handles the root task plus CNI detach plus cleanup) so
// the root container shell does not zombie. Cell metadata is left
// in place — the operator runs `kuke delete cell` explicitly when
// they are done with it. KillCell already populates and persists
// container statuses, so the caller need not double-write metadata.
//
// KillCell is best-effort and idempotent (workload containers that
// are already gone are no-ops); errors surface so the next reconcile
// pass retries.
//
// KillCell stamps CellStateStopped, but the cell got here because its
// workloads self-exited cleanly (the reconciler derived CellStateExited, the
// only state that fires shouldWindDownCell). Stopped is non-sticky (#1267), so
// the next reconcile tick re-derives the now-root-dead cell back to Exited —
// the operator-stop-vs-self-exit label settles correctly without a second
// write here.
func (r *Exec) windDownCell(cell intmodel.Cell) (intmodel.Cell, ReconcileOutcome, error) {
	r.logger.InfoContext(r.ctx, "reconcile: winding down cell after non-root workloads exited",
		"cell", cell.Metadata.Name,
		"realm", cell.Spec.RealmName,
		"space", cell.Spec.SpaceName,
		"stack", cell.Spec.StackName,
		"state", cell.Status.State)

	updatedCell, err := r.killCellLocked(cell)
	if err != nil {
		return cell, ReconcileOutcome{}, fmt.Errorf("wind-down: kill cell: %w", err)
	}
	return updatedCell, ReconcileOutcome{Updated: true}, nil
}

// autoDeleteCell runs the kill+delete sequence the per-cell watcher used
// to drive. KillCell is best-effort and idempotent (the root task may
// already be gone — that's the trigger condition); a kill failure is
// surfaced as the error return and the cell is left alone, so the next
// reconcile pass retries. Once kill succeeds, DeleteCell tears down
// containers, cgroup, and metadata; an error here also bubbles up for
// retry on the next tick.
func (r *Exec) autoDeleteCell(cell intmodel.Cell) (intmodel.Cell, ReconcileOutcome, error) {
	r.logger.InfoContext(r.ctx, "reconcile: auto-deleting cell",
		"cell", cell.Metadata.Name,
		"realm", cell.Spec.RealmName,
		"space", cell.Spec.SpaceName,
		"stack", cell.Spec.StackName,
		"state", cell.Status.State)

	if _, err := r.killCellLocked(cell); err != nil {
		return cell, ReconcileOutcome{}, fmt.Errorf("auto-delete: kill cell: %w", err)
	}
	if err := r.deleteCellLocked(cell); err != nil {
		return cell, ReconcileOutcome{}, fmt.Errorf("auto-delete: delete cell: %w", err)
	}
	return cell, ReconcileOutcome{Deleted: true}, nil
}

// cellStateIsSticky reports whether the reconciler must preserve the cell's
// persisted state instead of re-deriving from cgroup + container task state.
// Two states qualify:
//
//   - CellStateFailed (#407): a kukeon bring-up fault. Stays Failed until the
//     operator runs `kuke delete cell`, regardless of what containerd reports
//     for its (now-killed) containers.
//   - CellStateError (#1267): a workload crash. Inherits Failed's stickiness so
//     the crashed cell is preserved with its exit code intact for the operator
//     rather than being re-derived away on a later tick.
//
// Stickiness is a *derivation* contract, not a restart gate: it only stops the
// background reconcile loop from re-deriving the persisted state away. An
// operator-initiated restart (`kuke run`/`restart`/`start` -> controller.StartCell
// -> runner.RecreateCell/StartCell) drives the cell straight to Ready under the
// per-cell lock the reconciler also takes, so a sticky Error/Failed cell is
// terminal-for-derivation but still operator-restartable (#1268). markCellReady
// then clears the failure breadcrumb on that transition.
//
// CellStateStopped is deliberately NOT sticky: the `kuke run --rm` reap and the
// non-AutoDelete wind-down both reach Stopped via KillCell, then rely on the
// next reconcile tick re-deriving the now-terminal cell to Exited (so `--rm`
// auto-deletes and a wound-down cell settles at the clean-exit label). Making
// Stopped sticky would freeze those paths. The operator-stop-vs-self-exit
// distinction is carried by the derivation never *producing* Stopped (#1267) —
// a persisted Stopped originates only from the operator stop/kill verbs.
//
// Extracted as a pure function so the stickiness predicate is unit-testable
// without spinning up a runner + containerd fake.
func cellStateIsSticky(s intmodel.CellState) bool {
	return s == intmodel.CellStateFailed ||
		s == intmodel.CellStateError
}

// cellStateAutoDeleteTriggers returns true for the post-reconcile cell
// states that mean "the root container is no longer running and the cell
// has run its course successfully", which is the trigger AutoDelete cares
// about. Only CellStateExited qualifies (#1267) — a cell whose workloads all
// exited 0 of their own accord, so `kuke run --rm` reaps a cleanly-finished
// job. Excluded states:
//
//   - Unknown: a transient containerd hiccup that flips a cell to Unknown
//     should not nuke the cell — wait for the next pass.
//   - Error (#1267, inheriting #407's Failed contract): a workload crash is
//     sticky and preserved. `kuke run --rm` does not auto-clean it — `--rm`
//     only reaps a *successful* run that exited cleanly (Exited). Preserving
//     the cell on a crash keeps the diagnostic surface (spec, captured logs,
//     exit code) intact until the operator runs `kuke delete cell`.
//   - Failed (#407): a kukeon bring-up fault, sticky for the same reason.
//   - Stopped (#1267): an operator-initiated stop is not a job that "ran its
//     course" — the operator chose to interrupt it, so `--rm` leaves it for
//     explicit `kuke delete cell`.
func cellStateAutoDeleteTriggers(s intmodel.CellState) bool {
	return s == intmodel.CellStateExited
}

// latchReadyObserved is the one-way latch the reconciler uses to decide
// whether a cell has ever been Ready. A cell that has never reached
// Ready must not be auto-deleted: GetContainerState returns NotCreated
// for a not-yet-existing container (the "container does not exist in
// containerd" branch in container_state.go), which the cell-level
// derivation treats as Stopped, and the reconciler ticking
// inside the gap between cgroup creation and root-container
// registration in CreateCell would otherwise reap an in-flight cell.
//
// Inputs that latch ReadyObserved true:
//   - the prior persisted ReadyObserved (the latch survives across
//     ticks and across daemon restarts via CellStatus serialization),
//   - newState == Ready (the current observation),
//   - originalStatus.State == Ready (a synchronous Start persisted
//     Ready into the cell metadata before this reconciler tick saw it,
//     even on the first tick after a restart),
//   - newState/originalState == Degraded (#1233 follow-up): a cell whose
//     first observed state is Degraded — a sibling crashed before the cell
//     ever derived Ready — has still come up (a peer workload is active /
//     the cell is mid-convergence), so the latch must close. Without this the
//     restart pass's ReadyObserved gate would skip it and the crashed sibling
//     would never be restarted.
func latchReadyObserved(prior bool, originalState, newState intmodel.CellState) bool {
	return prior ||
		newState == intmodel.CellStateReady ||
		originalState == intmodel.CellStateReady ||
		newState == intmodel.CellStateDegraded ||
		originalState == intmodel.CellStateDegraded
}

// shouldAutoDeleteCell returns true when the reconciler should kick off
// AutoDelete cleanup. The latch (readyObserved) is the gate that
// prevents an in-flight CreateCell from being reaped before the root
// container has come up; see latchReadyObserved.
func shouldAutoDeleteCell(autoDelete bool, newState intmodel.CellState, readyObserved bool) bool {
	return autoDelete && cellStateAutoDeleteTriggers(newState) && readyObserved
}

// shouldWindDownCell decides whether the reconciler should kill the
// cell (root container + leftover plumbing) because its non-root
// workloads have all exited. Same trigger states + ReadyObserved gate
// as shouldAutoDeleteCell, plus two extra preconditions:
//
//   - At least one non-root container in spec. Without this, the cell
//     is kukeond-style — the root *is* the workload, and killing it
//     because "the workload is gone" would be circular. Those cells
//     stay in their derived state and the operator stops them
//     explicitly via `kuke daemon stop` / `kuke daemon reset`.
//   - The root container task is still running. Once the root is dead
//     there is nothing to wind down; firing again would be a wasted
//     KillCell on every subsequent tick. Read from the freshly
//     populated container statuses so this check costs no extra
//     containerd round-trip.
func shouldWindDownCell(cell intmodel.Cell, newState intmodel.CellState) bool {
	if !cellStateAutoDeleteTriggers(newState) || !cell.Status.ReadyObserved {
		return false
	}
	if !hasNonRootContainerSpec(cell.Spec.Containers) {
		return false
	}
	rootSpec := findRootContainerSpec(cell)
	if rootSpec == nil {
		return false
	}
	return rootContainerStillRunning(rootSpec.ID, cell.Status.Containers)
}

// restartPolicyPermitsCellReap reports whether every terminally-exited
// non-root container in the cell carries a RestartPolicy that permits
// the cell-level wind-down kill to fire (#1003). The reconciler asks this
// gate before shouldWindDownCell — a single container's `never` (or
// `on-failure` with a clean exit) blocks the cell-level reap, preserving
// the workload in Stopped state so the operator can decide explicitly. The
// auto-delete (`--rm`) branch is NOT gated on this — an explicit AutoDelete
// reaps such a preserved exit anyway. It does not override a restart-requiring
// policy, which fires earlier in ReconcileCell and wins the tick (see
// ReconcileCell).
//
// Decision per terminally-exited non-root container, keyed on its
// ContainerStatus.ExitCode (populated by GetContainerObservation):
//
//   - "always"        → permit reap regardless of exit code.
//   - "on-failure"    → permit reap only when ExitCode != 0; a clean exit
//     (0) means the workload completed successfully and the cell stays.
//   - "" or "never"   → never permit reap. Empty is the default: an exited
//     non-root container preserves the cell in Stopped (kept for restart),
//     matching the Kubernetes default restartPolicy.
//
// Unknown values fall through to the permissive default to mirror the
// pre-#1003 behavior — typos and future-spec values do not silently
// strand cells in Stopped.
//
// Containers in non-terminal states (Ready/Pending/Paused/Unknown) are
// ignored: the cell-state derivation already gates on "every non-root
// terminal" before this function runs, so a non-terminal container being
// "permitted to reap" is meaningless.
func restartPolicyPermitsCellReap(cell intmodel.Cell) bool {
	statusByID := make(map[string]intmodel.ContainerStatus, len(cell.Status.Containers))
	for i := range cell.Status.Containers {
		statusByID[cell.Status.Containers[i].ID] = cell.Status.Containers[i]
	}
	for i := range cell.Spec.Containers {
		spec := cell.Spec.Containers[i]
		if spec.Root {
			continue
		}
		status, ok := statusByID[spec.ID]
		if !ok {
			continue
		}
		if !containerStateIsTerminal(status.State) {
			continue
		}
		if !restartPolicyPermitsContainerReap(spec.RestartPolicy, status.ExitCode) {
			return false
		}
	}
	return true
}

// restartPolicyPermitsContainerReap is the per-container half of
// restartPolicyPermitsCellReap. Pulled out so it can be exercised
// directly without building a full cell snapshot. See
// restartPolicyPermitsCellReap for the policy table.
func restartPolicyPermitsContainerReap(policy string, exitCode int) bool {
	switch policy {
	case intmodel.RestartPolicyAlways:
		return true
	case intmodel.RestartPolicyOnFailure:
		return exitCode != 0
	case "", intmodel.RestartPolicyNever:
		return false
	default:
		return true
	}
}

// containerStateIsTerminal reports whether the state means "the task
// is no longer running and will not run again on its own". Used by
// restartPolicyPermitsCellReap to scope its policy check to containers
// the reconciler is about to reap. Stopped, Failed, and NotCreated all
// count — the cell-state derivation already treats them equivalently
// (see deriveCellStateFromNonRootContainerStatuses).
func containerStateIsTerminal(s intmodel.ContainerState) bool {
	switch s {
	case intmodel.ContainerStateStopped,
		intmodel.ContainerStateExited,
		intmodel.ContainerStateError,
		intmodel.ContainerStateFailed,
		intmodel.ContainerStateNotCreated:
		return true
	}
	return false
}

// Restart-on-exit tuning (#1233, phase 1 of the #1151 epic). Hardcoded named
// constants this phase; the user-authored knobs are #1235's scope.
const (
	// restartBackoff is the minimum wall-clock interval between successive
	// reconciler-driven restarts of the same non-root container. It matches the
	// default KUKEOND_RECONCILE_INTERVAL (30s) so under the default cadence the
	// restart fires on the next reconcile tick that observes the terminal state,
	// while still flooring sub-interval thrash when the interval is configured
	// shorter than the backoff. The runner has no view of the configured
	// interval, so this is a fixed floor rather than a multiple of it.
	restartBackoff = 30 * time.Second

	// onFailureMaxRestarts caps how many times the reconciler relaunches an
	// `on-failure` container that keeps exiting non-zero before giving up and
	// leaving it terminal for the operator (#1233 ratified decision 2 — no
	// infinite retry). `always` / empty policies are uncapped: they restart on
	// every exit by contract. The counter is per restart attempt and resets
	// whenever the container is next observed running, so the cap bites a tight
	// crash loop (re-exits before a reconcile tick can observe it Ready) but not
	// a workload that runs for a while between crashes.
	onFailureMaxRestarts = 5
)

// containerRestartState is the runner-local bookkeeping the reconciler's
// restart-on-exit pass keeps per non-root container to enforce the backoff
// floor and the on-failure retry cap. See Exec.restartStates for why this is
// deliberately separate from the persisted ContainerStatus counters.
type containerRestartState struct {
	// attempts counts reconciler-driven restart attempts since the container
	// was last observed running. Drives the on-failure cap.
	attempts int
	// lastAttempt is when the restart pass last fired a relaunch for this
	// container. Drives the backoff floor.
	lastAttempt time.Time
}

// restartPolicyRequiresRestart is the per-container restart-trigger half of the
// reconciler's restart-on-exit pass (#1233), sibling to
// restartPolicyPermitsContainerReap. It encodes the policy rows:
//
//   - "always"        → restart on any exit.
//   - "on-failure"    → restart only on a non-zero exit; a clean exit (0) means
//     the workload completed successfully and is not restarted.
//   - "" or "never"   → never restart. Empty/unset defaults to `never`,
//     matching the Kubernetes default restartPolicy (modelhub/container.go):
//     a non-root cell that never set the field is NOT restarted on exit, it is
//     left for the reap gate (preserved in Stopped, or deleted under `--rm`).
//
// Unknown values fall through to the permissive default for the same reason
// restartPolicyPermitsContainerReap does — a typo or future-spec value should
// not silently strand a workload. The truth table currently coincides with
// restartPolicyPermitsContainerReap (both encode "this policy + exit triggers an
// action"), but the two are kept as named siblings: the restart pass and the
// reap gate are distinct decisions, and #1235's user-authored knobs will diverge
// them.
func restartPolicyRequiresRestart(policy string, exitCode int) bool {
	switch policy {
	case intmodel.RestartPolicyAlways:
		return true
	case intmodel.RestartPolicyOnFailure:
		return exitCode != 0
	case "", intmodel.RestartPolicyNever:
		return false
	default:
		return true
	}
}

// restartPassResult is the outcome of a single maybeRestartExitedContainers
// pass over a cell's non-root containers.
type restartPassResult int

const (
	// restartNone — nothing to restart this tick (no container requires a
	// restart, the cell never reached Ready, or every candidate's on-failure
	// cap is exhausted). The caller runs the normal reap gate.
	restartNone restartPassResult = iota
	// restartFired — at least one container was relaunched. The caller
	// suppresses the reap gate and returns Updated for this tick.
	restartFired
	// restartDeferred — at least one container requires a restart but its
	// backoff has not elapsed, and none fired. The caller suppresses the reap
	// gate and must avoid persisting the (sticky) Error transition so a later
	// tick can fire once the backoff clears.
	restartDeferred
)

// restartDecisionFor decides whether a terminally-exited container that its
// policy says must restart should fire now, wait for backoff, or give up
// against the on-failure cap. Reads (does not mutate) the per-container restart
// state under the lock.
func (r *Exec) restartDecisionFor(cell intmodel.Cell, containerID, policy string) restartPassResult {
	r.restartStatesMu.Lock()
	defer r.restartStatesMu.Unlock()

	st := r.restartStates[r.restartStateKey(cell, containerID)]
	if st == nil {
		// First observation of this exit: fire immediately, no prior backoff.
		return restartFired
	}
	if policy == intmodel.RestartPolicyOnFailure && st.attempts >= onFailureMaxRestarts {
		// on-failure cap exhausted: stop retrying. Reported as restartNone so
		// the caller falls through to the reap gate (the crashed workload
		// settles as Error and is preserved for the operator).
		return restartNone
	}
	if !st.lastAttempt.IsZero() && r.nowUTC().Sub(st.lastAttempt) < restartBackoff {
		return restartDeferred
	}
	return restartFired
}

// maybeRestartExitedContainers is the reconciler's restart-on-exit pass (#1233).
// For each non-root container whose task is terminally exited and whose
// RestartPolicy requires a restart, it relaunches the container via the
// restartContainer action (StartContainer in production) subject to the
// per-container backoff floor and the on-failure retry cap.
//
// Returns the (possibly relaunched) cell and a restartPassResult the caller uses
// to suppress the reap gate and decide persistence. Containers observed running
// have their restart bookkeeping cleared so a future exit starts from a clean
// slate (and the on-failure cap counts consecutive thrash, not lifetime
// restarts). A failed relaunch returns the error: restartContainer
// (StartContainer) has already flipped the cell to Failed (sticky) via its own
// markCellFailed defer, which stops further restarts.
//
// The caller (ReconcileCell) holds the per-cell lifecycle lock; StartContainer
// does not re-acquire it, matching the UpdateCell recreate path (#485).
func (r *Exec) maybeRestartExitedContainers(cell intmodel.Cell) (intmodel.Cell, restartPassResult, error) {
	// Never restart a cell that never came up (#1233 ReadyObserved gate). The
	// Failed-sticky short-circuit at the top of ReconcileCell already excludes
	// Failed cells before this pass runs.
	if !cell.Status.ReadyObserved {
		return cell, restartNone, nil
	}

	// A non-root workload can only be relaunched into a live cell: StartContainer
	// joins it to the root container's net/IPC/UTS namespaces and reads the root
	// task's PID. If the root is not running — e.g. post-reboot before the cgroup
	// heal / operator start brings the cell back up — leave the workloads alone;
	// cell-level recovery owns that path, not the per-container restart. Mirrors
	// the same root-liveness precondition shouldWindDownCell already enforces.
	if rootSpec := findRootContainerSpec(cell); rootSpec != nil &&
		!rootContainerStillRunning(rootSpec.ID, cell.Status.Containers) {
		return cell, restartNone, nil
	}

	statusByID := make(map[string]intmodel.ContainerStatus, len(cell.Status.Containers))
	for i := range cell.Status.Containers {
		statusByID[cell.Status.Containers[i].ID] = cell.Status.Containers[i]
	}

	result := restartNone
	for i := range cell.Spec.Containers {
		spec := cell.Spec.Containers[i]
		if spec.Root {
			continue
		}
		status, ok := statusByID[spec.ID]
		if !ok {
			continue
		}
		if !containerStateIsTerminal(status.State) {
			// Running (again): clear the backoff/cap bookkeeping so a later exit
			// is treated as fresh.
			r.clearRestartState(cell, spec.ID)
			continue
		}
		if !restartPolicyRequiresRestart(spec.RestartPolicy, status.ExitCode) {
			continue
		}

		switch r.restartDecisionFor(cell, spec.ID, spec.RestartPolicy) {
		case restartFired:
			started, startErr := r.restartContainer(cell, spec.ID)
			// Record the attempt regardless of outcome: a failed relaunch still
			// advances the cap so a permanently-unstartable container can't be
			// retried forever, and StartContainer's defer already marked the
			// cell Failed which ends the loop anyway.
			r.recordRestartAttempt(cell, spec.ID)
			if startErr != nil {
				return cell, result, fmt.Errorf("restart container %q: %w", spec.ID, startErr)
			}
			cell = started
			// Sole-writer counter bump (#1234): the relaunch succeeded, so record
			// it on the user-visible RestartCount/RestartTime. populate is a pure
			// preserver of these fields, so this pass is the only place they ever
			// advance — RestartCount = prior + 1 is an exact per-restart count that
			// stays monotonic and survives subsequent reconciliation. StartContainer
			// re-derived statuses from the persisted (preserved) prior, so the bump
			// lands on top of that prior; ReconcileCell's restartFired branch then
			// persists the incremented value via persistCellStatusGuarded.
			stampContainerRestart(&cell, spec.ID, r.nowUTC())
			result = restartFired
		case restartDeferred:
			// A fired restart in the same pass takes precedence; only downgrade
			// to deferred when nothing has fired yet.
			if result == restartNone {
				result = restartDeferred
			}
		case restartNone:
			// on-failure cap exhausted for this container — leave it terminal
			// and let the reap gate settle the cell.
		}
	}
	return cell, result, nil
}

// restartContainer invokes the relaunch action for a container the restart pass
// decided to restart. Falls through to (*Exec).StartContainer unless a test
// injected restartContainerFn.
func (r *Exec) restartContainer(cell intmodel.Cell, containerID string) (intmodel.Cell, error) {
	if r.restartContainerFn != nil {
		return r.restartContainerFn(cell, containerID)
	}
	return r.StartContainer(cell, containerID)
}

// stampContainerRestart records a fired restart on the user-visible
// ContainerStatus counters (#1234): it bumps RestartCount by exactly one over the
// preserved prior and stamps RestartTime to now. maybeRestartExitedContainers is
// the sole caller; populateCellContainerStatuses only preserves these fields, so
// the count is an exact per-restart tally that stays monotonic across ticks.
// Distinct from the runner-local recordRestartAttempt bookkeeping (backoff/cap
// gate state), which resets when the container is observed running again. A no-op
// if the container is absent from cell.Status.Containers.
func stampContainerRestart(cell *intmodel.Cell, containerID string, now time.Time) {
	for i := range cell.Status.Containers {
		if cell.Status.Containers[i].ID == containerID {
			cell.Status.Containers[i].RestartCount++
			cell.Status.Containers[i].RestartTime = now
			return
		}
	}
}

// restartStateKey derives the per-container key for the restart bookkeeping map
// from the cell's lock identity plus the container ID.
func (r *Exec) restartStateKey(cell intmodel.Cell, containerID string) string {
	return cellLockKey(cell) + "\x00" + containerID
}

// recordRestartAttempt bumps the attempt count and stamps the last-attempt time
// for a container the restart pass just relaunched.
func (r *Exec) recordRestartAttempt(cell intmodel.Cell, containerID string) {
	r.restartStatesMu.Lock()
	defer r.restartStatesMu.Unlock()

	if r.restartStates == nil {
		r.restartStates = make(map[string]*containerRestartState)
	}
	key := r.restartStateKey(cell, containerID)
	st := r.restartStates[key]
	if st == nil {
		st = &containerRestartState{}
		r.restartStates[key] = st
	}
	st.attempts++
	st.lastAttempt = r.nowUTC()
}

// clearRestartState drops a container's restart bookkeeping — called when the
// container is observed running again so the backoff and on-failure cap reset.
func (r *Exec) clearRestartState(cell intmodel.Cell, containerID string) {
	r.restartStatesMu.Lock()
	defer r.restartStatesMu.Unlock()

	if r.restartStates == nil {
		return
	}
	delete(r.restartStates, r.restartStateKey(cell, containerID))
}

// rootContainerStillRunning answers the "is the root task still
// alive" question by consulting the snapshot
// populateCellContainerStatuses just wrote. Returns false when the
// root status is missing (populate skipped or partial — be
// conservative and don't fire the wind-down kill on incomplete
// data).
func rootContainerStillRunning(rootID string, statuses []intmodel.ContainerStatus) bool {
	for i := range statuses {
		if statuses[i].ID != rootID {
			continue
		}
		switch statuses[i].State {
		case intmodel.ContainerStateReady,
			intmodel.ContainerStatePending,
			intmodel.ContainerStatePaused,
			intmodel.ContainerStatePausing:
			return true
		case intmodel.ContainerStateStopped,
			intmodel.ContainerStateExited,
			intmodel.ContainerStateError,
			intmodel.ContainerStateFailed,
			intmodel.ContainerStateNotCreated,
			intmodel.ContainerStateUnknown:
			return false
		}
	}
	return false
}

// markCellReady stamps a synchronous Ready transition: state and the
// ReadyObserved latch must close together. Without the eager latch
// close, a KillCell that races the first reconciler tick (e.g. `kuke
// run -a --rm` exiting attach within the reconcile interval) flips
// persisted state Ready → Stopped before any reconciler observation,
// leaving readyObserved=false on disk; subsequent ticks then see
// originalState=Stopped and shouldAutoDeleteCell never fires.
func markCellReady(cell *intmodel.Cell) {
	cell.Status.State = intmodel.CellStateReady
	cell.Status.ReadyObserved = true
	// A successful Ready transition supersedes any prior failure breadcrumb
	// (WorkloadFailed from a runtime crash, or StartCellFailed /
	// CreateCellFailed / RecreateCellFailed from a bring-up fault). Clearing it
	// here means an operator restart of an Error/Failed cell lands Ready with a
	// clean Status.Reason/Message instead of a stale crash record — every
	// runner start/recreate path funnels through startCellLocked -> markCellReady,
	// so this is the single point that owns the clear (#1268).
	cell.Status.Reason = ""
	cell.Status.Message = ""
}

// deriveCellState dispatches between the two derivation strategies
// the reconciler supports. See ReconcileCell's contract comment for
// the full state-machine description.
//
// kukeond-style cells (kuke-system / kukeon / kukeon / kukeond) and
// cgroup-only cells have no non-root container in spec, so they fall
// through to the legacy root-only derivation — without that fallback
// the kukeond cell would have no signal to derive its state from
// (its root *is* the workload, and there are no non-root entries to
// union over). User-workload cells with at least one non-root
// container in spec take the new derivation, which makes the cell
// follow its workloads' lifecycle: a long-lived `sleep infinity`
// root no longer holds the cell open after every workload has
// exited.
func (r *Exec) deriveCellState(cell intmodel.Cell) intmodel.CellState {
	if !hasNonRootContainerSpec(cell.Spec.Containers) {
		return r.deriveCellStateFromRootContainer(cell)
	}
	return deriveCellStateFromNonRootContainerStatuses(cell.Spec.Containers, cell.Status.Containers)
}

// deriveCellStateFromNonRootContainerStatuses unions the non-root
// container task states the populate pass just snapshotted. Pure
// (no containerd I/O) so it is straightforward to test.
//
// The "any non-root reads back Unknown ⇒ cell Unknown" branch is the
// defensive read-side check that pairs with #301 — a transient
// containerd hiccup or namespace-race-induced misread on a workload
// container must not flip the cell to Stopped and trigger the
// wind-down KillCell. Wait for the next reconcile pass to settle.
//
// "Container does not exist in containerd" is mapped to
// ContainerStateNotCreated by GetContainerState; this derivation treats
// NotCreated as a terminal state alongside Stopped/Failed, so a non-root
// workload that exited and was reaped naturally still counts toward "all
// stopped" here.
//
// Once every non-root workload is terminal, the exit triple decides between
// the two self-observed terminal cell states (#1206, refined by #1267): a
// workload that exited non-zero (or landed in ContainerStateError /
// ContainerStateFailed) settles the cell in CellStateError so a crash is
// distinguishable from a clean completion, which settles CellStateExited.
// Neither path yields CellStateStopped — that label is reserved for the
// operator stop/kill verbs, so a clean self-exit (Exited) stays distinct from
// an operator stop (Stopped). ExitCode is preserved across task reaping by
// populateCellContainerStatuses, so a failed-then-reaped (NotCreated) workload
// still carries its non-zero code here. CellStateError is sticky and excluded
// from auto-delete / wind-down (cellStateIsSticky, cellStateAutoDeleteTriggers),
// so the crashed cell is preserved with its exit code intact for the operator
// instead of being reaped.
func deriveCellStateFromNonRootContainerStatuses(
	specs []intmodel.ContainerSpec,
	statuses []intmodel.ContainerStatus,
) intmodel.CellState {
	nonRootSpecs := make(map[string]intmodel.ContainerSpec, len(specs))
	for i := range specs {
		if !specs[i].Root {
			nonRootSpecs[specs[i].ID] = specs[i]
		}
	}

	seen := 0
	anyActive := false
	anyUnknown := false
	anyFailed := false
	// anyDegraded tracks a terminal non-root workload that is down in a
	// non-clean way — crashed, or exited under a policy that should keep it
	// running. Paired with anyActive it distinguishes "some workloads up, some
	// down" (Degraded) from "all up" (Ready); see nonRootWorkloadDegraded.
	anyDegraded := false
	for i := range statuses {
		spec, ok := nonRootSpecs[statuses[i].ID]
		if !ok {
			continue
		}
		seen++
		switch statuses[i].State {
		case intmodel.ContainerStateReady,
			intmodel.ContainerStatePending,
			intmodel.ContainerStatePaused,
			intmodel.ContainerStatePausing:
			anyActive = true
		case intmodel.ContainerStateUnknown:
			anyUnknown = true
		case intmodel.ContainerStateStopped,
			intmodel.ContainerStateExited,
			intmodel.ContainerStateError,
			intmodel.ContainerStateFailed,
			intmodel.ContainerStateNotCreated:
			// terminal — does not contribute to active. A reaped/absent
			// workload (NotCreated) counts the same as a clean stop here,
			// unless its preserved exit triple marks it a failure (#1206).
			if containerExitedNonZero(statuses[i]) {
				anyFailed = true
			}
			if nonRootWorkloadDegraded(spec, statuses[i]) {
				anyDegraded = true
			}
		}
	}

	if seen < len(nonRootSpecs) {
		// populate skipped or didn't reach every non-root spec — be
		// defensive and stay Unknown so a partial-populate failure
		// can't reap a healthy cell.
		return intmodel.CellStateUnknown
	}
	if anyActive {
		// Some workloads are up. If another is down in a non-clean way the
		// cell is only partially healthy — Degraded, not Ready (#1233 follow-up).
		// A cleanly-completed oneshot (never / on-failure + exit 0) is NOT
		// degraded, so a sidecar+job cell stays Ready once the job finishes.
		if anyDegraded {
			return intmodel.CellStateDegraded
		}
		return intmodel.CellStateReady
	}
	if anyUnknown {
		return intmodel.CellStateUnknown
	}
	// Every non-root workload is terminal. A non-zero exit among them settles
	// the cell in CellStateError (a workload crash); an all-clean set settles
	// CellStateExited (a clean self-exit). The reconciler never yields
	// CellStateStopped here — Stopped is reserved for operator `kuke stop`/
	// `kill` (#1267). Note this is the derived label only: a persisted Stopped
	// is non-sticky, so the next tick re-derives an operator-stopped cell to
	// Exited — durable operator-stop preservation is deferred to #1268.
	if anyFailed {
		return intmodel.CellStateError
	}
	return intmodel.CellStateExited
}

// containerExitedNonZero reports whether a terminal container's observed exit
// indicates a workload failure rather than a clean completion (#1206, #1267):
// a non-zero exit code (the common case — populateCellContainerStatuses
// preserves it across task reaping), the explicit ContainerStateError
// observation (a stopped task whose code was non-zero), or ContainerStateFailed
// (kukeon's own container bring-up fault). A clean exit (code 0) — including a
// never-started container reaped to NotCreated with a zero code, or an explicit
// ContainerStateExited — is not a failure. Callers must only consult this for
// containers already known to be terminal.
func containerExitedNonZero(status intmodel.ContainerStatus) bool {
	return status.State == intmodel.ContainerStateFailed ||
		status.State == intmodel.ContainerStateError ||
		status.ExitCode != 0
}

// nonRootWorkloadDegraded reports whether a terminal non-root container leaves
// the cell degraded rather than cleanly complete: a non-zero exit (a crash), or
// a clean exit under a RestartPolicy that should keep it running (so the
// reconciler will restart it / it is meant to be up). A clean exit under a
// policy that does NOT restart (never, or on-failure that exited 0) is a
// successful completion — a finished oneshot, not a degradation. Callers must
// only consult this for containers already known to be terminal. Pairs the
// crash check (containerExitedNonZero) with the restart-trigger predicate so
// the two derivation sites and the restart pass agree on what "degraded" means.
func nonRootWorkloadDegraded(spec intmodel.ContainerSpec, status intmodel.ContainerStatus) bool {
	return containerExitedNonZero(status) ||
		restartPolicyRequiresRestart(spec.RestartPolicy, status.ExitCode)
}

// hasNonRootContainerSpec reports whether the cell has at least one
// non-root container in spec — i.e. whether the new union-of-non-root
// derivation applies.
func hasNonRootContainerSpec(specs []intmodel.ContainerSpec) bool {
	for i := range specs {
		if !specs[i].Root {
			return true
		}
	}
	return false
}

// deriveCellStateFromRootContainer resolves the cell's state from the root
// container's task. Returns Ready when no root container is configured (a
// cgroup-only cell counts as Ready by cgroup existence).
//
// When the root has terminally exited, the exit triple chooses between the
// two self-observed terminal cell states the same way the non-root path does
// (#1206, #1267): a non-zero root exit settles the cell Error (preserving the
// crash + exit code), a clean exit settles Exited. Neither yields Stopped —
// that label is reserved for the operator stop/kill verbs. The exit triple is read from the
// container-status snapshot populateCellContainerStatuses just wrote (both
// callers populate before deriving), which preserves the exit code across
// task reaping.
func (r *Exec) deriveCellStateFromRootContainer(cell intmodel.Cell) intmodel.CellState {
	rootSpec := findRootContainerSpec(cell)
	if rootSpec == nil {
		return intmodel.CellStateReady
	}
	state, err := r.GetContainerState(cell, rootSpec.ID)
	if err != nil {
		r.logger.DebugContext(r.ctx, "reconcile: failed to read root container state",
			"cell", cell.Metadata.Name, "container", rootSpec.ID, "error", err)
		return intmodel.CellStateUnknown
	}
	switch state {
	case intmodel.ContainerStateReady,
		intmodel.ContainerStatePending,
		intmodel.ContainerStatePaused,
		intmodel.ContainerStatePausing:
		return intmodel.CellStateReady
	case intmodel.ContainerStateStopped,
		intmodel.ContainerStateExited,
		intmodel.ContainerStateError,
		intmodel.ContainerStateFailed,
		intmodel.ContainerStateNotCreated:
		if rootContainerExitedNonZero(rootSpec.ID, cell.Status.Containers) {
			return intmodel.CellStateError
		}
		return intmodel.CellStateExited
	default:
		return intmodel.CellStateUnknown
	}
}

// rootContainerExitedNonZero looks up the root container's exit triple in the
// freshly populated status snapshot and reports whether it marks a failure
// (#1206). Returns false when the root's status is missing from the snapshot —
// a partial populate must not flip the cell to Failed on absent data.
func rootContainerExitedNonZero(rootID string, statuses []intmodel.ContainerStatus) bool {
	for i := range statuses {
		if statuses[i].ID == rootID {
			return containerExitedNonZero(statuses[i])
		}
	}
	return false
}

// reasonWorkloadFailed is the stable PascalCase Status.Reason label stamped
// when a runtime workload exit (not a startup-path error — markCellFailed owns
// those) drives a cell to CellStateFailed (#1206). Distinct from the
// StartCellFailed / CreateCellFailed / StartContainerFailed startup reasons so
// machine consumers can tell a post-Ready crash apart from a failed bring-up.
const reasonWorkloadFailed = "WorkloadFailed"

// cellFailureBreadcrumb returns the (Reason, Message) pair to stamp on a cell
// transitioning to CellStateFailed from a runtime workload exit (#1206).
// Reason is the stable machine label; Message names the failing container and
// its exit code/signal, falling back to a generic line if the failing
// container can't be pinpointed in the snapshot.
func cellFailureBreadcrumb(cell intmodel.Cell) (string, string) {
	msg := describeCellFailure(cell)
	if msg == "" {
		msg = "workload exited with a non-zero status"
	}
	return reasonWorkloadFailed, msg
}

// stampCellFailure writes the runtime-failure breadcrumb onto cell.Status in
// place. The cell's container-status snapshot must already be populated.
func stampCellFailure(cell *intmodel.Cell) {
	cell.Status.Reason, cell.Status.Message = cellFailureBreadcrumb(*cell)
}

// describeCellFailure builds the operator-facing Status.Message for a cell that
// derived CellStateError from a workload exit (#1206, renamed from Failed in
// #1267), naming the first failing container and its exit code/signal. Mirrors
// deriveCellState's dispatch so the reported container matches the one that
// drove the state: non-root workloads for cells that have them, otherwise the
// root. Returns "" when no failing container is found (defensive — callers only
// invoke this after derivation returned Error).
func describeCellFailure(cell intmodel.Cell) string {
	statusByID := make(map[string]intmodel.ContainerStatus, len(cell.Status.Containers))
	for i := range cell.Status.Containers {
		statusByID[cell.Status.Containers[i].ID] = cell.Status.Containers[i]
	}

	if hasNonRootContainerSpec(cell.Spec.Containers) {
		for i := range cell.Spec.Containers {
			spec := cell.Spec.Containers[i]
			if spec.Root {
				continue
			}
			if st, ok := statusByID[spec.ID]; ok &&
				containerStateIsTerminal(st.State) && containerExitedNonZero(st) {
				return formatContainerFailure(st)
			}
		}
		return ""
	}

	rootSpec := findRootContainerSpec(cell)
	if rootSpec != nil {
		if st, ok := statusByID[rootSpec.ID]; ok && containerExitedNonZero(st) {
			return formatContainerFailure(st)
		}
	}
	return ""
}

// formatContainerFailure renders a single container's exit triple as a human
// breadcrumb, surfacing the decoded signal when the exit code carries one
// (128+signum, per ContainerStatus.ExitSignal).
func formatContainerFailure(st intmodel.ContainerStatus) string {
	if st.ExitSignal != "" {
		return fmt.Sprintf("container %q terminated by %s (exit code %d)",
			st.ID, st.ExitSignal, st.ExitCode)
	}
	return fmt.Sprintf("container %q exited with code %d", st.ID, st.ExitCode)
}

func findRootContainerSpec(cell intmodel.Cell) *intmodel.ContainerSpec {
	for i := range cell.Spec.Containers {
		if cell.Spec.Containers[i].Root {
			return &cell.Spec.Containers[i]
		}
	}
	if cell.Spec.RootContainerID != "" {
		for i := range cell.Spec.Containers {
			if cell.Spec.Containers[i].ID == cell.Spec.RootContainerID {
				return &cell.Spec.Containers[i]
			}
		}
	}
	return nil
}

func containerStatusesEqual(a, b []intmodel.ContainerStatus) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].State != b[i].State {
			return false
		}
	}
	return true
}
