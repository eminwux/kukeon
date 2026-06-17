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

//nolint:testpackage // exercises the unexported restart-on-exit pass + gating helpers inside *Exec
package runner

import (
	"errors"
	"testing"
	"time"

	cgroup2 "github.com/containerd/cgroups/v2/cgroup2"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/eminwux/kukeon/internal/ctr"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// TestRestartPolicyRequiresRestart pins the per-container restart-trigger table
// (#1233): the restart-side rows of the #1003 reap gate.
//
//   - "always"        → restart on any exit.
//   - "on-failure"    → restart iff the exit code is non-zero.
//   - "" or "never"   → never restart (empty/unset defaults to never, matching
//     the Kubernetes default restartPolicy).
//   - unknown values  → permissive fallback (a typo/future value must not
//     silently strand a workload).
func TestRestartPolicyRequiresRestart(t *testing.T) {
	cases := []struct {
		name     string
		policy   string
		exitCode int
		want     bool
	}{
		{"empty_default_clean_exit_no_restart", "", 0, false},
		{"empty_default_failed_exit_no_restart", "", 1, false},
		{"always_clean_exit_restarts", intmodel.RestartPolicyAlways, 0, true},
		{"always_failed_exit_restarts", intmodel.RestartPolicyAlways, 137, true},
		{"on_failure_clean_exit_no_restart", intmodel.RestartPolicyOnFailure, 0, false},
		{"on_failure_failed_exit_restarts", intmodel.RestartPolicyOnFailure, 1, true},
		{"on_failure_signal_exit_restarts", intmodel.RestartPolicyOnFailure, 137, true},
		{"never_clean_exit_no_restart", intmodel.RestartPolicyNever, 0, false},
		{"never_failed_exit_no_restart", intmodel.RestartPolicyNever, 1, false},
		{"unknown_policy_restarts_default", "no-such-policy", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := restartPolicyRequiresRestart(tc.policy, tc.exitCode); got != tc.want {
				t.Errorf("restartPolicyRequiresRestart(%q, %d) = %v, want %v",
					tc.policy, tc.exitCode, got, tc.want)
			}
		})
	}
}

// fixedClock returns a nowFn pinned to t for deterministic backoff math.
func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

// restartTestCell builds a single-non-root-container cell whose workload carries
// the given policy and observed terminal status, ReadyObserved by default.
func restartTestCell(policy string, state intmodel.ContainerState, exitCode int) intmodel.Cell {
	return intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: "web"},
		Spec: intmodel.CellSpec{
			ID:        "web",
			RealmName: "default",
			SpaceName: "kukeon",
			StackName: "kukeon",
			Containers: []intmodel.ContainerSpec{
				{ID: "root", Root: true},
				{ID: "work", Root: false, RestartPolicy: policy},
			},
		},
		Status: intmodel.CellStatus{
			State:         intmodel.CellStateReady,
			ReadyObserved: true,
			Containers: []intmodel.ContainerStatus{
				{ID: "root", State: intmodel.ContainerStateReady},
				{ID: "work", State: state, ExitCode: exitCode},
			},
		},
	}
}

// recordingRestarter returns an Exec whose restart action records the container
// IDs it was asked to relaunch (and optionally fails), plus a pointer to the
// recorded slice.
func recordingRestarter(now time.Time, fail error) (*Exec, *[]string) {
	var fired []string
	r := &Exec{nowFn: fixedClock(now)}
	r.restartContainerFn = func(cell intmodel.Cell, containerID string) (intmodel.Cell, error) {
		fired = append(fired, containerID)
		if fail != nil {
			return intmodel.Cell{}, fail
		}
		return cell, nil
	}
	return r, &fired
}

// TestMaybeRestartExitedContainers_FiresPerPolicyRow drives the restart pass
// end-to-end (minus the real StartContainer) across the policy table: it must
// relaunch always on any exit and on-failure on a failed exit, and leave
// never / empty-default / on-failure-clean alone.
func TestMaybeRestartExitedContainers_FiresPerPolicyRow(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	cases := []struct {
		name      string
		policy    string
		state     intmodel.ContainerState
		exitCode  int
		wantFire  bool
		wantState restartPassResult
	}{
		{"always_clean_fires", intmodel.RestartPolicyAlways, intmodel.ContainerStateExited, 0, true, restartFired},
		{"always_failed_fires", intmodel.RestartPolicyAlways, intmodel.ContainerStateStopped, 1, true, restartFired},
		{"empty_default_failed_no_fire", "", intmodel.ContainerStateStopped, 1, false, restartNone},
		{"on_failure_failed_fires", intmodel.RestartPolicyOnFailure, intmodel.ContainerStateStopped, 137, true, restartFired},
		{"on_failure_clean_no_fire", intmodel.RestartPolicyOnFailure, intmodel.ContainerStateStopped, 0, false, restartNone},
		{"never_failed_no_fire", intmodel.RestartPolicyNever, intmodel.ContainerStateStopped, 1, false, restartNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, fired := recordingRestarter(now, nil)
			cell := restartTestCell(tc.policy, tc.state, tc.exitCode)

			_, result, err := r.maybeRestartExitedContainers(cell)
			if err != nil {
				t.Fatalf("maybeRestartExitedContainers: unexpected error: %v", err)
			}
			if result != tc.wantState {
				t.Errorf("result = %v, want %v", result, tc.wantState)
			}
			gotFire := len(*fired) == 1 && (*fired)[0] == "work"
			if gotFire != tc.wantFire {
				t.Errorf("relaunched %v, want fire=%v", *fired, tc.wantFire)
			}
		})
	}
}

