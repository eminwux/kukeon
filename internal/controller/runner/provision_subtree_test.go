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

//nolint:testpackage // tests exercise private create-cgroup helpers on *Exec
package runner

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	cgroup2 "github.com/containerd/cgroups/v2/cgroup2"
	apitypes "github.com/containerd/containerd/api/types"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/eminwux/kukeon/internal/cgroupcheck"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/metadata"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// subtreeRecorderClient is a minimal ctr.Client stub for the per-level
// delegation test (issue #327). It records EnsureSubtreeControllers calls
// and returns success for the few methods exercised by the create paths
// (Connect, GetCgroupMountpoint, GetCurrentCgroupPath, NewCgroup); every
// other interface method panics so that an inadvertent code path change
// gets caught loudly instead of silently passing through a no-op stub.
type subtreeRecorderClient struct {
	mountpoint        string
	currentCgroupPath string
	ensureCalls       []ensureSubtreeCall
}

type ensureSubtreeCall struct {
	group       string
	mountpoint  string
	controllers []string
}

func (c *subtreeRecorderClient) Connect() error { return nil }
func (c *subtreeRecorderClient) Close() error   { return nil }

func (c *subtreeRecorderClient) GetCgroupMountpoint() string { return c.mountpoint }
func (c *subtreeRecorderClient) GetCurrentCgroupPath() (string, error) {
	return c.currentCgroupPath, nil
}

// NewCgroup returns a (nil, nil) pair: the create-cgroup paths under test
// discard the returned manager and the cgroup2.Manager type has unexported
// fields, so a non-nil sentinel is impossible to construct from outside the
// containerd cgroups package. Callers in this package only check the error.
//
//nolint:nilnil // see comment above; (nil, nil) is the only valid stub here
func (c *subtreeRecorderClient) NewCgroup(_ ctr.CgroupSpec) (*cgroup2.Manager, error) {
	return nil, nil
}

func (c *subtreeRecorderClient) EnsureSubtreeControllers(
	group, mountpoint string,
	controllers []string,
) ([]string, error) {
	c.ensureCalls = append(c.ensureCalls, ensureSubtreeCall{
		group:       group,
		mountpoint:  mountpoint,
		controllers: append([]string(nil), controllers...),
	})
	return controllers, nil
}

// All other methods panic — calling them from a per-level cgroup-create
// test would mean the path under test grew an unexpected dependency, and
// we want the failure surfaced immediately.
func (c *subtreeRecorderClient) CreateNamespace(string) error { panic("unexpected") }
func (c *subtreeRecorderClient) DeleteNamespace(string) error { panic("unexpected") }
func (c *subtreeRecorderClient) ListNamespaces() ([]string, error) {
	panic("unexpected")
}
func (c *subtreeRecorderClient) GetNamespace(string) (string, error)  { panic("unexpected") }
func (c *subtreeRecorderClient) ExistsNamespace(string) (bool, error) { panic("unexpected") }
func (c *subtreeRecorderClient) CleanupNamespaceResources(string, string) error {
	panic("unexpected")
}

func (c *subtreeRecorderClient) CgroupPath(string, string) (string, error) {
	panic("unexpected")
}

func (c *subtreeRecorderClient) LoadCgroup(string, string) (*cgroup2.Manager, error) {
	panic("unexpected")
}
func (c *subtreeRecorderClient) DeleteCgroup(string, string) error { panic("unexpected") }
func (c *subtreeRecorderClient) EnableCellSubtreeControllers(string, string, []string) ([]string, error) {
	panic("unexpected")
}

func (c *subtreeRecorderClient) EnableCellAllSubtreeControllers(string, string) ([]string, error) {
	panic("unexpected")
}

func (c *subtreeRecorderClient) CreateContainerFromSpec(
	string, intmodel.ContainerSpec, []ctr.RegistryCredentials, ...ctr.BuildOption,
) (containerd.Container, error) {
	panic("unexpected")
}

func (c *subtreeRecorderClient) CreateContainer(
	string, ctr.ContainerSpec, []ctr.RegistryCredentials,
) (containerd.Container, error) {
	panic("unexpected")
}

func (c *subtreeRecorderClient) GetContainer(string, string) (containerd.Container, error) {
	panic("unexpected")
}

func (c *subtreeRecorderClient) ListContainers(string, ...string) ([]containerd.Container, error) {
	panic("unexpected")
}

func (c *subtreeRecorderClient) ExistsContainer(string, string) (bool, error) {
	panic("unexpected")
}

func (c *subtreeRecorderClient) DeleteContainer(string, string, ctr.ContainerDeleteOptions) error {
	panic("unexpected")
}

func (c *subtreeRecorderClient) StartContainer(
	string, ctr.ContainerSpec, ctr.TaskSpec,
) (containerd.Task, error) {
	panic("unexpected")
}

