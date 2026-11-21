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

//nolint:testpackage // drives the unexported teardown core and reconciler against in-package fakes
package runner

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	cgroup2 "github.com/containerd/cgroups/v2/cgroup2"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// Tests for #1557: exit, root death, stop and kill settle a cell
// consistently. Each test drives the real verbs and ReconcileCell over a small
// in-test model of containerd tasks, and asserts the cell state, reason and
// every container's state and exit code on both the returned and the
// persisted cell, over at least two reconcile passes.

const (
	settleRealm = "default"
	settleSpace = "lab"
	settleStack = "suite"
	settleCell  = "settle"
	settleRoot  = "root"
	settleWork  = "work"
)

func settleCID(id string) string {
	return settleSpace + "_" + settleStack + "_" + settleCell + "_" + id
}

// settleTask models one containerd task. A task is running, exited (a dead
// task still present, with its exit code and a non-zero exit time), or gone
// (deleted; containerd reports ErrTaskNotFound).
type settleTask struct {
	running  bool
	gone     bool
	code     uint32
	exitTime time.Time
}

// runningTask is a live task. Stopped with SIGTERM it exits 0 (like a
// kukepause root or a well-behaved workload); SIGKILL always yields 137.
func runningTask() *settleTask {
	return &settleTask{running: true}
}

func exitedTask(code uint32) *settleTask {
	return &settleTask{code: code, exitTime: time.Now().Add(-time.Minute)}
}

func goneTask() *settleTask { return &settleTask{gone: true} }

// settleTasks is the task table behind the fake's TaskStatus / StopContainer
// hooks, keyed by containerd ID.
type settleTasks struct {
	mu    sync.Mutex
	byID  map[string]*settleTask
	stops int64
}

func (m *settleTasks) status(id string) (containerd.Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	task := m.byID[id]
	if task == nil || task.gone {
		return taskNotFound(id)
	}
	if task.running {
		return containerd.Status{Status: containerd.Running}, nil
	}
	return containerd.Status{Status: containerd.Stopped, ExitStatus: task.code, ExitTime: task.exitTime}, nil
}

// stop mirrors ctr.StopContainer after #1557: a running task is signalled,
// waited for and deleted; an already-exited task is deleted and its recorded
// exit returned; a missing task is ErrTaskNotFound.
func (m *settleTasks) stop(id string, opts ctr.StopContainerOptions) (*containerd.ExitStatus, error) {
	atomic.AddInt64(&m.stops, 1)
	m.mu.Lock()
	defer m.mu.Unlock()
	task := m.byID[id]
	if task == nil || task.gone {
		return nil, fmt.Errorf("%w: %s", errdefs.ErrTaskNotFound, id)
	}
	if task.running {
		task.code = 0
		if opts.Signal == signalSIGKILL {
			task.code = 137
		}
		task.exitTime = time.Now()
		task.running = false
	}
	task.gone = true
	return containerd.NewExitStatus(task.code, task.exitTime, nil), nil
}

// settleSetup describes a fixture: the persisted cell status, the workload
// spec knobs and the initial tasks.
type settleSetup struct {
	persisted      intmodel.CellStatus
	autoDelete     bool
	restartPolicy  string
	restartBackoff *int64
	root, work     *settleTask
	// allowRestart lets the reconciler's restart pass fire; otherwise any
	// restart fails the test.
	allowRestart bool
}

type settleFixture struct {
	r        *Exec
	tasks    *settleTasks
	deletes  int64
	restarts int64
}