// TestMaybeRestartExitedContainers_ReadyObservedGate confirms a cell that never
// reached Ready is never restarted, even with an always-policy terminal
// container — the #1233 ReadyObserved gate.
func TestMaybeRestartExitedContainers_ReadyObservedGate(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	r, fired := recordingRestarter(now, nil)
	cell := restartTestCell(intmodel.RestartPolicyAlways, intmodel.ContainerStateStopped, 1)
	cell.Status.ReadyObserved = false

	_, result, err := r.maybeRestartExitedContainers(cell)
	if err != nil {
		t.Fatalf("maybeRestartExitedContainers: unexpected error: %v", err)
	}
	if result != restartNone {
		t.Errorf("result = %v, want restartNone (never-Ready cell must not restart)", result)
	}
	if len(*fired) != 0 {
		t.Errorf("relaunched %v, want none (ReadyObserved gate)", *fired)
	}
}

// TestMaybeRestartExitedContainers_RootNotRestarted confirms a terminally
// exited root container is ignored by the restart pass — the root's lifecycle
// is the cell's, driven by start/stop verbs, not the per-container restart pass.
func TestMaybeRestartExitedContainers_RootNotRestarted(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	r, fired := recordingRestarter(now, nil)
	cell := restartTestCell(intmodel.RestartPolicyAlways, intmodel.ContainerStateReady, 0)
	// Flip the root terminal; the only non-root container stays Ready.
	cell.Status.Containers[0].State = intmodel.ContainerStateStopped
	cell.Status.Containers[0].ExitCode = 1

	_, result, err := r.maybeRestartExitedContainers(cell)
	if err != nil {
		t.Fatalf("maybeRestartExitedContainers: unexpected error: %v", err)
	}
	if result != restartNone || len(*fired) != 0 {
		t.Errorf("result=%v fired=%v, want restartNone and no relaunch (root is not restart-managed)", result, *fired)
	}
}

// TestMaybeRestartExitedContainers_BackoffDefers confirms a second exit within
// the backoff window defers the restart (no relaunch this tick) so the caller
// can suppress the sticky-Error persist and retry once the backoff clears.
func TestMaybeRestartExitedContainers_BackoffDefers(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	r, fired := recordingRestarter(now, nil)
	cell := restartTestCell(intmodel.RestartPolicyAlways, intmodel.ContainerStateStopped, 1)

	// Seed a prior attempt 10s ago — inside the 30s backoff floor.
	r.restartStates = map[string]*containerRestartState{
		r.restartStateKey(cell, "work"): {attempts: 1, lastAttempt: now.Add(-10 * time.Second)},
	}

	_, result, err := r.maybeRestartExitedContainers(cell)
	if err != nil {
		t.Fatalf("maybeRestartExitedContainers: unexpected error: %v", err)
	}
	if result != restartDeferred {
		t.Errorf("result = %v, want restartDeferred (backoff not elapsed)", result)
	}
	if len(*fired) != 0 {
		t.Errorf("relaunched %v during backoff window, want none", *fired)
	}

	// Advance past the backoff floor: the next pass must fire.
	r.nowFn = fixedClock(now.Add(restartBackoff + time.Second))
	_, result, err = r.maybeRestartExitedContainers(cell)
	if err != nil {
		t.Fatalf("maybeRestartExitedContainers (post-backoff): unexpected error: %v", err)
	}
	if result != restartFired || len(*fired) != 1 {
		t.Errorf("post-backoff result=%v fired=%v, want restartFired and one relaunch", result, *fired)
	}
}

