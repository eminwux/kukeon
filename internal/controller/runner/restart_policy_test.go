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

// TestRestartPolicyPermitsContainerReap pins the per-container half of
// the #1003 reap gate. The decision table:
//
//   - "always"        → permit reap regardless of exit code.
//   - "on-failure"    → permit reap iff exit code is non-zero; clean exits
//     (0) preserve the cell.
//   - "" or "never"   → never permit reap. Empty is the default: an exited
//     non-root container preserves the cell, matching the Kubernetes
//     default restartPolicy.
//   - unknown values  → permissive fallback so a typo or a future spec
//     value does not silently strand cells.
func TestRestartPolicyPermitsContainerReap(t *testing.T) {
	cases := []struct {
		name     string
		policy   string
		exitCode int
		want     bool
	}{
		{"empty_default_clean_exit_blocks", "", 0, false},
		{"empty_default_failed_exit_blocks", "", 1, false},
		{"always_clean_exit_permits", intmodel.RestartPolicyAlways, 0, true},
		{"always_failed_exit_permits", intmodel.RestartPolicyAlways, 137, true},
		{"on_failure_clean_exit_blocks", intmodel.RestartPolicyOnFailure, 0, false},
		{"on_failure_failed_exit_permits", intmodel.RestartPolicyOnFailure, 1, true},
		{"on_failure_signal_exit_permits", intmodel.RestartPolicyOnFailure, 137, true},
		{"never_clean_exit_blocks", intmodel.RestartPolicyNever, 0, false},
		{"never_failed_exit_blocks", intmodel.RestartPolicyNever, 1, false},
		{"unknown_policy_permits_default", "no-such-policy", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := restartPolicyPermitsContainerReap(tc.policy, tc.exitCode); got != tc.want {
				t.Errorf("restartPolicyPermitsContainerReap(%q, %d) = %v, want %v",
					tc.policy, tc.exitCode, got, tc.want)
			}
		})
	}
}

// TestReconcileCell_AutoDeleteOverridesRestartPolicyNever is the end-to-end
// pin for the rule that `--rm` / Spec.AutoDelete overrides restartPolicy
// entirely: a cell that opted into auto-delete is reaped on exit even when its
// non-root container carries an explicit `restartPolicy: never` (which would
// otherwise block the wind-down path). This is what keeps `kuke run --rm`
// deterministic regardless of the cell's restart policy — matching
// `docker run --rm`. Modeled on TestReconcileCell_PostReboot_TransitionsToExited:
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
				// Explicit never would block the wind-down path — but --rm must win.
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

	// Sanity: the same cell WITHOUT AutoDelete must be preserved by the
	// explicit never (the wind-down path still honors restartPolicy).
	cell.Spec.AutoDelete = false
	seedPostRebootCell(t, r, realm, space, stack, cellName, rootID, workloadID, rootContainerdID, workloadContainerdID)
	_, outcome, err = r.ReconcileCell(cell)
	if err != nil {
		t.Fatalf("ReconcileCell (no --rm): unexpected error: %v", err)
	}
	if outcome.Deleted {
		t.Errorf(
			"ReconcileOutcome.Deleted = true, want false (without --rm, an explicit restartPolicy: never must preserve the cell)",
		)
	}
}

// TestContainerStateIsTerminal locks the terminal-state set the
// RestartPolicy gate scopes its check to. Stopped / Failed / NotCreated
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