// newSettleFixture seeds a realm, space and a root + workload cell persisted
// with setup.persisted, and returns the fixture plus the cell as the
// reconciler would list it.
func newSettleFixture(t *testing.T, setup settleSetup) (*settleFixture, intmodel.Cell) {
	t.Helper()
	f := &settleFixture{tasks: &settleTasks{byID: map[string]*settleTask{
		settleCID(settleRoot): setup.root,
		settleCID(settleWork): setup.work,
	}}}
	fake := &deleteCellFakeClient{
		loadCgroupFn: func(string, string) (*cgroup2.Manager, error) {
			return &cgroup2.Manager{}, nil
		},
		existsContainerFn: func(_, _ string) (bool, error) { return true, nil },
		taskStatusFn:      func(_, id string) (containerd.Status, error) { return f.tasks.status(id) },
		stopContainerFn: func(_, id string, opts ctr.StopContainerOptions) (*containerd.ExitStatus, error) {
			return f.tasks.stop(id, opts)
		},
		deleteContainerFn: func(_, _ string, _ ctr.ContainerDeleteOptions) error {
			atomic.AddInt64(&f.deletes, 1)
			return nil
		},
	}
	r := newDeleteCellTestExec(t, fake)
	r.restartContainerFn = func(cell intmodel.Cell, containerID string) (intmodel.Cell, error) {
		atomic.AddInt64(&f.restarts, 1)
		if !setup.allowRestart {
			t.Errorf("restart of %q fired; no restart is expected here", containerID)
		}
		return cell, nil
	}
	f.r = r
	seedDeleteCellRealm(t, r, settleRealm)
	seedStopKillSpace(t, r, settleRealm, settleSpace)

	container := func(id string, root bool) intmodel.ContainerSpec {
		return intmodel.ContainerSpec{
			ID:           id,
			ContainerdID: settleCID(id),
			Root:         root,
			RealmName:    settleRealm,
			SpaceName:    settleSpace,
			StackName:    settleStack,
			CellName:     settleCell,
			Image:        "busybox:latest",
		}
	}
	work := container(settleWork, false)
	work.RestartPolicy = setup.restartPolicy
	work.RestartBackoffSeconds = setup.restartBackoff
	seed := intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: settleCell},
		Spec: intmodel.CellSpec{
			ID:              settleCell,
			RealmName:       settleRealm,
			SpaceName:       settleSpace,
			StackName:       settleStack,
			RootContainerID: settleRoot,
			AutoDelete:      setup.autoDelete,
			Containers:      []intmodel.ContainerSpec{container(settleRoot, true), work},
		},
		Status: setup.persisted,
	}
	if err := r.UpdateCellMetadata(seed); err != nil {
		t.Fatalf("seed cell metadata: %v", err)
	}
	listed, err := r.GetCell(seed)
	if err != nil {
		t.Fatalf("GetCell after seed: %v", err)
	}
	return f, listed
}

// containerWant is the expected state and exit code of one container.
type containerWant struct {
	state intmodel.ContainerState
	code  int
}

// cellWant is the expected cell label and container end state.
type cellWant struct {
	state       intmodel.CellState
	reason      string
	message     string   // exact message, when set
	msgContains []string // substrings the message must contain
	root, work  containerWant
}

// assertSettled checks want against the returned cell and the persisted copy.
func (f *settleFixture) assertSettled(t *testing.T, step string, got intmodel.Cell, want cellWant) {
	t.Helper()
	persisted, err := f.r.GetCell(got)
	if err != nil {
		t.Fatalf("%s: GetCell: %v", step, err)
	}
	for _, view := range []struct {
		name string
		cell intmodel.Cell
	}{{"returned", got}, {"persisted", persisted}} {
		c := view.cell
		where := step + " (" + view.name + ")"
		if c.Status.State != want.state {
			t.Errorf("%s: State = %v, want %v", where, c.Status.State, want.state)
		}
		if c.Status.Reason != want.reason {
			t.Errorf("%s: Reason = %q, want %q", where, c.Status.Reason, want.reason)
		}
		if want.message != "" && c.Status.Message != want.message {
			t.Errorf("%s: Message = %q, want %q", where, c.Status.Message, want.message)
		}
		for _, sub := range want.msgContains {
			if !strings.Contains(c.Status.Message, sub) {
				t.Errorf("%s: Message = %q, want it to contain %q", where, c.Status.Message, sub)
			}
		}
		for id, cw := range map[string]containerWant{settleRoot: want.root, settleWork: want.work} {
			st, ok := settleContainerStatus(c, id)
			if !ok {
				t.Errorf("%s: container %q missing from status", where, id)
				continue
			}
			if st.State != cw.state || st.ExitCode != cw.code {
				t.Errorf("%s: container %q = %v/exit %d, want %v/exit %d",
					where, id, st.State, st.ExitCode, cw.state, cw.code)
			}
		}
	}
}