// TestMaybeRestartExitedContainers_OnFailureCap confirms the on-failure retry
// cap stops further restarts once exhausted (reported as restartNone so the
// caller's reap gate settles the cell), and that always-policy is uncapped.
func TestMaybeRestartExitedContainers_OnFailureCap(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	longAgo := now.Add(-time.Hour) // past any backoff

	t.Run("on_failure_capped", func(t *testing.T) {
		r, fired := recordingRestarter(now, nil)
		cell := restartTestCell(intmodel.RestartPolicyOnFailure, intmodel.ContainerStateStopped, 1)
		r.restartStates = map[string]*containerRestartState{
			r.restartStateKey(cell, "work"): {attempts: onFailureMaxRestarts, lastAttempt: longAgo},
		}
		_, result, err := r.maybeRestartExitedContainers(cell)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != restartNone || len(*fired) != 0 {
			t.Errorf("result=%v fired=%v, want restartNone and no relaunch (cap exhausted)", result, *fired)
		}
	})

	t.Run("always_uncapped", func(t *testing.T) {
		r, fired := recordingRestarter(now, nil)
		cell := restartTestCell(intmodel.RestartPolicyAlways, intmodel.ContainerStateStopped, 1)
		r.restartStates = map[string]*containerRestartState{
			r.restartStateKey(cell, "work"): {attempts: onFailureMaxRestarts * 10, lastAttempt: longAgo},
		}
		_, result, err := r.maybeRestartExitedContainers(cell)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != restartFired || len(*fired) != 1 {
			t.Errorf("result=%v fired=%v, want restartFired (always is uncapped)", result, *fired)
		}
	})
}

// TestMaybeRestartExitedContainers_RunningClearsState confirms observing a
// container running again clears its backoff/cap bookkeeping so a future exit
// is treated as fresh — the on-failure cap counts consecutive thrash, not
// lifetime restarts.
func TestMaybeRestartExitedContainers_RunningClearsState(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	r, _ := recordingRestarter(now, nil)
	cell := restartTestCell(intmodel.RestartPolicyOnFailure, intmodel.ContainerStateReady, 0)
	key := r.restartStateKey(cell, "work")
	r.restartStates = map[string]*containerRestartState{
		key: {attempts: onFailureMaxRestarts, lastAttempt: now.Add(-time.Hour)},
	}

	if _, result, err := r.maybeRestartExitedContainers(cell); err != nil || result != restartNone {
		t.Fatalf("running container: result=%v err=%v, want restartNone/nil", result, err)
	}
	if _, ok := r.restartStates[key]; ok {
		t.Errorf("restart state for a running container was not cleared")
	}
}

// TestMaybeRestartExitedContainers_FailedRelaunchPropagates confirms a failed
// relaunch surfaces as an error from the pass (the reconcile loop records it;
// StartContainer's own markCellFailed defer flips the cell to Failed sticky).
func TestMaybeRestartExitedContainers_FailedRelaunchPropagates(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	wantErr := errors.New("createContainer: boom")
	r, fired := recordingRestarter(now, wantErr)
	cell := restartTestCell(intmodel.RestartPolicyAlways, intmodel.ContainerStateStopped, 1)

	_, _, err := r.maybeRestartExitedContainers(cell)
	if err == nil {
		t.Fatalf("maybeRestartExitedContainers: want error, got nil")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("error = %v, want it to wrap %v", err, wantErr)
	}
	if len(*fired) != 1 {
		t.Errorf("relaunch attempts = %d, want 1 (the failing attempt)", len(*fired))
	}
	// The failed attempt must still advance the cap so a permanently
	// unstartable container is not retried forever.
	if st := r.restartStates[r.restartStateKey(cell, "work")]; st == nil || st.attempts != 1 {
		t.Errorf("attempt bookkeeping after failed relaunch = %+v, want attempts=1", st)
	}
}