func (c *subtreeRecorderClient) StopContainer(
	string, string, ctr.StopContainerOptions,
) (*containerd.ExitStatus, error) {
	panic("unexpected")
}

func (c *subtreeRecorderClient) TaskStatus(string, string) (containerd.Status, error) {
	panic("unexpected")
}

func (c *subtreeRecorderClient) TaskMetrics(string, string) (*apitypes.Metric, error) {
	panic("unexpected")
}

func (c *subtreeRecorderClient) ResolveSbshCachePath(
	string, string, string, []ctr.RegistryCredentials,
) (string, error) {
	panic("unexpected")
}

func (c *subtreeRecorderClient) ContainerProcessUID(string, containerd.Container) (uint32, error) {
	panic("unexpected")
}

func (c *subtreeRecorderClient) LoadImage(string, io.Reader) ([]string, error) {
	panic("unexpected")
}

func (c *subtreeRecorderClient) ListImages(string) ([]ctr.ImageInfo, error) {
	panic("unexpected")
}

func (c *subtreeRecorderClient) GetImage(string, string) (ctr.ImageInfo, error) {
	panic("unexpected")
}

func (c *subtreeRecorderClient) DeleteImage(string, string) error {
	panic("unexpected")
}

// Compile-time: the stub satisfies the full Client surface.
var _ ctr.Client = (*subtreeRecorderClient)(nil)

// newSubtreeTestExec builds a minimal *Exec backed by subtreeRecorderClient.
// The currentCgroupPath is fixed at consts.KukeonCgroupRoot to mirror the
// real GetCurrentCgroupPath, so spec.Group ends up rooted at /kukeon/...
// just like the production path.
func newSubtreeTestExec(t *testing.T, fake *subtreeRecorderClient) *Exec {
	t.Helper()
	return &Exec{
		ctx:       context.Background(),
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		ctrClient: fake,
		opts:      Options{RunPath: t.TempDir()},
	}
}

