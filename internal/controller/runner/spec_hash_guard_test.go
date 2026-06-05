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

//nolint:testpackage // exercises *Exec.reuseOrRefuseExistingChildContainer against an in-package fake
package runner

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	cgroup2 "github.com/containerd/cgroups/v2/cgroup2"
	apitypes "github.com/containerd/containerd/api/types"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/cio"
	containerderrdefs "github.com/containerd/errdefs"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// stubLabeledContainer is a minimal containerd.Container implementation used
// to drive the spec-hash guard tests. Returns a configurable labels map from
// Labels(); Task() returns containerd's NotFound so the guard's stale-task
// drop is a no-op.
type stubLabeledContainer struct {
	containerd.Container

	id     string
	labels map[string]string
}

func (c stubLabeledContainer) ID() string { return c.id }

func (c stubLabeledContainer) Labels(context.Context) (map[string]string, error) {
	return c.labels, nil
}

func (c stubLabeledContainer) Task(context.Context, cio.Attach) (containerd.Task, error) {
	// Mirror the real "no task" path so the guard skips its stale-task
	// drop without panicking. The exact error wrapping matters: the guard
	// recovers from any non-nil task error; surface a containerd-style
	// NotFound to keep the intent obvious to readers.
	return nil, containerderrdefs.ErrNotFound
}

// specHashFakeClient is a near-empty ctr.Client whose GetContainer returns a
// configurable stubLabeledContainer or a NotFound error. Everything else
// returns the zero value to satisfy the ctr.Client interface.
type specHashFakeClient struct {
	getContainerFn func(namespace, id string) (containerd.Container, error)
}

func (c *specHashFakeClient) Connect() error { return nil }
func (c *specHashFakeClient) Close() error   { return nil }

func (c *specHashFakeClient) CreateNamespace(string) error         { return nil }
func (c *specHashFakeClient) DeleteNamespace(string) error         { return nil }
func (c *specHashFakeClient) ListNamespaces() ([]string, error)    { return nil, nil }
func (c *specHashFakeClient) GetNamespace(string) (string, error)  { return "", nil }
func (c *specHashFakeClient) ExistsNamespace(string) (bool, error) { return false, nil }
func (c *specHashFakeClient) CleanupNamespaceResources(string, string) error {
	return nil
}

func (c *specHashFakeClient) GetCgroupMountpoint() string               { return "" }
func (c *specHashFakeClient) GetCurrentCgroupPath() (string, error)     { return "", nil }
func (c *specHashFakeClient) CgroupPath(string, string) (string, error) { return "", nil }

// NewCgroup / LoadCgroup return nil, nil — cgroup2.Manager has unexported
// fields so a zero value satisfies *Exec call sites the spec-hash guard
// path never exercises. The //nolint:nilnil mute is required because
// nolintlint is configured with require-explanation in .golangci.yml.
func (c *specHashFakeClient) NewCgroup(
	ctr.CgroupSpec,
) (*cgroup2.Manager, error) {
	return nil, nil //nolint:nilnil // unused stub satisfying ctr.Client
}

func (c *specHashFakeClient) LoadCgroup(
	string,
	string,
) (*cgroup2.Manager, error) {
	return nil, nil //nolint:nilnil // unused stub satisfying ctr.Client
}
func (c *specHashFakeClient) DeleteCgroup(string, string) error { return nil }

func (c *specHashFakeClient) EnsureSubtreeControllers(string, string, []string) ([]string, error) {
	return nil, nil
}

func (c *specHashFakeClient) EnableCellSubtreeControllers(string, string, []string) ([]string, error) {
	return nil, nil
}

func (c *specHashFakeClient) EnableCellAllSubtreeControllers(string, string) ([]string, error) {
	return nil, nil
}
func (c *specHashFakeClient) RelocateProcessesToLeaf(string, string, string) error { return nil }

func (c *specHashFakeClient) CreateContainerFromSpec(
	string, intmodel.ContainerSpec, []ctr.RegistryCredentials, ...ctr.BuildOption,
) (containerd.Container, error) {
	return nil, nil //nolint:nilnil
}

