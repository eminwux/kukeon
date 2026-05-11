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
// is the only trigger — Unknown is excluded so a transient containerd
// hiccup doesn't nuke the cell, Failed is excluded per the issue #407
// contract (the terminal Error state is sticky and preserves diagnostic
// surface — `--rm` does not auto-clean it), and the running states stay
// untouched.
func TestCellStateAutoDeleteTriggers(t *testing.T) {
	cases := []struct {
		state intmodel.CellState
		want  bool
	}{
		{intmodel.CellStateStopped, true},
		{intmodel.CellStateFailed, false},
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
// races the first reconciler tick (e.g. `kuke run --rm` exiting
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
			// Issue #407: Failed is the terminal Error state and must
			// NOT trigger AutoDelete. `kuke run --rm` only auto-cleans
			// on a *successful* run that subsequently exits cleanly;
			// a startup-path failure preserves the cell so the operator
			// has a diagnostic surface to inspect.
			name:          "blocked_on_failed_state_per_issue_407",
			autoDelete:    true,
			newState:      intmodel.CellStateFailed,
			readyObserved: true,
			want:          false,
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

// TestHasNonRootContainerSpec is the kukeond-fallback gate: cells with no
// non-root container in spec (kuke-system / kukeon / kukeon / kukeond and
// other supervised cells, or cgroup-only cells) must NOT take the new
// non-root-driven derivation. The sentinel keeps that contract.
func TestHasNonRootContainerSpec(t *testing.T) {
	cases := []struct {
		name  string
		specs []intmodel.ContainerSpec
		want  bool
	}{
		{name: "empty", specs: nil, want: false},
		{
			name:  "only_root",
			specs: []intmodel.ContainerSpec{{ID: "kukeond", Root: true}},
			want:  false,
		},
		{
			name: "root_plus_workload",
			specs: []intmodel.ContainerSpec{
				{ID: "root", Root: true},
				{ID: "work", Root: false},
			},
			want: true,
		},
		{
			name:  "only_workload",
			specs: []intmodel.ContainerSpec{{ID: "work", Root: false}},
			want:  true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasNonRootContainerSpec(tc.specs); got != tc.want {
				t.Errorf("hasNonRootContainerSpec(%+v) = %v, want %v", tc.specs, got, tc.want)
			}
		})
	}
}

// TestDeriveCellStateFromNonRootContainerStatuses pins the union-of-non-root
// derivation introduced for #302. Five invariants:
//   - Any non-root in an active state ⇒ Ready (the cell is hosting work).
//   - Every non-root in a terminal state (Stopped/Failed/non-existent which
//     GetContainerState maps to Stopped) ⇒ Stopped (cell shell is no longer
//     hosting any workload — the wind-down trigger).
//   - Any non-root reading back Unknown blocks the Stopped derivation: a
//     transient containerd hiccup or namespace-race misread (#301) on a
//     workload container must not flip the cell to Stopped and reap a
//     healthy host.
//   - Root containers in cell.Status.Containers are ignored — the whole
//     point of this derivation is that the root is no longer the lifecycle
//     anchor for cells that have non-root workloads.
//   - When populate skipped or didn't reach every non-root spec (seen <
//     expected), the derivation stays Unknown rather than declaring
//     Stopped on a partial snapshot.
func TestDeriveCellStateFromNonRootContainerStatuses(t *testing.T) {
	cases := []struct {
		name     string
		specs    []intmodel.ContainerSpec
		statuses []intmodel.ContainerStatus
		want     intmodel.CellState
	}{
		{
			name: "any_active_workload_keeps_cell_ready",
			specs: []intmodel.ContainerSpec{
				{ID: "root", Root: true},
				{ID: "work-a", Root: false},
				{ID: "work-b", Root: false},
			},
			statuses: []intmodel.ContainerStatus{
				{ID: "root", State: intmodel.ContainerStateReady},
				{ID: "work-a", State: intmodel.ContainerStateStopped},
				{ID: "work-b", State: intmodel.ContainerStateReady},
			},
			want: intmodel.CellStateReady,
		},
		{
			name: "all_workloads_stopped_winds_cell_to_stopped",
			specs: []intmodel.ContainerSpec{
				{ID: "root", Root: true},
				{ID: "work-a", Root: false},
				{ID: "work-b", Root: false},
			},
			statuses: []intmodel.ContainerStatus{
				{ID: "root", State: intmodel.ContainerStateReady},
				{ID: "work-a", State: intmodel.ContainerStateStopped},
				{ID: "work-b", State: intmodel.ContainerStateStopped},
			},
			want: intmodel.CellStateStopped,
		},
		{
			name: "all_workloads_failed_also_stopped",
			specs: []intmodel.ContainerSpec{
				{ID: "root", Root: true},
				{ID: "work", Root: false},
			},
			statuses: []intmodel.ContainerStatus{
				{ID: "root", State: intmodel.ContainerStateReady},
				{ID: "work", State: intmodel.ContainerStateFailed},
			},
			want: intmodel.CellStateStopped,
		},
		{
			name: "workload_reads_back_unknown_blocks_stopped_defensive_against_301",
			specs: []intmodel.ContainerSpec{
				{ID: "root", Root: true},
				{ID: "work-a", Root: false},
				{ID: "work-b", Root: false},
			},
			statuses: []intmodel.ContainerStatus{
				{ID: "root", State: intmodel.ContainerStateReady},
				{ID: "work-a", State: intmodel.ContainerStateStopped},
				{ID: "work-b", State: intmodel.ContainerStateUnknown},
			},
			want: intmodel.CellStateUnknown,
		},
		{
			name: "running_root_is_ignored_when_workloads_are_all_stopped",
			specs: []intmodel.ContainerSpec{
				{ID: "root", Root: true},
				{ID: "work", Root: false},
			},
			statuses: []intmodel.ContainerStatus{
				// Root is still Ready (long-lived sleep infinity) — but
				// the workload is gone. This is the headline #302 case:
				// the long-lived root must NOT keep the cell open after
				// every workload exited.
				{ID: "root", State: intmodel.ContainerStateReady},
				{ID: "work", State: intmodel.ContainerStateStopped},
			},
			want: intmodel.CellStateStopped,
		},
		{
			name: "pending_workload_is_active",
			specs: []intmodel.ContainerSpec{
				{ID: "work", Root: false},
			},
			statuses: []intmodel.ContainerStatus{
				{ID: "work", State: intmodel.ContainerStatePending},
			},
			want: intmodel.CellStateReady,
		},
		{
			name: "paused_workload_is_active",
			specs: []intmodel.ContainerSpec{
				{ID: "work", Root: false},
			},
			statuses: []intmodel.ContainerStatus{
				{ID: "work", State: intmodel.ContainerStatePaused},
			},
			want: intmodel.CellStateReady,
		},
		{
			name: "missing_workload_status_is_treated_as_unknown_not_stopped",
			specs: []intmodel.ContainerSpec{
				{ID: "work-a", Root: false},
				{ID: "work-b", Root: false},
			},
			// populate didn't write a status entry for work-b — defensive
			// path: don't reap on a partial snapshot.
			statuses: []intmodel.ContainerStatus{
				{ID: "work-a", State: intmodel.ContainerStateStopped},
			},
			want: intmodel.CellStateUnknown,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := deriveCellStateFromNonRootContainerStatuses(tc.specs, tc.statuses)
			if got != tc.want {
				t.Errorf("deriveCellStateFromNonRootContainerStatuses(...) = %v, want %v",
					got, tc.want)
			}
		})
	}
}

