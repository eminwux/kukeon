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

//nolint:testpackage // exercises *Exec.RecreateCell against an in-package ctr.Client fake
package runner

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/metadata"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
	"github.com/eminwux/kukeon/internal/util/naming"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// recreateCellTask is a near-empty containerd.Task. StartCell only reads
// Pid() on the root task it starts; every other method is promoted from the
// embedded nil interface and panics if called, which is the desired tripwire
// if StartCell ever grows a new dependency on the returned task.
type recreateCellTask struct {
	containerd.Task
	pid uint32
}

func (t recreateCellTask) Pid() uint32 { return t.pid }

// recreateCellFakeClient reuses deleteCellFakeClient for the bulk of the
// ctr.Client surface and adds the StartContainer hook RecreateCell -> StartCell
// needs (the embedded fake returns a nil task, which would panic at Pid()), plus
// a CreateContainerFromSpec hook so a non-root container creation can be made to
// fail mid-recreate.
type recreateCellFakeClient struct {
	*deleteCellFakeClient
	startContainerFn          func(namespace string, spec ctr.ContainerSpec, taskSpec ctr.TaskSpec) (containerd.Task, error)
	createContainerFromSpecFn func(namespace string, spec intmodel.ContainerSpec, creds []ctr.RegistryCredentials, opts ...ctr.BuildOption) (containerd.Container, error)
}

func (c *recreateCellFakeClient) StartContainer(
	namespace string, spec ctr.ContainerSpec, taskSpec ctr.TaskSpec,
) (containerd.Task, error) {
	if c.startContainerFn != nil {
		return c.startContainerFn(namespace, spec, taskSpec)
	}
	return c.deleteCellFakeClient.StartContainer(namespace, spec, taskSpec)
}

func (c *recreateCellFakeClient) CreateContainerFromSpec(
	namespace string, spec intmodel.ContainerSpec, creds []ctr.RegistryCredentials, opts ...ctr.BuildOption,
) (containerd.Container, error) {
	if c.createContainerFromSpecFn != nil {
		return c.createContainerFromSpecFn(namespace, spec, creds, opts...)
	}
	return c.deleteCellFakeClient.CreateContainerFromSpec(namespace, spec, creds, opts...)
}

var _ ctr.Client = (*recreateCellFakeClient)(nil)

func newRecreateCellTestExec(t *testing.T, fake *recreateCellFakeClient) *Exec {
	t.Helper()
	runPath := t.TempDir()
	cniCacheDir := t.TempDir()
	return &Exec{
		ctx:       context.Background(),
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		ctrClient: fake,
		opts:      Options{RunPath: runPath},
		cniConf:   &cni.Conf{CniCacheDir: cniCacheDir},
		nowFn:     func() time.Time { return time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC) },
	}
}

func seedRecreateCellSpace(t *testing.T, r *Exec, realm, space string) {
	t.Helper()
	doc := v1beta1.SpaceDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindSpace,
		Metadata:   v1beta1.SpaceMetadata{Name: space},
		// An explicit CNIConfigPath short-circuits resolveSpaceCNIConfigPath's
		// default-path build; the path is never read because the root is
		// host-network (CNI ADD is skipped).
		Spec: v1beta1.SpaceSpec{RealmID: realm, CNIConfigPath: filepath.Join(t.TempDir(), "cni.conflist")},
	}
	path := fs.SpaceMetadataPath(r.opts.RunPath, realm, space)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir space metadata dir: %v", err)
	}
	if err := metadata.WriteMetadata(r.ctx, r.logger, doc, path); err != nil {
		t.Fatalf("write space metadata: %v", err)
	}
}

// recreateCellHostNetworkCell returns an existing (on-disk) cell and the
// desired cell carrying a changed root image. The root is host-network so
// StartCell skips the CNI dance the unit harness can't satisfy; a single
// root container keeps StartCell's non-root recreate loop out of the picture.
func recreateCellHostNetworkCell(realm, space, stack, name, image string) intmodel.Cell {
	return intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: name},
		Spec: intmodel.CellSpec{
			ID:              name,
			RealmName:       realm,
			SpaceName:       space,
			StackName:       stack,
			RootContainerID: "root",
			Containers: []intmodel.ContainerSpec{
				{ID: "root", Root: true, HostNetwork: true, Image: image},
			},
		},
		Status: intmodel.CellStatus{State: intmodel.CellStatePending},
	}
}

