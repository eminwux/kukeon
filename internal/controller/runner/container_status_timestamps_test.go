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

//nolint:testpackage // exercises unexported exitSignalName and the ExitTime plumbing on GetContainerObservation
package runner

import (
	"errors"
	"fmt"
	"testing"
	"time"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// TestExitSignalName pins the 128+signum decode containerd/runc use for a
// signal-terminated task (issue #1137). Ordinary exit codes (including 0 and
// the conventional "command not found"/"general error" codes below 128) carry
// no signal; 128+N in the valid signal range maps to the signal name via
// unix.SignalName; out-of-range values collapse back to "".
func TestExitSignalName(t *testing.T) {
	cases := []struct {
		name     string
		exitCode int
		want     string
	}{
		{"clean_exit_no_signal", 0, ""},
		{"general_error_no_signal", 1, ""},
		{"exactly_128_no_signal", 128, ""},
		{"sigkill", 137, "SIGKILL"}, // 128 + 9
		{"sigterm", 143, "SIGTERM"}, // 128 + 15
		{"sigint", 130, "SIGINT"},   // 128 + 2
		{"above_signal_range_no_signal", 128 + 65, ""},
		{"way_above_no_signal", 255, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := exitSignalName(tc.exitCode); got != tc.want {
				t.Errorf("exitSignalName(%d) = %q, want %q", tc.exitCode, got, tc.want)
			}
		})
	}
}

// TestGetContainerObservation_PopulatesExitTime confirms the
// GetContainerObservation plumbing threads containerd.Status.ExitTime into
// ContainerObservation.ExitTime, the source of ContainerStatus.FinishTime
// (issue #1137). Without this, a Stopped container's FinishTime stays at the
// zero value the long-standing TODO hardcoded.
func TestGetContainerObservation_PopulatesExitTime(t *testing.T) {
	realm, space, stack, cellName := "default", "kukeon", "kukeon", "web"
	containerID, containerdID := "root", "kukeon_kukeon_web_root"

	exitTime := time.Date(2026, 6, 7, 20, 35, 4, 0, time.UTC)
	fake := &deleteCellFakeClient{
		existsContainerFn: func(_, _ string) (bool, error) { return true, nil },
		taskStatusFn: func(_, _ string) (containerd.Status, error) {
			return containerd.Status{Status: containerd.Stopped, ExitStatus: 137, ExitTime: exitTime}, nil
		},
	}
	r := newDeleteCellTestExec(t, fake)
	seedDeleteCellRealm(t, r, realm)

	cell := containerStateCell(realm, space, stack, cellName, containerID, containerdID)

	obs, err := r.GetContainerObservation(cell, containerID)
	if err != nil {
		t.Fatalf("GetContainerObservation: unexpected error: %v", err)
	}
	if obs.State != intmodel.ContainerStateStopped {
		t.Errorf("GetContainerObservation State = %v, want Stopped", obs.State)
	}
	if !obs.ExitTime.Equal(exitTime) {
		t.Errorf("GetContainerObservation ExitTime = %v, want %v (sources ContainerStatus.FinishTime)", obs.ExitTime, exitTime)
	}
}

// TestGetContainerObservation_ZeroExitTimeOnRunningTask locks the
// running-task contract: a Running task carries no exit time, so ExitTime
// stays zero and FinishTime renders as the zero value (the container has not
// finished). Issue #1137.
func TestGetContainerObservation_ZeroExitTimeOnRunningTask(t *testing.T) {
	realm, space, stack, cellName := "default", "kukeon", "kukeon", "web"
	containerID, containerdID := "root", "kukeon_kukeon_web_root"

	fake := &deleteCellFakeClient{
		existsContainerFn: func(_, _ string) (bool, error) { return true, nil },
		taskStatusFn: func(_, _ string) (containerd.Status, error) {
			return containerd.Status{Status: containerd.Running}, nil
		},
	}
	r := newDeleteCellTestExec(t, fake)
	seedDeleteCellRealm(t, r, realm)

	cell := containerStateCell(realm, space, stack, cellName, containerID, containerdID)

	obs, err := r.GetContainerObservation(cell, containerID)
	if err != nil {
		t.Fatalf("GetContainerObservation: unexpected error: %v", err)
	}
	if obs.State != intmodel.ContainerStateReady {
		t.Errorf("GetContainerObservation State = %v, want Ready", obs.State)
	}
	if !obs.ExitTime.IsZero() {
		t.Errorf("GetContainerObservation ExitTime = %v, want zero on a Running task", obs.ExitTime)
	}
}

