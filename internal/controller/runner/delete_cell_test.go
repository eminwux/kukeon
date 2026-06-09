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

//nolint:testpackage // exercises *Exec.DeleteCell against an in-package ctr.Client fake
package runner

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
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

// deleteCellFakeClient is a near-empty ctr.Client used to drive DeleteCell
// tests. Methods unused by DeleteCell return zero values rather than panic so
// adding a new code path through ctr.Client doesn't require revisiting this
// fake — the controller-level error assertions catch unintended behavior.
// Also reused by container_state / reconcile tests that need ExistsContainer
// + TaskStatus hooks (the post-reboot reproduction in #543), and by the
// reconcile-heal tests (#855) that need NewCgroup +
// EnableCellSubtreeControllers hooks to observe the re-assert pass.
type deleteCellFakeClient struct {
	deleteContainerFn   func(namespace, id string, opts ctr.ContainerDeleteOptions) error
	stopContainerFn     func(namespace, id string, opts ctr.StopContainerOptions) (*containerd.ExitStatus, error)
	listContainersFn    func(namespace string, filters ...string) ([]containerd.Container, error)
	existsContainerFn   func(namespace, id string) (bool, error)
	taskStatusFn        func(namespace, id string) (containerd.Status, error)
	loadCgroupFn        func(group, mountpoint string) (*cgroup2.Manager, error)
	newCgroupFn         func(spec ctr.CgroupSpec) (*cgroup2.Manager, error)
	enableCellSubtreeFn func(group, mountpoint string, controllers []string) ([]string, error)
}

func (c *deleteCellFakeClient) Connect() error { return nil }
func (c *deleteCellFakeClient) Close() error   { return nil }

func (c *deleteCellFakeClient) CreateNamespace(string) error { return nil }
func (c *deleteCellFakeClient) DeleteNamespace(string) error { return nil }
func (c *deleteCellFakeClient) ListNamespaces() ([]string, error) {
	return nil, nil
}
func (c *deleteCellFakeClient) GetNamespace(string) (string, error)  { return "", nil }
func (c *deleteCellFakeClient) ExistsNamespace(string) (bool, error) { return false, nil }
func (c *deleteCellFakeClient) CleanupNamespaceResources(string, string) error {
	return nil
}

func (c *deleteCellFakeClient) GetCgroupMountpoint() string               { return "" }
func (c *deleteCellFakeClient) GetCurrentCgroupPath() (string, error)     { return "", nil }
func (c *deleteCellFakeClient) CgroupPath(string, string) (string, error) { return "", nil }
func (c *deleteCellFakeClient) NewCgroup(spec ctr.CgroupSpec) (*cgroup2.Manager, error) {
	if c.newCgroupFn != nil {
		return c.newCgroupFn(spec)
	}
	//nolint:nilnil // cgroup2.Manager has unexported fields; the test path discards the value
	return nil, nil
}

func (c *deleteCellFakeClient) LoadCgroup(group, mountpoint string) (*cgroup2.Manager, error) {
	if c.loadCgroupFn != nil {
		return c.loadCgroupFn(group, mountpoint)
	}
	//nolint:nilnil // same as NewCgroup
	return nil, nil
}
func (c *deleteCellFakeClient) DeleteCgroup(string, string) error { return nil }
func (c *deleteCellFakeClient) EnsureSubtreeControllers(string, string, []string) ([]string, error) {
	return nil, nil
}

func (c *deleteCellFakeClient) EnableCellSubtreeControllers(
	group, mountpoint string, controllers []string,
) ([]string, error) {
	if c.enableCellSubtreeFn != nil {
		return c.enableCellSubtreeFn(group, mountpoint, controllers)
	}
	return nil, nil
}

func (c *deleteCellFakeClient) EnableCellAllSubtreeControllers(string, string) ([]string, error) {
	return nil, nil
}
func (c *deleteCellFakeClient) RelocateProcessesToLeaf(string, string, string) error { return nil }