func (c *specHashFakeClient) CreateContainer(
	string, ctr.ContainerSpec, []ctr.RegistryCredentials,
) (containerd.Container, error) {
	return nil, nil //nolint:nilnil
}

func (c *specHashFakeClient) GetContainer(namespace, id string) (containerd.Container, error) {
	if c.getContainerFn != nil {
		return c.getContainerFn(namespace, id)
	}
	return nil, errdefs.ErrContainerNotFound
}

func (c *specHashFakeClient) ListContainers(string, ...string) ([]containerd.Container, error) {
	return nil, nil
}
func (c *specHashFakeClient) ExistsContainer(string, string) (bool, error) { return false, nil }
func (c *specHashFakeClient) DeleteContainer(string, string, ctr.ContainerDeleteOptions) error {
	return nil
}

func (c *specHashFakeClient) StartContainer(string, ctr.ContainerSpec, ctr.TaskSpec) (containerd.Task, error) {
	return nil, nil //nolint:nilnil
}

func (c *specHashFakeClient) StopContainer(
	string, string, ctr.StopContainerOptions,
) (*containerd.ExitStatus, error) {
	return nil, nil //nolint:nilnil
}

func (c *specHashFakeClient) TaskStatus(string, string) (containerd.Status, error) {
	return containerd.Status{}, nil
}

func (c *specHashFakeClient) TaskMetrics(string, string) (*apitypes.Metric, error) {
	return nil, nil //nolint:nilnil
}

func (c *specHashFakeClient) ContainerProcessUID(string, containerd.Container) (uint32, error) {
	return 0, nil
}
func (c *specHashFakeClient) LoadImage(string, io.Reader) ([]string, error) { return nil, nil }
func (c *specHashFakeClient) ListImages(string) ([]ctr.ImageInfo, error)    { return nil, nil }
func (c *specHashFakeClient) GetImage(string, string) (ctr.ImageInfo, error) {
	return ctr.ImageInfo{}, nil
}
func (c *specHashFakeClient) ImageChainID(string, string) (string, error)         { return "", nil }
func (c *specHashFakeClient) ContainerRootChainID(string, string) (string, error) { return "", nil }
func (c *specHashFakeClient) DeleteImage(string, string) error                    { return nil }
func (c *specHashFakeClient) PruneImages(string) (ctr.PruneResult, error) {
	return ctr.PruneResult{}, nil
}

var _ ctr.Client = (*specHashFakeClient)(nil)

func newSpecHashTestExec(fake *specHashFakeClient) *Exec {
	return &Exec{
		ctx:       context.Background(),
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		ctrClient: fake,
	}
}

// TestReuseOrRefuseExistingChildContainer_AbsentRecordFallsThrough locks the
// fresh-create path: when GetContainer returns ErrContainerNotFound the
// helper reports reuse=false with nil error so the caller proceeds to
// CreateContainerFromSpec. Issue #867.
func TestReuseOrRefuseExistingChildContainer_AbsentRecordFallsThrough(t *testing.T) {
	fake := &specHashFakeClient{
		getContainerFn: func(string, string) (containerd.Container, error) {
			return nil, errdefs.ErrContainerNotFound
		},
	}
	r := newSpecHashTestExec(fake)

	reuse, err := r.reuseOrRefuseExistingChildContainer(
		"ns", "id", "cell",
		intmodel.ContainerSpec{Image: "alpine", Command: "sleep"},
	)
	if err != nil {
		t.Fatalf("unexpected error on absent record: %v", err)
	}
	if reuse {
		t.Errorf("absent record must return reuse=false so caller takes fresh-create path; got reuse=true")
	}
}

