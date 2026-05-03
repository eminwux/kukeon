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

	containerd "github.com/containerd/containerd/v2/client"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/naming"
)

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
		// If check fails, state remains Unknown (already set)
	case cgroupExists:
		newStatus.State = intmodel.RealmStateReady
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
		case !namespaceExists && newStatus.State == intmodel.RealmStateReady:
			// Cgroup exists but namespace doesn't - realm is in inconsistent state
			newStatus.State = intmodel.RealmStateUnknown
		}
	}

	// Update if status changed
	updated := false
	if newStatus.State != originalStatus.State || newStatus.CgroupPath != originalStatus.CgroupPath {
		realm.Status = newStatus
		if updateErr := r.UpdateRealmMetadata(realm); updateErr != nil {
			return realm, false, fmt.Errorf("failed to update realm metadata: %w", updateErr)
		}
		updated = true
	}

	return realm, updated, nil
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
		case !cgroupExists && newStatus.State == intmodel.SpaceStateReady:
			// CNI exists but cgroup doesn't - space is in inconsistent state
			newStatus.State = intmodel.SpaceStateUnknown
		}
	}

	// Update if status changed
	updated := false
	if newStatus.State != originalStatus.State || newStatus.CgroupPath != originalStatus.CgroupPath {
		space.Status = newStatus
		if updateErr := r.UpdateSpaceMetadata(space); updateErr != nil {
			return space, false, fmt.Errorf("failed to update space metadata: %w", updateErr)
		}
		updated = true
	}

	return space, updated, nil
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
	default:
		// Cgroup doesn't exist - stack is unknown
		newStatus.State = intmodel.StackStateUnknown
	}

	// Update if status changed
	updated := false
	if newStatus.State != originalStatus.State || newStatus.CgroupPath != originalStatus.CgroupPath {
		stack.Status = newStatus
		if updateErr := r.UpdateStackMetadata(stack); updateErr != nil {
			return stack, false, fmt.Errorf("failed to update stack metadata: %w", updateErr)
		}
		updated = true
	}

	return stack, updated, nil
}

