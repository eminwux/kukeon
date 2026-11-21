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

//nolint:testpackage // exercises unexported runner helpers and ReconcileCell against in-package fakes
package runner

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	cgroup2 "github.com/containerd/cgroups/v2/cgroup2"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

func TestCellWorkloadActive(t *testing.T) {
	withWorkload := []intmodel.ContainerSpec{
		{ID: "root", Root: true},
		{ID: "work"},
	}
	rootOnly := []intmodel.ContainerSpec{{ID: "root", Root: true}}
	cases := []struct {
		name     string
		specs    []intmodel.ContainerSpec
		statuses []intmodel.ContainerStatus
		want     bool
	}{
		{
			name:  "workload_running",
			specs: withWorkload,
			statuses: []intmodel.ContainerStatus{
				{ID: "root", State: intmodel.ContainerStateReady},
				{ID: "work", State: intmodel.ContainerStateReady},
			},
			want: true,
		},
		{
			// Natural clean exit (or the reconciler's own wind-down): the
			// workload is terminal even though the root is still up.
			name:  "workload_exited_root_running",
			specs: withWorkload,
			statuses: []intmodel.ContainerStatus{
				{ID: "root", State: intmodel.ContainerStateReady},
				{ID: "work", State: intmodel.ContainerStateStopped},
			},
			want: false,
		},
		{
			name:  "workload_reaped",
			specs: withWorkload,
			statuses: []intmodel.ContainerStatus{
				{ID: "root", State: intmodel.ContainerStateReady},
				{ID: "work", State: intmodel.ContainerStateNotCreated},
			},
			want: false,
		},
		{
			name:  "workload_unknown_is_not_active",
			specs: withWorkload,
			statuses: []intmodel.ContainerStatus{
				{ID: "work", State: intmodel.ContainerStateUnknown},
			},
			want: false,
		},
		{
			name:     "root_only_running",
			specs:    rootOnly,
			statuses: []intmodel.ContainerStatus{{ID: "root", State: intmodel.ContainerStateReady}},
			want:     true,
		},
		{
			name:     "root_only_exited",
			specs:    rootOnly,
			statuses: []intmodel.ContainerStatus{{ID: "root", State: intmodel.ContainerStateStopped}},
			want:     false,
		},
		{
			name:  "no_statuses",
			specs: withWorkload,
			want:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cell := intmodel.Cell{
				Spec:   intmodel.CellSpec{Containers: tc.specs},
				Status: intmodel.CellStatus{Containers: tc.statuses},
			}
			if got := cellWorkloadActive(cell); got != tc.want {
				t.Errorf("cellWorkloadActive = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestMarkCellReady_ClearsOperatorStopped pins that a restart lifts the
// operator-stop breadcrumb: the cell becomes eligible for natural clean-exit
// auto-delete again once it runs (#1371).
func TestMarkCellReady_ClearsOperatorStopped(t *testing.T) {
	cell := intmodel.Cell{Status: intmodel.CellStatus{State: intmodel.CellStateStopped}}
	markCellOperatorStopped(&cell)
	if !cellOperatorStopped(cell) {
		t.Fatalf("cellOperatorStopped = false right after markCellOperatorStopped")
	}
	markCellReady(&cell)
	if cellOperatorStopped(cell) {
		t.Errorf("cellOperatorStopped = true after markCellReady; restart must clear the breadcrumb")
	}
}

// TestMarkCellMaterialized pins the create-only contract (#1371):
// provisioning leaves the cell Stopped with the ReadyObserved latch open and
// no first-ready timestamp, even if the inbound document carried them.
func TestMarkCellMaterialized(t *testing.T) {
	cell := intmodel.Cell{Status: intmodel.CellStatus{
		State:         intmodel.CellStatePending,
		ReadyObserved: true,
		ReadyAt:       time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}}
	markCellMaterialized(&cell)
	if cell.Status.State != intmodel.CellStateStopped {
		t.Errorf("State = %v, want Stopped", cell.Status.State)
	}
	if cell.Status.ReadyObserved {
		t.Errorf("ReadyObserved = true, want false (no start has succeeded)")
	}
	if !cell.Status.ReadyAt.IsZero() {
		t.Errorf("ReadyAt = %v, want zero", cell.Status.ReadyAt)
	}
}

// TestMaterializedCell_PersistsWithoutReadyAt drives the on-disk write: a
// materialized cell persisted through UpdateCellMetadata must not get a
// ReadyAt stamp (stampCellLifecycle only stamps it on State==Ready), and a
// subsequent start-failure cleanup (markCellFailed) must not invent one
// either — the failed fused-run case from #1378/#1371.
func TestMaterializedCell_PersistsWithoutReadyAt(t *testing.T) {
	t0 := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	r := newMetadataTestExec(t, t.TempDir(), t0)

	cell := intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: "never-run"},
		Spec: intmodel.CellSpec{
			ID:         "never-run",
			RealmName:  "default",
			SpaceName:  "default",
			StackName:  "default",
			AutoDelete: true,
			Containers: []intmodel.ContainerSpec{},
		},
	}
	markCellMaterialized(&cell)
	if err := r.UpdateCellMetadata(cell); err != nil {
		t.Fatalf("UpdateCellMetadata: %v", err)
	}
	got, err := r.GetCell(cell)
	if err != nil {
		t.Fatalf("GetCell: %v", err)
	}
	if got.Status.ReadyObserved || !got.Status.ReadyAt.IsZero() {
		t.Errorf("materialized cell: ReadyObserved=%v ReadyAt=%v, want false/zero",
			got.Status.ReadyObserved, got.Status.ReadyAt)
	}

	r.markCellFailed(got, "StartCellFailed", errors.New("exec: /audit-no-such-program: not found"))
	failed, err := r.GetCell(cell)
	if err != nil {
		t.Fatalf("GetCell after markCellFailed: %v", err)
	}
	if failed.Status.State != intmodel.CellStateFailed {
		t.Errorf("State = %v, want Failed", failed.Status.State)
	}
	if failed.Status.ReadyObserved || !failed.Status.ReadyAt.IsZero() {
		t.Errorf("failed start: ReadyObserved=%v ReadyAt=%v, want false/zero (never reached Ready)",
			failed.Status.ReadyObserved, failed.Status.ReadyAt)
	}

	// Successful start: readiness latches eagerly and ReadyAt is stamped on
	// the Ready persist.
	markCellReady(&failed)
	if err = r.UpdateCellMetadata(failed); err != nil {
		t.Fatalf("UpdateCellMetadata (ready): %v", err)
	}
	ready, err := r.GetCell(cell)
	if err != nil {
		t.Fatalf("GetCell after ready: %v", err)
	}
	if !ready.Status.ReadyObserved || !ready.Status.ReadyAt.Equal(t0) {
		t.Errorf("successful start: ReadyObserved=%v ReadyAt=%v, want true/%v",
			ready.Status.ReadyObserved, ready.Status.ReadyAt, t0)
	}
}

// reconcileRmCell builds the two-container `--rm` cell shape the reconcile
// regression tests below drive through ReconcileCell.
func reconcileRmCell(realm, space, stack, cellName, rootID, workloadID, rootCID, workCID string,
	status intmodel.CellStatus,
) intmodel.Cell {
	return intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: cellName},
		Spec: intmodel.CellSpec{
			ID:         cellName,
			RealmName:  realm,
			SpaceName:  space,
			StackName:  stack,
			AutoDelete: true,
			Containers: []intmodel.ContainerSpec{
				{ID: rootID, ContainerdID: rootCID, Root: true},
				{ID: workloadID, ContainerdID: workCID},
			},
		},
		Status: status,
	}
}

// TestReconcileCell_OperatorStoppedRmCellSurvives is the #1371 regression:
// `kuke run --rm -d` + `kuke stop` leaves a Stopped cell whose workload is
// terminal with exit 0. Stopped is non-sticky, so the reconciler re-derives it
// to Exited — the auto-delete trigger — and the pre-fix gate reaped the cell.
// The operator-stop breadcrumb must keep it across consecutive ticks; the
// control (same cell, no breadcrumb — a natural clean exit) is still reaped.
func TestReconcileCell_OperatorStoppedRmCellSurvives(t *testing.T) {
	realm, space, stack, cellName := "default", "lab", "suite", "rm-stop"
	rootID, workloadID := "root", "workload"
	rootCID := space + "_" + stack + "_" + cellName + "_" + rootID
	workCID := space + "_" + stack + "_" + cellName + "_" + workloadID

	var deletes int64
	fake := &deleteCellFakeClient{
		loadCgroupFn: func(string, string) (*cgroup2.Manager, error) {
			return &cgroup2.Manager{}, nil
		},
		existsContainerFn: func(_, _ string) (bool, error) { return true, nil },
		// Both tasks gone after the operator stop — the stopped-cell signature.
		taskStatusFn: func(_, _ string) (containerd.Status, error) {
			return containerd.Status{}, fmt.Errorf("%w: %w", errdefs.ErrTaskNotFound, errors.New("task: not found"))
		},
		deleteContainerFn: func(_, _ string, _ ctr.ContainerDeleteOptions) error {
			atomic.AddInt64(&deletes, 1)
			return nil
		},
	}
	r := newDeleteCellTestExec(t, fake)
	seedDeleteCellRealm(t, r, realm)
	seedStopKillSpace(t, r, realm, space)
	seedPostRebootCell(t, r, realm, space, stack, cellName, rootID, workloadID, rootCID, workCID)

	status := intmodel.CellStatus{State: intmodel.CellStateStopped, ReadyObserved: true}
	cell := reconcileRmCell(realm, space, stack, cellName, rootID, workloadID, rootCID, workCID, status)
	markCellOperatorStopped(&cell)

	for tick := 1; tick <= 3; tick++ {
		next, outcome, err := r.ReconcileCell(cell)
		if err != nil {
			t.Fatalf("tick %d: ReconcileCell: %v", tick, err)
		}
		if outcome.Deleted || outcome.Vanished {
			t.Fatalf("tick %d: operator-stopped --rm cell was reaped (outcome=%+v)", tick, outcome)
		}
		if !cellOperatorStopped(next) {
			t.Fatalf("tick %d: operator-stop breadcrumb lost (Reason=%q)", tick, next.Status.Reason)
		}
		cell = next
	}
	if got := atomic.LoadInt64(&deletes); got != 0 {
		t.Errorf("DeleteContainer calls = %d, want 0 while the cell is preserved", got)
	}
	if _, err := r.GetCell(cell); err != nil {
		t.Errorf("GetCell after reconcile ticks: %v (cell must stay inspectable)", err)
	}

	// Control: the same terminal cell without the breadcrumb is a natural
	// clean exit and `--rm` still reaps it.
	control := reconcileRmCell(realm, space, stack, cellName, rootID, workloadID, rootCID, workCID,
		intmodel.CellStatus{State: intmodel.CellStateReady, ReadyObserved: true})
	_, outcome, err := r.ReconcileCell(control)
	if err != nil {
		t.Fatalf("control ReconcileCell: %v", err)
	}
	if !outcome.Deleted {
		t.Errorf("control: outcome.Deleted = false, want true (natural clean-exit --rm cleanup must still work)")
	}
}

// TestReconcileCell_CreateOnlyAutoDeleteCellSurvives is the #1378 half of
// #1371: a create-only cell (materialized, never started) whose Blueprint
// sets autoDelete must survive reconciliation, keeping the ReadyObserved
// latch open so it can later be started.
func TestReconcileCell_CreateOnlyAutoDeleteCellSurvives(t *testing.T) {
	realm, space, stack, cellName := "default", "lab", "suite", "never-run"
	rootID, workloadID := "root", "main"
	rootCID := space + "_" + stack + "_" + cellName + "_" + rootID
	workCID := space + "_" + stack + "_" + cellName + "_" + workloadID

	fake := &deleteCellFakeClient{
		loadCgroupFn: func(string, string) (*cgroup2.Manager, error) {
			return &cgroup2.Manager{}, nil
		},
		existsContainerFn: func(_, _ string) (bool, error) { return true, nil },
		taskStatusFn: func(_, _ string) (containerd.Status, error) {
			return containerd.Status{}, fmt.Errorf("%w: %w", errdefs.ErrTaskNotFound, errors.New("task: not found"))
		},
		deleteContainerFn: func(_, _ string, _ ctr.ContainerDeleteOptions) error {
			t.Errorf("DeleteContainer called — a never-started cell must not be reaped")
			return nil
		},
	}
	r := newDeleteCellTestExec(t, fake)
	seedDeleteCellRealm(t, r, realm)
	seedStopKillSpace(t, r, realm, space)
	seedPostRebootCell(t, r, realm, space, stack, cellName, rootID, workloadID, rootCID, workCID)

	cell := reconcileRmCell(realm, space, stack, cellName, rootID, workloadID, rootCID, workCID,
		intmodel.CellStatus{})
	markCellMaterialized(&cell)

	for tick := 1; tick <= 2; tick++ {
		next, outcome, err := r.ReconcileCell(cell)
		if err != nil {
			t.Fatalf("tick %d: ReconcileCell: %v", tick, err)
		}
		if outcome.Deleted || outcome.Vanished {
			t.Fatalf("tick %d: create-only autoDelete cell was reaped (outcome=%+v)", tick, outcome)
		}
		if next.Status.ReadyObserved {
			t.Fatalf("tick %d: ReadyObserved latched on a never-started cell", tick)
		}
		cell = next
	}
}

// TestStopCell_StampsOperatorStoppedOnlyWhenWorkloadRunning drives the real
// stop/kill paths through the #1557 label rule (classifyOperatorStop), keyed
// on the persisted state the verb finds:
//
//   - Ready: interrupting a running workload stamps the breadcrumb, while
//     stopping a cell whose workload already exited (`kuke run -a --rm` after
//     the shell exits) leaves a plain Stopped (D4 row 3).
//   - Exited / Error: with the workload finished the label is kept (row 1);
//     a workload running again is an interrupt (row 2).
//   - Degraded: always an interrupt (row 2).
func TestStopCell_StampsOperatorStoppedOnlyWhenWorkloadRunning(t *testing.T) {
	type verb func(r *Exec, cell intmodel.Cell) (intmodel.Cell, error)
	verbs := map[string]verb{
		"stop": func(r *Exec, c intmodel.Cell) (intmodel.Cell, error) { return r.StopCell(c) },
		"kill": func(r *Exec, c intmodel.Cell) (intmodel.Cell, error) { return r.KillCell(c) },
	}
	persistedStates := []intmodel.CellState{
		intmodel.CellStateReady,
		intmodel.CellStateExited,
		intmodel.CellStateError,
		intmodel.CellStateDegraded,
	}
	for verbName, run := range verbs {
		for _, persistedState := range persistedStates {
			for _, workloadRunning := range []bool{true, false} {
				name := fmt.Sprintf("%s/persisted=%v/workloadRunning=%v", verbName, persistedState, workloadRunning)
				t.Run(name, func(t *testing.T) {
					wantState := intmodel.CellStateStopped
					wantCrumb := workloadRunning || persistedState == intmodel.CellStateDegraded
					if !workloadRunning && (persistedState == intmodel.CellStateExited ||
						persistedState == intmodel.CellStateError) {
						wantState = persistedState
					}
					runOperatorStopCase(t, run, persistedState, workloadRunning, wantState, wantCrumb)
				})
			}
		}
	}
}

func runOperatorStopCase(
	t *testing.T,
	run func(r *Exec, cell intmodel.Cell) (intmodel.Cell, error),
	persistedState intmodel.CellState,
	workloadRunning bool,
	wantState intmodel.CellState,
	wantCrumb bool,
) {
	t.Helper()
	realm, space, stack, cellName := "default", "lab", "suite", "rm-stop"
	rootID, workloadID := "root", "workload"
	rootCID := space + "_" + stack + "_" + cellName + "_" + rootID
	workCID := space + "_" + stack + "_" + cellName + "_" + workloadID

	var stopped atomic.Bool
	fake := &deleteCellFakeClient{
		existsContainerFn: func(_, _ string) (bool, error) { return true, nil },
		taskStatusFn: func(_, id string) (containerd.Status, error) {
			if stopped.Load() {
				return containerd.Status{Status: containerd.Stopped}, nil
			}
			if id == workCID && !workloadRunning {
				return containerd.Status{Status: containerd.Stopped}, nil
			}
			return containerd.Status{Status: containerd.Running}, nil
		},
		stopContainerFn: func(_, _ string, _ ctr.StopContainerOptions) (*containerd.ExitStatus, error) {
			stopped.Store(true)
			//nolint:nilnil // the stop path tolerates a nil exit status; nil mirrors stopKillFakeClient
			return nil, nil
		},
	}
	r := newDeleteCellTestExec(t, fake)
	seedDeleteCellRealm(t, r, realm)
	seedStopKillSpace(t, r, realm, space)
	seedPostRebootCell(t, r, realm, space, stack, cellName, rootID, workloadID, rootCID, workCID)

	req := buildStopKillCellRequest(realm, space, stack, cellName)
	if persistedState != intmodel.CellStateReady {
		seeded, err := r.GetCell(req)
		if err != nil {
			t.Fatalf("GetCell: %v", err)
		}
		seeded.Status.State = persistedState
		if err = r.UpdateCellMetadata(seeded); err != nil {
			t.Fatalf("UpdateCellMetadata: %v", err)
		}
	}

	got, err := run(r, req)
	if err != nil {
		t.Fatalf("verb: %v", err)
	}
	if got.Status.State != wantState {
		t.Errorf("State = %v, want %v", got.Status.State, wantState)
	}
	if cellOperatorStopped(got) != wantCrumb {
		t.Errorf("cellOperatorStopped = %v, want %v (Reason=%q)",
			cellOperatorStopped(got), wantCrumb, got.Status.Reason)
	}
}
