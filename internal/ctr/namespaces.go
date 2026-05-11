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
	"slices"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/opencontainers/go-digest"
)

// containerdNamespaceServices is the subset of containerd.Client used by
// drainNamespaceResources. Extracted so a fake modeling lease→snapshot
// pinning can drive the drain in unit tests without a real containerd —
// real-containerd integration in this repo lives in the e2e suite, not the
// per-package `make test` target. *containerd.Client satisfies it directly.
type containerdNamespaceServices interface {
	ImageService() images.Store
	SnapshotService(name string) snapshots.Snapshotter
	ContentStore() content.Store
	LeasesService() leases.Manager
}

// namespaceCtx returns a context with the namespace set.
func (c *client) namespaceCtx(namespace string) context.Context {
	return namespaces.WithNamespace(c.ctx, namespace)
}

func (c *client) CreateNamespace(namespace string) error {
	c.logger.DebugContext(c.ctx, "creating namespace", "namespace", namespace)
	namespaces := c.cClient.NamespaceService()

	err := namespaces.Create(c.ctx, namespace, nil)
	if err != nil {
		c.logger.ErrorContext(
			c.ctx,
			"failed to create containerd namespace",
			"namespace",
			namespace,
			"err",
			fmt.Sprintf("%v", err),
		)
		return err
	}

	c.logger.InfoContext(c.ctx, "created containerd namespace", "namespace", namespace)
	return nil
}

func (c *client) DeleteNamespace(namespace string) error {
	if namespace == "" {
		return errors.New("namespace name is required")
	}

	// Ensure client is connected
	if c.cClient == nil {
		if err := c.Connect(); err != nil {
			return fmt.Errorf("failed to connect to containerd: %w", err)
		}
	}

	c.logger.DebugContext(c.ctx, "deleting namespace", "namespace", namespace)
	namespaces := c.cClient.NamespaceService()

	// Check if namespace exists first
	nsList, err := namespaces.List(c.ctx)
	if err != nil {
		c.logger.ErrorContext(c.ctx, "failed to list containerd namespaces", "err", fmt.Sprintf("%v", err))
		return fmt.Errorf("failed to list namespaces: %w", err)
	}

	if !slices.Contains(nsList, namespace) {
		c.logger.DebugContext(c.ctx, "namespace not found, skipping deletion", "namespace", namespace)
		return nil // Idempotent: namespace doesn't exist, consider it deleted
	}

	// Delete the namespace
	// Note: The namespace must be empty (no containers, images, etc.) to be deleted
	c.logger.InfoContext(c.ctx, "deleting containerd namespace", "namespace", namespace)
	if err = namespaces.Delete(c.ctx, namespace); err != nil {
		c.logger.ErrorContext(
			c.ctx,
			"failed to delete containerd namespace",
			"namespace",
			namespace,
			"err",
			fmt.Sprintf("%v", err),
		)
		return fmt.Errorf("failed to delete namespace: %w", err)
	}

	c.logger.InfoContext(c.ctx, "deleted containerd namespace", "namespace", namespace)
	return nil
}

func (c *client) ListNamespaces() ([]string, error) {
	c.logger.DebugContext(c.ctx, "listing namespaces")

	namespaces := c.cClient.NamespaceService()
	// containerd requires a namespace for most API calls.
	// But listing namespaces does not require entering one.

	nsList, err := namespaces.List(c.ctx)
	if err != nil {
		c.logger.Error("failed to list containerd namespaces: %v", "err", fmt.Sprintf("%v", err))
		return nil, err
	}

	return nsList, nil
}

func (c *client) ExistsNamespace(namespace string) (bool, error) {
	ns, err := c.GetNamespace(namespace)
	if err != nil {
		return false, err
	}
	return ns == namespace, nil
}

