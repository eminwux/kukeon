//go:build !integration

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

//nolint:testpackage // exercises unexported restart-policy predicates and the GetContainerObservation exit-code plumbing
package runner

import (
	"errors"
	"fmt"
	"testing"

	cgroup2 "github.com/containerd/cgroups/v2/cgroup2"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// TestReconcileCell_AutoDeleteOverridesRestartPolicyNever is the end-to-end
// pin for the rule that `--rm` / Spec.AutoDelete deletes a cleanly Exited cell
// even when its non-root container carries an explicit `restartPolicy: never`:
// the policy only decides whether the workload restarts (#1557), so `never`
// never holds a cell back from the reap gate. `--rm` does NOT override a
// restart-requiring policy — that case is pinned by
// TestReconcileCell_RestartFiresAndSuppressesWindDown (`--rm` + `always`) and
// TestReconcileCell_AutoDeleteWithOnFailureRestartWins (`--rm` + `on-failure`
// on a non-zero exit), where the restart fires first and the cell is preserved.
// Modeled on TestReconcileCell_PostReboot_TransitionsToExited:
// the reboot-wiped tasks (no exit info ⇒ exit 0) derive CellStateExited, the
// auto-delete trigger, and the latched ReadyObserved opens the gate. An
// explicit `never` also keeps the restart-on-exit pass from firing, so the
// reconcile reaches the auto-delete branch.
func TestReconcileCell_AutoDeleteOverridesRestartPolicyNever(t *testing.T) {
	realm, space, stack, cellName := "default", "kukeon", "kukeon", "web"
	rootID, workloadID := "root", "workload"
	rootContainerdID := space + "_" + stack + "_" + cellName + "_" + rootID
	workloadContainerdID := space + "_" + stack + "_" + cellName + "_" + workloadID

	fake := &deleteCellFakeClient{
		loadCgroupFn: func(string, string) (*cgroup2.Manager, error) {
			return nil, errors.New("cgroup path does not exist")
		},
		existsContainerFn: func(_, _ string) (bool, error) { return true, nil },
		taskStatusFn: func(_, _ string) (containerd.Status, error) {
			return containerd.Status{}, fmt.Errorf("%w: %w", errdefs.ErrTaskNotFound, errors.New("task: not found"))
		},
	}
	r := newDeleteCellTestExec(t, fake)
	seedDeleteCellRealm(t, r, realm)
	seedStopKillSpace(t, r, realm, space) // killCellLocked resolves the space CNI config
	seedPostRebootCell(t, r, realm, space, stack, cellName, rootID, workloadID, rootContainerdID, workloadContainerdID)

	cell := intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: cellName},
		Spec: intmodel.CellSpec{
			ID:         cellName,
			RealmName:  realm,
			SpaceName:  space,
			StackName:  stack,
			AutoDelete: true, // --rm
			Containers: []intmodel.ContainerSpec{
				{ID: rootID, ContainerdID: rootContainerdID, Root: true},
				// Explicit never: no restart is owed, so --rm deletes the cell.
				{
					ID:            workloadID,
					ContainerdID:  workloadContainerdID,
					Root:          false,
					RestartPolicy: intmodel.RestartPolicyNever,
				},
			},
		},
		Status: intmodel.CellStatus{
			State:         intmodel.CellStateReady,
			ReadyObserved: true,
		},
	}

	_, outcome, err := r.ReconcileCell(cell)
	if err != nil {
		t.Fatalf("ReconcileCell: unexpected error: %v", err)
	}
	if !outcome.Deleted {
		t.Errorf(
			"ReconcileOutcome.Deleted = false, want true (--rm/AutoDelete must reap an exited cell regardless of an explicit restartPolicy: never)",
		)
	}

	// Sanity: the same cell WITHOUT AutoDelete is not deleted. Its root task
	// is already gone (the reboot wiped it), so there is nothing to wind down
	// either and the cell metadata stays for the operator.
	cell.Spec.AutoDelete = false
	seedPostRebootCell(t, r, realm, space, stack, cellName, rootID, workloadID, rootContainerdID, workloadContainerdID)
	_, outcome, err = r.ReconcileCell(cell)
	if err != nil {
		t.Fatalf("ReconcileCell (no --rm): unexpected error: %v", err)
	}
	if outcome.Deleted {
		t.Errorf(
			"ReconcileOutcome.Deleted = true, want false (without --rm, the reconciler must never delete the cell)",
		)
	}
}

