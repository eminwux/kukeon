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
	"testing"

	containerd "github.com/containerd/containerd/v2/client"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// TestRestartPolicyPermitsContainerReap pins the per-container half of
// the #1003 reap gate. The decision table:
//
//   - "" or "always"  → permit reap regardless of exit code (back-compat
//     and explicit Always both default to today's wind-down behavior).
//   - "on-failure"    → permit reap iff exit code is non-zero; clean exits
//     (0) preserve the cell.
//   - "never"         → never permit reap.
//   - unknown values  → permissive fallback so a typo or a future spec
//     value does not silently strand cells.
func TestRestartPolicyPermitsContainerReap(t *testing.T) {
	cases := []struct {
		name     string
		policy   string
		exitCode int
		want     bool
	}{
		{"empty_back_compat_clean_exit_permits", "", 0, true},
		{"empty_back_compat_failed_exit_permits", "", 1, true},
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
			name: "empty_policy_terminal_stopped_permits_back_compat",
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
			want: true,
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
					{ID: "work", Root: false, RestartPolicy: ""},
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