// TestRestartDecisionFor pins the gating helper directly across the
// first-attempt, backoff, and on-failure-cap branches.
func TestRestartDecisionFor(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	cell := restartTestCell(intmodel.RestartPolicyOnFailure, intmodel.ContainerStateStopped, 1)
	key := func(r *Exec) string { return r.restartStateKey(cell, "work") }

	t.Run("first_attempt_fires", func(t *testing.T) {
		r := &Exec{nowFn: fixedClock(now)}
		if got := r.restartDecisionFor(cell, "work", intmodel.RestartPolicyOnFailure, restartBackoff, onFailureMaxRestarts); got != restartFired {
			t.Errorf("got %v, want restartFired on first attempt", got)
		}
	})
	t.Run("within_backoff_defers", func(t *testing.T) {
		r := &Exec{nowFn: fixedClock(now)}
		r.restartStates = map[string]*containerRestartState{key(r): {attempts: 1, lastAttempt: now.Add(-time.Second)}}
		if got := r.restartDecisionFor(cell, "work", intmodel.RestartPolicyOnFailure, restartBackoff, onFailureMaxRestarts); got != restartDeferred {
			t.Errorf("got %v, want restartDeferred within backoff", got)
		}
	})
	t.Run("past_backoff_fires", func(t *testing.T) {
		r := &Exec{nowFn: fixedClock(now)}
		r.restartStates = map[string]*containerRestartState{key(r): {attempts: 1, lastAttempt: now.Add(-restartBackoff)}}
		if got := r.restartDecisionFor(cell, "work", intmodel.RestartPolicyOnFailure, restartBackoff, onFailureMaxRestarts); got != restartFired {
			t.Errorf("got %v, want restartFired past backoff", got)
		}
	})
	t.Run("cap_exhausted_gives_up", func(t *testing.T) {
		r := &Exec{nowFn: fixedClock(now)}
		r.restartStates = map[string]*containerRestartState{key(r): {attempts: onFailureMaxRestarts, lastAttempt: now.Add(-time.Hour)}}
		if got := r.restartDecisionFor(cell, "work", intmodel.RestartPolicyOnFailure, restartBackoff, onFailureMaxRestarts); got != restartNone {
			t.Errorf("got %v, want restartNone (cap exhausted)", got)
		}
	})
}

// TestReconcileCell_RestartFiresAndSuppressesWindDown is the integration guard:
// a Ready cell whose always-policy non-root workload exited cleanly (which would
// otherwise wind the cell down) instead relaunches the workload, returns
// Updated, and never invokes the wind-down kill path.
func TestReconcileCell_RestartFiresAndSuppressesWindDown(t *testing.T) {
	realm, space, stack, cellName := "default", "kukeon", "kukeon", "web"
	rootID, workloadID := "root", "workload"
	rootContainerdID := space + "_" + stack + "_" + cellName + "_" + rootID
	workloadContainerdID := space + "_" + stack + "_" + cellName + "_" + workloadID

	fake := &deleteCellFakeClient{
		// Cgroup present — the normal (non-heal) reconcile path.
		loadCgroupFn: func(string, string) (*cgroup2.Manager, error) {
			return &cgroup2.Manager{}, nil
		},
		existsContainerFn: func(_, _ string) (bool, error) { return true, nil },
		// Root still running; workload exited cleanly (Stopped, code 0).
		taskStatusFn: func(_, id string) (containerd.Status, error) {
			if id == workloadContainerdID {
				return containerd.Status{Status: containerd.Stopped, ExitStatus: 0}, nil
			}
			return containerd.Status{Status: containerd.Running}, nil
		},
		// Wind-down would call into the kill path; fail loudly if it does.
		stopContainerFn: func(_, _ string, _ ctr.StopContainerOptions) (*containerd.ExitStatus, error) {
			t.Errorf("stopContainer called — wind-down must be suppressed by the fired restart")
			return nil, nil
		},
		deleteContainerFn: func(_, _ string, _ ctr.ContainerDeleteOptions) error {
			t.Errorf("deleteContainer called — wind-down must be suppressed by the fired restart")
			return nil
		},
	}
	r := newDeleteCellTestExec(t, fake)
	seedDeleteCellRealm(t, r, realm)
	seedPostRebootCell(t, r, realm, space, stack, cellName, rootID, workloadID, rootContainerdID, workloadContainerdID)

	var fired []string
	r.restartContainerFn = func(cell intmodel.Cell, containerID string) (intmodel.Cell, error) {
		fired = append(fired, containerID)
		return cell, nil
	}

	cell := intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: cellName},
		Spec: intmodel.CellSpec{
			ID:        cellName,
			RealmName: realm,
			SpaceName: space,
			StackName: stack,
			AutoDelete: true, // even with --rm the fired restart wins this tick
			Containers: []intmodel.ContainerSpec{
				{ID: rootID, ContainerdID: rootContainerdID, Root: true},
				{ID: workloadID, ContainerdID: workloadContainerdID, Root: false, RestartPolicy: intmodel.RestartPolicyAlways},
			},
		},
		Status: intmodel.CellStatus{State: intmodel.CellStateReady, ReadyObserved: true},
	}

	_, outcome, err := r.ReconcileCell(cell)
	if err != nil {
		t.Fatalf("ReconcileCell: unexpected error: %v", err)
	}
	if !outcome.Updated {
		t.Errorf("outcome.Updated = false, want true (a fired restart reports Updated)")
	}
	if outcome.Deleted {
		t.Errorf("outcome.Deleted = true, want false (auto-delete must be suppressed by the fired restart)")
	}
	if len(fired) != 1 || fired[0] != workloadID {
		t.Errorf("relaunched %v, want a single relaunch of %q", fired, workloadID)
	}
}

