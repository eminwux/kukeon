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

//nolint:testpackage // exercises the private heal path inside *Exec.ReconcileCell against an in-package ctr.Client fake
package runner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	cgroup2 "github.com/containerd/cgroups/v2/cgroup2"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/metadata"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// TestReconcileCell_HealsWipedCgroupWhenReadyObserved is the headline #855
// guard: a cell whose cgroup the host reboot wiped — metadata survives, the
// ReadyObserved latch is still true — must have its cgroup re-created and
// its subtree controllers re-asserted by the reconcile loop within one
// tick, without an operator running `kuke start <name>` per cell.
// Pre-fix the loop's ExistsCgroup result was discarded; only `kuke start`
// could heal the missing cgroup.
func TestReconcileCell_HealsWipedCgroupWhenReadyObserved(t *testing.T) {
	realm, space, stack, cellName := "default", "kukeon", "kukeon", "web"
	rootID := "root"
	workloadID := "workload"
	rootContainerdID := space + "_" + stack + "_" + cellName + "_" + rootID
	workloadContainerdID := space + "_" + stack + "_" + cellName + "_" + workloadID

	var newCgroupCalls int32
	var enableSubtreeCalls int32
	fake := &deleteCellFakeClient{
		// Cgroup absent on every Load — exists.go's probe sees absent
		// and the heal path's ensureCgroupInternal Load also sees absent
		// and triggers the create branch.
		loadCgroupFn: func(string, string) (*cgroup2.Manager, error) {
			return nil, errors.New("cgroup path does not exist")
		},
		// NewCgroup must be called exactly once on the heal pass.
		// Returns a non-nil zero-valued sentinel so ensureCgroupInternal's
		// nil-manager check does not abort the heal pre-subtree-toggle.
		newCgroupFn: func(ctr.CgroupSpec) (*cgroup2.Manager, error) {
			atomic.AddInt32(&newCgroupCalls, 1)
			return &cgroup2.Manager{}, nil
		},
		// EnableCellSubtreeControllers is the second half of the post-
		// #314/#328 contract — the heal must re-assert subtree
		// controllers in the same pass.
		enableCellSubtreeFn: func(string, string, []string) ([]string, error) {
			atomic.AddInt32(&enableSubtreeCalls, 1)
			return nil, nil
		},
		// Containerd records survive the reboot — both root and non-root.
		existsContainerFn: func(_, _ string) (bool, error) { return true, nil },
		// Tasks are gone — the kernel side wiped them with the cgroup.
		taskStatusFn: func(_, _ string) (containerd.Status, error) {
			return containerd.Status{}, fmt.Errorf("%w: %w", errdefs.ErrTaskNotFound, errors.New("task: not found"))
		},
	}
	r := newDeleteCellTestExec(t, fake)
	seedDeleteCellRealm(t, r, realm)
	seedPostRebootCell(t, r, realm, space, stack, cellName, rootID, workloadID, rootContainerdID, workloadContainerdID)

	cell := intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: cellName},
		Spec: intmodel.CellSpec{
			ID:        cellName,
			RealmName: realm,
			SpaceName: space,
			StackName: stack,
			Containers: []intmodel.ContainerSpec{
				{ID: rootID, ContainerdID: rootContainerdID, Root: true},
				{ID: workloadID, ContainerdID: workloadContainerdID, Root: false},
			},
		},
		Status: intmodel.CellStatus{
			State:         intmodel.CellStateReady,
			ReadyObserved: true,
		},
	}

	updatedCell, _, err := r.ReconcileCell(cell)
	if err != nil {
		t.Fatalf("ReconcileCell: unexpected error: %v", err)
	}

	if got := atomic.LoadInt32(&newCgroupCalls); got != 1 {
		t.Errorf(
			"NewCgroup call count = %d, want 1 (the reconcile loop must re-create the wiped cgroup on the ReadyObserved cell — #855)",
			got,
		)
	}
	if got := atomic.LoadInt32(&enableSubtreeCalls); got != 1 {
		t.Errorf(
			"EnableCellSubtreeControllers call count = %d, want 1 (heal must re-assert subtree controllers on the same pass — post-#314/#328 contract)",
			got,
		)
	}
	if updatedCell.Status.CgroupPath == "" {
		t.Errorf("Status.CgroupPath = %q, want non-empty after heal (ensureCgroupInternal must backfill it)", updatedCell.Status.CgroupPath)
	}
	if !updatedCell.Status.ReadyObserved {
		t.Errorf("Status.ReadyObserved = false, want true (the latch must survive the heal)")
	}
	// Pair with the #861 probe: the heal must ship cgroupReady:true in
	// the same tick. Pre-fix the probe stamped false (cgroup absent)
	// and ensureCellCgroup never touched the field, so the persisted
	// snapshot carried cgroupReady:false for a cell whose cgroup is in
	// fact present — exactly the stale-state surface #861 just closed.
	if !updatedCell.Status.CgroupReady {
		t.Errorf("Status.CgroupReady = false, want true after heal (heal must pair with the #861 probe so cgroupReady:true ships in the same tick, not next-pass)")
	}
}