// TestRootContainerStillRunning pins the snapshot read used by the
// wind-down gate. The wind-down kill is a no-op on every subsequent
// tick once the root task is dead, so this read is the cheap guard
// that keeps shouldWindDownCell from firing again. Missing-status
// reads false (the snapshot didn't see the root — be conservative
// and skip the kill).
func TestRootContainerStillRunning(t *testing.T) {
	cases := []struct {
		name     string
		rootID   string
		statuses []intmodel.ContainerStatus
		want     bool
	}{
		{
			name:   "ready_root_still_running",
			rootID: "root",
			statuses: []intmodel.ContainerStatus{
				{ID: "root", State: intmodel.ContainerStateReady},
			},
			want: true,
		},
		{
			name:   "pending_root_still_running",
			rootID: "root",
			statuses: []intmodel.ContainerStatus{
				{ID: "root", State: intmodel.ContainerStatePending},
			},
			want: true,
		},
		{
			name:   "paused_root_still_running",
			rootID: "root",
			statuses: []intmodel.ContainerStatus{
				{ID: "root", State: intmodel.ContainerStatePaused},
			},
			want: true,
		},
		{
			name:   "stopped_root_not_running",
			rootID: "root",
			statuses: []intmodel.ContainerStatus{
				{ID: "root", State: intmodel.ContainerStateStopped},
			},
			want: false,
		},
		{
			name:   "failed_root_not_running",
			rootID: "root",
			statuses: []intmodel.ContainerStatus{
				{ID: "root", State: intmodel.ContainerStateFailed},
			},
			want: false,
		},
		{
			name:   "unknown_root_not_running",
			rootID: "root",
			statuses: []intmodel.ContainerStatus{
				{ID: "root", State: intmodel.ContainerStateUnknown},
			},
			want: false,
		},
		{
			name:     "missing_root_status_not_running",
			rootID:   "root",
			statuses: []intmodel.ContainerStatus{{ID: "work", State: intmodel.ContainerStateReady}},
			want:     false,
		},
		{
			name:     "empty_status_not_running",
			rootID:   "root",
			statuses: nil,
			want:     false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rootContainerStillRunning(tc.rootID, tc.statuses); got != tc.want {
				t.Errorf("rootContainerStillRunning(%q, %+v) = %v, want %v",
					tc.rootID, tc.statuses, got, tc.want)
			}
		})
	}
}