// TestReconcileCell_AutoDeleteWithOnFailureRestartWins pins the corrected
// precedence for `--rm` + `restartPolicy: on-failure` on a non-zero exit: the
// exit owes a restart, so the restart pass fires first and the cell is
// preserved for the tick — `--rm` does NOT reap it. (A clean-exit `on-failure`
// owes no restart and is reaped by `--rm`, the same restartNone → autoDelete
// path pinned by TestReconcileCell_AutoDeleteOverridesRestartPolicyNever.)
// Companion to TestReconcileCell_RestartFiresAndSuppressesWindDown, which pins
// the `always` case; together they pin that `--rm` overrides only the
// restartPolicy *preserve* gate, never an owed restart.
func TestReconcileCell_AutoDeleteWithOnFailureRestartWins(t *testing.T) {
	realm, space, stack, cellName := "default", "kukeon", "kukeon", "web"
	rootID, workloadID := "root", "workload"
	rootContainerdID := space + "_" + stack + "_" + cellName + "_" + rootID
	workloadContainerdID := space + "_" + stack + "_" + cellName + "_" + workloadID

	fake := &deleteCellFakeClient{
		// Cgroup present — the normal (non-heal) reconcile path.
		loadCgroupFn: func(string, string) (*cgroup2.Manager, error) {
			return &cgroup2.Manager{}, nil
		},
		existsContainerFn: func(_, _ string) (bool, error) { return true, nil },
		// Root still running; workload exited non-zero (Stopped, code 1) — an
		// on-failure exit that owes a restart. A non-zero ExitTime is required for
		// the fresh exit code to propagate into ContainerStatus.ExitCode (the
		// FinishTime/ExitCode lockstep in buildContainerStatuses, #1137).
		taskStatusFn: func(_, id string) (containerd.Status, error) {
			if id == workloadContainerdID {
				return containerd.Status{
					Status:     containerd.Stopped,
					ExitStatus: 1,
					ExitTime:   time.Date(2026, 6, 7, 20, 35, 4, 0, time.UTC),
				}, nil
			}
			return containerd.Status{Status: containerd.Running}, nil
		},
		// Neither wind-down nor auto-delete may fire while the restart is owed.
		stopContainerFn: func(_, _ string, _ ctr.StopContainerOptions) (*containerd.ExitStatus, error) {
			t.Errorf("stopContainer called — reap must be suppressed by the fired restart")
			return nil, nil
		},
		deleteContainerFn: func(_, _ string, _ ctr.ContainerDeleteOptions) error {
			t.Errorf("deleteContainer called — auto-delete must be suppressed by the fired restart")
			return nil
		},
	}
	r := newDeleteCellTestExec(t, fake)
	seedDeleteCellRealm(t, r, realm)
	seedPostRebootCell(t, r, realm, space, stack, cellName, rootID, workloadID, rootContainerdID, workloadContainerdID)

	var fired []string
	r.restartContainerFn = func(cell intmodel.Cell, containerID string) (intmodel.Cell, error) {
		fired = append(fired, containerID)
		return cell, nil
	}

	cell := intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: cellName},
		Spec: intmodel.CellSpec{
			ID:         cellName,
			RealmName:  realm,
			SpaceName:  space,
			StackName:  stack,
			AutoDelete: true, // --rm must NOT win against an owed on-failure restart
			Containers: []intmodel.ContainerSpec{
				{ID: rootID, ContainerdID: rootContainerdID, Root: true},
				{ID: workloadID, ContainerdID: workloadContainerdID, Root: false, RestartPolicy: intmodel.RestartPolicyOnFailure},
			},
		},
		Status: intmodel.CellStatus{State: intmodel.CellStateReady, ReadyObserved: true},
	}

	_, outcome, err := r.ReconcileCell(cell)
	if err != nil {
		t.Fatalf("ReconcileCell: unexpected error: %v", err)
	}
	if !outcome.Updated {
		t.Errorf("outcome.Updated = false, want true (a fired restart reports Updated)")
	}
	if outcome.Deleted {
		t.Errorf("outcome.Deleted = true, want false (--rm must not reap an on-failure workload that owes a restart)")
	}
	if len(fired) != 1 || fired[0] != workloadID {
		t.Errorf("relaunched %v, want a single relaunch of %q", fired, workloadID)
	}
}