// TestReuseOrRefuseExistingChildContainer_MatchingHashReuses locks the happy
// reuse path: an existing record whose label matches the freshly-computed
// hash returns reuse=true and the caller's create call is skipped.
func TestReuseOrRefuseExistingChildContainer_MatchingHashReuses(t *testing.T) {
	spec := intmodel.ContainerSpec{Image: "alpine", Command: "sleep", Args: []string{"infinity"}}
	matchingHash := ComputeContainerSpecHash(spec)
	fake := &specHashFakeClient{
		getContainerFn: func(_, id string) (containerd.Container, error) {
			return stubLabeledContainer{
				id:     id,
				labels: map[string]string{SpecHashLabelKey: matchingHash},
			}, nil
		},
	}
	r := newSpecHashTestExec(fake)

	reuse, err := r.reuseOrRefuseExistingChildContainer("ns", "id", "cell", spec)
	if err != nil {
		t.Fatalf("matching-hash reuse must not error; got %v", err)
	}
	if !reuse {
		t.Errorf("matching spec-hash must return reuse=true (overlay preserved); got reuse=false")
	}
}

// TestReuseOrRefuseExistingChildContainer_LegacyRecordReuses locks the
// backward-compatibility branch: a record with no SpecHashLabelKey label
// (created by pre-#867 kukeon) is treated as a match so the operator's first
// restart after the upgrade does not refuse with ErrCellSpecHashDrift on a
// healthy cell.
func TestReuseOrRefuseExistingChildContainer_LegacyRecordReuses(t *testing.T) {
	fake := &specHashFakeClient{
		getContainerFn: func(_, id string) (containerd.Container, error) {
			return stubLabeledContainer{
				id:     id,
				labels: map[string]string{"kukeon.io/cell": "demo"}, // unrelated label
			}, nil
		},
	}
	r := newSpecHashTestExec(fake)

	reuse, err := r.reuseOrRefuseExistingChildContainer(
		"ns", "id", "cell",
		intmodel.ContainerSpec{Image: "alpine"},
	)
	if err != nil {
		t.Fatalf("legacy record must not error; got %v", err)
	}
	if !reuse {
		t.Errorf(
			"legacy record (no kukeon.io/spec-hash label) must be treated as match (safe default for first post-upgrade restart); got reuse=false",
		)
	}
}

// TestReuseOrRefuseExistingChildContainer_DivergedHashRefuses is the
// regression guard for AC #6 of issue #867: a containerd record whose
// SpecHashLabelKey value disagrees with the freshly-computed hash must
// refuse with ErrCellSpecHashDrift instead of silently resuming a stale
// snapshot. The wrapped error must name both hashes and point at
// `kuke apply -f` so the operator has an actionable next step.
func TestReuseOrRefuseExistingChildContainer_DivergedHashRefuses(t *testing.T) {
	desired := intmodel.ContainerSpec{Image: "alpine", Command: "sleep"}
	staleHash := "deadbeef" // intentionally not the hash of desired
	fake := &specHashFakeClient{
		getContainerFn: func(_, id string) (containerd.Container, error) {
			return stubLabeledContainer{
				id:     id,
				labels: map[string]string{SpecHashLabelKey: staleHash},
			}, nil
		},
	}
	r := newSpecHashTestExec(fake)

	reuse, err := r.reuseOrRefuseExistingChildContainer("ns", "cell_stack_demo_child", "demo", desired)
	if err == nil {
		t.Fatalf("diverged spec-hash must error; got reuse=%v err=nil", reuse)
	}
	if !errors.Is(err, errdefs.ErrCellSpecHashDrift) {
		t.Errorf("diverged hash error must wrap ErrCellSpecHashDrift so callers can branch on it; got %v", err)
	}
	if reuse {
		t.Errorf("diverged hash must return reuse=false (refuse, do not silently recreate); got reuse=true")
	}
	// The operator-facing message must carry enough context to act on:
	// the diverged hash pair + the `kuke apply -f` next step.
	msg := err.Error()
	if !contains(msg, staleHash) || !contains(msg, ComputeContainerSpecHash(desired)) {
		t.Errorf("error message must name both hashes; got %q", msg)
	}
	if !contains(msg, "kuke apply -f") {
		t.Errorf("error message must point at `kuke apply -f` as the reconcile next step; got %q", msg)
	}
}

// contains is a thin substring helper used by the diverged-hash assertion.
// Keeps the test self-contained without pulling in the `strings` package.
func contains(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