func (c *client) GetNamespace(namespace string) (string, error) {
	c.logger.DebugContext(c.ctx, "getting namespace", "namespace", namespace)
	namespaces := c.cClient.NamespaceService()

	nsList, err := namespaces.List(c.ctx)
	if err != nil {
		c.logger.Error("failed to list containerd namespaces: %v", "err", fmt.Sprintf("%v", err))
		return "", err
	}

	if slices.Contains(nsList, namespace) {
		c.logger.InfoContext(c.ctx, "namespace found", "namespace", namespace)
		return namespace, nil
	}

	c.logger.InfoContext(c.ctx, "namespace not found", "namespace", namespace)
	return "", nil
}

// KukeonKnownSnapshotters is the list of containerd snapshotters
// CleanupNamespaceResources walks when no snapshotter is specified. Stays
// in sync with the set of snapshotters supported by the kukeond image
// (overlayfs in production; native is always present as containerd's
// fallback). Listed in the order they will be drained — overlayfs first
// because it is the only one populated on a real install, but the others
// are tried so a host that experimented with btrfs/zfs/etc. still gets a
// clean uninstall instead of "namespace not empty" surfacing the day after.
//
// Listed snapshotters that are not registered in the daemon return errors
// from snapshotService.Walk; cleanupSnapshotsFor handles those at WARN and
// keeps walking the rest.
//
//nolint:gochecknoglobals // Immutable enumeration; package-level so uninstall callers iterate without re-allocating.
var KukeonKnownSnapshotters = []string{
	"overlayfs",
	"native",
	"btrfs",
	"zfs",
	"devmapper",
	"blockfile",
}

// CleanupNamespaceResources empties a containerd namespace so DeleteNamespace
// (which requires the namespace to be empty) can succeed. It drains in this
// fixed order:
//  1. leases (with leases.SynchronousDelete),
//  2. images,
//  3. snapshots — for every snapshotter when `snapshotter == ""` (the
//     uninstall path's default), or just the named snapshotter when one is
//     specified explicitly,
//  4. blobs (content store).
//
// Leases run first because leases.SynchronousDelete triggers the containerd
// GC scheduler's ScheduleAndWait sweep before returning. That sweep is what
// reconciles any metadata-only snapshot entries the image-pull path left
// behind (entries whose lease was the GC root keeping them reachable); a
// straight Walk+Remove pass after the fact misses those because the
// metadata snapshotter's Walk silently skips entries with no underlying
// state (containerd v2 metadata/snapshot.go). Draining leases first lets the
// GC sweep clear them, then the subsequent snapshotter walk handles whatever
// the user/runtime layer still pinned directly.
//
// Each class logs a count summary at INFO and per-resource debug lines at
// DEBUG, so a future "namespace not empty" failure points at the exact class
// still pinning the namespace. Per-resource failures are logged at WARN and
// processing continues — the goal is best-effort drain, with the load-bearing
// signal coming from the caller's subsequent DeleteNamespace check.
func (c *client) CleanupNamespaceResources(namespace, snapshotter string) error {
	// Ensure client is connected
	if c.cClient == nil {
		if err := c.Connect(); err != nil {
			return fmt.Errorf("failed to connect to containerd: %w", err)
		}
	}

	nsCtx := namespaces.WithNamespace(c.ctx, namespace)
	c.drainNamespaceResources(nsCtx, namespace, snapshotter, c.cClient)
	return nil
}

// drainNamespaceResources is the body of CleanupNamespaceResources split out
// so unit tests can drive the drain order with a fake containerdNamespaceServices
// that models lease→snapshot pinning. The order is load-bearing — see the
// CleanupNamespaceResources doc for the GC-sweep rationale — and is therefore
// asserted by TestCleanupNamespaceResourcesDrainsLeasePinnedSnapshots.
func (c *client) drainNamespaceResources(
	nsCtx context.Context,
	namespace, snapshotter string,
	srv containerdNamespaceServices,
) {
	c.drainLeases(nsCtx, namespace, srv)
	c.drainImages(nsCtx, namespace, srv)

	snapshotters := []string{snapshotter}
	if snapshotter == "" {
		snapshotters = KukeonKnownSnapshotters
	}
	for _, snap := range snapshotters {
		c.cleanupSnapshotsFor(nsCtx, namespace, snap, srv)
	}

	c.drainBlobs(nsCtx, namespace, srv)
}

