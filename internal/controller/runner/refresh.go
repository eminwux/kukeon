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
//     non-root is active; Stopped if every non-root is Stopped/Failed/
//     non-existent; Unknown if any non-root reads back Unknown — the
//     defensive read-side check that pairs with #301)
//
// Container statuses are populated up front so the derivation reads
// them straight from the snapshot instead of re-querying containerd
// (and so `kuke get cell` sees the same per-container view the
// reconciler decided on).
//
// Wind-down side-effects: when the derivation flips to Stopped/Failed
// and the cell has at least one non-root container in spec and the
// root container task is still running, the reconciler kills the cell
// so the root container shell stops too. Two flavors:
//
//   - AutoDelete=true: autoDeleteCell runs (KillCell + DeleteCell) and
//     the cell metadata is removed. Subsumes the per-cell
//     `kuke run --rm` watcher.
//   - AutoDelete=false: windDownCell runs (KillCell only). The cell
//     metadata is preserved in Stopped state for the operator to
//     `kuke delete` explicitly — a long-lived `sleep infinity` root
//     does not get to hold the cell open after every workload has
//     exited.
//
// Both flavors are gated by the ReadyObserved latch so an in-flight
// CreateCell (cgroup created, non-root containers not yet registered
// → derivation reads Stopped from "container does not exist") is not
// reaped before it ever reached Ready.
//
// Errors during kill/delete are returned to the caller so the loop's
// per-pass `Errors` slice records them and the cell is preserved for
// retry on the next tick (best-effort, like the watcher it replaces).
func (r *Exec) ReconcileCell(cell intmodel.Cell) (intmodel.Cell, ReconcileOutcome, error) {
	defer r.lockCell(cell)()

	originalStatus := cell.Status

	// CellStateFailed is terminal (issue #407): once a cell has been marked
	// Failed by a startup-path error in StartCell / StartContainer, the
	// reconciler must keep the state sticky so a subsequent populate that
	// reads back "no task" doesn't quietly downgrade Failed to Unknown or
	// Stopped — and so cellStateAutoDeleteTriggers (which excludes Failed)
	// cannot be bypassed via a re-derivation tick. Container statuses are
	// still refreshed below so `kuke get cell -o yaml` shows the latest
	// per-container view, but the cell-level state is left alone until the
	// operator runs `kuke delete cell`.
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

	_, cgroupErr := r.ExistsCgroup(cell)
	if cgroupErr != nil {
		r.logger.DebugContext(r.ctx, "failed to check cell cgroup",
			"cell", cell.Metadata.Name, "error", cgroupErr)
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

	if shouldAutoDeleteCell(cell.Spec.AutoDelete, newState, cell.Status.ReadyObserved) {
		return r.autoDeleteCell(cell)
	}

	if shouldWindDownCell(cell, newState) {
		return r.windDownCell(cell)
	}

	updated := false
	if originalStatus.State != cell.Status.State {
		updated = true
	}
	if originalStatus.ReadyObserved != cell.Status.ReadyObserved {
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
// Currently a single state qualifies — CellStateFailed (the terminal Error
// state introduced for issue #407). A Failed cell stays Failed across every
// reconcile tick until the operator runs `kuke delete cell`, regardless of
// what containerd reports for its (now-killed) containers. Extracted as a
// pure function so the stickiness predicate is unit-testable without spinning
// up a runner + containerd fake.
func cellStateIsSticky(s intmodel.CellState) bool {
	return s == intmodel.CellStateFailed
}

// cellStateAutoDeleteTriggers returns true for the post-reconcile cell
// states that mean "the root container is no longer running and the cell
// has run its course successfully", which is the trigger AutoDelete cares
// about. Excluded states:
//
//   - Unknown: a transient containerd hiccup that flips a cell to Unknown
//     should not nuke the cell — wait for the next pass.
//   - Failed (issue #407): the terminal Error state is reserved for
//     startup-path failures and is deliberately sticky. `kuke run --rm`
//     does not auto-clean a Failed cell — `--rm` only takes effect on a
//     *successful* run that subsequently exits cleanly (i.e. Stopped).
//     Preserving the cell on failure keeps the diagnostic surface (spec,
//     captured logs) intact until the operator runs `kuke delete cell`.
func cellStateAutoDeleteTriggers(s intmodel.CellState) bool {
	return s == intmodel.CellStateStopped
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
//     even on the first tick after a restart).
func latchReadyObserved(prior bool, originalState, newState intmodel.CellState) bool {
	return prior ||
		newState == intmodel.CellStateReady ||
		originalState == intmodel.CellStateReady
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
func deriveCellStateFromNonRootContainerStatuses(
	specs []intmodel.ContainerSpec,
	statuses []intmodel.ContainerStatus,
) intmodel.CellState {
	nonRootIDs := make(map[string]struct{}, len(specs))
	for i := range specs {
		if !specs[i].Root {
			nonRootIDs[specs[i].ID] = struct{}{}
		}
	}

	seen := 0
	anyActive := false
	anyUnknown := false
	for i := range statuses {
		if _, ok := nonRootIDs[statuses[i].ID]; !ok {
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
			intmodel.ContainerStateFailed,
			intmodel.ContainerStateNotCreated:
			// terminal — does not contribute to active. A reaped/absent
			// workload (NotCreated) counts the same as a clean stop here.
		}
	}

	if seen < len(nonRootIDs) {
		// populate skipped or didn't reach every non-root spec — be
		// defensive and stay Unknown so a partial-populate failure
		// can't reap a healthy cell.
		return intmodel.CellStateUnknown
	}
	if anyActive {
		return intmodel.CellStateReady
	}
	if anyUnknown {
		return intmodel.CellStateUnknown
	}
	return intmodel.CellStateStopped
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
		intmodel.ContainerStateFailed,
		intmodel.ContainerStateNotCreated:
		return intmodel.CellStateStopped
	default:
		return intmodel.CellStateUnknown
	}
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