// RefreshCell refreshes the status of a cell and its containers.
// Returns the updated cell, number of containers updated, and any error.
func (r *Exec) RefreshCell(cell intmodel.Cell) (intmodel.Cell, int, error) {
	originalStatus := cell.Status
	newStatus := intmodel.CellStatus{
		State:      intmodel.CellStateUnknown, // Default to Unknown - will be updated if checks succeed
		CgroupPath: originalStatus.CgroupPath,
	}

	// Check cgroup existence
	cgroupExists, err := r.ExistsCgroup(cell)
	switch {
	case err != nil:
		r.logger.DebugContext(r.ctx, "failed to check cell cgroup", "cell", cell.Metadata.Name, "error", err)
		// If check fails, state remains Unknown (already set)
	case cgroupExists:
		newStatus.State = intmodel.CellStateReady
	default:
		// Cgroup doesn't exist - cell is unknown
		newStatus.State = intmodel.CellStateUnknown
	}

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

	// Update cell metadata if status changed or containers were updated
	cellUpdated := false
	if newStatus.State != originalStatus.State || newStatus.CgroupPath != originalStatus.CgroupPath {
		cell.Status = newStatus
		cellUpdated = true
	}

	if cellUpdated || containersUpdated > 0 {
		if updateErr := r.UpdateCellMetadata(cell); updateErr != nil {
			return cell, containersUpdated, fmt.Errorf("failed to update cell metadata: %w", updateErr)
		}
		return cell, containersUpdated, nil
	}

	return cell, containersUpdated, nil
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

	// Set namespace for containerd operations
	if r.ctrClient != nil {
		r.ctrClient.SetNamespace(namespace)
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
		containerExists, err = r.ExistsContainer(containerdID)
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
		taskStatus, err = r.ctrClient.TaskStatus(containerdID)
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
// considers the root container's task state — which is what flips a Ready
// cell to Stopped when an operator runs `ctr task kill` outside of kukeon.
//
// State derivation:
//   - cgroup check error or cgroup absent → Unknown
//   - cgroup present, no root container in spec → Ready (cgroup-only cell)
//   - cgroup present, root container task running/created/paused → Ready
//   - cgroup present, root container missing in containerd or task
//     stopped/unknown → Stopped
//
// Container statuses are also re-populated so callers (and `kuke get cell`)
// see up-to-date per-container state.
//
// AutoDelete: when Spec.AutoDelete is set and the freshly derived state is
// Stopped or Failed, the reconciler kills + deletes the cell instead of
// persisting the state transition. This subsumes the per-cell `kuke run
// --rm` watcher (#161 unblocks the move): a single, restart-resilient
// ticker is enough, no goroutine-per-cell, and an AutoDelete cell created
// before a daemon restart still gets cleaned up on the next tick after
// the daemon is back. Errors during kill/delete are returned to the
// caller so the loop's per-pass `Errors` slice records them and the cell
// is preserved for retry on the next tick (best-effort, like the watcher
// it replaces).
func (r *Exec) ReconcileCell(cell intmodel.Cell) (intmodel.Cell, ReconcileOutcome, error) {
	originalStatus := cell.Status
	newState := intmodel.CellStateUnknown

	cgroupExists, cgroupErr := r.ExistsCgroup(cell)
	switch {
	case cgroupErr != nil:
		r.logger.DebugContext(r.ctx, "failed to check cell cgroup",
			"cell", cell.Metadata.Name, "error", cgroupErr)
	case !cgroupExists:
		newState = intmodel.CellStateUnknown
	default:
		newState = r.deriveCellStateFromRootContainer(cell)
	}

	if err := r.populateCellContainerStatuses(&cell); err != nil {
		r.logger.DebugContext(r.ctx, "populate container statuses failed",
			"cell", cell.Metadata.Name, "error", err)
	}
	cell.Status.State = newState
	cell.Status.ReadyObserved = latchReadyObserved(
		originalStatus.ReadyObserved, originalStatus.State, newState)

	if shouldAutoDeleteCell(cell.Spec.AutoDelete, newState, cell.Status.ReadyObserved) {
		return r.autoDeleteCell(cell)
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

	if updated {
		if updateErr := r.UpdateCellMetadata(cell); updateErr != nil {
			return cell, ReconcileOutcome{}, fmt.Errorf("failed to update cell metadata: %w", updateErr)
		}
	}
	return cell, ReconcileOutcome{Updated: updated}, nil
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

	if _, err := r.KillCell(cell); err != nil {
		return cell, ReconcileOutcome{}, fmt.Errorf("auto-delete: kill cell: %w", err)
	}
	if err := r.DeleteCell(cell); err != nil {
		return cell, ReconcileOutcome{}, fmt.Errorf("auto-delete: delete cell: %w", err)
	}
	return cell, ReconcileOutcome{Deleted: true}, nil
}

// cellStateAutoDeleteTriggers returns true for the post-reconcile cell
// states that mean "the root container is no longer running", which is
// the trigger AutoDelete cares about. Unknown is deliberately excluded:
// a transient containerd hiccup that flips a cell to Unknown should not
// nuke the cell — wait for the next pass.
func cellStateAutoDeleteTriggers(s intmodel.CellState) bool {
	return s == intmodel.CellStateStopped || s == intmodel.CellStateFailed
}

// latchReadyObserved is the one-way latch the reconciler uses to decide
// whether a cell has ever been Ready. A cell that has never reached
// Ready must not be auto-deleted: GetContainerState returns Stopped
// for a not-yet-existing container (the "container does not exist in
// containerd" branch in container_state.go), and the reconciler ticking
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
	case intmodel.ContainerStateStopped, intmodel.ContainerStateFailed:
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