// TestReconcileCell_DoesNotHealHalfCreateCell is the regression guard for
// the AC #4 case: a cell that has never reached Ready (a half-CreateCell
// that crashed mid-way — cgroup may or may not exist on disk; ReadyObserved
// is still false on the persisted metadata) must NOT be promoted by the
// re-ensure pass. The in-flight CreateCell will finish its own
// ensureCellCgroup path under the per-cell lock; the reconciler stepping
// in would race that flow and double-write the cgroup metadata.
func TestReconcileCell_DoesNotHealHalfCreateCell(t *testing.T) {
	realm, space, stack, cellName := "default", "kukeon", "kukeon", "halfcreate"

	fake := &deleteCellFakeClient{
		// Cgroup absent: the half-CreateCell crashed before
		// ensureCellCgroup could complete.
		loadCgroupFn: func(string, string) (*cgroup2.Manager, error) {
			return nil, errors.New("cgroup path does not exist")
		},
		// Any NewCgroup call here is the regression — fail loudly so
		// the gate change reads as a behavior change in the test
		// surface, not a silent log-only divergence.
		newCgroupFn: func(ctr.CgroupSpec) (*cgroup2.Manager, error) {
			t.Errorf("NewCgroup called on a never-Ready cell — heal must be gated on the ReadyObserved latch (#855 AC#4)")
			return nil, errors.New("must not be called")
		},
		enableCellSubtreeFn: func(string, string, []string) ([]string, error) {
			t.Errorf("EnableCellSubtreeControllers called on a never-Ready cell — same gate as NewCgroup")
			return nil, errors.New("must not be called")
		},
		// No containerd records yet for this cell — the CreateCell crash
		// happened pre-container-registration. Derivation will read back
		// NotCreated for the (missing) root, which the cell-state
		// derivation maps to Stopped; the ReadyObserved=false on disk
		// is the AutoDelete gate that prevents reaping.
		existsContainerFn: func(_, _ string) (bool, error) { return false, nil },
	}
	r := newDeleteCellTestExec(t, fake)
	seedDeleteCellRealm(t, r, realm)
	seedNeverReadyCell(t, r, realm, space, stack, cellName)

	cell := intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: cellName},
		Spec: intmodel.CellSpec{
			ID:        cellName,
			RealmName: realm,
			SpaceName: space,
			StackName: stack,
			Containers: []intmodel.ContainerSpec{
				{
					ID:           "root",
					ContainerdID: space + "_" + stack + "_" + cellName + "_root",
					Root:         true,
				},
			},
		},
		Status: intmodel.CellStatus{
			// State Pending and ReadyObserved false is the persisted
			// snapshot a half-CreateCell leaves behind: the cgroup
			// create may or may not have landed, but markCellReady
			// never fired so the latch never closed.
			State:         intmodel.CellStatePending,
			ReadyObserved: false,
		},
	}

	if _, _, err := r.ReconcileCell(cell); err != nil {
		t.Fatalf("ReconcileCell: unexpected error: %v", err)
	}
	// Assertions live inside the fakes' t.Errorf paths above — the
	// reconcile call returning is the negative-space proof that the
	// heal gate held.
}

// seedNeverReadyCell writes a cell metadata doc shaped like the on-disk
// snapshot a half-CreateCell leaves behind: ReadyObserved false (the
// latch never closed), one root container with an explicit ContainerdID
// so the reconciler's container-status pass can look it up.
func seedNeverReadyCell(t *testing.T, r *Exec, realm, space, stack, cellName string) {
	t.Helper()
	rootContainerdID := space + "_" + stack + "_" + cellName + "_root"
	doc := v1beta1.CellDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCell,
		Metadata:   v1beta1.CellMetadata{Name: cellName},
		Spec: v1beta1.CellSpec{
			ID:      cellName,
			RealmID: realm,
			SpaceID: space,
			StackID: stack,
			Containers: []v1beta1.ContainerSpec{
				{
					ID:           "root",
					ContainerdID: rootContainerdID,
					RealmID:      realm,
					SpaceID:      space,
					StackID:      stack,
					CellID:       cellName,
					Image:        "alpine:latest",
					Root:         true,
				},
			},
		},
		Status: v1beta1.CellStatus{
			State:         v1beta1.CellStatePending,
			ReadyObserved: false,
		},
	}
	path := fs.CellMetadataPath(r.opts.RunPath, realm, space, stack, cellName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir cell metadata dir: %v", err)
	}
	if err := metadata.WriteMetadata(r.ctx, r.logger, doc, path); err != nil {
		t.Fatalf("write cell metadata: %v", err)
	}
}

