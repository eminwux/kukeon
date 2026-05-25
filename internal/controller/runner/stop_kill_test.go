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

//nolint:testpackage // exercises *Exec.StopCell / *Exec.KillCell against an in-package ctr.Client fake
package runner

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	cgroup2 "github.com/containerd/cgroups/v2/cgroup2"
	apitypes "github.com/containerd/containerd/api/types"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/metadata"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// stopKillFakeClient is the minimal ctr.Client fake used by the StopCell /
// KillCell snapshot-preservation regression guards. Only the methods StopCell
// / KillCell exercise are non-trivial; everything else returns the zero value
// to satisfy the ctr.Client interface.
type stopKillFakeClient struct {
	stopContainerFn       func(namespace, id string, opts ctr.StopContainerOptions) (*containerd.ExitStatus, error)
	killContainerTaskFn   func(namespace, id string) error
	deleteContainerCalls  int64
	stopContainerCalls    int64
	killContainerTaskHits int64
}

func (c *stopKillFakeClient) Connect() error { return nil }
func (c *stopKillFakeClient) Close() error   { return nil }

func (c *stopKillFakeClient) CreateNamespace(string) error             { return nil }
func (c *stopKillFakeClient) DeleteNamespace(string) error             { return nil }
func (c *stopKillFakeClient) ListNamespaces() ([]string, error)        { return nil, nil }
func (c *stopKillFakeClient) GetNamespace(string) (string, error)      { return "", nil }
func (c *stopKillFakeClient) ExistsNamespace(string) (bool, error)     { return false, nil }
func (c *stopKillFakeClient) CleanupNamespaceResources(string, string) error {
	return nil
}

func (c *stopKillFakeClient) GetCgroupMountpoint() string               { return "" }
func (c *stopKillFakeClient) GetCurrentCgroupPath() (string, error)     { return "", nil }
func (c *stopKillFakeClient) CgroupPath(string, string) (string, error) { return "", nil }
func (c *stopKillFakeClient) NewCgroup(ctr.CgroupSpec) (*cgroup2.Manager, error) {
	//nolint:nilnil // cgroup2.Manager has unexported fields; the test path discards the value
	return nil, nil
}
func (c *stopKillFakeClient) LoadCgroup(string, string) (*cgroup2.Manager, error) {
	//nolint:nilnil // same as NewCgroup
	return nil, nil
}
func (c *stopKillFakeClient) DeleteCgroup(string, string) error { return nil }
func (c *stopKillFakeClient) EnsureSubtreeControllers(string, string, []string) ([]string, error) {
	return nil, nil
}
func (c *stopKillFakeClient) EnableCellSubtreeControllers(string, string, []string) ([]string, error) {
	return nil, nil
}
func (c *stopKillFakeClient) EnableCellAllSubtreeControllers(string, string) ([]string, error) {
	return nil, nil
}
func (c *stopKillFakeClient) RelocateProcessesToLeaf(string, string, string) error { return nil }

func (c *stopKillFakeClient) CreateContainerFromSpec(
	string, intmodel.ContainerSpec, []ctr.RegistryCredentials, ...ctr.BuildOption,
) (containerd.Container, error) {
	//nolint:nilnil // not invoked by StopCell / KillCell
	return nil, nil
}

func (c *stopKillFakeClient) CreateContainer(
	string, ctr.ContainerSpec, []ctr.RegistryCredentials,
) (containerd.Container, error) {
	//nolint:nilnil // not invoked by StopCell / KillCell
	return nil, nil
}

func (c *stopKillFakeClient) GetContainer(string, string) (containerd.Container, error) {
	return nil, errdefs.ErrContainerNotFound
}

func (c *stopKillFakeClient) ListContainers(string, ...string) ([]containerd.Container, error) {
	return nil, nil
}

func (c *stopKillFakeClient) ExistsContainer(string, string) (bool, error) {
	return false, nil
}

func (c *stopKillFakeClient) DeleteContainer(string, string, ctr.ContainerDeleteOptions) error {
	atomic.AddInt64(&c.deleteContainerCalls, 1)
	return nil
}

func (c *stopKillFakeClient) StartContainer(
	string, ctr.ContainerSpec, ctr.TaskSpec,
) (containerd.Task, error) {
	//nolint:nilnil // not invoked by StopCell / KillCell
	return nil, nil
}