func (c *deleteCellFakeClient) CreateContainerFromSpec(
	string, intmodel.ContainerSpec, []ctr.RegistryCredentials, ...ctr.BuildOption,
) (containerd.Container, error) {
	//nolint:nilnil // not invoked by DeleteCell; present only to satisfy ctr.Client
	return nil, nil
}

func (c *deleteCellFakeClient) CreateContainer(
	string, ctr.ContainerSpec, []ctr.RegistryCredentials,
) (containerd.Container, error) {
	//nolint:nilnil // same as CreateContainerFromSpec
	return nil, nil
}

func (c *deleteCellFakeClient) GetContainer(string, string) (containerd.Container, error) {
	return nil, errdefs.ErrContainerNotFound
}

func (c *deleteCellFakeClient) ListContainers(namespace string, filters ...string) ([]containerd.Container, error) {
	if c.listContainersFn != nil {
		return c.listContainersFn(namespace, filters...)
	}

	return nil, nil
}

func (c *deleteCellFakeClient) ExistsContainer(namespace, id string) (bool, error) {
	if c.existsContainerFn != nil {
		return c.existsContainerFn(namespace, id)
	}
	return false, nil
}

func (c *deleteCellFakeClient) DeleteContainer(namespace, id string, opts ctr.ContainerDeleteOptions) error {
	if c.deleteContainerFn != nil {
		return c.deleteContainerFn(namespace, id, opts)
	}
	return nil
}

func (c *deleteCellFakeClient) StartContainer(
	string, ctr.ContainerSpec, ctr.TaskSpec,
) (containerd.Task, error) {
	//nolint:nilnil // not invoked by DeleteCell; present only to satisfy ctr.Client
	return nil, nil
}

func (c *deleteCellFakeClient) StopContainer(
	namespace, id string, opts ctr.StopContainerOptions,
) (*containerd.ExitStatus, error) {
	if c.stopContainerFn != nil {
		return c.stopContainerFn(namespace, id, opts)
	}
	return nil, errdefs.ErrTaskNotFound
}

func (c *deleteCellFakeClient) TaskStatus(namespace, id string) (containerd.Status, error) {
	if c.taskStatusFn != nil {
		return c.taskStatusFn(namespace, id)
	}
	return containerd.Status{}, nil
}

func (c *deleteCellFakeClient) TaskMetrics(string, string) (*apitypes.Metric, error) {
	//nolint:nilnil // not invoked by DeleteCell; present only to satisfy ctr.Client
	return nil, nil
}

func (c *deleteCellFakeClient) ContainerProcessUID(string, containerd.Container) (uint32, error) {
	return 0, nil
}

func (c *deleteCellFakeClient) LoadImage(string, io.Reader) ([]string, error) {
	return nil, nil
}

func (c *deleteCellFakeClient) ListImages(string) ([]ctr.ImageInfo, error) {
	return nil, nil
}

func (c *deleteCellFakeClient) GetImage(string, string) (ctr.ImageInfo, error) {
	return ctr.ImageInfo{}, nil
}

func (c *deleteCellFakeClient) ImageChainID(string, string) (string, error) {
	return "", nil
}

func (c *deleteCellFakeClient) ContainerRootChainID(string, string) (string, error) {
	return "", nil
}

func (c *deleteCellFakeClient) DeleteImage(string, string) error { return nil }
func (c *deleteCellFakeClient) PruneImages(string) (ctr.PruneResult, error) {
	return ctr.PruneResult{}, nil
}

func (c *deleteCellFakeClient) NamespaceStorage(string) (ctr.StorageStats, error) {
	return ctr.StorageStats{}, nil
}

var _ ctr.Client = (*deleteCellFakeClient)(nil)

// newDeleteCellTestExec builds a *Exec wired to the fake ctr.Client and an
// isolated RunPath under t.TempDir(). The realm + cell metadata seeded by
// the per-test helpers below live under that path so GetCell / GetRealm read
// off the same tmpdir DeleteCell would later try to remove.
func newDeleteCellTestExec(t *testing.T, fake *deleteCellFakeClient) *Exec {
	t.Helper()
	cniCacheDir := t.TempDir()
	return &Exec{
		ctx:       context.Background(),
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		ctrClient: fake,
		opts:      Options{RunPath: t.TempDir()},
		// cniConf is dereferenced by purgeCNIForContainer's cache-cleanup
		// path even when networkName is empty; point it at a per-test
		// tmpdir so the glob no-ops instead of nil-panicking.
		cniConf: &cni.Conf{CniCacheDir: cniCacheDir},
	}
}

