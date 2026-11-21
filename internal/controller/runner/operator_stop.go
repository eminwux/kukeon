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
// when an operator stop/kill interrupts a cell that had not run its course
// (#1371, widened by #1557): a workload was still running, the cell was
// Degraded, or a workload restart was owed or deferred. It is the persisted
// breadcrumb that tells "the operator interrupted it" apart from "it ran to
// completion": ReconcileCell keeps a Stopped cell carrying it at Stopped
// instead of re-deriving the all-terminal workload to Exited / Error
// (cellStoppedIsDurable, #1486), and the auto-delete gate skips it (#1371). It
// is cleared by markCellReady, so the cell stays inspectable until the
// operator deletes or restarts it. See classifyOperatorStop for when it is set.
const reasonOperatorStopped = "OperatorStopped"

// operatorStoppedMessage is the human-readable Status.Message paired with
// reasonOperatorStopped.
const operatorStoppedMessage = "cell was stopped before its workload finished; " +
	"auto-delete is suppressed until the cell is restarted or deleted"

// cellWorkloadActive reports whether the cell's workload is still running in
// the given container-status snapshot. For a cell with non-root containers in
// spec, the workload is the non-root set (any one active counts); for a
// root-only cell (kukeond-style), the root *is* the workload.
//
// classifyOperatorStop consults it to decide whether a stop/kill interrupts a
// live workload, and cellStoppedIsDurable to tell whether a persisted Stopped
// is masking a task started outside kukeon.
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
// set it only when classifyOperatorStop returns operatorStopLabelInterrupted.
func markCellOperatorStopped(cell *intmodel.Cell) {
	cell.Status.Reason = reasonOperatorStopped
	cell.Status.Message = operatorStoppedMessage
}

// cellOperatorStopped reports whether the cell carries the operator-interrupt
// breadcrumb, i.e. an operator stop or kill interrupted it before its workload
// finished and it has not been restarted since.
func cellOperatorStopped(cell intmodel.Cell) bool {
	return cell.Status.Reason == reasonOperatorStopped
}

// operatorStopLabel is the cell label an operator stop/kill leaves behind.
type operatorStopLabel int

const (
	// operatorStopLabelStopped sets State=Stopped and leaves Reason/Message
	// untouched. This is the default for a cell that is neither running nor
	// already settled at a terminal label, and it must not stamp the
	// breadcrumb: `kuke run --rm` kills a cell whose workload has already
	// exited while the persisted state is still Ready, and a breadcrumb there
	// would stop the reconciler from ever auto-deleting it.
	operatorStopLabelStopped operatorStopLabel = iota
	// operatorStopLabelInterrupted sets State=Stopped plus the
	// reasonOperatorStopped breadcrumb, so the Stopped is durable (#1486).
	operatorStopLabelInterrupted
	// operatorStopLabelKeep leaves State, Reason and Message untouched: the
	// cell had already settled at Exited or Error, and the verb only tears
	// down what was still running (typically the root).
	operatorStopLabelKeep
)

// classifyOperatorStop decides, before teardown, the label a `kuke stop` /
// `kuke kill` leaves on the cell (#1557). cell is the GetCell result: its
// State, Reason, Message and ReadyObserved are the persisted values and its
// container statuses are fresh from containerd. First match wins:
//
//  1. Persisted Exited or Error, no workload active: keep the label.
//  2. Persisted Degraded; or a workload is active; or the cell has been Ready
//     and a terminal workload's restartPolicy owes it a restart: Stopped plus
//     the OperatorStopped breadcrumb.
//  3. Anything else: Stopped, Reason/Message untouched.
func classifyOperatorStop(cell intmodel.Cell) operatorStopLabel {
	active := cellWorkloadActive(cell)
	switch {
	case (cell.Status.State == intmodel.CellStateExited || cell.Status.State == intmodel.CellStateError) && !active:
		return operatorStopLabelKeep
	case cell.Status.State == intmodel.CellStateDegraded,
		active,
		cell.Status.ReadyObserved && cellHasRestartRequiringExit(cell):
		return operatorStopLabelInterrupted
	default:
		return operatorStopLabelStopped
	}
}

// applyOperatorStopLabel writes the label classifyOperatorStop chose.
func applyOperatorStopLabel(cell *intmodel.Cell, label operatorStopLabel) {
	switch label {
	case operatorStopLabelKeep:
		// Exited / Error and its reason stay as they were.
	case operatorStopLabelInterrupted:
		cell.Status.State = intmodel.CellStateStopped
		markCellOperatorStopped(cell)
	case operatorStopLabelStopped:
		cell.Status.State = intmodel.CellStateStopped
	}
}

// cellHasRestartRequiringExit reports whether some terminal non-root container
// exited in a way its restartPolicy says must be restarted. Pure: it reads the
// container-status snapshot only and ignores the backoff and retry-cap
// bookkeeping (see restartOwed for the reconciler's full check).
func cellHasRestartRequiringExit(cell intmodel.Cell) bool {
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
		if !ok || !containerStateIsTerminal(status.State) {
			continue
		}
		if restartPolicyRequiresRestart(spec.RestartPolicy, status.ExitCode) {
			return true
		}
	}
	return false
}