// TestShouldWindDownCell pins the gate the reconciler uses to decide
// whether to KillCell a non-AutoDelete cell whose workloads have all
// exited. The gate must be:
//   - Same trigger states (Stopped/Failed only) as shouldAutoDeleteCell.
//   - ReadyObserved latch must be set (mirrors the CreateCell-protection
//     behavior of shouldAutoDeleteCell — a cell that never reached
//     Ready must not be reaped, regression #269).
//   - hasNonRootContainerSpec must be true. This is the kukeond
//     fallback: kukeond-style cells (root *is* the workload) keep
//     their legacy lifecycle and the operator stops them via
//     `kuke daemon stop`.
//   - The root container task must still be running. Once the root is
//     dead the kill becomes a wasted tick-after-tick KillCell call.
func TestShouldWindDownCell(t *testing.T) {
	rootRunningStatus := []intmodel.ContainerStatus{
		{ID: "root", State: intmodel.ContainerStateReady},
		{ID: "work", State: intmodel.ContainerStateStopped},
	}
	rootDeadStatus := []intmodel.ContainerStatus{
		{ID: "root", State: intmodel.ContainerStateStopped},
		{ID: "work", State: intmodel.ContainerStateStopped},
	}
	specsWithWork := []intmodel.ContainerSpec{
		{ID: "root", Root: true},
		{ID: "work", Root: false},
	}
	specsRootOnly := []intmodel.ContainerSpec{{ID: "root", Root: true}}

	cell := func(specs []intmodel.ContainerSpec, statuses []intmodel.ContainerStatus, ready bool) intmodel.Cell {
		return intmodel.Cell{
			Spec: intmodel.CellSpec{Containers: specs},
			Status: intmodel.CellStatus{
				ReadyObserved: ready,
				Containers:    statuses,
			},
		}
	}

	cases := []struct {
		name     string
		cell     intmodel.Cell
		newState intmodel.CellState
		want     bool
	}{
		{
			name:     "fires_when_all_three_satisfied",
			cell:     cell(specsWithWork, rootRunningStatus, true),
			newState: intmodel.CellStateStopped,
			want:     true,
		},
		{
			// Issue #407: Failed cells are already torn down at the
			// startup-failure transition (markCellFailedAfterStartupFailure
			// runs KillCell before stamping Failed), so the wind-down
			// predicate must NOT re-fire on them.
			name:     "blocked_on_failed_state_per_issue_407",
			cell:     cell(specsWithWork, rootRunningStatus, true),
			newState: intmodel.CellStateFailed,
			want:     false,
		},
		{
			name:     "blocked_when_state_not_trigger",
			cell:     cell(specsWithWork, rootRunningStatus, true),
			newState: intmodel.CellStateReady,
			want:     false,
		},
		{
			name:     "blocked_when_state_unknown",
			cell:     cell(specsWithWork, rootRunningStatus, true),
			newState: intmodel.CellStateUnknown,
			want:     false,
		},
		{
			name:     "blocked_when_never_ready",
			cell:     cell(specsWithWork, rootRunningStatus, false),
			newState: intmodel.CellStateStopped,
			want:     false,
		},
		{
			name:     "blocked_for_kukeond_style_cell_root_only",
			cell:     cell(specsRootOnly, []intmodel.ContainerStatus{{ID: "root", State: intmodel.ContainerStateReady}}, true),
			newState: intmodel.CellStateStopped,
			want:     false,
		},
		{
			name:     "blocked_when_root_already_dead_idempotency_guard",
			cell:     cell(specsWithWork, rootDeadStatus, true),
			newState: intmodel.CellStateStopped,
			want:     false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldWindDownCell(tc.cell, tc.newState); got != tc.want {
				t.Errorf("shouldWindDownCell(...) = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestCellStateIsSticky locks down the issue-#407 contract that the terminal
// Error state must be preserved across reconcile ticks. Failed is sticky;
// every other state is re-derivable. Pairs with the auto-delete/wind-down
// predicate tests above: together they ensure a Failed cell stays Failed
// until the operator runs `kuke delete cell`.
func TestCellStateIsSticky(t *testing.T) {
	cases := []struct {
		state intmodel.CellState
		want  bool
	}{
		{intmodel.CellStateFailed, true},
		{intmodel.CellStateStopped, false},
		{intmodel.CellStateReady, false},
		{intmodel.CellStatePending, false},
		{intmodel.CellStateUnknown, false},
	}
	for _, tc := range cases {
		if got := cellStateIsSticky(tc.state); got != tc.want {
			t.Errorf("cellStateIsSticky(%v) = %v, want %v",
				tc.state, got, tc.want)
		}
	}
}