func settleContainerStatus(cell intmodel.Cell, id string) (intmodel.ContainerStatus, bool) {
	for _, st := range cell.Status.Containers {
		if st.ID == id {
			return st, true
		}
	}
	return intmodel.ContainerStatus{}, false
}

// settlePasses is how many reconcile passes reconcileAndAssert runs: the
// first settles the cell, the second proves the settled view is stable.
const settlePasses = 2

// reconcileAndAssert runs settlePasses reconcile passes, asserting want after
// each.
func (f *settleFixture) reconcileAndAssert(t *testing.T, cell intmodel.Cell, want cellWant) intmodel.Cell {
	t.Helper()
	for pass := 1; pass <= settlePasses; pass++ {
		next, outcome, err := f.r.ReconcileCell(cell)
		if err != nil {
			t.Fatalf("pass %d: ReconcileCell: %v", pass, err)
		}
		if outcome.Deleted || outcome.Vanished {
			t.Fatalf("pass %d: cell was reaped (outcome=%+v)", pass, outcome)
		}
		f.assertSettled(t, fmt.Sprintf("pass %d", pass), next, want)
		cell = next
	}
	return cell
}

func stoppedWith(code int) containerWant {
	return containerWant{state: intmodel.ContainerStateStopped, code: code}
}

func persistedStatus(state intmodel.CellState) intmodel.CellStatus {
	return intmodel.CellStatus{State: state, ReadyObserved: true, CgroupReady: true}
}

// Context row 1: a workload that exits 0 is wound down whatever its
// restartPolicy, and the root ends Stopped with exit 0 (graceful stop).
func TestSettle_CleanExitWindsDownEveryPolicy(t *testing.T) {
	for _, policy := range []string{"", intmodel.RestartPolicyNever, intmodel.RestartPolicyOnFailure} {
		t.Run("policy="+policy, func(t *testing.T) {
			f, cell := newSettleFixture(t, settleSetup{
				persisted:     persistedStatus(intmodel.CellStateReady),
				restartPolicy: policy,
				root:          runningTask(),
				work:          exitedTask(0),
			})
			f.reconcileAndAssert(t, cell, cellWant{
				state: intmodel.CellStateExited,
				root:  stoppedWith(0),
				work:  stoppedWith(0),
			})
			if got := atomic.LoadInt64(&f.deletes); got != 0 {
				t.Errorf("DeleteContainer calls = %d, want 0 (records stay, #867)", got)
			}
		})
	}
}

// Context row 1, then `kuke kill` on the already wound-down cell: nothing
// changes.
func TestSettle_KillAfterWindDownIsNoOp(t *testing.T) {
	f, cell := newSettleFixture(t, settleSetup{
		persisted: persistedStatus(intmodel.CellStateReady),
		root:      runningTask(),
		work:      exitedTask(0),
	})
	want := cellWant{state: intmodel.CellStateExited, root: stoppedWith(0), work: stoppedWith(0)}
	cell = f.reconcileAndAssert(t, cell, want)

	killed, err := f.r.KillCell(cell)
	if err != nil {
		t.Fatalf("KillCell: %v", err)
	}
	f.assertSettled(t, "after kill", killed, want)
	f.reconcileAndAssert(t, killed, want)
}

// Context row 2: a workload that exits 1 settles Error with the failure
// breadcrumb, and the root is wound down too.
func TestSettle_FailedExitWindsDownAsError(t *testing.T) {
	f, cell := newSettleFixture(t, settleSetup{
		persisted: persistedStatus(intmodel.CellStateReady),
		root:      runningTask(),
		work:      exitedTask(1),
	})
	f.reconcileAndAssert(t, cell, cellWant{
		state:   intmodel.CellStateError,
		reason:  reasonWorkloadFailed,
		message: `container "work" exited with code 1`,
		root:    stoppedWith(0),
		work:    stoppedWith(1),
	})
}