// TestRecreateCell_StartsTasksBeforeReady is the regression guard for issue
// #682: RecreateCell (the apply root-container-change handler at
// apply/reconcile.go's diff.RootContainerChanged branch) used to stamp Ready
// straight after createCellContainers, which only creates container *records*
// — no task running, no CNI ADD. The fix routes the recreated cell through
// StartCell so the root task is actually started before Ready is persisted.
func TestRecreateCell_StartsTasksBeforeReady(t *testing.T) {
	const (
		realm = "default"
		space = "default"
		stack = "default"
		cell  = "web"
	)

	var startContainerCalls int
	fake := &recreateCellFakeClient{
		deleteCellFakeClient: &deleteCellFakeClient{},
		startContainerFn: func(_ string, _ ctr.ContainerSpec, _ ctr.TaskSpec) (containerd.Task, error) {
			startContainerCalls++
			return recreateCellTask{pid: 4242}, nil
		},
	}
	r := newRecreateCellTestExec(t, fake)
	seedDeleteCellRealm(t, r, realm)
	seedRecreateCellSpace(t, r, realm, space)

	// Seed the existing cell with the old root image; persisting it is what
	// RecreateCell's leading GetCell reads back.
	existing := recreateCellHostNetworkCell(realm, space, stack, cell, "alpine:3.18")
	existing.Status.State = intmodel.CellStateReady
	if err := r.UpdateCellMetadata(existing); err != nil {
		t.Fatalf("seed existing cell: %v", err)
	}

	desired := recreateCellHostNetworkCell(realm, space, stack, cell, "alpine:3.19")

	got, err := r.RecreateCell(desired)
	if err != nil {
		t.Fatalf("RecreateCell: %v", err)
	}

	if startContainerCalls == 0 {
		t.Error("RecreateCell did not start any task — Ready was stamped over created-but-not-started containers (issue #682)")
	}
	if got.Status.State != intmodel.CellStateReady {
		t.Errorf("returned cell State = %v, want Ready", got.Status.State)
	}

	persisted, err := r.GetCell(desired)
	if err != nil {
		t.Fatalf("GetCell after RecreateCell: %v", err)
	}
	if persisted.Status.State != intmodel.CellStateReady {
		t.Errorf("persisted cell State = %v, want Ready", persisted.Status.State)
	}
}

// TestRecreateCell_DoesNotStampReadyWhenStartFails pins AC #2: Ready is never
// persisted on created-but-not-started containers. When the root task fails to
// start, RecreateCell must surface the error and the cell must not be left
// Ready — the precise divergence the old markCellReady-without-start produced.
func TestRecreateCell_DoesNotStampReadyWhenStartFails(t *testing.T) {
	const (
		realm = "default"
		space = "default"
		stack = "default"
		cell  = "web"
	)

	startBoom := errors.New("task start refused")
	fake := &recreateCellFakeClient{
		deleteCellFakeClient: &deleteCellFakeClient{},
		startContainerFn: func(_ string, _ ctr.ContainerSpec, _ ctr.TaskSpec) (containerd.Task, error) {
			return nil, startBoom
		},
	}
	r := newRecreateCellTestExec(t, fake)
	seedDeleteCellRealm(t, r, realm)
	seedRecreateCellSpace(t, r, realm, space)

	existing := recreateCellHostNetworkCell(realm, space, stack, cell, "alpine:3.18")
	existing.Status.State = intmodel.CellStateReady
	if err := r.UpdateCellMetadata(existing); err != nil {
		t.Fatalf("seed existing cell: %v", err)
	}

	desired := recreateCellHostNetworkCell(realm, space, stack, cell, "alpine:3.19")

	if _, err := r.RecreateCell(desired); err == nil {
		t.Fatal("RecreateCell returned nil error despite the root task failing to start")
	}

	persisted, err := r.GetCell(desired)
	if err != nil {
		t.Fatalf("GetCell after failed RecreateCell: %v", err)
	}
	if persisted.Status.State == intmodel.CellStateReady {
		t.Error("cell persisted Ready after the root task failed to start (issue #682 AC #2)")
	}
}