// TestReconcileCell_RestartHoldsCellDegraded pins the #1233 follow-up: while a
// restart is owed (fired here), ReconcileCell holds the cell at the non-sticky
// CellStateDegraded rather than the sticky CellStateError the crash would derive
// — Error would short-circuit the next tick and strand the restart loop. The
// crash breadcrumb is cleared since this is not (yet) a terminal failure.
func TestReconcileCell_RestartHoldsCellDegraded(t *testing.T) {
	realm, space, stack, cellName := "default", "kukeon", "kukeon", "web"
	rootID, workloadID := "root", "workload"
	rootContainerdID := space + "_" + stack + "_" + cellName + "_" + rootID
	workloadContainerdID := space + "_" + stack + "_" + cellName + "_" + workloadID

	fake := &deleteCellFakeClient{
		loadCgroupFn: func(string, string) (*cgroup2.Manager, error) {
			return &cgroup2.Manager{}, nil
		},
		existsContainerFn: func(_, _ string) (bool, error) { return true, nil },
		// Root running; workload crashed (Stopped, code 1) — owes an always restart.
		taskStatusFn: func(_, id string) (containerd.Status, error) {
			if id == workloadContainerdID {
				return containerd.Status{
					Status:     containerd.Stopped,
					ExitStatus: 1,
					ExitTime:   time.Date(2026, 6, 7, 20, 35, 4, 0, time.UTC),
				}, nil
			}
			return containerd.Status{Status: containerd.Running}, nil
		},
	}
	r := newDeleteCellTestExec(t, fake)
	seedDeleteCellRealm(t, r, realm)
	seedPostRebootCell(t, r, realm, space, stack, cellName, rootID, workloadID, rootContainerdID, workloadContainerdID)

	r.restartContainerFn = func(cell intmodel.Cell, _ string) (intmodel.Cell, error) {
		return cell, nil
	}

	cell := intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: cellName},
		Spec: intmodel.CellSpec{
			ID:        cellName,
			RealmName: realm,
			SpaceName: space,
			StackName: stack,
			Containers: []intmodel.ContainerSpec{
				{ID: rootID, ContainerdID: rootContainerdID, Root: true},
				{
					ID:            workloadID,
					ContainerdID:  workloadContainerdID,
					Root:          false,
					RestartPolicy: intmodel.RestartPolicyAlways,
				},
			},
		},
		Status: intmodel.CellStatus{State: intmodel.CellStateReady, ReadyObserved: true},
	}

	outCell, outcome, err := r.ReconcileCell(cell)
	if err != nil {
		t.Fatalf("ReconcileCell: unexpected error: %v", err)
	}
	if !outcome.Updated {
		t.Errorf("outcome.Updated = false, want true")
	}
	if outCell.Status.State != intmodel.CellStateDegraded {
		t.Errorf("cell state = %v, want Degraded (a fired restart holds the cell at non-sticky Degraded)",
			outCell.Status.State)
	}
	if cellStateIsSticky(outCell.Status.State) {
		t.Errorf("cell state %v is sticky — a held-Degraded cell must stay re-derivable so a later tick can fire",
			outCell.Status.State)
	}
}

// TestMaybeRestartExitedContainers_BumpsRestartCounterByOne pins AC items 1 & 2
// of #1234: a fired restart bumps the user-visible RestartCount by exactly one
// over the preserved prior and stamps RestartTime to now. maybeRestartExitedContainers
// is the sole writer, so the increment must land on the returned cell's status.
func TestMaybeRestartExitedContainers_BumpsRestartCounterByOne(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	r, fired := recordingRestarter(now, nil)
	cell := restartTestCell(intmodel.RestartPolicyAlways, intmodel.ContainerStateExited, 0)

	out, result, err := r.maybeRestartExitedContainers(cell)
	if err != nil {
		t.Fatalf("maybeRestartExitedContainers: unexpected error: %v", err)
	}
	if result != restartFired {
		t.Fatalf("result = %v, want restartFired", result)
	}
	if len(*fired) != 1 || (*fired)[0] != "work" {
		t.Fatalf("relaunched %v, want exactly [work]", *fired)
	}

	work := containerStatusByID(t, out, "work")
	if work.RestartCount != 1 {
		t.Errorf("RestartCount = %d, want 1 (exactly one bump over the zero prior)", work.RestartCount)
	}
	if !work.RestartTime.Equal(now) {
		t.Errorf("RestartTime = %v, want %v (the wall-clock of the relaunch)", work.RestartTime, now)
	}

	// The root container is not restarted, so its counters stay zero — the bump
	// is per-container, not cell-wide.
	root := containerStatusByID(t, out, "root")
	if root.RestartCount != 0 || !root.RestartTime.IsZero() {
		t.Errorf("root RestartCount=%d RestartTime=%v, want 0/zero (root is never restarted)",
			root.RestartCount, root.RestartTime)
	}
}