// D5: a cell already persisted as Error with a live root (pre-#1557 shape, or
// a failed wind-down) is wound down from the sticky branch, keeping its label.
func TestSettle_StickyErrorWithLiveRootIsWoundDown(t *testing.T) {
	persisted := persistedStatus(intmodel.CellStateError)
	persisted.Reason = reasonWorkloadFailed
	persisted.Message = `container "work" exited with code 1`
	f, cell := newSettleFixture(t, settleSetup{
		persisted: persisted,
		root:      runningTask(),
		work:      exitedTask(1),
	})
	f.reconcileAndAssert(t, cell, cellWant{
		state:   intmodel.CellStateError,
		reason:  reasonWorkloadFailed,
		message: persisted.Message,
		root:    stoppedWith(0),
		work:    stoppedWith(1),
	})
}

// Context row 3: `kuke kill` on an Exited cell whose root was left running
// keeps the Exited label (no flip to Stopped and back) and leaves no task.
func TestSettle_KillExitedCellKeepsLabel(t *testing.T) {
	f, cell := newSettleFixture(t, settleSetup{
		persisted: persistedStatus(intmodel.CellStateExited),
		root:      runningTask(),
		work:      exitedTask(0),
	})
	want := cellWant{state: intmodel.CellStateExited, root: stoppedWith(137), work: stoppedWith(0)}
	killed, err := f.r.KillCell(cell)
	if err != nil {
		t.Fatalf("KillCell: %v", err)
	}
	f.assertSettled(t, "after kill", killed, want)
	f.reconcileAndAssert(t, killed, want)
}

// Context row 4: `kuke stop` on an Error cell keeps Error and its breadcrumb,
// and deletes the workload's dead task while preserving its exit code.
func TestSettle_StopErrorCellKeepsLabel(t *testing.T) {
	persisted := persistedStatus(intmodel.CellStateError)
	persisted.Reason = reasonWorkloadFailed
	persisted.Message = `container "work" exited with code 1`
	f, cell := newSettleFixture(t, settleSetup{
		persisted: persisted,
		root:      runningTask(),
		work:      exitedTask(1),
	})
	want := cellWant{
		state:   intmodel.CellStateError,
		reason:  reasonWorkloadFailed,
		message: persisted.Message,
		root:    stoppedWith(0),
		work:    stoppedWith(1),
	}
	stopped, err := f.r.StopCell(cell)
	if err != nil {
		t.Fatalf("StopCell: %v", err)
	}
	f.assertSettled(t, "after stop", stopped, want)
	f.reconcileAndAssert(t, stopped, want)
}

// Context row 5: `kuke kill` on a running cell stays Stopped with
// OperatorStopped, and every container reads Stopped with exit 137.
func TestSettle_KillRunningCellIsOperatorStopped(t *testing.T) {
	f, cell := newSettleFixture(t, settleSetup{
		persisted: persistedStatus(intmodel.CellStateReady),
		root:      runningTask(),
		work:      runningTask(),
	})
	want := cellWant{
		state:   intmodel.CellStateStopped,
		reason:  reasonOperatorStopped,
		message: operatorStoppedMessage,
		root:    stoppedWith(137),
		work:    stoppedWith(137),
	}
	killed, err := f.r.KillCell(cell)
	if err != nil {
		t.Fatalf("KillCell: %v", err)
	}
	f.assertSettled(t, "after kill", killed, want)
	f.reconcileAndAssert(t, killed, want)
}

// Context row 6: `kuke stop` on a Degraded cell whose `always` workload is
// waiting out its backoff is durable: Stopped + OperatorStopped, no restart.
func TestSettle_StopDegradedCellIsOperatorStopped(t *testing.T) {
	backoff := int64(600)
	f, cell := newSettleFixture(t, settleSetup{
		persisted:      persistedStatus(intmodel.CellStateDegraded),
		restartPolicy:  intmodel.RestartPolicyAlways,
		restartBackoff: &backoff,
		root:           runningTask(),
		work:           exitedTask(1),
	})
	f.r.recordRestartAttempt(cell, settleWork) // the backoff is running
	want := cellWant{
		state:   intmodel.CellStateStopped,
		reason:  reasonOperatorStopped,
		message: operatorStoppedMessage,
		root:    stoppedWith(0),
		work:    stoppedWith(1),
	}
	stopped, err := f.r.StopCell(cell)
	if err != nil {
		t.Fatalf("StopCell: %v", err)
	}
	f.assertSettled(t, "after stop", stopped, want)
	f.reconcileAndAssert(t, stopped, want)
	if got := atomic.LoadInt64(&f.restarts); got != 0 {
		t.Errorf("restarts = %d, want 0", got)
	}
}

