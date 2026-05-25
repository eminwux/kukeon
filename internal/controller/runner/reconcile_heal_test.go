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
// tick, without an operator running `kuke start cell <name>` per cell.
// Pre-fix the loop's ExistsCgroup result was discarded; only `kuke start
// cell` could heal the missing cgroup.
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