// recreateCellMultiContainerCell builds an existing (on-disk) multi-container
// cell and the matching desired cell carrying a changed root image. Both the
// host-network root and the single workload carry their deterministic
// containerd IDs so the rollback's killCellLocked has concrete IDs to tear
// down. The desired cell is a deep copy with the new image so RecreateCell's
// leading GetCell reads the existing one back and createCellContainers
// operates on the desired one.
func recreateCellMultiContainerCell(
	t *testing.T, realm, space, stack, name, oldImage, newImage string,
) (existing, desired intmodel.Cell, rootID, workloadID string) {
	t.Helper()
	rootID, err := naming.BuildRootContainerdID(space, stack, name)
	if err != nil {
		t.Fatalf("BuildRootContainerdID: %v", err)
	}
	workloadID, err = naming.BuildContainerdID(space, stack, name, "app")
	if err != nil {
		t.Fatalf("BuildContainerdID: %v", err)
	}
	build := func(image string) intmodel.Cell {
		return intmodel.Cell{
			Metadata: intmodel.CellMetadata{Name: name},
			Spec: intmodel.CellSpec{
				ID:              name,
				RealmName:       realm,
				SpaceName:       space,
				StackName:       stack,
				RootContainerID: "root",
				Containers: []intmodel.ContainerSpec{
					{ID: "root", Root: true, HostNetwork: true, Image: image, ContainerdID: rootID},
					{ID: "app", Image: "alpine:3.18", ContainerdID: workloadID},
				},
			},
			Status: intmodel.CellStatus{State: intmodel.CellStatePending},
		}
	}
	return build(oldImage), build(newImage), rootID, workloadID
}

// TestRecreateCell_RollsBackWhenContainerCreateFails is the regression guard
// for issue #718: a createCellContainers failure on a later container used to
// propagate the error with no rollback — the root container already created
// leaked as an orphan and the cell metadata was never flipped to Failed. The
// fix routes that path through markCellFailed("RecreateCellFailed"), which
// tears the created containers/IPAM down and stamps the terminal Failed state.
func TestRecreateCell_RollsBackWhenContainerCreateFails(t *testing.T) {
	const (
		realm = "default"
		space = "default"
		stack = "default"
		cell  = "web"
	)

	createBoom := errors.New("workload container create refused")
	fake := &recreateCellFakeClient{
		deleteCellFakeClient: &deleteCellFakeClient{},
		// Fail the non-root container creation inside createCellContainers so
		// RecreateCell never reaches the start phase — the genuinely-unhandled
		// rollback path the issue targets.
		createContainerFromSpecFn: func(
			_ string, _ intmodel.ContainerSpec, _ []ctr.RegistryCredentials, _ ...ctr.BuildOption,
		) (containerd.Container, error) {
			return nil, createBoom
		},
	}
	existing, desired, _, _ := recreateCellMultiContainerCell(
		t, realm, space, stack, cell, "alpine:3.18", "alpine:3.19",
	)
	r := newRecreateCellTestExec(t, fake)
	seedDeleteCellRealm(t, r, realm)
	seedRecreateCellSpace(t, r, realm, space)

	existing.Status.State = intmodel.CellStateReady
	if err := r.UpdateCellMetadata(existing); err != nil {
		t.Fatalf("seed existing cell: %v", err)
	}

	if _, err := r.RecreateCell(desired); err == nil {
		t.Fatal("RecreateCell returned nil error despite createCellContainers failing")
	}

	persisted, err := r.GetCell(desired)
	if err != nil {
		t.Fatalf("GetCell after failed RecreateCell: %v", err)
	}
	// State=Failed + Reason="RecreateCellFailed" is produced only by the new
	// rollback defer, and markCellFailed unconditionally runs killCellLocked
	// (the orphan-container + IPAM teardown, covered by kill_test.go) before
	// stamping it — so this pair is the precise routing guard. Pre-fix the
	// createCellContainers error propagated with the cell left Ready.
	if persisted.Status.State != intmodel.CellStateFailed {
		t.Errorf("cell State = %v, want Failed after createCellContainers failure (issue #718)", persisted.Status.State)
	}
	if persisted.Status.Reason != "RecreateCellFailed" {
		t.Errorf("cell Reason = %q, want \"RecreateCellFailed\"", persisted.Status.Reason)
	}
}