// Root death: the root task exited (SIGKILL) while the workload still runs.
// The reconciler kills the workload in the same pass and settles Error with
// RootContainerExited naming the root and its signal.
func TestSettle_RootDeathKillsCell(t *testing.T) {
	root := exitedTask(137)
	f, cell := newSettleFixture(t, settleSetup{
		persisted: persistedStatus(intmodel.CellStateReady),
		root:      root,
		work:      runningTask(),
	})
	f.reconcileAndAssert(t, cell, cellWant{
		state:       intmodel.CellStateError,
		reason:      reasonRootContainerExited,
		msgContains: []string{`"root"`, "SIGKILL", "torn down"},
		root:        stoppedWith(137),
		work:        stoppedWith(137),
	})
	if got := atomic.LoadInt64(&f.deletes); got != 0 {
		t.Errorf("DeleteContainer calls = %d, want 0", got)
	}
}

// A root whose task vanished with no exit record is reported as such, not as
// "exit code 0".
func TestRootDeathMessage_NoExitRecord(t *testing.T) {
	msg := rootDeathMessage(intmodel.ContainerStatus{ID: "root", State: intmodel.ContainerStateStopped})
	if !strings.Contains(msg, "without an exit record") || strings.Contains(msg, "code 0") {
		t.Errorf("rootDeathMessage = %q, want the no-exit-record wording", msg)
	}
}

// An Unknown root read is not terminal: a transient misread must not kill a
// cell, and root-only cells never take the root-death path.
func TestRootDiedUnderWorkloads(t *testing.T) {
	specs := []intmodel.ContainerSpec{{ID: "root", Root: true}, {ID: "work"}}
	cases := []struct {
		name     string
		specs    []intmodel.ContainerSpec
		ready    bool
		root     intmodel.ContainerState
		work     intmodel.ContainerState
		wantDied bool
	}{
		{"root_dead_workload_running", specs, true, intmodel.ContainerStateError, intmodel.ContainerStateReady, true},
		{"root_gone_workload_running", specs, true, intmodel.ContainerStateStopped, intmodel.ContainerStateReady, true},
		{"root_unknown", specs, true, intmodel.ContainerStateUnknown, intmodel.ContainerStateReady, false},
		{"root_dead_workload_done", specs, true, intmodel.ContainerStateError, intmodel.ContainerStateExited, false},
		{"never_ready", specs, false, intmodel.ContainerStateError, intmodel.ContainerStateReady, false},
		{
			"root_only",
			[]intmodel.ContainerSpec{{ID: "root", Root: true}},
			true, intmodel.ContainerStateError,
			intmodel.ContainerStateReady, false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			statuses := []intmodel.ContainerStatus{
				{ID: "root", State: tc.root},
				{ID: "work", State: tc.work}, // ignored for a root-only spec
			}
			cell := intmodel.Cell{
				Spec:   intmodel.CellSpec{Containers: tc.specs},
				Status: intmodel.CellStatus{Containers: statuses},
			}
			died, _ := rootDiedUnderWorkloads(intmodel.CellStatus{ReadyObserved: tc.ready}, cell)
			if died != tc.wantDied {
				t.Errorf("rootDiedUnderWorkloads = %v, want %v", died, tc.wantDied)
			}
		})
	}
}

// D4 row 3 / `--rm` regression: killing a persisted-Ready cell whose workload
// already exited stamps no breadcrumb, so the next pass still auto-deletes it.
func TestSettle_RmKillAfterExitStillAutoDeletes(t *testing.T) {
	f, cell := newSettleFixture(t, settleSetup{
		persisted:  persistedStatus(intmodel.CellStateReady),
		autoDelete: true,
		root:       runningTask(),
		work:       exitedTask(0),
	})
	killed, err := f.r.KillCell(cell)
	if err != nil {
		t.Fatalf("KillCell: %v", err)
	}
	if killed.Status.State != intmodel.CellStateStopped || cellOperatorStopped(killed) {
		t.Fatalf("after kill: State=%v Reason=%q, want Stopped with no breadcrumb",
			killed.Status.State, killed.Status.Reason)
	}
	_, outcome, err := f.r.ReconcileCell(killed)
	if err != nil {
		t.Fatalf("ReconcileCell: %v", err)
	}
	if !outcome.Deleted {
		t.Errorf("outcome.Deleted = false, want true (--rm must still reap a finished cell)")
	}
}