// TestMaybeRestartExitedContainers_RestartCounterMonotonic pins AC item 4: the
// count advances by one from whatever prior the status carried, so successive
// restarts tally monotonically (no Docker-style reset).
func TestMaybeRestartExitedContainers_RestartCounterMonotonic(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	r, _ := recordingRestarter(now, nil)
	cell := restartTestCell(intmodel.RestartPolicyAlways, intmodel.ContainerStateExited, 0)
	// Seed a non-zero prior, as populate would have preserved from earlier ticks.
	for i := range cell.Status.Containers {
		if cell.Status.Containers[i].ID == "work" {
			cell.Status.Containers[i].RestartCount = 4
			cell.Status.Containers[i].RestartTime = now.Add(-time.Hour)
		}
	}

	out, result, err := r.maybeRestartExitedContainers(cell)
	if err != nil {
		t.Fatalf("maybeRestartExitedContainers: unexpected error: %v", err)
	}
	if result != restartFired {
		t.Fatalf("result = %v, want restartFired", result)
	}
	work := containerStatusByID(t, out, "work")
	if work.RestartCount != 5 {
		t.Errorf("RestartCount = %d, want 5 (prior 4 + 1, monotonic — no reset)", work.RestartCount)
	}
	if !work.RestartTime.Equal(now) {
		t.Errorf("RestartTime = %v, want refreshed to %v on each restart", work.RestartTime, now)
	}
}

// TestMaybeRestartExitedContainers_NoBumpWhenNoRestartFires confirms the counter
// is untouched when no restart is owed — the sole-writer contract means a
// no-fire tick must leave RestartCount/RestartTime exactly as populate preserved
// them.
func TestMaybeRestartExitedContainers_NoBumpWhenNoRestartFires(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	r, fired := recordingRestarter(now, nil)
	// never-policy clean exit: no restart owed.
	cell := restartTestCell(intmodel.RestartPolicyNever, intmodel.ContainerStateStopped, 0)
	for i := range cell.Status.Containers {
		if cell.Status.Containers[i].ID == "work" {
			cell.Status.Containers[i].RestartCount = 2
			cell.Status.Containers[i].RestartTime = now.Add(-time.Hour)
		}
	}

	out, result, err := r.maybeRestartExitedContainers(cell)
	if err != nil {
		t.Fatalf("maybeRestartExitedContainers: unexpected error: %v", err)
	}
	if result != restartNone || len(*fired) != 0 {
		t.Fatalf("result = %v, fired = %v, want restartNone with no relaunch", result, *fired)
	}
	work := containerStatusByID(t, out, "work")
	if work.RestartCount != 2 || !work.RestartTime.Equal(now.Add(-time.Hour)) {
		t.Errorf("RestartCount=%d RestartTime=%v, want untouched 2/%v (no fire ⇒ no bump)",
			work.RestartCount, work.RestartTime, now.Add(-time.Hour))
	}
}

// containerStatusByID returns the status for containerID in cell, failing the
// test if absent.
func containerStatusByID(t *testing.T, cell intmodel.Cell, containerID string) intmodel.ContainerStatus {
	t.Helper()
	for _, s := range cell.Status.Containers {
		if s.ID == containerID {
			return s
		}
	}
	t.Fatalf("container %q not found in cell status", containerID)
	return intmodel.ContainerStatus{}
}

// TestPopulateCellContainerStatuses_PreservesRestartCounters is the populate
// half of #1234: populateCellContainerStatuses is a pure preserver of
// RestartCount/RestartTime — it carries the persisted values across the
// unconditional overwrite and never increments, even across repeated passes.
func TestPopulateCellContainerStatuses_PreservesRestartCounters(t *testing.T) {
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
	restartTime := time.Date(2026, 6, 7, 20, 35, 4, 0, time.UTC)
	cell.Status.Containers = []intmodel.ContainerStatus{
		{ID: containerID, RestartCount: 3, RestartTime: restartTime},
	}

	// Two consecutive populate passes must both preserve the prior values
	// unchanged — populate never writes these counters.
	for pass := 1; pass <= 2; pass++ {
		if err := r.populateCellContainerStatuses(&cell); err != nil {
			t.Fatalf("populateCellContainerStatuses (pass %d): unexpected error: %v", pass, err)
		}
		got := cell.Status.Containers[0]
		if got.RestartCount != 3 {
			t.Errorf("pass %d: RestartCount = %d, want preserved 3 (populate never increments)", pass, got.RestartCount)
		}
		if !got.RestartTime.Equal(restartTime) {
			t.Errorf("pass %d: RestartTime = %v, want preserved %v", pass, got.RestartTime, restartTime)
		}
	}
}

