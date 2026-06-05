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
	"errors"
	"fmt"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/errdefs"
)

// StorageStats is the per-namespace footprint NamespaceStorage returns —
// the figures `kuke status`'s storage section surfaces so snapshot/lease/
// content accumulation is observable before the data volume fills. All
// counts are metadata-store reads (boltdb iterators), so the call stays
// cheap. Snapshot disk usage is intentionally omitted: it would require
// `du` against the snapshotter's filesystem and the status budget
// doesn't fit it.
type StorageStats struct {
	// Snapshots is the total snapshot count across every registered
	// snapshotter in KukeonKnownSnapshotters. Aggregated so the status
	// row reads at-a-glance — a per-snapshotter breakdown would just
	// noise the report when one snapshotter dominates in practice
	// (overlayfs on real installs).
	Snapshots int
	// Leases is the total lease count from leases.Manager.List. Leases
	// are containerd GC roots — accumulation past the live-image count
	// is the leak signal issue #1039 surfaces.
	Leases int
	// Blobs is the count of content-store blobs (committed manifests,
	// layers, configs).
	Blobs int
	// BlobsBytes is the summed Size field across the content-store
	// walk — cheap because content.Info already carries the byte size,
	// so no extra file stat is needed.
	BlobsBytes int64
}

// NamespaceStorage returns StorageStats for the given namespace. Errors
// from the underlying probes are wrapped with a leading label naming
// which probe failed (leases / blobs / snapshots/<snapshotter>) so the
// caller's report can name the failing class.
func (c *client) NamespaceStorage(namespace string) (StorageStats, error) {
	cc := c.conn()
	if cc == nil {
		if err := c.Connect(); err != nil {
			return StorageStats{}, fmt.Errorf("failed to connect to containerd: %w", err)
		}
		cc = c.conn()
	}

	nsCtx := c.namespaceCtx(namespace)

	var stats StorageStats

	existingLeases, leaseErr := cc.LeasesService().List(nsCtx)
	if leaseErr != nil {
		return stats, fmt.Errorf("leases: %w", leaseErr)
	}
	stats.Leases = len(existingLeases)

	blobErr := cc.ContentStore().Walk(nsCtx, func(info content.Info) error {
		stats.Blobs++
		stats.BlobsBytes += info.Size
		return nil
	})
	if blobErr != nil {
		return stats, fmt.Errorf("blobs: %w", blobErr)
	}

	for _, snapshotter := range KukeonKnownSnapshotters {
		snapSvc := cc.SnapshotService(snapshotter)
		if snapSvc == nil {
			continue
		}
		count := 0
		walkErr := snapSvc.Walk(nsCtx, func(_ context.Context, _ snapshots.Info) error {
			count++
			return nil
		})
		if walkErr != nil {
			// An unregistered snapshotter on the host surfaces as
			// errdefs.ErrNotFound (or a wrapped variant) — skip it
			// rather than fail the whole probe. Real users only ever
			// populate one snapshotter, so the rest are misses on
			// every healthy host.
			if errors.Is(walkErr, errdefs.ErrNotFound) {
				continue
			}
			return stats, fmt.Errorf("snapshots/%s: %w", snapshotter, walkErr)
		}
		stats.Snapshots += count
	}

	return stats, nil
}
