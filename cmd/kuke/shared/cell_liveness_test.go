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

package shared_test

import (
	"strings"
	"testing"

	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// readyGet builds a healthy Ready+task-live GetCellResult — the only
// passing input to the guard. Helper isolates the boilerplate so each
// state-specific case below only diffs the field it cares about.
func readyGet() kukeonv1.GetCellResult {
	return kukeonv1.GetCellResult{
		Cell:                     v1beta1.CellDoc{Status: v1beta1.CellStatus{State: v1beta1.CellStateReady}},
		MetadataExists:           true,
		RootContainerExists:      true,
		RootContainerTaskRunning: true,
	}
}

func TestGuardCellTaskLiveness_ReadyTaskLive_Passes(t *testing.T) {
	if err := kukeshared.GuardCellTaskLiveness(readyGet(), "c1"); err != nil {
		t.Fatalf("guard returned %v on healthy Ready+task-live cell, want nil", err)
	}
}

// TestGuardCellTaskLiveness_ReadyTaskDead_DivergedError pins #683's
// existing message verbatim — operator muscle memory keys on "diverged"
// and "kuke delete cell". The generalized #852 guard must not regress
// this branch when broadening to non-Ready states.
func TestGuardCellTaskLiveness_ReadyTaskDead_DivergedError(t *testing.T) {
	get := readyGet()
	get.RootContainerTaskRunning = false

	err := kukeshared.GuardCellTaskLiveness(get, "c1")
	if err == nil {
		t.Fatal("guard returned nil on Ready+task-dead, want divergence error")
	}
	if got := err.Error(); !strings.Contains(got, "diverged") {
		t.Errorf("error %q missing %q wording", got, "diverged")
	}
	if got := err.Error(); !strings.Contains(got, "kuke delete cell c1") {
		t.Errorf("error %q missing `kuke delete cell c1` recovery pointer", got)
	}
}

func TestGuardCellTaskLiveness_NotCreated_PointsAtRun(t *testing.T) {
	get := kukeonv1.GetCellResult{MetadataExists: false}

	err := kukeshared.GuardCellTaskLiveness(get, "kukeon-pm-0")
	if err == nil {
		t.Fatal("guard returned nil on !MetadataExists, want NotCreated error")
	}
	if got := err.Error(); !strings.Contains(got, "does not exist") {
		t.Errorf("error %q missing %q wording", got, "does not exist")
	}
	if got := err.Error(); !strings.Contains(got, "kuke run") {
		t.Errorf("error %q missing `kuke run` recovery pointer", got)
	}
	if got := err.Error(); !strings.Contains(got, "--name kukeon-pm-0") {
		t.Errorf("error %q missing `--name kukeon-pm-0` recovery pointer", got)
	}
}

// TestGuardCellTaskLiveness_Stopped_PointsAtStart is the regression that
// motivated #852: the wind-down flow leaves the cell at Stopped with an
// orphan socket inode, and the guard previously short-circuited on any
// non-Ready state. The new branch must surface the "start it first"
// pointer so the operator's next move is `kuke start cell`, not
// `connection refused` on the next dial.
func TestGuardCellTaskLiveness_Stopped_PointsAtStart(t *testing.T) {
	get := readyGet()
	get.Cell.Status.State = v1beta1.CellStateStopped
	get.RootContainerTaskRunning = false

	err := kukeshared.GuardCellTaskLiveness(get, "kukeon-pm-0")
	if err == nil {
		t.Fatal("guard returned nil on Stopped cell, want start-it-first error")
	}
	if got := err.Error(); !strings.Contains(got, "Stopped") {
		t.Errorf("error %q missing %q state name", got, "Stopped")
	}
	if got := err.Error(); !strings.Contains(got, "kuke start cell kukeon-pm-0") {
		t.Errorf("error %q missing `kuke start cell kukeon-pm-0` recovery pointer", got)
	}
}

// TestGuardCellTaskLiveness_Failed_PointsAtDelete covers the Failed
// branch's delete-then-rerun pointer (parity with Ready+task-dead). A
// Failed cell is sticky per the reconciler so only a delete clears it.
func TestGuardCellTaskLiveness_Failed_PointsAtDelete(t *testing.T) {
	get := readyGet()
	get.Cell.Status.State = v1beta1.CellStateFailed
	get.RootContainerTaskRunning = false

	err := kukeshared.GuardCellTaskLiveness(get, "c1")
	if err == nil {
		t.Fatal("guard returned nil on Failed cell, want delete-it-first error")
	}
	if got := err.Error(); !strings.Contains(got, "Failed") {
		t.Errorf("error %q missing %q state name", got, "Failed")
	}
	if got := err.Error(); !strings.Contains(got, "kuke delete cell c1") {
		t.Errorf("error %q missing `kuke delete cell c1` recovery pointer", got)
	}
}

// TestGuardCellTaskLiveness_Unknown_PointsAtDelete covers the Unknown
// branch (transient containerd hiccup or a missing cgroup with surviving
// container records). The pointer is the same delete-then-rerun as
// Failed — there is no clean attach path either way.
func TestGuardCellTaskLiveness_Unknown_PointsAtDelete(t *testing.T) {
	get := readyGet()
	get.Cell.Status.State = v1beta1.CellStateUnknown
	get.RootContainerTaskRunning = false

	err := kukeshared.GuardCellTaskLiveness(get, "c1")
	if err == nil {
		t.Fatal("guard returned nil on Unknown cell, want delete-it-first error")
	}
	if got := err.Error(); !strings.Contains(got, "Unknown") {
		t.Errorf("error %q missing %q state name", got, "Unknown")
	}
	if got := err.Error(); !strings.Contains(got, "kuke delete cell c1") {
		t.Errorf("error %q missing `kuke delete cell c1` recovery pointer", got)
	}
}

// TestGuardCellTaskLiveness_Pending_PointsAtDelete: Pending is the
// reconciler's transient "mid-creation" label; from the operator's
// perspective at attach time it means the cell is not yet usable. Treat
// it like Failed/Unknown — point at delete-then-rerun rather than wait
// indefinitely for a state transition that may never come.
func TestGuardCellTaskLiveness_Pending_PointsAtDelete(t *testing.T) {
	get := readyGet()
	get.Cell.Status.State = v1beta1.CellStatePending
	get.RootContainerTaskRunning = false

	err := kukeshared.GuardCellTaskLiveness(get, "c1")
	if err == nil {
		t.Fatal("guard returned nil on Pending cell, want delete-it-first error")
	}
	if got := err.Error(); !strings.Contains(got, "kuke delete cell c1") {
		t.Errorf("error %q missing `kuke delete cell c1` recovery pointer", got)
	}
}