// TestContainerStateIsTerminal locks the terminal-state set the restart pass
// and the root-death check scope their checks to. Stopped / Failed / NotCreated
// are terminal — they map to "the task is no longer running and will not
// run again on its own". The running and transient states are not.
func TestContainerStateIsTerminal(t *testing.T) {
	cases := []struct {
		state intmodel.ContainerState
		want  bool
	}{
		{intmodel.ContainerStateStopped, true},
		{intmodel.ContainerStateFailed, true},
		{intmodel.ContainerStateNotCreated, true},
		{intmodel.ContainerStateReady, false},
		{intmodel.ContainerStatePending, false},
		{intmodel.ContainerStatePaused, false},
		{intmodel.ContainerStatePausing, false},
		{intmodel.ContainerStateUnknown, false},
	}
	for _, tc := range cases {
		if got := containerStateIsTerminal(tc.state); got != tc.want {
			t.Errorf("containerStateIsTerminal(%v) = %v, want %v", tc.state, got, tc.want)
		}
	}
}

// TestGetContainerObservation_PopulatesExitCode confirms the
// GetContainerObservation plumbing wires containerd.Status.ExitStatus
// into the ContainerObservation.ExitCode column. Without this, the
// on-failure restart trigger (restartPolicyRequiresRestart) reads ExitCode=0
// from every stopped container and the policy collapses to Never.
func TestGetContainerObservation_PopulatesExitCode(t *testing.T) {
	realm, space, stack, cellName := "default", "kukeon", "kukeon", "web"
	containerID, containerdID := "root", "kukeon_kukeon_web_root"

	fake := &deleteCellFakeClient{
		existsContainerFn: func(_, _ string) (bool, error) { return true, nil },
		taskStatusFn: func(_, _ string) (containerd.Status, error) {
			return containerd.Status{Status: containerd.Stopped, ExitStatus: 42}, nil
		},
	}
	r := newDeleteCellTestExec(t, fake)
	seedDeleteCellRealm(t, r, realm)

	cell := containerStateCell(realm, space, stack, cellName, containerID, containerdID)

	obs, err := r.GetContainerObservation(cell, containerID)
	if err != nil {
		t.Fatalf("GetContainerObservation: unexpected error: %v", err)
	}
	// ExitStatus 42 is non-zero, so the stopped task maps to Error (#1267).
	if obs.State != intmodel.ContainerStateError {
		t.Errorf("GetContainerObservation State = %v, want Error", obs.State)
	}
	if obs.ExitCode != 42 {
		t.Errorf(
			"GetContainerObservation ExitCode = %d, want 42 (the OnFailure policy gate keys on this — a zeroed exit code collapses on-failure to never)",
			obs.ExitCode,
		)
	}
}

// TestGetContainerObservation_ZeroExitOnNonTerminalBranches locks the
// non-TaskStatus-success branches (NotCreated + ErrTaskNotFound) at
// ExitCode=0. The contract: ExitCode is only meaningful when TaskStatus
// returned successfully; every other return path leaves it zero so the
// on-failure restart trigger treats it as a clean exit (no restart).
func TestGetContainerObservation_ZeroExitOnNonTerminalBranches(t *testing.T) {
	realm, space, stack, cellName := "default", "kukeon", "kukeon", "web"
	containerID, containerdID := "root", "kukeon_kukeon_web_root"

	fake := &deleteCellFakeClient{
		existsContainerFn: func(_, _ string) (bool, error) { return false, nil },
	}
	r := newDeleteCellTestExec(t, fake)
	seedDeleteCellRealm(t, r, realm)

	cell := containerStateCell(realm, space, stack, cellName, containerID, containerdID)

	obs, err := r.GetContainerObservation(cell, containerID)
	if err != nil {
		t.Fatalf("GetContainerObservation: unexpected error: %v", err)
	}
	if obs.State != intmodel.ContainerStateNotCreated {
		t.Errorf("GetContainerObservation State = %v, want NotCreated", obs.State)
	}
	if obs.ExitCode != 0 {
		t.Errorf("GetContainerObservation ExitCode = %d, want 0 on the NotCreated branch", obs.ExitCode)
	}
}
