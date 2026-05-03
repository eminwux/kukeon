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

//nolint:testpackage // tests the AutoDelete predicate which lives unexported
package runner

import (
	"testing"

	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// TestCellStateAutoDeleteTriggers locks down the predicate the reconciler
// uses to decide when Spec.AutoDelete=true should kick off cleanup. Stopped
// and Failed are the only triggers — Unknown is excluded so a transient
// containerd hiccup doesn't nuke the cell, and the running states stay
// untouched.
func TestCellStateAutoDeleteTriggers(t *testing.T) {
	cases := []struct {
		state intmodel.CellState
		want  bool
	}{
		{intmodel.CellStateStopped, true},
		{intmodel.CellStateFailed, true},
		{intmodel.CellStateReady, false},
		{intmodel.CellStatePending, false},
		{intmodel.CellStateUnknown, false},
	}
	for _, tc := range cases {
		if got := cellStateAutoDeleteTriggers(tc.state); got != tc.want {
			t.Errorf("cellStateAutoDeleteTriggers(%v) = %v, want %v",
				tc.state, got, tc.want)
		}
	}
}

// TestLatchReadyObserved pins the one-way latch behavior the reconciler
// uses to gate Spec.AutoDelete cleanup. The latch must:
//   - flip to true the first time newState==Ready is observed,
//   - flip to true when the persisted state is already Ready (covers
//     the first reconciler tick after a synchronous Start that wrote
//     Ready before any reconciler observation, including the first
//     tick after a daemon restart),
//   - never flip back to false once true (a cell that flapped through
//     Ready and is now Stopped is still a candidate for AutoDelete),
//   - stay false in the bug window from #269 — newState==Stopped on a
//     cell whose persisted state is Pending/Unknown, i.e. the "container
//     does not exist in containerd yet" branch during CreateCell.
func TestLatchReadyObserved(t *testing.T) {
	cases := []struct {
		name          string
		prior         bool
		originalState intmodel.CellState
		newState      intmodel.CellState
		want          bool
	}{
		{
			name:          "never_ready_mid_create_stays_false",
			prior:         false,
			originalState: intmodel.CellStatePending,
			newState:      intmodel.CellStateStopped,
			want:          false,
		},
		{
			name:          "never_ready_unknown_persisted_stays_false",
			prior:         false,
			originalState: intmodel.CellStateUnknown,
			newState:      intmodel.CellStateStopped,
			want:          false,
		},
		{
			name:          "first_ready_observation_flips_true",
			prior:         false,
			originalState: intmodel.CellStatePending,
			newState:      intmodel.CellStateReady,
			want:          true,
		},
		{
			name:          "persisted_ready_flips_true_even_when_now_stopped",
			prior:         false,
			originalState: intmodel.CellStateReady,
			newState:      intmodel.CellStateStopped,
			want:          true,
		},
		{
			name:          "prior_true_stays_true_through_stopped",
			prior:         true,
			originalState: intmodel.CellStateStopped,
			newState:      intmodel.CellStateStopped,
			want:          true,
		},
		{
			name:          "prior_true_stays_true_through_unknown",
			prior:         true,
			originalState: intmodel.CellStateUnknown,
			newState:      intmodel.CellStateUnknown,
			want:          true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := latchReadyObserved(tc.prior, tc.originalState, tc.newState)
			if got != tc.want {
				t.Errorf("latchReadyObserved(prior=%v, original=%v, new=%v) = %v, want %v",
					tc.prior, tc.originalState, tc.newState, got, tc.want)
			}
		})
	}
}

