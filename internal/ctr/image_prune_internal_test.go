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
	"io"
	"log/slog"
	"slices"
	"testing"

	"github.com/containerd/containerd/v2/core/leases"
)

func newPruneTestClient() *client {
	return &client{
		ctx:    context.Background(),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// TestPruneLeasesDropsOrphansKeepsProtected is the per-package regression
// guard for issue #1036 — `kuke image prune` must release the orphaned
// build/pull leases pinning dangling layers while never touching a lease
// that backs a live container's snapshot. The fake models leases carrying
// snapshotter resources; pruneLeases retains a lease iff one of its
// resources matches the protected (live-container) snapshot-key set.
func TestPruneLeasesDropsOrphansKeepsProtected(t *testing.T) {
	srv := newFakeServices()
	// Orphaned build-time lease pinning only dangling layers.
	srv.addLease("buildkit/lease.temporary", "snapshots/overlayfs", "sha256:dangling-a", "sha256:dangling-b")
	// Orphaned gc.flat lease pinning a dangling blob.
	srv.addLease("containerd.io/gc.flat/2026-01-01", "content", "sha256:dangling-c")
	// Lease backing a live container's snapshot — must be retained.
	srv.addLease("live-cell-lease", "snapshots/overlayfs", "sha256:live-root")

	protected := map[string]struct{}{"sha256:live-root": {}}

	c := newPruneTestClient()
	res := c.pruneLeases(context.Background(), "test-ns", protected, srv)

	if res.LeasesDeleted != 2 {
		t.Errorf("LeasesDeleted: got %d, want 2", res.LeasesDeleted)
	}
	if res.LeasesRetained != 1 {
		t.Errorf("LeasesRetained: got %d, want 1", res.LeasesRetained)
	}
	if _, ok := srv.leases["live-cell-lease"]; !ok {
		t.Errorf("live-cell-lease was deleted; a lease backing a live container must be retained")
	}
	if _, ok := srv.leases["buildkit/lease.temporary"]; ok {
		t.Errorf("buildkit/lease.temporary survived; orphaned build leases must be released")
	}
	if _, ok := srv.leases["containerd.io/gc.flat/2026-01-01"]; ok {
		t.Errorf("gc.flat lease survived; orphaned leases must be released")
	}

	// Every deleted lease must go through SynchronousDelete (the GC-sweep
	// trigger). We assert the delete happened for the orphans and not for
	// the protected lease via the call log.
	if !slices.Contains(srv.callLog, "lease.Delete(buildkit/lease.temporary)") {
		t.Errorf("expected delete of buildkit/lease.temporary; call log %v", srv.callLog)
	}
	if slices.Contains(srv.callLog, "lease.Delete(live-cell-lease)") {
		t.Errorf("unexpected delete of live-cell-lease; call log %v", srv.callLog)
	}
}

// TestPruneLeasesIdempotentOnCleanRealm asserts the AC's idempotency clause:
// a second run on a realm whose only remaining leases back live containers
// deletes nothing.
func TestPruneLeasesIdempotentOnCleanRealm(t *testing.T) {
	srv := newFakeServices()
	srv.addLease("live-cell-lease", "snapshots/overlayfs", "sha256:live-root")
	protected := map[string]struct{}{"sha256:live-root": {}}

	c := newPruneTestClient()
	res := c.pruneLeases(context.Background(), "test-ns", protected, srv)

	if res.LeasesDeleted != 0 {
		t.Errorf("LeasesDeleted on clean realm: got %d, want 0", res.LeasesDeleted)
	}
	if res.LeasesRetained != 1 {
		t.Errorf("LeasesRetained on clean realm: got %d, want 1", res.LeasesRetained)
	}
}

// TestPruneLeasesEmptyNamespace covers the fully-empty namespace: no leases,
// no deletes, no retains, no error.
func TestPruneLeasesEmptyNamespace(t *testing.T) {
	srv := newFakeServices()
	c := newPruneTestClient()
	res := c.pruneLeases(context.Background(), "test-ns", map[string]struct{}{}, srv)
	if res.LeasesDeleted != 0 || res.LeasesRetained != 0 {
		t.Errorf("empty namespace: got %+v, want zero", res)
	}
}

// TestLeaseBacksProtected unit-tests the resource→protected-set matcher.
func TestLeaseBacksProtected(t *testing.T) {
	protected := map[string]struct{}{"sha256:keep": {}}
	cases := []struct {
		name string
		res  []leases.Resource
		want bool
	}{
		{"matches protected", []leases.Resource{{ID: "sha256:keep", Type: "snapshots/overlayfs"}}, true},
		{"no match", []leases.Resource{{ID: "sha256:drop", Type: "snapshots/overlayfs"}}, false},
		{"empty resources", nil, false},
		{
			"one of many matches",
			[]leases.Resource{{ID: "sha256:drop", Type: "content"}, {ID: "sha256:keep", Type: "snapshots/overlayfs"}},
			true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := leaseBacksProtected(tc.res, protected); got != tc.want {
				t.Errorf("leaseBacksProtected: got %v, want %v", got, tc.want)
			}
		})
	}
}
