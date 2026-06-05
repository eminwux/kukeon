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

	"github.com/containerd/containerd/v2/core/leases"
	internalerrdefs "github.com/eminwux/kukeon/internal/errdefs"
)

// PruneResult reports the outcome of a surgical image prune: how many leases
// were released (each release triggers a synchronous containerd GC sweep
// that reclaims the dangling content/snapshots the lease was the last GC
// root for) and how many were retained because they still back a live
// container's snapshot.
type PruneResult struct {
	LeasesDeleted  int
	LeasesRetained int
}

// PruneImages reclaims dangling image layers and the orphaned leases pinning
// them in the given containerd namespace (#1036). Repeated `kuke build` /
// image pulls accumulate leases (buildkit temporaries, gc.flat variants,
// pull leases) and committed snapshots without bound; until now the only
// reclaim path was deleting the whole namespace (CleanupNamespaceResources
// on uninstall/realm purge). PruneImages is the surgical, operator-facing
// counterpart.
//
// The reclaim mechanism mirrors drainLeases (see CleanupNamespaceResources's
// GC-sweep rationale): each lease is released with leases.SynchronousDelete,
// which blocks on the containerd GC scheduler's ScheduleAndWait sweep. That
// sweep walks every surviving GC root — tagged images (the image metadata
// bucket is itself a root referencing its manifest/config/layers), live
// containers (the container metadata bucket is a root referencing its
// snapshot + image content), and the leases we keep — and reclaims content
// and snapshots no surviving root references.
//
// Surgical vs. the namespace-wide drain:
//   - PruneImages never deletes images, snapshots, or blobs directly. It
//     only releases leases and lets the GC sweep decide what is unreachable,
//     so a tagged image or a live cell's layers are reclaimed only if no
//     surviving root still references them — which never happens, because
//     those roots survive the prune.
//   - Leases that back a live container's snapshot are retained (belt and
//     suspenders with the container GC root) so an in-flight prune can never
//     race a running cell's content out from under it. Leases that reference
//     only a tagged image's content are *not* specially retained: the image
//     metadata root already protects that content, and those build-time
//     leases are exactly the orphans that also pin the dangling sibling
//     layers this verb exists to reclaim — retaining them would defeat the
//     prune.
//
// Idempotent: a second run on a clean realm finds only retained (or zero)
// leases and deletes nothing.
//
// Real-containerd integration of the prune lives in the dev-init smoke and
// e2e suite; CI's `make test` target avoids needing a containerd socket, so
// the per-package regression tests drive pruneLeases with a fake.
func (c *client) PruneImages(namespace string) (PruneResult, error) {
	nsCtx := c.namespaceCtx(namespace)

	protected, err := c.liveContainerSnapshotKeys(nsCtx, namespace)
	if err != nil {
		return PruneResult{}, fmt.Errorf("%w: %w", internalerrdefs.ErrPruneImages, err)
	}

	res := c.pruneLeases(nsCtx, namespace, protected, c.conn())
	c.logger.InfoContext(
		c.ctx,
		"pruned images",
		"namespace", namespace,
		"leasesDeleted", res.LeasesDeleted,
		"leasesRetained", res.LeasesRetained,
	)
	return res, nil
}

// liveContainerSnapshotKeys returns the set of snapshot keys backing live
// (running or stopped) containers in the namespace. A lease referencing any
// of these keys is retained by pruneLeases so the prune never races a live
// cell's content. Defaults the key to the container ID when SnapshotKey is
// unset (mirrors CreateContainerFromSpec / ContainerRootChainID).
func (c *client) liveContainerSnapshotKeys(nsCtx context.Context, namespace string) (map[string]struct{}, error) {
	containers, err := c.conn().Containers(nsCtx)
	if err != nil {
		c.logger.ErrorContext(
			c.ctx,
			"failed to list containers for prune",
			"namespace",
			namespace,
			"err",
			formatError(err),
		)
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}
	keys := make(map[string]struct{}, len(containers))
	for _, container := range containers {
		info, infoErr := container.Info(nsCtx)
		if infoErr != nil {
			c.logger.WarnContext(
				c.ctx,
				"failed to read container info for prune; treating its lease as droppable",
				"namespace", namespace,
				"container", container.ID(),
				"err", formatError(infoErr),
			)
			continue
		}
		key := info.SnapshotKey
		if key == "" {
			key = container.ID()
		}
		keys[key] = struct{}{}
	}
	return keys, nil
}

// pruneLeases releases every lease in the namespace that does not back a
// protected (live-container) snapshot, using leases.SynchronousDelete so each
// delete blocks on the GC sweep that reclaims now-unreachable content and
// snapshots. Split out (taking a containerdNamespaceServices, like
// drainNamespaceResources) so a fake can drive the retain/delete decision in
// unit tests without a real containerd.
//
// A lease whose resources cannot be listed, or whose delete fails, is counted
// as retained — pruneLeases is best-effort and conservative, never reporting a
// delete it did not actually perform.
func (c *client) pruneLeases(
	nsCtx context.Context,
	namespace string,
	protected map[string]struct{},
	srv containerdNamespaceServices,
) PruneResult {
	var res PruneResult
	leaseManager := srv.LeasesService()
	existingLeases, err := leaseManager.List(nsCtx)
	if err != nil {
		c.logger.WarnContext(c.ctx, "failed to list leases for prune", "namespace", namespace, "error", err)
		return res
	}
	for _, lease := range existingLeases {
		resources, resErr := leaseManager.ListResources(nsCtx, lease)
		if resErr != nil {
			c.logger.WarnContext(
				c.ctx,
				"failed to list lease resources; retaining lease",
				"namespace", namespace,
				"lease", lease.ID,
				"error", resErr,
			)
			res.LeasesRetained++
			continue
		}
		if leaseBacksProtected(resources, protected) {
			c.logger.DebugContext(
				c.ctx,
				"retaining lease backing live container",
				"namespace",
				namespace,
				"lease",
				lease.ID,
			)
			res.LeasesRetained++
			continue
		}
		c.logger.DebugContext(c.ctx, "deleting orphaned lease", "namespace", namespace, "lease", lease.ID)
		if deleteErr := leaseManager.Delete(nsCtx, lease, leases.SynchronousDelete); deleteErr != nil {
			c.logger.WarnContext(
				c.ctx,
				"failed to delete lease; retaining",
				"namespace", namespace,
				"lease", lease.ID,
				"error", deleteErr,
			)
			res.LeasesRetained++
			continue
		}
		res.LeasesDeleted++
	}
	return res
}

// leaseBacksProtected reports whether any of the lease's resources reference a
// protected snapshot key. Resource.ID carries the snapshot key for
// snapshotter resources (leases.Resource{Type:"snapshots/<name>",ID:<key>}),
// which is the identity liveContainerSnapshotKeys collects.
func leaseBacksProtected(resources []leases.Resource, protected map[string]struct{}) bool {
	for _, r := range resources {
		if _, ok := protected[r.ID]; ok {
			return true
		}
	}
	return false
}