// TestMarkCellReady is the eager-latch contract synchronous Ready
// writers (provisionNewCell, StartCell idempotent skip, StartCell
// happy path, StartContainer, RecreateCell) all rely on. State and
// ReadyObserved must close together — without that, a KillCell that
// races the first reconciler tick (e.g. `kuke run -a --rm` exiting
// attach inside the reconcile interval) flips persisted state Ready
// → Stopped before any reconciler observation, leaving
// readyObserved=false on disk. Subsequent ticks then see
// originalState=Stopped, latchReadyObserved returns false, and
// shouldAutoDeleteCell never fires (regression #275).
func TestMarkCellReady(t *testing.T) {
	cell := intmodel.Cell{Status: intmodel.CellStatus{
		State:         intmodel.CellStatePending,
		ReadyObserved: false,
	}}
	markCellReady(&cell)
	if cell.Status.State != intmodel.CellStateReady {
		t.Errorf("State = %v, want Ready", cell.Status.State)
	}
	if !cell.Status.ReadyObserved {
		t.Errorf("ReadyObserved = false, want true")
	}
}

// TestAutoDeleteSurvivesKillCellRace is the end-to-end invariant for
// #275: after a synchronous Ready write followed by a synchronous
// KillCell that flips state to Stopped (without touching the latch),
// the persisted ReadyObserved must still be true so the next
// reconciler tick gates AutoDelete on. Models the on-disk handoff
// between the runner's synchronous writes and the reconciler's
// shouldAutoDeleteCell predicate.
func TestAutoDeleteSurvivesKillCellRace(t *testing.T) {
	cell := intmodel.Cell{Status: intmodel.CellStatus{
		State:         intmodel.CellStatePending,
		ReadyObserved: false,
	}}

	// Runner writes Ready synchronously (StartCell happy path).
	markCellReady(&cell)

	// Synchronous KillCell flips state to Stopped without touching
	// the latch — this is the persisted state the reconciler reads
	// on its first tick after the race.
	cell.Status.State = intmodel.CellStateStopped

	persisted := cell.Status

	// Reconciler tick: derive newState=Stopped from a not-yet-existing
	// containerd task, run the latch over the persisted snapshot.
	newState := intmodel.CellStateStopped
	latched := latchReadyObserved(persisted.ReadyObserved, persisted.State, newState)
	if !latched {
		t.Fatalf("latchReadyObserved = false; AutoDelete gate would never fire")
	}
	if !shouldAutoDeleteCell(true, newState, latched) {
		t.Errorf("shouldAutoDeleteCell = false; cell would be stranded in Stopped")
	}
}

// TestShouldAutoDeleteCell is the complete gate guarding AutoDelete
// cleanup: AutoDelete=true AND newState is a trigger (Stopped/Failed)
// AND the ReadyObserved latch is set. The Ready-gate row is the
// regression #269 calls out — without it, an in-flight CreateCell that
// has not yet registered its root container resolves to Stopped and the
// pre-fix code reaped the cell.
func TestShouldAutoDeleteCell(t *testing.T) {
	cases := []struct {
		name          string
		autoDelete    bool
		newState      intmodel.CellState
		readyObserved bool
		want          bool
	}{
		{
			name:          "fires_when_all_three_satisfied",
			autoDelete:    true,
			newState:      intmodel.CellStateStopped,
			readyObserved: true,
			want:          true,
		},
		{
			name:          "fires_on_failed_state",
			autoDelete:    true,
			newState:      intmodel.CellStateFailed,
			readyObserved: true,
			want:          true,
		},
		{
			name:          "blocked_when_autoDelete_false",
			autoDelete:    false,
			newState:      intmodel.CellStateStopped,
			readyObserved: true,
			want:          false,
		},
		{
			name:          "blocked_when_state_not_trigger",
			autoDelete:    true,
			newState:      intmodel.CellStateReady,
			readyObserved: true,
			want:          false,
		},
		{
			name:          "blocked_when_state_unknown",
			autoDelete:    true,
			newState:      intmodel.CellStateUnknown,
			readyObserved: true,
			want:          false,
		},
		{
			name:          "blocked_when_never_ready_regression_269",
			autoDelete:    true,
			newState:      intmodel.CellStateStopped,
			readyObserved: false,
			want:          false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldAutoDeleteCell(tc.autoDelete, tc.newState, tc.readyObserved)
			if got != tc.want {
				t.Errorf("shouldAutoDeleteCell(autoDelete=%v, new=%v, ready=%v) = %v, want %v",
					tc.autoDelete, tc.newState, tc.readyObserved, got, tc.want)
			}
		})
	}
}