// drainLeases releases every lease in the namespace using
// leases.SynchronousDelete so each delete blocks on the GC scheduler's
// ScheduleAndWait sweep. That sweep is what reconciles metadata-only
// snapshot orphans (entries whose lease was the only GC root keeping them
// reachable); see the CleanupNamespaceResources doc for the full story.
func (c *client) drainLeases(nsCtx context.Context, namespace string, srv containerdNamespaceServices) {
	c.logger.DebugContext(c.ctx, "cleaning up leases", "namespace", namespace)
	leaseManager := srv.LeasesService()
	existingLeases, err := leaseManager.List(nsCtx)
	if err != nil {
		c.logger.WarnContext(c.ctx, "failed to list leases", "namespace", namespace, "error", err)
		return
	}
	c.logger.InfoContext(c.ctx, "draining leases", "namespace", namespace, "count", len(existingLeases))
	for _, lease := range existingLeases {
		c.logger.DebugContext(c.ctx, "deleting lease", "namespace", namespace, "lease", lease.ID)
		if deleteErr := leaseManager.Delete(nsCtx, lease, leases.SynchronousDelete); deleteErr != nil {
			c.logger.WarnContext(
				c.ctx,
				"failed to delete lease",
				"namespace",
				namespace,
				"lease",
				lease.ID,
				"error",
				deleteErr,
			)
			continue
		}
		c.logger.DebugContext(c.ctx, "deleted lease", "namespace", namespace, "lease", lease.ID)
	}
}

// drainImages deletes every image in the namespace via the metadata image
// store. Image deletion only unlinks the metadata image bucket entry; the
// underlying snapshot/blob refcount drops to zero and the next GC sweep (or
// the snapshot/blob drain below) reclaims them.
func (c *client) drainImages(nsCtx context.Context, namespace string, srv containerdNamespaceServices) {
	c.logger.DebugContext(c.ctx, "listing images in namespace", "namespace", namespace)
	imageStore := srv.ImageService()
	imgs, err := imageStore.List(nsCtx)
	if err != nil {
		c.logger.WarnContext(c.ctx, "failed to list images", "namespace", namespace, "error", err)
		return
	}
	c.logger.DebugContext(c.ctx, "found images in namespace", "namespace", namespace, "count", len(imgs))
	for _, image := range imgs {
		imageName := image.Name
		if imageName == "" {
			// If image has no name, use target digest
			if image.Target.Digest.String() != "" {
				imageName = image.Target.Digest.String()
			} else {
				c.logger.WarnContext(c.ctx, "skipping image with no name or digest", "namespace", namespace)
				continue
			}
		}
		c.logger.DebugContext(c.ctx, "deleting image", "namespace", namespace, "image", imageName)
		if deleteErr := imageStore.Delete(nsCtx, imageName); deleteErr != nil {
			c.logger.WarnContext(
				c.ctx,
				"failed to delete image",
				"namespace",
				namespace,
				"image",
				imageName,
				"error",
				deleteErr,
			)
			continue
		}
		c.logger.DebugContext(c.ctx, "deleted image", "namespace", namespace, "image", imageName)
	}
}

// drainBlobs deletes every blob in the content store for the namespace.
// Runs last because content-store entries are still referenced by leases
// and images at the moment of cleanup; by the time the drain reaches here
// those higher-level holders are gone and Delete proceeds.
func (c *client) drainBlobs(nsCtx context.Context, namespace string, srv containerdNamespaceServices) {
	c.logger.DebugContext(c.ctx, "cleaning up blobs", "namespace", namespace)
	contentStore := srv.ContentStore()
	var blobDigests []digest.Digest
	err := contentStore.Walk(nsCtx, func(info content.Info) error {
		blobDigests = append(blobDigests, info.Digest)
		return nil
	})
	if err != nil {
		c.logger.WarnContext(c.ctx, "failed to walk blobs", "namespace", namespace, "error", err)
		return
	}
	c.logger.DebugContext(c.ctx, "found blobs", "namespace", namespace, "count", len(blobDigests))
	for _, dgst := range blobDigests {
		c.logger.DebugContext(c.ctx, "deleting blob", "namespace", namespace, "digest", dgst.String())
		if deleteErr := contentStore.Delete(nsCtx, dgst); deleteErr != nil {
			c.logger.WarnContext(
				c.ctx,
				"failed to delete blob",
				"namespace",
				namespace,
				"digest",
				dgst.String(),
				"error",
				deleteErr,
			)
			continue
		}
		c.logger.DebugContext(c.ctx, "deleted blob", "namespace", namespace, "digest", dgst.String())
	}
}