// AC7: RefreshCell keeps the reconciler's sticky, durable-Stopped and
// Degraded-hold rules and triggers none of its side effects.
func TestRefreshCell_FollowsReconcilerRules(t *testing.T) {
	backoff := int64(600)
	exitedAt := time.Now().Add(-time.Minute)
	cases := []struct {
		name  string
		setup func() settleSetup
		prep  func(f *settleFixture, cell intmodel.Cell)
		want  intmodel.CellState
	}{
		{
			// Pre-fix: derivation relabelled the killed (exit 137) cell Error.
			name: "operator_killed_stopped_stays_stopped",
			setup: func() settleSetup {
				st := persistedStatus(intmodel.CellStateStopped)
				st.Reason, st.Message = reasonOperatorStopped, operatorStoppedMessage
				st.Containers = []intmodel.ContainerStatus{
					{
						ID:         settleRoot,
						Name:       settleRoot,
						State:      intmodel.ContainerStateStopped,
						ExitCode:   137,
						FinishTime: exitedAt,
					},
					{
						ID:         settleWork,
						Name:       settleWork,
						State:      intmodel.ContainerStateStopped,
						ExitCode:   137,
						FinishTime: exitedAt,
					},
				}
				return settleSetup{persisted: st, root: goneTask(), work: goneTask()}
			},
			want: intmodel.CellStateStopped,
		},
		{
			// Pre-fix: derivation recomputed the Failed cell to Exited.
			name: "failed_stays_failed",
			setup: func() settleSetup {
				st := persistedStatus(intmodel.CellStateFailed)
				st.Reason, st.Message = "StartCellFailed", "boom"
				return settleSetup{persisted: st, root: goneTask(), work: goneTask()}
			},
			want: intmodel.CellStateFailed,
		},
		{
			// Pre-fix: derivation persisted sticky Error, ending the restart loop.
			name: "degraded_with_deferred_restart_stays_degraded",
			setup: func() settleSetup {
				return settleSetup{
					persisted:      persistedStatus(intmodel.CellStateDegraded),
					restartPolicy:  intmodel.RestartPolicyAlways,
					restartBackoff: &backoff,
					root:           runningTask(),
					work:           exitedTask(1),
				}
			},
			prep: func(f *settleFixture, cell intmodel.Cell) { f.r.recordRestartAttempt(cell, settleWork) },
			want: intmodel.CellStateDegraded,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setup := tc.setup()
			f, cell := newSettleFixture(t, setup)
			if tc.prep != nil {
				tc.prep(f, cell)
			}
			for pass := 1; pass <= 2; pass++ {
				got, _, err := f.r.RefreshCell(cell)
				if err != nil {
					t.Fatalf("pass %d: RefreshCell: %v", pass, err)
				}
				persisted, err := f.r.GetCell(got)
				if err != nil {
					t.Fatalf("pass %d: GetCell: %v", pass, err)
				}
				for name, c := range map[string]intmodel.Cell{"returned": got, "persisted": persisted} {
					if c.Status.State != tc.want {
						t.Errorf("pass %d (%s): State = %v, want %v", pass, name, c.Status.State, tc.want)
					}
					if c.Status.Reason != setup.persisted.Reason || c.Status.Message != setup.persisted.Message {
						t.Errorf("pass %d (%s): Reason/Message = %q/%q, want %q/%q", pass, name,
							c.Status.Reason, c.Status.Message, setup.persisted.Reason, setup.persisted.Message)
					}
				}
				cell = got
			}
			if got := atomic.LoadInt64(&f.tasks.stops); got != 0 {
				t.Errorf("StopContainer calls = %d, want 0 (refresh has no side effects)", got)
			}
			if got := atomic.LoadInt64(&f.deletes); got != 0 {
				t.Errorf("DeleteContainer calls = %d, want 0", got)
			}
			if got := atomic.LoadInt64(&f.restarts); got != 0 {
				t.Errorf("restarts = %d, want 0", got)
			}
		})
	}
}