// TestRestartPolicyPermitsCellReap pins the cell-level aggregation: a
// single non-root container that says "do not reap" must block the
// whole cell, but only terminal containers participate in the decision.
// Root containers are ignored — the wind-down kills the root anyway.
func TestRestartPolicyPermitsCellReap(t *testing.T) {
	cell := func(specs []intmodel.ContainerSpec, statuses []intmodel.ContainerStatus) intmodel.Cell {
		return intmodel.Cell{
			Spec:   intmodel.CellSpec{Containers: specs},
			Status: intmodel.CellStatus{Containers: statuses},
		}
	}

	cases := []struct {
		name string
		cell intmodel.Cell
		want bool
	}{
		{
			name: "empty_policy_terminal_stopped_blocks_default",
			cell: cell(
				[]intmodel.ContainerSpec{
					{ID: "root", Root: true},
					{ID: "work", Root: false, RestartPolicy: ""},
				},
				[]intmodel.ContainerStatus{
					{ID: "root", State: intmodel.ContainerStateReady},
					{ID: "work", State: intmodel.ContainerStateStopped, ExitCode: 0},
				},
			),
			want: false,
		},
		{
			name: "never_policy_terminal_blocks",
			cell: cell(
				[]intmodel.ContainerSpec{
					{ID: "root", Root: true},
					{ID: "work", Root: false, RestartPolicy: intmodel.RestartPolicyNever},
				},
				[]intmodel.ContainerStatus{
					{ID: "root", State: intmodel.ContainerStateReady},
					{ID: "work", State: intmodel.ContainerStateStopped, ExitCode: 0},
				},
			),
			want: false,
		},
		{
			name: "on_failure_clean_exit_blocks",
			cell: cell(
				[]intmodel.ContainerSpec{
					{ID: "root", Root: true},
					{ID: "work", Root: false, RestartPolicy: intmodel.RestartPolicyOnFailure},
				},
				[]intmodel.ContainerStatus{
					{ID: "root", State: intmodel.ContainerStateReady},
					{ID: "work", State: intmodel.ContainerStateStopped, ExitCode: 0},
				},
			),
			want: false,
		},
		{
			name: "on_failure_failed_exit_permits",
			cell: cell(
				[]intmodel.ContainerSpec{
					{ID: "root", Root: true},
					{ID: "work", Root: false, RestartPolicy: intmodel.RestartPolicyOnFailure},
				},
				[]intmodel.ContainerStatus{
					{ID: "root", State: intmodel.ContainerStateReady},
					{ID: "work", State: intmodel.ContainerStateStopped, ExitCode: 137},
				},
			),
			want: true,
		},
		{
			name: "any_never_in_multi_container_blocks_cell",
			cell: cell(
				[]intmodel.ContainerSpec{
					{ID: "root", Root: true},
					{ID: "worker", Root: false, RestartPolicy: intmodel.RestartPolicyAlways},
					{ID: "sidecar", Root: false, RestartPolicy: intmodel.RestartPolicyNever},
				},
				[]intmodel.ContainerStatus{
					{ID: "root", State: intmodel.ContainerStateReady},
					{ID: "worker", State: intmodel.ContainerStateStopped, ExitCode: 0},
					{ID: "sidecar", State: intmodel.ContainerStateStopped, ExitCode: 0},
				},
			),
			want: false,
		},
		{
			name: "non_terminal_container_ignored_for_policy_decision",
			cell: cell(
				[]intmodel.ContainerSpec{
					{ID: "root", Root: true},
					{ID: "still-running", Root: false, RestartPolicy: intmodel.RestartPolicyNever},
				},
				[]intmodel.ContainerStatus{
					{ID: "root", State: intmodel.ContainerStateReady},
					{ID: "still-running", State: intmodel.ContainerStateReady},
				},
			),
			want: true,
		},
		{
			name: "root_policy_ignored",
			cell: cell(
				[]intmodel.ContainerSpec{
					{ID: "root", Root: true, RestartPolicy: intmodel.RestartPolicyNever},
					{ID: "work", Root: false, RestartPolicy: intmodel.RestartPolicyAlways},
				},
				[]intmodel.ContainerStatus{
					{ID: "root", State: intmodel.ContainerStateStopped, ExitCode: 0},
					{ID: "work", State: intmodel.ContainerStateStopped, ExitCode: 0},
				},
			),
			want: true,
		},
		{
			name: "container_with_no_status_ignored",
			cell: cell(
				[]intmodel.ContainerSpec{
					{ID: "root", Root: true},
					{ID: "work", Root: false, RestartPolicy: intmodel.RestartPolicyNever},
				},
				[]intmodel.ContainerStatus{
					{ID: "root", State: intmodel.ContainerStateReady},
				},
			),
			want: true,
		},
		{
			name: "on_failure_notcreated_treated_as_clean_blocks",
			cell: cell(
				[]intmodel.ContainerSpec{
					{ID: "root", Root: true},
					{ID: "work", Root: false, RestartPolicy: intmodel.RestartPolicyOnFailure},
				},
				[]intmodel.ContainerStatus{
					{ID: "root", State: intmodel.ContainerStateReady},
					{ID: "work", State: intmodel.ContainerStateNotCreated, ExitCode: 0},
				},
			),
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := restartPolicyPermitsCellReap(tc.cell); got != tc.want {
				t.Errorf("restartPolicyPermitsCellReap(...) = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestGetContainerObservation_PopulatesExitCode confirms the
// GetContainerObservation plumbing wires containerd.Status.ExitStatus
// into the ContainerObservation.ExitCode column. Without this, the
// OnFailure gate in restartPolicyPermitsCellReap reads ExitCode=0
// from every stopped container and the gate collapses to Never.
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
// OnFailure gate falls back to its "clean exit blocks reap" branch.
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