// cleanupSnapshotsFor drains every snapshot under one snapshotter from the
// supplied namespace context. Walks repeatedly so a parent with multiple
// children clears once each pass — Walk order is implementation-defined
// (boltdb key order), so a single reverse-order pass cannot guarantee
// children are removed before parents. Capped at maxSnapshotPasses to bound
// work on a pathological graph; in practice the depth is small (image layers
// + a few committed container layers).
func (c *client) cleanupSnapshotsFor(
	nsCtx context.Context,
	namespace, snapshotter string,
	srv containerdNamespaceServices,
) {
	if snapshotter == "" {
		return
	}
	c.logger.DebugContext(c.ctx, "cleaning up snapshots", "namespace", namespace, "snapshotter", snapshotter)
	snapshotService := srv.SnapshotService(snapshotter)

	const maxSnapshotPasses = 32
	totalRemoved := 0
	for pass := range maxSnapshotPasses {
		removed, done := c.cleanupSnapshotPass(nsCtx, snapshotService, namespace, snapshotter, pass)
		totalRemoved += removed
		if done {
			break
		}
	}
	if totalRemoved > 0 {
		c.logger.InfoContext(
			c.ctx,
			"drained snapshots",
			"namespace",
			namespace,
			"snapshotter",
			snapshotter,
			"removed",
			totalRemoved,
		)
	}
}

// cleanupSnapshotPass walks `snapshotter` once, removing as many snapshots as
// it can in reverse-walk order. Returns the number of snapshots removed in
// this pass and a `done` flag set when the loop should stop — either because
// the snapshotter is unregistered, the namespace is empty, or the pass made
// no forward progress.
func (c *client) cleanupSnapshotPass(
	nsCtx context.Context,
	snapshotService snapshots.Snapshotter,
	namespace, snapshotter string,
	pass int,
) (int, bool) {
	var snapshotKeys []string
	walkErr := snapshotService.Walk(nsCtx, func(_ context.Context, info snapshots.Info) error {
		snapshotKeys = append(snapshotKeys, info.Name)
		return nil
	})
	if walkErr != nil {
		// Snapshotter not registered (or socket-level error). Common when
		// iterating KukeonKnownSnapshotters on a host that only has
		// overlayfs — log at DEBUG so we don't WARN-spam every uninstall.
		c.logger.DebugContext(
			c.ctx,
			"snapshot walk failed; skipping snapshotter",
			"namespace",
			namespace,
			"snapshotter",
			snapshotter,
			"error",
			walkErr,
		)
		return 0, true
	}
	if len(snapshotKeys) == 0 {
		return 0, true
	}
	c.logger.DebugContext(
		c.ctx,
		"found snapshots",
		"namespace",
		namespace,
		"snapshotter",
		snapshotter,
		"count",
		len(snapshotKeys),
		"pass",
		pass,
	)
	removed := 0
	for i := len(snapshotKeys) - 1; i >= 0; i-- {
		key := snapshotKeys[i]
		if removeErr := snapshotService.Remove(nsCtx, key); removeErr != nil {
			c.logger.DebugContext(
				c.ctx,
				"snapshot remove deferred",
				"namespace",
				namespace,
				"snapshotter",
				snapshotter,
				"key",
				key,
				"error",
				removeErr,
			)
			continue
		}
		removed++
	}
	if removed == 0 {
		c.logger.WarnContext(
			c.ctx,
			"snapshot cleanup made no progress, giving up",
			"namespace",
			namespace,
			"snapshotter",
			snapshotter,
			"remaining",
			len(snapshotKeys),
		)
		return 0, true
	}
	return removed, false
}
