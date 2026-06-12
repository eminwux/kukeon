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
	"reflect"
	"sort"
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
	// stamped, when non-nil, captures the labels written by SetLabels on the
	// guard's re-stamp path so tests can assert the (hash, version) pair the
	// reuse branch wrote. Issue #1171.
	stamped *map[string]string
}

func (c stubLabeledContainer) ID() string { return c.id }

func (c stubLabeledContainer) Labels(context.Context) (map[string]string, error) {
	return c.labels, nil
}

// SetLabels records the re-stamp the guard performs on the reuse path. The
// real containerd.Container merges and returns the full label set; the stub
// echoes the written pair, which is all the guard tests assert. Issue #1171.
func (c stubLabeledContainer) SetLabels(_ context.Context, labels map[string]string) (map[string]string, error) {
	if c.stamped != nil {
		*c.stamped = labels
	}
	return labels, nil
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

func (c *specHashFakeClient) NamespaceStorage(string) (ctr.StorageStats, error) {
	return ctr.StorageStats{}, nil
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
	var stamped map[string]string
	fake := &specHashFakeClient{
		getContainerFn: func(_, id string) (containerd.Container, error) {
			return stubLabeledContainer{
				id: id,
				labels: map[string]string{
					SpecHashLabelKey:        matchingHash,
					SpecHashVersionLabelKey: SpecHashDomainVersion,
				},
				stamped: &stamped,
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
	if stamped != nil {
		t.Errorf(
			"a record stamped under the current version with a matching hash must reuse untouched; got re-stamp %v",
			stamped,
		)
	}
}

// TestReuseOrRefuseExistingChildContainer_LegacyRecordReusesAndRestamps locks
// the legacy-absent branch: a record with no version label (created by a
// pre-#1171 daemon, possibly carrying a bare hash from an older domain) is
// reused AND re-stamped to the current (hash, version) pair, so the first
// restart after the upgrade heals the record instead of refusing with
// ErrCellSpecHashDrift. Issues #867, #1171.
func TestReuseOrRefuseExistingChildContainer_LegacyRecordReusesAndRestamps(t *testing.T) {
	spec := intmodel.ContainerSpec{Image: "alpine"}
	var stamped map[string]string
	fake := &specHashFakeClient{
		getContainerFn: func(_, id string) (containerd.Container, error) {
			return stubLabeledContainer{
				id: id,
				// Bare pre-#1171 hash from an older domain + no version label —
				// exactly the shape of the live-incident stranded cells.
				labels:  map[string]string{SpecHashLabelKey: "6271df3bstale"},
				stamped: &stamped,
			}, nil
		},
	}
	r := newSpecHashTestExec(fake)

	reuse, err := r.reuseOrRefuseExistingChildContainer("ns", "id", "cell", spec)
	if err != nil {
		t.Fatalf("legacy record must not error; got %v", err)
	}
	if !reuse {
		t.Errorf("legacy record (no version label) must be reused after re-stamp; got reuse=false")
	}
	if stamped[SpecHashVersionLabelKey] != SpecHashDomainVersion {
		t.Errorf(
			"legacy record must be re-stamped to the current domain version %q; got %v",
			SpecHashDomainVersion,
			stamped,
		)
	}
	if stamped[SpecHashLabelKey] != ComputeContainerSpecHash(spec) {
		t.Errorf("legacy record must be re-stamped to the current spec hash; got %v", stamped)
	}
}

// TestReuseOrRefuseExistingChildContainer_DivergedHashRefuses is the
// regression guard for AC #6 of issue #867 and AC #2 of #1171: a containerd
// record stamped under the *current* domain version whose SpecHashLabelKey
// value disagrees with the freshly-computed hash is genuine out-of-band
// tampering and must refuse with ErrCellSpecHashDrift instead of silently
// resuming a stale snapshot. The wrapped error must name both hashes and point
// at `kuke apply -f` so the operator has an actionable next step.
func TestReuseOrRefuseExistingChildContainer_DivergedHashRefuses(t *testing.T) {
	desired := intmodel.ContainerSpec{Image: "alpine", Command: "sleep"}
	staleHash := "deadbeef" // intentionally not the hash of desired
	fake := &specHashFakeClient{
		getContainerFn: func(_, id string) (containerd.Container, error) {
			return stubLabeledContainer{
				id: id,
				// Current version + diverged hash == within-version tamper.
				labels: map[string]string{
					SpecHashLabelKey:        staleHash,
					SpecHashVersionLabelKey: SpecHashDomainVersion,
				},
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

// TestReuseOrRefuseExistingChildContainer_PriorDomainVersionRestamps locks the
// upgrade path: a record stamped under a prior domain version — even when its
// stale hash disagrees with the current spec — is re-stamped and reused, not
// refused. This is the fix's core arm: a daemon upgrade that crosses a
// hash-domain change must not strand pre-existing cells. Issue #1171.
func TestReuseOrRefuseExistingChildContainer_PriorDomainVersionRestamps(t *testing.T) {
	spec := intmodel.ContainerSpec{Image: "alpine", Command: "sleep"}
	var stamped map[string]string
	fake := &specHashFakeClient{
		getContainerFn: func(_, id string) (containerd.Container, error) {
			return stubLabeledContainer{
				id: id,
				labels: map[string]string{
					// Older domain: a hash that would refuse under a bare guard.
					SpecHashLabelKey:        "priordomainstalehash",
					SpecHashVersionLabelKey: "2",
				},
				stamped: &stamped,
			}, nil
		},
	}
	r := newSpecHashTestExec(fake)

	reuse, err := r.reuseOrRefuseExistingChildContainer("ns", "id", "cell", spec)
	if err != nil {
		t.Fatalf("prior-domain record must re-stamp + reuse, not error; got %v", err)
	}
	if !reuse {
		t.Errorf("prior-domain record must be reused after re-stamp; got reuse=false")
	}
	if stamped[SpecHashVersionLabelKey] != SpecHashDomainVersion ||
		stamped[SpecHashLabelKey] != ComputeContainerSpecHash(spec) {
		t.Errorf("prior-domain record must be re-stamped to the current (hash, version) pair; got %v", stamped)
	}
}

// TestClassifySpecHashReuse is the table-driven guard for the three-way reuse
// decision at the heart of #1171: absent (legacy) / same-version-match /
// same-version-mismatch / different-version.
func TestClassifySpecHashReuse(t *testing.T) {
	const desired = "currenthash"
	cases := []struct {
		name   string
		labels map[string]string
		want   specHashReuseAction
	}{
		{
			name:   "absent legacy version re-stamps",
			labels: map[string]string{SpecHashLabelKey: "anyoldhash"},
			want:   specHashRestamp,
		},
		{
			name:   "no labels at all re-stamps",
			labels: map[string]string{},
			want:   specHashRestamp,
		},
		{
			name: "different version re-stamps even on hash mismatch",
			labels: map[string]string{
				SpecHashLabelKey:        "priorhash",
				SpecHashVersionLabelKey: "2",
			},
			want: specHashRestamp,
		},
		{
			name: "same version matching hash reuses as-is",
			labels: map[string]string{
				SpecHashLabelKey:        desired,
				SpecHashVersionLabelKey: SpecHashDomainVersion,
			},
			want: specHashReuseAsIs,
		},
		{
			name: "same version diverged hash refuses",
			labels: map[string]string{
				SpecHashLabelKey:        "tampered",
				SpecHashVersionLabelKey: SpecHashDomainVersion,
			},
			want: specHashRefuse,
		},
		{
			name: "same version empty hash reuses as-is",
			labels: map[string]string{
				SpecHashVersionLabelKey: SpecHashDomainVersion,
			},
			want: specHashReuseAsIs,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifySpecHashReuse(tc.labels, desired); got != tc.want {
				t.Errorf("classifySpecHashReuse(%v) = %d, want %d", tc.labels, got, tc.want)
			}
		})
	}
}

// TestSpecHashDomainVersionPinsToPayload forces a SpecHashDomainVersion bump in
// the same edit as any change to containerSpecHashPayload's field set. The hash
// domain *is* the payload's JSON field set, so this test reflects that set and
// pins it to the current version: adding, removing, or renaming a payload field
// changes the reflected set, fails this test, and the only green fix is to bump
// SpecHashDomainVersion and register the new field set below. This is the
// versioned counterpart of TestSpecHashDomainPinsToDiffCellBreakingFields (which
// pins the payload to apply.DiffCell's breaking domain); kept in-package because
// the payload struct is unexported. Issue #1171.
func TestSpecHashDomainVersionPinsToPayload(t *testing.T) {
	// domainFieldSets records, per version, the sorted JSON field set the hash
	// domain carried at that version. Append a new entry whenever you bump
	// SpecHashDomainVersion; never edit an existing entry (prior domains are
	// historical fact, and the re-stamp path relies on old versions staying
	// distinct from the current one).
	domainFieldSets := map[string][]string{
		"3": {
			"args", "capabilities", "command", "image", "privileged",
			"readOnlyRootFilesystem", "resources", "secrets", "securityOpts",
			"tmpfs", "user", "volumes", "workingDir",
		},
		"4": {
			"args", "capabilities", "command", "devices", "image", "privileged",
			"readOnlyRootFilesystem", "resources", "secrets", "securityOpts",
			"tmpfs", "user", "volumes", "workingDir",
		},
	}

	want, ok := domainFieldSets[SpecHashDomainVersion]
	if !ok {
		t.Fatalf(
			"no domain field set registered for SpecHashDomainVersion %q — append one to domainFieldSets in the same edit that bumped the version",
			SpecHashDomainVersion,
		)
	}

	pt := reflect.TypeOf(containerSpecHashPayload{})
	got := make([]string, 0, pt.NumField())
	for i := range pt.NumField() {
		tag := pt.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			t.Fatalf(
				"containerSpecHashPayload field %q has no json tag — the hash domain must be fully tagged",
				pt.Field(i).Name,
			)
		}
		got = append(got, tag)
	}
	sort.Strings(got)

	if len(got) != len(want) {
		t.Fatalf(
			"containerSpecHashPayload field set changed without a SpecHashDomainVersion bump: got %v, registered for version %q is %v — bump SpecHashDomainVersion and register the new set",
			got,
			SpecHashDomainVersion,
			want,
		)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf(
				"containerSpecHashPayload field set changed without a SpecHashDomainVersion bump: got %v, registered for version %q is %v — bump SpecHashDomainVersion and register the new set",
				got,
				SpecHashDomainVersion,
				want,
			)
		}
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
