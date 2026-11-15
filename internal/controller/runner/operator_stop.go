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
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// reasonOperatorStopped is the stable PascalCase Status.Reason label stamped
// when a stop/kill interrupts a cell whose workload was still running (#1371).
// It is the persisted breadcrumb that keeps an operator-interrupted `--rm` /
// Spec.AutoDelete cell out of the reconciler's auto-delete gate: Stopped is
// non-sticky, so the next reconcile tick re-derives the all-terminal workload
// to Exited — the very state the gate triggers on — and the persisted label
// alone cannot tell "the operator interrupted it" from "it ran to completion".
// The breadcrumb survives that re-derivation (ReconcileCell only rewrites
// Reason on an Error transition) and is cleared by markCellReady, so the cell
// stays inspectable until the operator deletes or restarts it.
const reasonOperatorStopped = "OperatorStopped"

// operatorStoppedMessage is the human-readable Status.Message paired with
// reasonOperatorStopped.
const operatorStoppedMessage = "cell was stopped while its workload was still running; " +
	"auto-delete is suppressed until the cell is restarted or deleted"

// cellWorkloadActive reports whether the cell's workload is still running in
// the given container-status snapshot. For a cell with non-root containers in
// spec, the workload is the non-root set (any one active counts); for a
// root-only cell (kukeond-style), the root *is* the workload.
//
// The reconciler's own kills (wind-down, auto-delete) only fire once every
// non-root workload is terminal, so they never observe an active workload
// here; an operator stop/kill of a live cell does. That asymmetry is what
// lets stopCellLocked / killCellLocked stamp reasonOperatorStopped without
// knowing which caller drove them.
func cellWorkloadActive(cell intmodel.Cell) bool {
	statusByID := make(map[string]intmodel.ContainerState, len(cell.Status.Containers))
	for i := range cell.Status.Containers {
		statusByID[cell.Status.Containers[i].ID] = cell.Status.Containers[i].State
	}
	nonRoot := hasNonRootContainerSpec(cell.Spec.Containers)
	for i := range cell.Spec.Containers {
		spec := cell.Spec.Containers[i]
		if nonRoot == spec.Root {
			// Non-root cell: skip the root. Root-only cell: only the root counts.
			continue
		}
		state, ok := statusByID[spec.ID]
		if !ok {
			continue
		}
		if containerStateIsActive(state) {
			return true
		}
	}
	return false
}

// containerStateIsActive reports whether a container task is (or is about to
// be) running — the complement of the terminal set plus Unknown, mirroring
// rootContainerStillRunning's active states.
func containerStateIsActive(s intmodel.ContainerState) bool {
	switch s {
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
	return false
}

// markCellOperatorStopped stamps the operator-interrupt breadcrumb. Callers
// set it only when cellWorkloadActive was true immediately before the stop.
func markCellOperatorStopped(cell *intmodel.Cell) {
	cell.Status.Reason = reasonOperatorStopped
	cell.Status.Message = operatorStoppedMessage
}

// cellOperatorStopped reports whether the cell carries the operator-interrupt
// breadcrumb, i.e. it was stopped or killed while its workload was running and
// has not been restarted since.
func cellOperatorStopped(cell intmodel.Cell) bool {
	return cell.Status.Reason == reasonOperatorStopped
}

// workloadActiveBeforeStop refreshes the cell's container statuses from
// containerd and reports whether its workload is still running, so the
// stop/kill paths can decide whether this teardown interrupts a live workload.
// A populate failure falls back to the persisted snapshot — best-effort, the
// same discipline the rest of the stop path follows.
func (r *Exec) workloadActiveBeforeStop(cell intmodel.Cell) bool {
	probe := cell
	probe.Status.Containers = append([]intmodel.ContainerStatus(nil), cell.Status.Containers...)
	if err := r.populateCellContainerStatuses(&probe); err != nil {
		r.logger.DebugContext(r.ctx, "pre-stop populate container statuses failed",
			"cell", cell.Metadata.Name, "error", err)
		return cellWorkloadActive(cell)
	}
	return cellWorkloadActive(probe)
}