func (c *stopKillFakeClient) StopContainer(
	namespace, id string, opts ctr.StopContainerOptions,
) (*containerd.ExitStatus, error) {
	atomic.AddInt64(&c.stopContainerCalls, 1)
	if c.stopContainerFn != nil {
		return c.stopContainerFn(namespace, id, opts)
	}
	return nil, nil
}

func (c *stopKillFakeClient) TaskStatus(string, string) (containerd.Status, error) {
	return containerd.Status{}, nil
}

func (c *stopKillFakeClient) TaskMetrics(string, string) (*apitypes.Metric, error) {
	//nolint:nilnil // not invoked by StopCell / KillCell
	return nil, nil
}

func (c *stopKillFakeClient) ContainerProcessUID(string, containerd.Container) (uint32, error) {
	return 0, nil
}

func (c *stopKillFakeClient) LoadImage(string, io.Reader) ([]string, error) {
	return nil, nil
}
func (c *stopKillFakeClient) ListImages(string) ([]ctr.ImageInfo, error) {
	return nil, nil
}
func (c *stopKillFakeClient) GetImage(string, string) (ctr.ImageInfo, error) {
	return ctr.ImageInfo{}, nil
}
func (c *stopKillFakeClient) DeleteImage(string, string) error { return nil }

var _ ctr.Client = (*stopKillFakeClient)(nil)

func newStopKillTestExec(t *testing.T, fake *stopKillFakeClient) *Exec {
	t.Helper()
	cniCacheDir := t.TempDir()
	return &Exec{
		ctx:       context.Background(),
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		ctrClient: fake,
		opts:      Options{RunPath: t.TempDir()},
		cniConf:   &cni.Conf{CniCacheDir: cniCacheDir},
	}
}

func seedStopKillRealm(t *testing.T, r *Exec, realmName string) {
	t.Helper()
	doc := v1beta1.RealmDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindRealm,
		Metadata:   v1beta1.RealmMetadata{Name: realmName},
		Spec:       v1beta1.RealmSpec{Namespace: realmName + ".kukeon.io"},
	}
	path := fs.RealmMetadataPath(r.opts.RunPath, realmName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir realm metadata dir: %v", err)
	}
	if err := metadata.WriteMetadata(r.ctx, r.logger, doc, path); err != nil {
		t.Fatalf("write realm metadata: %v", err)
	}
}

func seedStopKillSpace(t *testing.T, r *Exec, realm, space string) {
	t.Helper()
	doc := v1beta1.SpaceDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindSpace,
		Metadata:   v1beta1.SpaceMetadata{Name: space},
		// An explicit CNIConfigPath short-circuits the default-path build in
		// resolveSpaceCNIConfigPath; the file is never read because the
		// stop/kill paths only forward it to detach calls the fake ignores.
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

func seedStopKillCell(t *testing.T, r *Exec, realm, space, stack, cellName string) {
	t.Helper()
	containerdID := space + "_" + stack + "_" + cellName + "_workload"
	rootContainerdID := space + "_" + stack + "_" + cellName + "_root"
	doc := v1beta1.CellDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCell,
		Metadata:   v1beta1.CellMetadata{Name: cellName},
		Spec: v1beta1.CellSpec{
			ID:              cellName,
			RealmID:         realm,
			SpaceID:         space,
			StackID:         stack,
			RootContainerID: "root",
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
				{
					ID:           "workload",
					ContainerdID: containerdID,
					RealmID:      realm,
					SpaceID:      space,
					StackID:      stack,
					CellID:       cellName,
					Image:        "alpine:latest",
				},
			},
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

func buildStopKillCellRequest(realm, space, stack, cellName string) intmodel.Cell {
	return intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: cellName},
		Spec: intmodel.CellSpec{
			ID:        cellName,
			RealmName: realm,
			SpaceName: space,
			StackName: stack,
		},
	}
}