func seedDeleteCellRealm(t *testing.T, r *Exec, realmName string) {
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

// seedDeleteCellCell writes a minimal cell metadata doc with one workload
// container carrying an explicit ContainerdID, so DeleteCell's workload loop
// has something concrete to call DeleteContainer with.
func seedDeleteCellCell(t *testing.T, r *Exec, realm, space, stack, cellName string) string {
	t.Helper()
	containerdID := space + "_" + stack + "_" + cellName + "_workload"
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
	return path
}

// TestDeleteCell_PreservesMetadataOnDeleteContainerFailure is the regression
// guard for issue #371. A non-NotFound DeleteContainer failure used to be
// warn-and-continue: the cell metadata file got removed and `ctr containers
// ls` still showed the orphan, so the next `kuke init` collided on the
// surviving containerd ID with no way for `kuke daemon reset` to recover.
// The new contract: DeleteCell aggregates per-container failures, returns
// them wrapped under ErrDeleteCell, and leaves the metadata file in place so
// the next reset call finds the cell and retries the teardown.
func TestDeleteCell_PreservesMetadataOnDeleteContainerFailure(t *testing.T) {
	realm, space, stack, cellName := "kuke-system", "kukeon", "kukeon", "kukeond"
	workloadID := space + "_" + stack + "_" + cellName + "_workload"

	fake := &deleteCellFakeClient{
		deleteContainerFn: func(_, id string, _ ctr.ContainerDeleteOptions) error {
			if id == workloadID {
				return errors.New("container already exists")
			}
			return nil
		},
	}
	r := newDeleteCellTestExec(t, fake)
	seedDeleteCellRealm(t, r, realm)
	metadataPath := seedDeleteCellCell(t, r, realm, space, stack, cellName)

	cell := buildDeleteCellRequest(realm, space, stack, cellName)
	err := r.DeleteCell(cell)
	if err == nil {
		t.Fatal("DeleteCell: want non-nil error when DeleteContainer fails with non-NotFound, got nil")
	}
	if !errors.Is(err, errdefs.ErrDeleteCell) {
		t.Errorf(
			"DeleteCell error must wrap ErrDeleteCell so the reset CLI flips its `kukeond cell deleted` line to a failure surface; got %v",
			err,
		)
	}
	if _, statErr := os.Stat(metadataPath); statErr != nil {
		t.Errorf(
			"cell metadata must remain on disk when container deletion failed, so the next `kuke daemon reset` can retry; stat err=%v",
			statErr,
		)
	}
}

// TestDeleteCell_ReconcilesOrphanFromEmptyContainerdID covers the second
// contributor named in issue #371: a partial init can persist the cell
// document before the runner has filled in ContainerdID. Pre-fix DeleteCell
// silently `continue`d past the empty-ID entry and removed metadata, leaving
// the containerd-side container behind. The name-based orphan scan must now
// pick it up via the `<space>_<stack>_<cell>_*` prefix and tear it down.
func TestDeleteCell_ReconcilesOrphanFromEmptyContainerdID(t *testing.T) {
	realm, space, stack, cellName := "kuke-system", "kukeon", "kukeon", "kukeond"
	orphanID := space + "_" + stack + "_" + cellName + "_kukeond"

	var deletedIDs []string
	fake := &deleteCellFakeClient{
		deleteContainerFn: func(_, id string, _ ctr.ContainerDeleteOptions) error {
			deletedIDs = append(deletedIDs, id)
			return nil
		},
		listContainersFn: func(string, ...string) ([]containerd.Container, error) {
			return []containerd.Container{stubContainer{id: orphanID}}, nil
		},
	}
	r := newDeleteCellTestExec(t, fake)
	seedDeleteCellRealm(t, r, realm)

	// Seed a cell whose only container has an empty ContainerdID — the
	// repro for the partial-init case the issue calls out. The workload
	// loop must skip it (no ID), the name-based scan must catch it.
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
					ID:      "kukeond",
					RealmID: realm,
					SpaceID: space,
					StackID: stack,
					CellID:  cellName,
					Image:   "alpine:latest",
				},
			},
		},
	}
	cellMetadataPath := fs.CellMetadataPath(r.opts.RunPath, realm, space, stack, cellName)
	if err := os.MkdirAll(filepath.Dir(cellMetadataPath), 0o755); err != nil {
		t.Fatalf("mkdir cell metadata dir: %v", err)
	}
	if err := metadata.WriteMetadata(r.ctx, r.logger, doc, cellMetadataPath); err != nil {
		t.Fatalf("write cell metadata: %v", err)
	}

	if err := r.DeleteCell(buildDeleteCellRequest(realm, space, stack, cellName)); err != nil {
		t.Fatalf("DeleteCell: unexpected error: %v", err)
	}

	var foundOrphan bool
	for _, id := range deletedIDs {
		if id == orphanID {
			foundOrphan = true
			break
		}
	}
	if !foundOrphan {
		t.Errorf("orphan container %q was not deleted; DeleteContainer was called for %v", orphanID, deletedIDs)
	}
	if _, statErr := os.Stat(cellMetadataPath); !os.IsNotExist(statErr) {
		t.Errorf("cell metadata should be removed on full success; stat err=%v", statErr)
	}
}