// TestPopulateCellContainerStatuses_PreservesExitInfoAcrossReap is the
// kill->reap consistency guard (issue #1137): in the normal lifecycle a
// SIGKILLed task is first observed Stopped with ExitStatus=137/ExitTime=T,
// then — once containerd reaps the task record — the ErrTaskNotFound -> Stopped
// branch reports a zero ExitCode/ExitTime while the container record survives.
// FinishTime, ExitCode and ExitSignal must all be preserved in lockstep across
// that reap; without the ExitCode preservation the status self-contradicts,
// showing FinishTime=T with exit code 0 / no signal.
func TestPopulateCellContainerStatuses_PreservesExitInfoAcrossReap(t *testing.T) {
	realm, space, stack, cellName := "default", "kukeon", "kukeon", "web"
	containerID, containerdID := "root", "kukeon_kukeon_web_root"

	exitTime := time.Date(2026, 6, 7, 20, 35, 4, 0, time.UTC)

	// Pull 1 reports the freshly-Stopped task with its exit info; pull 2 reports
	// the same container with its task record reaped (ErrTaskNotFound -> Stopped,
	// zero ExitCode/ExitTime).
	pull := 0
	fake := &deleteCellFakeClient{
		existsContainerFn: func(_, _ string) (bool, error) { return true, nil },
		taskStatusFn: func(_, _ string) (containerd.Status, error) {
			pull++
			if pull == 1 {
				return containerd.Status{Status: containerd.Stopped, ExitStatus: 137, ExitTime: exitTime}, nil
			}
			return containerd.Status{}, fmt.Errorf("%w: %w", errdefs.ErrTaskNotFound, errors.New("task: not found"))
		},
	}
	r := newDeleteCellTestExec(t, fake)
	seedDeleteCellRealm(t, r, realm)

	cell := containerStateCell(realm, space, stack, cellName, containerID, containerdID)

	// Pull 1: Stopped with exit info — the exit triple is stamped from the obs.
	if err := r.populateCellContainerStatuses(&cell); err != nil {
		t.Fatalf("populateCellContainerStatuses (pull 1): unexpected error: %v", err)
	}
	if len(cell.Status.Containers) != 1 {
		t.Fatalf("pull 1: got %d container statuses, want 1", len(cell.Status.Containers))
	}
	got := cell.Status.Containers[0]
	if !got.FinishTime.Equal(exitTime) || got.ExitCode != 137 || got.ExitSignal != "SIGKILL" {
		t.Fatalf("pull 1: FinishTime=%v ExitCode=%d ExitSignal=%q, want %v/137/SIGKILL",
			got.FinishTime, got.ExitCode, got.ExitSignal, exitTime)
	}

	// Pull 2: task record reaped — FinishTime/ExitCode/ExitSignal must all
	// survive rather than reset to T/0/"" (the self-contradictory status).
	if err := r.populateCellContainerStatuses(&cell); err != nil {
		t.Fatalf("populateCellContainerStatuses (pull 2): unexpected error: %v", err)
	}
	got = cell.Status.Containers[0]
	if got.State != intmodel.ContainerStateStopped {
		t.Errorf("pull 2: State = %v, want Stopped", got.State)
	}
	if !got.FinishTime.Equal(exitTime) {
		t.Errorf("pull 2: FinishTime = %v, want preserved %v across the reap", got.FinishTime, exitTime)
	}
	if got.ExitCode != 137 {
		t.Errorf("pull 2: ExitCode = %d, want preserved 137 across the reap (a reset 0 would contradict FinishTime=%v)", got.ExitCode, exitTime)
	}
	if got.ExitSignal != "SIGKILL" {
		t.Errorf("pull 2: ExitSignal = %q, want preserved SIGKILL across the reap", got.ExitSignal)
	}
}