// seedRealmMetadata writes a minimal realm metadata file under r.opts.RunPath
// so r.GetRealm finds it. createSpaceCgroup looks up the parent realm only
// to log its name; nothing structural beyond name+namespace is needed.
func seedRealmMetadata(t *testing.T, r *Exec, realmName string) {
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

// TestEnableAncestorSubtreeControllersPassesResourceSubset pins the
// invariant that every realm/space/stack create path delegates exactly the
// kukeon resource subset (cgroupcheck.CellResourceControllers) — the same
// set the doctor pre-flight expects on the host root. Issue #327. Also
// pins that the helper returns the effective set so callers can persist
// it on Status.SubtreeControllers (issue #328).
func TestEnableAncestorSubtreeControllersPassesResourceSubset(t *testing.T) {
	fake := &subtreeRecorderClient{mountpoint: "/sys/fs/cgroup"}
	r := newSubtreeTestExec(t, fake)

	gotEffective, err := r.enableAncestorSubtreeControllers("/kukeon/foo", "/sys/fs/cgroup")
	if err != nil {
		t.Fatalf("enableAncestorSubtreeControllers() unexpected error: %v", err)
	}
	if want := cgroupcheck.CellResourceControllers(); !reflect.DeepEqual(gotEffective, want) {
		t.Errorf("enableAncestorSubtreeControllers() effective = %v, want %v (issue #328)",
			gotEffective, want)
	}
	if len(fake.ensureCalls) != 1 {
		t.Fatalf("EnsureSubtreeControllers call count = %d, want 1", len(fake.ensureCalls))
	}
	got := fake.ensureCalls[0]
	if got.group != "/kukeon/foo" {
		t.Errorf("EnsureSubtreeControllers group = %q, want %q", got.group, "/kukeon/foo")
	}
	if got.mountpoint != "/sys/fs/cgroup" {
		t.Errorf("EnsureSubtreeControllers mountpoint = %q, want %q", got.mountpoint, "/sys/fs/cgroup")
	}
	if want := cgroupcheck.CellResourceControllers(); !reflect.DeepEqual(got.controllers, want) {
		t.Errorf("EnsureSubtreeControllers controllers = %v, want %v", got.controllers, want)
	}
}

// TestCreateRealmCgroupEmptyRealmDelegates pins the AC1 invariant from
// issue #327: creating an empty realm — no descendant space/stack/cell —
// must still populate the realm's own cgroup.subtree_control with the
// kukeon resource subset. Pre-#327 the realm subtree was empty until the
// first cell landed; this test fails if that regression returns.
func TestCreateRealmCgroupEmptyRealmDelegates(t *testing.T) {
	fake := &subtreeRecorderClient{
		mountpoint:        "/sys/fs/cgroup",
		currentCgroupPath: consts.KukeonCgroupRoot,
	}
	r := newSubtreeTestExec(t, fake)

	if _, _, err := r.createRealmCgroup(intmodel.Realm{
		Metadata: intmodel.RealmMetadata{Name: "empty"},
		Spec:     intmodel.RealmSpec{Namespace: "empty.kukeon.io"},
	}); err != nil {
		t.Fatalf("createRealmCgroup() unexpected error: %v", err)
	}

	if len(fake.ensureCalls) != 1 {
		t.Fatalf("EnsureSubtreeControllers call count = %d, want 1 (empty-realm path)", len(fake.ensureCalls))
	}
	got := fake.ensureCalls[0]
	wantGroup := consts.KukeonCgroupRoot + "/empty"
	if got.group != wantGroup {
		t.Errorf("empty-realm subtree-control target group = %q, want %q", got.group, wantGroup)
	}
	if want := cgroupcheck.CellResourceControllers(); !reflect.DeepEqual(got.controllers, want) {
		t.Errorf("empty-realm subtree-control controllers = %v, want %v", got.controllers, want)
	}
}

// TestCreateLevelCgroupsDelegateSubtreeControllers verifies that each
// non-cell level (realm, space, stack) calls EnsureSubtreeControllers on
// its own cgroup with the kukeon resource subset right after creating it.
// Without these per-level call sites an empty realm/space/stack carries no
// controllers in subtree_control until the first descendant cell triggers
// the cell-level ancestor walk — issue #327.
func TestCreateLevelCgroupsDelegateSubtreeControllers(t *testing.T) {
	cases := []struct {
		name      string
		seed      func(t *testing.T, r *Exec)
		call      func(r *Exec) (string, []string, error)
		wantGroup string
	}{
		{
			name: "realm",
			call: func(r *Exec) (string, []string, error) {
				return r.createRealmCgroup(intmodel.Realm{
					Metadata: intmodel.RealmMetadata{Name: "alpha"},
					Spec:     intmodel.RealmSpec{Namespace: "alpha.kukeon.io"},
				})
			},
			wantGroup: consts.KukeonCgroupRoot + "/alpha",
		},
		{
			name: "space",
			seed: func(t *testing.T, r *Exec) {
				// createSpaceCgroup looks up the parent realm via GetRealm
				// (only to log its name); seed the realm metadata so the
				// lookup succeeds in the unit-test sandbox.
				seedRealmMetadata(t, r, "alpha")
			},
			call: func(r *Exec) (string, []string, error) {
				return r.createSpaceCgroup(intmodel.Space{
					Metadata: intmodel.SpaceMetadata{Name: "beta"},
					Spec:     intmodel.SpaceSpec{RealmName: "alpha"},
				})
			},
			wantGroup: consts.KukeonCgroupRoot + "/alpha/beta",
		},
		{
			name: "stack",
			call: func(r *Exec) (string, []string, error) {
				return r.createStackCgroup(intmodel.Stack{
					Metadata: intmodel.StackMetadata{Name: "gamma"},
					Spec:     intmodel.StackSpec{RealmName: "alpha", SpaceName: "beta"},
				})
			},
			wantGroup: consts.KukeonCgroupRoot + "/alpha/beta/gamma",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &subtreeRecorderClient{
				mountpoint:        "/sys/fs/cgroup",
				currentCgroupPath: consts.KukeonCgroupRoot,
			}
			r := newSubtreeTestExec(t, fake)
			if tc.seed != nil {
				tc.seed(t, r)
			}

			gotPath, gotControllers, err := tc.call(r)
			if err != nil {
				t.Fatalf("%s create path unexpected error: %v", tc.name, err)
			}
			if gotPath != tc.wantGroup {
				t.Errorf("%s cgroup path = %q, want %q", tc.name, gotPath, tc.wantGroup)
			}
			// Issue #328: every level's create path returns the effective
			// controller set so the caller can persist it on
			// Status.SubtreeControllers.
			if want := cgroupcheck.CellResourceControllers(); !reflect.DeepEqual(gotControllers, want) {
				t.Errorf("%s create returned controllers = %v, want %v",
					tc.name, gotControllers, want)
			}
			if len(fake.ensureCalls) != 1 {
				t.Fatalf(
					"%s EnsureSubtreeControllers call count = %d, want 1 (got: %+v)",
					tc.name, len(fake.ensureCalls), fake.ensureCalls,
				)
			}
			got := fake.ensureCalls[0]
			if got.group != tc.wantGroup {
				t.Errorf("%s EnsureSubtreeControllers group = %q, want %q",
					tc.name, got.group, tc.wantGroup)
			}
			if got.mountpoint != "/sys/fs/cgroup" {
				t.Errorf("%s EnsureSubtreeControllers mountpoint = %q, want %q",
					tc.name, got.mountpoint, "/sys/fs/cgroup")
			}
			if want := cgroupcheck.CellResourceControllers(); !reflect.DeepEqual(got.controllers, want) {
				t.Errorf("%s EnsureSubtreeControllers controllers = %v, want %v",
					tc.name, got.controllers, want)
			}
		})
	}
}