// AC7: a refresh that lands after the cell was deleted writes nothing.
func TestRefreshCell_SkipsDeletedCell(t *testing.T) {
	f, cell := newSettleFixture(t, settleSetup{
		persisted: persistedStatus(intmodel.CellStateReady),
		root:      runningTask(),
		work:      runningTask(),
	})
	if err := f.r.deleteCellLocked(cell); err != nil {
		t.Fatalf("deleteCellLocked: %v", err)
	}
	if _, n, err := f.r.RefreshCell(cell); err != nil || n != 0 {
		t.Fatalf("RefreshCell = (%d, %v), want (0, nil)", n, err)
	}
	if _, err := f.r.GetCell(cell); !errors.Is(err, errdefs.ErrCellNotFound) {
		t.Errorf("GetCell after refresh = %v, want ErrCellNotFound (no resurrection)", err)
	}
}

// D4: the label an operator stop/kill leaves, decided before teardown.
func TestClassifyOperatorStop(t *testing.T) {
	specs := func(policy string) []intmodel.ContainerSpec {
		return []intmodel.ContainerSpec{{ID: "root", Root: true}, {ID: "work", RestartPolicy: policy}}
	}
	statuses := func(work intmodel.ContainerState, code int) []intmodel.ContainerStatus {
		return []intmodel.ContainerStatus{
			{ID: "root", State: intmodel.ContainerStateReady},
			{ID: "work", State: work, ExitCode: code},
		}
	}
	cases := []struct {
		name     string
		state    intmodel.CellState
		ready    bool
		policy   string
		work     intmodel.ContainerState
		code     int
		expected operatorStopLabel
	}{
		{"exited_keeps", intmodel.CellStateExited, true, "", intmodel.ContainerStateExited, 0, operatorStopLabelKeep},
		{"error_keeps", intmodel.CellStateError, true, "", intmodel.ContainerStateError, 1, operatorStopLabelKeep},
		{
			"exited_but_running_is_interrupt", intmodel.CellStateExited, true, "", intmodel.ContainerStateReady, 0,
			operatorStopLabelInterrupted,
		},
		{
			"ready_running_is_interrupt", intmodel.CellStateReady, true, "", intmodel.ContainerStateReady, 0,
			operatorStopLabelInterrupted,
		},
		{
			"degraded_is_interrupt", intmodel.CellStateDegraded, true, intmodel.RestartPolicyOnFailure,
			intmodel.ContainerStateError, 1, operatorStopLabelInterrupted,
		},
		{
			"ready_restart_owed_is_interrupt", intmodel.CellStateReady, true, intmodel.RestartPolicyAlways,
			intmodel.ContainerStateExited, 0, operatorStopLabelInterrupted,
		},
		{
			"ready_restart_owed_never_ready_is_stopped", intmodel.CellStateReady, false, intmodel.RestartPolicyAlways,
			intmodel.ContainerStateExited, 0, operatorStopLabelStopped,
		},
		{
			"ready_finished_is_stopped", intmodel.CellStateReady, true, "", intmodel.ContainerStateExited, 0,
			operatorStopLabelStopped,
		},
		{
			"stopped_is_stopped", intmodel.CellStateStopped, true, "", intmodel.ContainerStateStopped, 0,
			operatorStopLabelStopped,
		},
		{
			"failed_is_stopped", intmodel.CellStateFailed, true, "", intmodel.ContainerStateStopped, 0,
			operatorStopLabelStopped,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cell := intmodel.Cell{
				Spec: intmodel.CellSpec{Containers: specs(tc.policy)},
				Status: intmodel.CellStatus{
					State:         tc.state,
					ReadyObserved: tc.ready,
					Containers:    statuses(tc.work, tc.code),
				},
			}
			if got := classifyOperatorStop(cell); got != tc.expected {
				t.Errorf("classifyOperatorStop = %v, want %v", got, tc.expected)
			}
		})
	}
}
