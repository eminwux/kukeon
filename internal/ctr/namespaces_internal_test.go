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

package ctr

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/errdefs"
	"github.com/opencontainers/go-digest"
)

// TestCleanupNamespaceResourcesDrainsLeasePinnedSnapshots is the regression
// guard for issue #401 — `kuke uninstall` stranded overlayfs snapshots when
// the default realm held lease-pinned snapshots (image pulls, container
// creates), forcing operators to re-run uninstall. The fix is to drain
// leases (with leases.SynchronousDelete to trigger a synchronous GC sweep)
// BEFORE snapshots so the snapshot pass sees an unpinned graph.
//
// The fake models the observed-in-the-field semantic: snapshotter.Remove
// fails with FailedPrecondition while a lease references the snapshot, and
// lease.Delete drops that reference. A drainNamespaceResources that runs in
// the old order (snapshots first, leases last) would call snapshotter.Remove
// while the pins are still live, strand every snapshot, then drop the
// leases too late to clean up. The new order (leases first) clears the pins
// before the snapshot pass, so snapshotter.Remove succeeds and the
// namespace ends up empty — which is what this test asserts.
//
// Real-containerd integration of the drain ordering lives in the e2e suite;
// CI's `make test` target intentionally avoids needing a containerd socket,
// so a fake is the only viable substrate for a per-package regression test.
func TestCleanupNamespaceResourcesDrainsLeasePinnedSnapshots(t *testing.T) {
	srv := newFakeServices()
	srv.addSnapshot("overlayfs", "sha256:base")
	srv.addSnapshot("overlayfs", "sha256:layer")
	srv.addSnapshot("overlayfs", "sha256:top")
	srv.addLease("lease-1", "snapshots/overlayfs", "sha256:base", "sha256:layer", "sha256:top")
	srv.addImage("docker.io/library/alpine:latest", "sha256:manifest")
	srv.addBlob("sha256:blob-a")
	srv.addBlob("sha256:blob-b")

	c := &client{
		ctx:    context.Background(),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	c.drainNamespaceResources(context.Background(), "test-ns", "overlayfs", srv)

	if got := len(srv.leases); got != 0 {
		t.Errorf("leases remaining after drain: got %d, want 0", got)
	}
	if got := len(srv.snaps["overlayfs"]); got != 0 {
		remaining := make([]string, 0, len(srv.snaps["overlayfs"]))
		for k := range srv.snaps["overlayfs"] {
			remaining = append(remaining, k)
		}
		t.Errorf(
			"snapshots remaining after drain: got %d (%v), want 0 — lease-first ordering regressed",
			got,
			remaining,
		)
	}
	if got := len(srv.imageList); got != 0 {
		t.Errorf("images remaining after drain: got %d, want 0", got)
	}
	if got := len(srv.blobs); got != 0 {
		t.Errorf("blobs remaining after drain: got %d, want 0", got)
	}

	// Ordering invariant: every lease delete must have completed before
	// the first snapshotter Walk on the pinned snapshotter. If a future
	// refactor inverts the order, every snapshot Remove would fail (pins
	// still live) and the previous assertion fails — but checking the call
	// log gives a sharper diagnostic.
	lastLeaseDel := -1
	firstSnapWalk := -1
	for i, op := range srv.callLog {
		if op == "lease.Delete(lease-1)" {
			lastLeaseDel = i
		}
		if firstSnapWalk == -1 && op == "snapshotter.Walk(overlayfs)" {
			firstSnapWalk = i
		}
	}
	if lastLeaseDel == -1 {
		t.Fatalf("call log missing lease.Delete; got %v", srv.callLog)
	}
	if firstSnapWalk == -1 {
		t.Fatalf("call log missing snapshotter.Walk(overlayfs); got %v", srv.callLog)
	}
	if firstSnapWalk < lastLeaseDel {
		t.Errorf(
			"snapshotter.Walk(overlayfs) at %d ran before lease.Delete at %d; got call log %v",
			firstSnapWalk,
			lastLeaseDel,
			srv.callLog,
		)
	}
}

// fakeServices models the subset of containerd surface drainNamespaceResources
// uses. The pinning semantic — snapshotter.Remove fails while any lease
// references the snapshot via a leases.Resource{Type:"snapshots/<name>",ID:<key>}
// pair — is the load-bearing piece; everything else is plumbing.
type fakeServices struct {
	snaps     map[string]map[string]*fakeSnapshot // snapshotter -> key -> snap
	leases    map[string]*fakeLease
	imageList []images.Image
	blobs     map[string]bool
	callLog   []string

	// imageDeleteSync records the DeleteOptions.Synchronous flag observed
	// on each images.Store Delete call (one entry per call, in order).
	// Load-bearing for TestDeleteImagePassesSynchronousDelete (#1037), which
	// asserts the single-image delete path triggers a synchronous GC sweep.
	imageDeleteSync []bool
}

type fakeSnapshot struct {
	name string
}

type fakeLease struct {
	id        string
	resources []leases.Resource
}

func newFakeServices() *fakeServices {
	return &fakeServices{
		snaps:  map[string]map[string]*fakeSnapshot{},
		leases: map[string]*fakeLease{},
		blobs:  map[string]bool{},
	}
}

func (f *fakeServices) addSnapshot(snapshotter, key string) {
	if f.snaps[snapshotter] == nil {
		f.snaps[snapshotter] = map[string]*fakeSnapshot{}
	}
	f.snaps[snapshotter][key] = &fakeSnapshot{name: key}
}

func (f *fakeServices) addLease(id, resourceType string, keys ...string) {
	res := make([]leases.Resource, len(keys))
	for i, k := range keys {
		res[i] = leases.Resource{ID: k, Type: resourceType}
	}
	f.leases[id] = &fakeLease{id: id, resources: res}
}

func (f *fakeServices) addImage(name string, dgst digest.Digest) {
	img := images.Image{Name: name}
	img.Target.Digest = dgst
	f.imageList = append(f.imageList, img)
}

func (f *fakeServices) addBlob(dgst digest.Digest) {
	f.blobs[dgst.String()] = true
}

// snapshotPinnedBy returns the lease IDs pinning the given snapshot key
// under the given snapshotter. A non-empty result means snapshotter.Remove
// must fail per the fake's modeled semantic.
func (f *fakeServices) snapshotPinnedBy(snapshotter, key string) []string {
	wantType := "snapshots/" + snapshotter
	var pins []string
	for _, l := range f.leases {
		for _, r := range l.resources {
			if r.Type == wantType && r.ID == key {
				pins = append(pins, l.id)
			}
		}
	}
	return pins
}

func (f *fakeServices) LeasesService() leases.Manager { return &fakeLeaseManager{f: f} }
func (f *fakeServices) ImageService() images.Store    { return &fakeImageStore{f: f} }
func (f *fakeServices) ContentStore() content.Store   { return &fakeContentStore{f: f} }
func (f *fakeServices) SnapshotService(name string) snapshots.Snapshotter {
	return &fakeSnapshotter{f: f, name: name}
}

// fakeLeaseManager implements the subset of leases.Manager the drain calls.
// Embedding leases.Manager (nil) promotes the unused methods; calling them
// would panic, which is the right behavior — drainLeases must not need them.
type fakeLeaseManager struct {
	leases.Manager

	f *fakeServices
}

func (m *fakeLeaseManager) List(_ context.Context, _ ...string) ([]leases.Lease, error) {
	m.f.callLog = append(m.f.callLog, "lease.List")
	out := make([]leases.Lease, 0, len(m.f.leases))
	for id := range m.f.leases {
		out = append(out, leases.Lease{ID: id})
	}
	return out, nil
}

func (m *fakeLeaseManager) Delete(_ context.Context, l leases.Lease, _ ...leases.DeleteOpt) error {
	m.f.callLog = append(m.f.callLog, "lease.Delete("+l.ID+")")
	delete(m.f.leases, l.ID)
	return nil
}

type fakeImageStore struct {
	images.Store

	f *fakeServices
}

func (s *fakeImageStore) List(_ context.Context, _ ...string) ([]images.Image, error) {
	s.f.callLog = append(s.f.callLog, "images.List")
	return append([]images.Image(nil), s.f.imageList...), nil
}

func (s *fakeImageStore) Delete(ctx context.Context, name string, opts ...images.DeleteOpt) error {
	s.f.callLog = append(s.f.callLog, "images.Delete("+name+")")
	var do images.DeleteOptions
	for _, opt := range opts {
		if err := opt(ctx, &do); err != nil {
			return err
		}
	}
	s.f.imageDeleteSync = append(s.f.imageDeleteSync, do.Synchronous)
	for i, img := range s.f.imageList {
		if img.Name == name {
			s.f.imageList = append(s.f.imageList[:i], s.f.imageList[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("image %q: %w", name, errdefs.ErrNotFound)
}

type fakeSnapshotter struct {
	snapshots.Snapshotter

	f    *fakeServices
	name string
}

func (s *fakeSnapshotter) Walk(_ context.Context, fn snapshots.WalkFunc, _ ...string) error {
	s.f.callLog = append(s.f.callLog, "snapshotter.Walk("+s.name+")")
	bucket, ok := s.f.snaps[s.name]
	if !ok {
		return nil
	}
	for key := range bucket {
		if err := fn(context.Background(), snapshots.Info{Name: key}); err != nil {
			return err
		}
	}
	return nil
}

func (s *fakeSnapshotter) Remove(_ context.Context, key string) error {
	s.f.callLog = append(s.f.callLog, "snapshotter.Remove("+s.name+","+key+")")
	if pins := s.f.snapshotPinnedBy(s.name, key); len(pins) > 0 {
		return fmt.Errorf("snapshot %q pinned by %v: %w", key, pins, errdefs.ErrFailedPrecondition)
	}
	delete(s.f.snaps[s.name], key)
	return nil
}

type fakeContentStore struct {
	content.Store

	f *fakeServices
}

func (s *fakeContentStore) Walk(_ context.Context, fn content.WalkFunc, _ ...string) error {
	s.f.callLog = append(s.f.callLog, "content.Walk")
	for dgst := range s.f.blobs {
		if err := fn(content.Info{Digest: digest.Digest(dgst)}); err != nil {
			return err
		}
	}
	return nil
}

func (s *fakeContentStore) Delete(_ context.Context, dgst digest.Digest) error {
	s.f.callLog = append(s.f.callLog, "content.Delete("+dgst.String()+")")
	delete(s.f.blobs, dgst.String())
	return nil
}