// restartTuningInt64Ptr is a local helper for the *int64 restart-tuning fields.
func restartTuningInt64Ptr(v int64) *int64 { return &v }

// TestEffectiveRestartBackoff pins the #1235 fallback contract: an unset
// (nil) Spec.RestartBackoffSeconds resolves to the hardcoded restartBackoff
// default (no behavior change), a set value resolves to that many seconds, and
// an explicit 0 disables the floor.
func TestEffectiveRestartBackoff(t *testing.T) {
	cases := []struct {
		name string
		spec intmodel.ContainerSpec
		want time.Duration
	}{
		{"unset falls back to default", intmodel.ContainerSpec{}, restartBackoff},
		{"set to 10s", intmodel.ContainerSpec{RestartBackoffSeconds: restartTuningInt64Ptr(10)}, 10 * time.Second},
		{"explicit zero disables floor", intmodel.ContainerSpec{RestartBackoffSeconds: restartTuningInt64Ptr(0)}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := effectiveRestartBackoff(tc.spec); got != tc.want {
				t.Errorf("effectiveRestartBackoff = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestEffectiveOnFailureMaxRestarts pins the #1235 fallback contract: an unset
// (nil) Spec.RestartMaxRetries resolves to the hardcoded onFailureMaxRestarts
// default, a set value resolves to itself.
func TestEffectiveOnFailureMaxRestarts(t *testing.T) {
	cases := []struct {
		name string
		spec intmodel.ContainerSpec
		want int
	}{
		{"unset falls back to default", intmodel.ContainerSpec{}, onFailureMaxRestarts},
		{"set to 3", intmodel.ContainerSpec{RestartMaxRetries: restartTuningInt64Ptr(3)}, 3},
		{"set to 1", intmodel.ContainerSpec{RestartMaxRetries: restartTuningInt64Ptr(1)}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := effectiveOnFailureMaxRestarts(tc.spec); got != tc.want {
				t.Errorf("effectiveOnFailureMaxRestarts = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestRestartDecisionFor_HonorsUserBackoff proves the user-authored backoff,
// not the hardcoded restartBackoff constant, governs the defer window: a
// lastAttempt that is past the default 30s floor but inside a user-set 60s floor
// must defer.
func TestRestartDecisionFor_HonorsUserBackoff(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	cell := restartTestCell(intmodel.RestartPolicyOnFailure, intmodel.ContainerStateStopped, 1)
	key := func(r *Exec) string { return r.restartStateKey(cell, "work") }

	// lastAttempt 45s ago: past the default 30s, inside a user 60s window.
	userBackoff := 60 * time.Second
	r := &Exec{nowFn: fixedClock(now)}
	r.restartStates = map[string]*containerRestartState{
		key(r): {attempts: 1, lastAttempt: now.Add(-45 * time.Second)},
	}
	if got := r.restartDecisionFor(cell, "work", intmodel.RestartPolicyOnFailure, userBackoff, onFailureMaxRestarts); got != restartDeferred {
		t.Errorf("got %v, want restartDeferred inside user 60s backoff", got)
	}
	// Same state under the default 30s backoff would fire — sanity that the
	// difference is the backoff value, not the bookkeeping.
	if got := r.restartDecisionFor(cell, "work", intmodel.RestartPolicyOnFailure, restartBackoff, onFailureMaxRestarts); got != restartFired {
		t.Errorf("got %v, want restartFired under default 30s backoff", got)
	}
}

// TestRestartDecisionFor_HonorsUserMaxRetries proves the user-authored cap, not
// the hardcoded onFailureMaxRestarts, decides when the on-failure loop gives up:
// 2 attempts exhausts a user cap of 2 while the default 5 would still fire.
func TestRestartDecisionFor_HonorsUserMaxRetries(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	cell := restartTestCell(intmodel.RestartPolicyOnFailure, intmodel.ContainerStateStopped, 1)
	key := func(r *Exec) string { return r.restartStateKey(cell, "work") }

	r := &Exec{nowFn: fixedClock(now)}
	r.restartStates = map[string]*containerRestartState{
		key(r): {attempts: 2, lastAttempt: now.Add(-time.Hour)},
	}
	if got := r.restartDecisionFor(cell, "work", intmodel.RestartPolicyOnFailure, restartBackoff, 2); got != restartNone {
		t.Errorf("got %v, want restartNone (user cap of 2 exhausted)", got)
	}
	if got := r.restartDecisionFor(cell, "work", intmodel.RestartPolicyOnFailure, restartBackoff, onFailureMaxRestarts); got != restartFired {
		t.Errorf("got %v, want restartFired (default cap of 5 not yet reached)", got)
	}
}