// TestStopCell_PreservesContainerRecord is the issue #867 regression guard
// for the stop verb. Pre-fix, stopCellLocked followed every task-shutdown
// step with DeleteContainer{SnapshotCleanup: true}, silently wiping the
// containerd container record (and its overlay snapshot) — the exact
// teardown delete is supposed to own exclusively. The fix removes those
// calls, leaving the record + snapshot in place so the next StartCell can
// resume the task on the preserved overlay (the marker-file invariant the
// issue's `--reuse` AC depends on).
func TestStopCell_PreservesContainerRecord(t *testing.T) {
	realm, space, stack, cellName := "kuke-system", "kukeon", "kukeon", "demo"
	fake := &stopKillFakeClient{}
	r := newStopKillTestExec(t, fake)
	seedStopKillRealm(t, r, realm)
	seedStopKillSpace(t, r, realm, space)
	seedStopKillCell(t, r, realm, space, stack, cellName)

	if _, err := r.StopCell(buildStopKillCellRequest(realm, space, stack, cellName)); err != nil {
		t.Fatalf("StopCell: unexpected error: %v", err)
	}

	if got := atomic.LoadInt64(&fake.deleteContainerCalls); got != 0 {
		t.Errorf(
			"StopCell must not call DeleteContainer (issue #867: stop owns task teardown only, delete owns record+snapshot); got %d calls",
			got,
		)
	}
	if got := atomic.LoadInt64(&fake.stopContainerCalls); got == 0 {
		t.Errorf("StopCell must still call StopContainer to tear down the task; got 0 calls")
	}
}

// TestKillCell_PreservesContainerRecord is the issue #867 regression guard
// for the kill verb. kill is the SIGKILL-escalated cousin of stop; the
// documented stop/kill/delete split distinguishes them on signal escalation,
// not on snapshot teardown. Pre-fix, killCellLocked also called
// DeleteContainer{SnapshotCleanup: true}, wiping the overlay on every kill;
// after the fix, kill leaves the record + snapshot in place.
func TestKillCell_PreservesContainerRecord(t *testing.T) {
	realm, space, stack, cellName := "kuke-system", "kukeon", "kukeon", "demo"
	fake := &stopKillFakeClient{
		stopContainerFn: func(_, _ string, _ ctr.StopContainerOptions) (*containerd.ExitStatus, error) {
			// KillCell calls StopContainer with Force=true via killContainerTask;
			// the per-task SIGKILL surface is the only stop-equivalent the fake
			// needs to acknowledge.
			return nil, nil
		},
	}
	r := newStopKillTestExec(t, fake)
	seedStopKillRealm(t, r, realm)
	seedStopKillSpace(t, r, realm, space)
	seedStopKillCell(t, r, realm, space, stack, cellName)

	if _, err := r.KillCell(buildStopKillCellRequest(realm, space, stack, cellName)); err != nil {
		t.Fatalf("KillCell: unexpected error: %v", err)
	}

	if got := atomic.LoadInt64(&fake.deleteContainerCalls); got != 0 {
		t.Errorf(
			"KillCell must not call DeleteContainer (issue #867: kill is stop-with-SIGKILL, not stop+delete); got %d calls",
			got,
		)
	}
}

// TestStopContainer_PreservesContainerRecord is the per-container counterpart
// of TestStopCell_PreservesContainerRecord. `kuke stop container` is the
// single-container verb the issue's AC also covers; pre-fix StopContainer
// matched stopCellLocked's destructive teardown.
func TestStopContainer_PreservesContainerRecord(t *testing.T) {
	realm, space, stack, cellName := "kuke-system", "kukeon", "kukeon", "demo"
	fake := &stopKillFakeClient{}
	r := newStopKillTestExec(t, fake)
	seedStopKillRealm(t, r, realm)
	seedStopKillSpace(t, r, realm, space)
	seedStopKillCell(t, r, realm, space, stack, cellName)

	// Reuse the seeded workload container ID — StopContainer looks the spec
	// up by base ID, not by ContainerdID.
	cell, err := r.GetCell(buildStopKillCellRequest(realm, space, stack, cellName))
	if err != nil {
		t.Fatalf("GetCell: %v", err)
	}
	if stopErr := r.StopContainer(cell, "workload"); stopErr != nil &&
		!errors.Is(stopErr, errdefs.ErrTaskNotFound) {
		t.Fatalf("StopContainer: unexpected error: %v", stopErr)
	}

	if got := atomic.LoadInt64(&fake.deleteContainerCalls); got != 0 {
		t.Errorf(
			"StopContainer must not call DeleteContainer (issue #867: stop-container verb mirrors the cell-wide stop contract); got %d calls",
			got,
		)
	}
}
