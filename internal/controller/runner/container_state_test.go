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

//nolint:testpackage // exercises *Exec.GetContainerState and ReconcileCell against an in-package ctr.Client fake
package runner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	cgroup2 "github.com/containerd/cgroups/v2/cgroup2"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/metadata"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// containerStateCell mimics the shape ReconcileCell + GetContainerState
// expect: a workload cell with one non-root container carrying an
// explicit ContainerdID so the runner skips the build-from-naming branch
// the test fake can't satisfy.
func containerStateCell(realm, space, stack, cellName, containerID, containerdID string) intmodel.Cell {
	return intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: cellName},
		Spec: intmodel.CellSpec{
			ID:        cellName,
			RealmName: realm,
			SpaceName: space,
			StackName: stack,
			Containers: []intmodel.ContainerSpec{
				{
					ID:           containerID,
					ContainerdID: containerdID,
					Root:         true,
				},
			},
		},
	}
}

// TestGetContainerState_TaskNotFound_ReturnsStopped is the headline #543
// Layer 2 fix: a container record that survives a host reboot (containerd's
// boltdb keeps the container entry, the task is gone with the cgroup) must
// read back as Stopped, not Unknown. Without this, the cell-level derivation
// can never transition past Unknown — cellStateAutoDeleteTriggers and
// shouldWindDownCell both exclude Unknown by design — and the reconciler
// leaves every previously-Ready cell stuck.
func TestGetContainerState_TaskNotFound_ReturnsStopped(t *testing.T) {
	realm, space, stack, cellName := "default", "kukeon", "kukeon", "web"
	containerID, containerdID := "root", "kukeon_kukeon_web_root"

	fake := &deleteCellFakeClient{
		existsContainerFn: func(_, _ string) (bool, error) { return true, nil },
		taskStatusFn: func(_, _ string) (containerd.Status, error) {
			return containerd.Status{}, fmt.Errorf("%w: %w", errdefs.ErrTaskNotFound, errors.New("task: not found"))
		},
	}
	r := newDeleteCellTestExec(t, fake)
	seedDeleteCellRealm(t, r, realm)

	cell := containerStateCell(realm, space, stack, cellName, containerID, containerdID)

	state, err := r.GetContainerState(cell, containerID)
	if err != nil {
		t.Fatalf("GetContainerState: unexpected error: %v", err)
	}
	if state != intmodel.ContainerStateStopped {
		t.Errorf(
			"GetContainerState = %v, want Stopped (container exists, task gone is the post-reboot signature #543)",
			state,
		)
	}
}

// TestGetContainerState_ContainerAbsentReturnsNotCreated locks down the #670
// reporting fix: a container with no containerd record at all (never realized,
// or reaped/lost — operator ran `ctr -n <ns> containers rm`, or the records
// vanished per #671) must read back as NotCreated, NOT Stopped. Collapsing the
// two makes a cell whose containers were lost indistinguishable from a normally
// stopped cell. The reconciler still treats NotCreated as terminal alongside
// Stopped (see refresh.go), so the cell-level derivation reaches a terminal
// state either way — only the reported per-container STATE differs.
func TestGetContainerState_ContainerAbsentReturnsNotCreated(t *testing.T) {
	realm, space, stack, cellName := "default", "kukeon", "kukeon", "web"
	containerID, containerdID := "root", "kukeon_kukeon_web_root"

	fake := &deleteCellFakeClient{
		existsContainerFn: func(_, _ string) (bool, error) { return false, nil },
	}
	r := newDeleteCellTestExec(t, fake)
	seedDeleteCellRealm(t, r, realm)

	cell := containerStateCell(realm, space, stack, cellName, containerID, containerdID)

	state, err := r.GetContainerState(cell, containerID)
	if err != nil {
		t.Fatalf("GetContainerState: unexpected error: %v", err)
	}
	if state != intmodel.ContainerStateNotCreated {
		t.Errorf(
			"GetContainerState = %v, want NotCreated (no containerd record must not collapse to Stopped, #670)",
			state,
		)
	}
}

// TestGetContainerState_TransientErrorStaysUnknown locks down the narrowing
// the Layer 2 fix performs: a TaskStatus failure that is NOT wrapped with
// ErrTaskNotFound must stay Unknown so a transient containerd RPC blip
// cannot flip a healthy cell to Stopped (and trigger AutoDelete /
// wind-down on the next reconcile pass).
func TestGetContainerState_TransientErrorStaysUnknown(t *testing.T) {
	realm, space, stack, cellName := "default", "kukeon", "kukeon", "web"
	containerID, containerdID := "root", "kukeon_kukeon_web_root"

	fake := &deleteCellFakeClient{
		existsContainerFn: func(_, _ string) (bool, error) { return true, nil },
		taskStatusFn: func(_, _ string) (containerd.Status, error) {
			return containerd.Status{}, errors.New("containerd: rpc transport closed")
		},
	}
	r := newDeleteCellTestExec(t, fake)
	seedDeleteCellRealm(t, r, realm)

	cell := containerStateCell(realm, space, stack, cellName, containerID, containerdID)

	state, err := r.GetContainerState(cell, containerID)
	if err != nil {
		t.Fatalf("GetContainerState: unexpected error: %v", err)
	}
	if state != intmodel.ContainerStateUnknown {
		t.Errorf(
			"GetContainerState = %v, want Unknown (transient RPC errors without ErrTaskNotFound must NOT flip to Stopped — defensive against #301-class hiccups)",
			state,
		)
	}
}