// TestReconcileCell_DoesNotResurrectDeletedCell is the headline #1251 guard:
// the delete-while-reconcile-blocked-on-lock race. ReconcileCells snapshots
// the cell list once per tick and iterates with the in-memory value captured
// at list time (ReadyObserved=true). A `kuke delete cell` that completes
// while this tick is blocked on the per-cell lock leaves the reconciler
// holding a pre-delete snapshot whose cgroup is now absent — pre-fix the
// #855 heal branch fired (`!cgroupExists && ReadyObserved`) and
// ensureCellCgroup recreated the cgroup AND rewrote the just-deleted
// metadata.json, silently resurrecting the cell. The post-lock GetCell
// recheck must short-circuit on ErrCellNotFound: no NewCgroup, no metadata
// rewrite, and a Vanished outcome (not Deleted — the reconciler observed an
// external delete, it did not perform one).
func TestReconcileCell_DoesNotResurrectDeletedCell(t *testing.T) {
	realm, space, stack, cellName := "default", "kukeon", "kukeon", "github-runner"
	rootID := "root"
	workloadID := "workload"
	rootContainerdID := space + "_" + stack + "_" + cellName + "_" + rootID
	workloadContainerdID := space + "_" + stack + "_" + cellName + "_" + workloadID

	fake := &deleteCellFakeClient{
		// Cgroup absent: DeleteCell removed it as part of the teardown
		// the reconcile tick was blocked behind. This is the same probe
		// result the post-reboot heal keys off — the bug is that the
		// heal can't tell "metadata survives" from "metadata just deleted".
		loadCgroupFn: func(string, string) (*cgroup2.Manager, error) {
			return nil, errors.New("cgroup path does not exist")
		},
		// Any NewCgroup / subtree-controller call is the resurrection
		// regression — fail loudly so the recheck reads as a behavior
		// change in the test surface, not a silent log-only divergence.
		newCgroupFn: func(ctr.CgroupSpec) (*cgroup2.Manager, error) {
			t.Errorf("NewCgroup called on a deleted cell — the post-lock recheck must short-circuit before the #855 heal (#1251)")
			return nil, errors.New("must not be called")
		},
		enableCellSubtreeFn: func(string, string, []string) ([]string, error) {
			t.Errorf("EnableCellSubtreeControllers called on a deleted cell — same recheck gate as NewCgroup (#1251)")
			return nil, errors.New("must not be called")
		},
	}
	r := newDeleteCellTestExec(t, fake)
	seedDeleteCellRealm(t, r, realm)
	// Seed the cell, then remove its metadata to model the just-completed
	// DeleteCell: the snapshot below is the pre-delete value, the on-disk
	// metadata is gone.
	seedPostRebootCell(t, r, realm, space, stack, cellName, rootID, workloadID, rootContainerdID, workloadContainerdID)
	metadataPath := fs.CellMetadataPath(r.opts.RunPath, realm, space, stack, cellName)
	if err := os.Remove(metadataPath); err != nil {
		t.Fatalf("remove cell metadata (simulate completed DeleteCell): %v", err)
	}

	// The stale pre-delete snapshot the reconcile tick still holds:
	// ReadyObserved=true is what drives the heal branch pre-fix.
	cell := intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: cellName},
		Spec: intmodel.CellSpec{
			ID:        cellName,
			RealmName: realm,
			SpaceName: space,
			StackName: stack,
			Containers: []intmodel.ContainerSpec{
				{ID: rootID, ContainerdID: rootContainerdID, Root: true},
				{ID: workloadID, ContainerdID: workloadContainerdID, Root: false},
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
	if !outcome.Vanished {
		t.Errorf("ReconcileOutcome.Vanished = false, want true (the cell's metadata is gone — the reconciler must report it vanished, not heal it)")
	}
	if outcome.Deleted {
		t.Errorf("ReconcileOutcome.Deleted = true, want false (the reconciler observed an external delete, it did not perform one — Deleted is reserved for the AutoDelete path)")
	}
	if outcome.Updated {
		t.Errorf("ReconcileOutcome.Updated = true, want false (a vanished cell is a pure no-op — no status write)")
	}
	// The resurrection signature: metadata.json must stay gone. Pre-fix
	// ensureCellCgroup's UpdateCellMetadata rewrote it.
	if _, statErr := os.Stat(metadataPath); !os.IsNotExist(statErr) {
		t.Errorf("cell metadata.json exists after reconcile (stat err = %v), want still-absent — the reconciler resurrected the deleted cell (#1251)", statErr)
	}
}

// TestReconcileCell_HealVsDeletedMetadata pins the AC #4 distinction the
// post-lock recheck draws: a missing cgroup on a Ready cell heals only when
// the metadata survives (genuine post-reboot, #855), and is left alone when
// the metadata is gone (just-deleted, #1251). Both share the identical
// absent-cgroup + ReadyObserved=true snapshot — the on-disk metadata is the
// only discriminator, which is exactly why the pre-fix heal branch
// (keyed only on cgroup-absent + ReadyObserved) could not tell them apart.
func TestReconcileCell_HealVsDeletedMetadata(t *testing.T) {
	realm, space, stack := "default", "kukeon", "kukeon"
	rootID, workloadID := "root", "workload"

	makeCell := func(cellName string) intmodel.Cell {
		return intmodel.Cell{
			Metadata: intmodel.CellMetadata{Name: cellName},
			Spec: intmodel.CellSpec{
				ID:        cellName,
				RealmName: realm,
				SpaceName: space,
				StackName: stack,
				Containers: []intmodel.ContainerSpec{
					{ID: rootID, ContainerdID: space + "_" + stack + "_" + cellName + "_" + rootID, Root: true},
					{ID: workloadID, ContainerdID: space + "_" + stack + "_" + cellName + "_" + workloadID, Root: false},
				},
			},
			Status: intmodel.CellStatus{State: intmodel.CellStateReady, ReadyObserved: true},
		}
	}

	t.Run("metadata survives -> heals", func(t *testing.T) {
		cellName := "survivor"
		var newCgroupCalls int32
		fake := &deleteCellFakeClient{
			loadCgroupFn: func(string, string) (*cgroup2.Manager, error) {
				return nil, errors.New("cgroup path does not exist")
			},
			newCgroupFn: func(ctr.CgroupSpec) (*cgroup2.Manager, error) {
				atomic.AddInt32(&newCgroupCalls, 1)
				return &cgroup2.Manager{}, nil
			},
			enableCellSubtreeFn: func(string, string, []string) ([]string, error) { return nil, nil },
			existsContainerFn:   func(_, _ string) (bool, error) { return true, nil },
			taskStatusFn: func(_, _ string) (containerd.Status, error) {
				return containerd.Status{}, fmt.Errorf("%w: %w", errdefs.ErrTaskNotFound, errors.New("task: not found"))
			},
		}
		r := newDeleteCellTestExec(t, fake)
		seedDeleteCellRealm(t, r, realm)
		seedPostRebootCell(t, r, realm, space, stack, cellName, rootID, workloadID,
			space+"_"+stack+"_"+cellName+"_"+rootID, space+"_"+stack+"_"+cellName+"_"+workloadID)

		if _, _, err := r.ReconcileCell(makeCell(cellName)); err != nil {
			t.Fatalf("ReconcileCell: unexpected error: %v", err)
		}
		if got := atomic.LoadInt32(&newCgroupCalls); got != 1 {
			t.Errorf("NewCgroup call count = %d, want 1 (metadata survives, so the #855 heal must still fire)", got)
		}
	})

	t.Run("metadata deleted -> no heal", func(t *testing.T) {
		cellName := "deleted"
		fake := &deleteCellFakeClient{
			loadCgroupFn: func(string, string) (*cgroup2.Manager, error) {
				return nil, errors.New("cgroup path does not exist")
			},
			newCgroupFn: func(ctr.CgroupSpec) (*cgroup2.Manager, error) {
				t.Errorf("NewCgroup called on a cell whose metadata is gone — the recheck must skip the heal (#1251)")
				return nil, errors.New("must not be called")
			},
			enableCellSubtreeFn: func(string, string, []string) ([]string, error) { return nil, nil },
		}
		r := newDeleteCellTestExec(t, fake)
		seedDeleteCellRealm(t, r, realm)
		// Intentionally do NOT seed the cell metadata — GetCell returns
		// ErrCellNotFound, the deleted-metadata arm of the distinction.
		_, outcome, err := r.ReconcileCell(makeCell(cellName))
		if err != nil {
			t.Fatalf("ReconcileCell: unexpected error: %v", err)
		}
		if !outcome.Vanished {
			t.Errorf("ReconcileOutcome.Vanished = false, want true (metadata is gone)")
		}
	})
}