// TestDeleteCell_SweepsOrphansWhenMetadataAbsent is the regression guard for
// issue #1175. When the cell metadata is already gone (GetCell returns
// ErrCellNotFound), DeleteCell used to bare-`return nil` and declare success
// while the cell's containerd container, Active snapshot, and IPAM reservation
// survived on the host. The idempotent path must now run the name-prefix
// orphan scan and tear down any survivors using request-derived identifiers.
func TestDeleteCell_SweepsOrphansWhenMetadataAbsent(t *testing.T) {
	realm, space, stack, cellName := "default", "default", "default", "kukeon-pr-3"
	rootID := space + "_" + stack + "_" + cellName + "_root"
	workID := space + "_" + stack + "_" + cellName + "_work"

	var deletedIDs []string
	fake := &deleteCellFakeClient{
		deleteContainerFn: func(_, id string, _ ctr.ContainerDeleteOptions) error {
			deletedIDs = append(deletedIDs, id)
			return nil
		},
		listContainersFn: func(string, ...string) ([]containerd.Container, error) {
			return []containerd.Container{
				stubContainer{id: rootID},
				stubContainer{id: workID},
			}, nil
		},
	}
	r := newDeleteCellTestExec(t, fake)
	seedDeleteCellRealm(t, r, realm)
	// Deliberately do NOT seed the cell metadata — GetCell returns
	// ErrCellNotFound, the exact entry point the fix targets.

	if err := r.DeleteCell(buildDeleteCellRequest(realm, space, stack, cellName)); err != nil {
		t.Fatalf("DeleteCell: unexpected error on metadata-absent path: %v", err)
	}

	for _, want := range []string{rootID, workID} {
		var found bool
		for _, id := range deletedIDs {
			if id == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("orphan container %q was not swept on the metadata-absent path; DeleteContainer called for %v", want, deletedIDs)
		}
	}
}