// TestReconcileCell_PostReboot_TransitionsToStopped is the headline #543
// end-to-end: a cell that was Ready before a host reboot (latch already
// closed, containerd container records survive) must transition to
// Stopped within one reconcile pass once the daemon comes back, even
// though the cgroup is gone (tmpfs-backed cgroups don't persist across
// reboot). Without the Layer 1 fix (drop the cgroup-existence gate
// around deriveCellState) AND the Layer 2 fix (TaskStatus failing with
// ErrTaskNotFound after ExistsContainer succeeded ⇒ Stopped, not
// Unknown), the cell stays parked at Unknown and the operator must
// manually `kuke purge cell <name>` for each orphan.
func TestReconcileCell_PostReboot_TransitionsToStopped(t *testing.T) {
	realm, space, stack, cellName := "default", "kukeon", "kukeon", "web"
	rootID := "root"
	workloadID := "workload"
	rootContainerdID := space + "_" + stack + "_" + cellName + "_" + rootID
	workloadContainerdID := space + "_" + stack + "_" + cellName + "_" + workloadID

	fake := &deleteCellFakeClient{
		// Cgroup absent: the tmpfs-backed /sys/fs/cgroup/kukeon/... tree
		// is wiped on reboot and not recreated until the daemon
		// rebuilds it.
		loadCgroupFn: func(string, string) (*cgroup2.Manager, error) {
			return nil, errors.New("cgroup path does not exist")
		},
		// Containerd container records survive the reboot — both root and
		// non-root.
		existsContainerFn: func(_, _ string) (bool, error) { return true, nil },
		// Tasks are gone — the kernel side wiped them with the cgroup,
		// but the boltdb-side container entries persist.
		taskStatusFn: func(_, _ string) (containerd.Status, error) {
			return containerd.Status{}, fmt.Errorf("%w: %w", errdefs.ErrTaskNotFound, errors.New("task: not found"))
		},
	}
	r := newDeleteCellTestExec(t, fake)
	seedDeleteCellRealm(t, r, realm)
	seedPostRebootCell(t, r, realm, space, stack, cellName, rootID, workloadID, rootContainerdID, workloadContainerdID)

	// Build the cell the way the reconcile loop would — load it back so
	// the test exercises the same code path daemon does on first tick
	// after restart.
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

	updatedCell, outcome, err := r.ReconcileCell(cell)
	if err != nil {
		t.Fatalf("ReconcileCell: unexpected error: %v", err)
	}
	if !outcome.Updated {
		t.Errorf(
			"ReconcileCell outcome.Updated = false; want true (post-reboot state transition Ready → Stopped must be reported as an update)",
		)
	}
	if updatedCell.Status.State != intmodel.CellStateStopped {
		t.Errorf(
			"ReconcileCell cell.Status.State = %v, want Stopped (post-reboot cells must transition out of Unknown so cellStateAutoDeleteTriggers and shouldWindDownCell become eligible)",
			updatedCell.Status.State,
		)
	}
	if !updatedCell.Status.ReadyObserved {
		t.Errorf(
			"ReconcileCell cell.Status.ReadyObserved = false; want true (the ReadyObserved latch must survive a reboot — it's the AutoDelete gate)",
		)
	}
}

// seedPostRebootCell writes a cell metadata doc shaped like the on-disk
// snapshot left by a clean shutdown / reboot: state Ready, ReadyObserved
// true (the latch persisted), and both root + workload containers carrying
// explicit ContainerdIDs so the reconcile path can look them up.
func seedPostRebootCell(
	t *testing.T,
	r *Exec,
	realm, space, stack, cellName, rootID, workloadID, rootContainerdID, workloadContainerdID string,
) {
	t.Helper()
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
					ID:           rootID,
					ContainerdID: rootContainerdID,
					RealmID:      realm,
					SpaceID:      space,
					StackID:      stack,
					CellID:       cellName,
					Image:        "alpine:latest",
					Root:         true,
				},
				{
					ID:           workloadID,
					ContainerdID: workloadContainerdID,
					RealmID:      realm,
					SpaceID:      space,
					StackID:      stack,
					CellID:       cellName,
					Image:        "nginx:latest",
				},
			},
		},
		Status: v1beta1.CellStatus{
			State:         v1beta1.CellStateReady,
			ReadyObserved: true,
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