// TestDeleteCell_MetadataAbsentSurfacesSweepFailure locks the host-resource
// idempotency contract (issue #1175): when an orphan teardown fails on the
// metadata-absent path, DeleteCell must surface a wrapped ErrDeleteCell rather
// than silently declare success, so an operator (or the next reset) sees the
// host is still dirty.
func TestDeleteCell_MetadataAbsentSurfacesSweepFailure(t *testing.T) {
	realm, space, stack, cellName := "default", "default", "default", "kukeon-dev-2"
	rootID := space + "_" + stack + "_" + cellName + "_root"

	fake := &deleteCellFakeClient{
		deleteContainerFn: func(_, id string, _ ctr.ContainerDeleteOptions) error {
			if id == rootID {
				return errors.New("containerd busy")
			}
			return nil
		},
		listContainersFn: func(string, ...string) ([]containerd.Container, error) {
			return []containerd.Container{stubContainer{id: rootID}}, nil
		},
	}
	r := newDeleteCellTestExec(t, fake)
	seedDeleteCellRealm(t, r, realm)

	err := r.DeleteCell(buildDeleteCellRequest(realm, space, stack, cellName))
	if err == nil {
		t.Fatal("DeleteCell: want non-nil error when an orphan teardown fails on the metadata-absent path, got nil")
	}
	if !errors.Is(err, errdefs.ErrDeleteCell) {
		t.Errorf("metadata-absent sweep failure must wrap ErrDeleteCell; got %v", err)
	}
}

// TestDeleteCell_MetadataAndRealmAbsentIsIdempotent confirms the historical
// idempotent success is preserved when both the cell and its realm are gone:
// no realm means no containerd namespace, hence nothing to sweep (issue #1175).
func TestDeleteCell_MetadataAndRealmAbsentIsIdempotent(t *testing.T) {
	realm, space, stack, cellName := "default", "default", "default", "kukeon-pr-3"

	fake := &deleteCellFakeClient{
		listContainersFn: func(string, ...string) ([]containerd.Container, error) {
			t.Fatal("ListContainers must not run when the realm is absent")
			return nil, nil
		},
	}
	r := newDeleteCellTestExec(t, fake)
	// Seed neither realm nor cell metadata.

	if err := r.DeleteCell(buildDeleteCellRequest(realm, space, stack, cellName)); err != nil {
		t.Fatalf("DeleteCell: want nil (idempotent) when both cell and realm are gone, got %v", err)
	}
}

// TestFindOrphanContainerIDs locks the prefix-filter behavior the orphan
// scan relies on. The prefix is `<space>_<stack>_<cell>_` and must match
// both `_root` and `_<workload>` IDs while excluding unrelated containers
// that happen to share part of the namespace.
func TestFindOrphanContainerIDs(t *testing.T) {
	fake := &deleteCellFakeClient{
		listContainersFn: func(string, ...string) ([]containerd.Container, error) {
			return []containerd.Container{
				stubContainer{id: "kukeon_kukeon_kukeond_root"},
				stubContainer{id: "kukeon_kukeon_kukeond_kukeond"},
				stubContainer{id: "kukeon_kukeon_other_root"},
				stubContainer{id: "unrelated_id"},
			}, nil
		},
	}
	r := newDeleteCellTestExec(t, fake)

	got, err := r.findOrphanContainerIDs("kuke-system.kukeon.io", "kukeon_kukeon_kukeond_")
	if err != nil {
		t.Fatalf("findOrphanContainerIDs: unexpected error: %v", err)
	}
	want := map[string]bool{
		"kukeon_kukeon_kukeond_root":    true,
		"kukeon_kukeon_kukeond_kukeond": true,
	}
	if len(got) != len(want) {
		t.Fatalf("findOrphanContainerIDs returned %d IDs, want %d; got=%v", len(got), len(want), got)
	}
	for _, id := range got {
		if !want[id] {
			t.Errorf("findOrphanContainerIDs returned unexpected ID %q (not under the cell prefix)", id)
		}
	}
}

// stubContainer is a minimal containerd.Container that only carries an ID.
// The orphan scan reads .ID() and nothing else, so every other method is
// satisfied by the embedded interface (calls panic at runtime, which is
// what we want if the scan grows new dependencies without test coverage).
type stubContainer struct {
	containerd.Container

	id string
}

func (c stubContainer) ID() string { return c.id }

// buildDeleteCellRequest mirrors the intmodel.Cell shape the controller
// hands to runner.DeleteCell — only the lookup keys are populated.
func buildDeleteCellRequest(realm, space, stack, cellName string) intmodel.Cell {
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
