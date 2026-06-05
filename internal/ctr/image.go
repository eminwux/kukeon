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
	"time"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/defaults"
	"github.com/containerd/errdefs"
	"github.com/containerd/platforms"
	"github.com/eminwux/kukeon/internal/consts"
	internalerrdefs "github.com/eminwux/kukeon/internal/errdefs"
	"github.com/opencontainers/image-spec/identity"
)

// ImageInfo is the ctr-layer view of a containerd image. The fields are the
// common subset surfaced to operators by `kuke get image`; downstream layers
// re-encode this onto their own wire types so the ctr package does not leak
// into pkg/api.
type ImageInfo struct {
	Name      string
	Size      int64
	CreatedAt time.Time
	Digest    string
	MediaType string
	Labels    map[string]string
}

// ensureImageUnpacked ensures that an image is unpacked for the given snapshotter.
// If the image is not unpacked, it will be unpacked. Returns an error if unpacking fails.
func (c *client) ensureImageUnpacked(namespace string, image containerd.Image, snapshotter string) error {
	nsCtx := c.namespaceCtx(namespace)

	// Check if image is already unpacked
	unpacked, err := image.IsUnpacked(nsCtx, snapshotter)
	if err != nil {
		c.logger.WarnContext(
			c.ctx,
			"failed to check if image is unpacked",
			"image",
			image.Name(),
			"snapshotter",
			snapshotter,
			"err",
			formatError(err),
		)
		// Continue to attempt unpack even if check failed
	} else if unpacked {
		c.logger.DebugContext(c.ctx, "image already unpacked", "image", image.Name(), "snapshotter", snapshotter)
		return nil
	}

	// Image is not unpacked, unpack it
	c.logger.DebugContext(c.ctx, "unpacking image", "image", image.Name(), "snapshotter", snapshotter)
	err = image.Unpack(nsCtx, snapshotter)
	if err != nil {
		c.logger.ErrorContext(
			c.ctx,
			"failed to unpack image",
			"image",
			image.Name(),
			"snapshotter",
			snapshotter,
			"err",
			formatError(err),
		)
		return fmt.Errorf("failed to unpack image %s: %w", image.Name(), err)
	}

	c.logger.DebugContext(c.ctx, "image unpacked successfully", "image", image.Name(), "snapshotter", snapshotter)
	return nil
}

// pullImage pulls an image from a registry if it's not found locally.
// Returns the image and any error encountered.
//
// Refs hosted under the local-only kukeon.internal registry (see
// consts.InternalImageRegistry) are never pulled: they are built into this
// realm's namespace by `kuke team init --build` (internal/teambuild), not
// published anywhere a pull could reach. A local miss on such a ref is an
// operator error — the image was never built — so pullImage short-circuits
// with ErrInternalImageNotBuilt ("build it") instead of attempting a doomed
// network pull against the non-routable host. The full build→bind→run path is
// exercised by the `kuke team init --build` two-project compose e2e and the
// dev-init smoke; this layer's contract is the no-pull short-circuit itself.
func (c *client) pullImage(namespace string, imageRef string, creds []RegistryCredentials) (containerd.Image, error) {
	nsCtx := c.namespaceCtx(namespace)
	cc := c.conn()

	// Try to get the image locally first
	image, err := cc.GetImage(nsCtx, imageRef)
	if err == nil {
		return image, nil
	}

	// Local-only kukeon.internal refs are never pulled — a miss means the
	// image was supposed to be built locally and was not.
	if consts.IsInternalImageRef(imageRef) {
		c.logger.WarnContext(
			c.ctx,
			"local-only image not present in realm; not pulling",
			"image", imageRef,
		)
		return nil, fmt.Errorf("%w: %s", internalerrdefs.ErrInternalImageNotBuilt, imageRef)
	}

	// Image not found locally, pull it
	c.logger.DebugContext(c.ctx, "image not found locally, pulling", "image", imageRef)

	// Create a lease for the pull operation to avoid lease management issues
	// The lease will be automatically cleaned up when the context is done
	leaseManager := cc.LeasesService()
	lease, leaseErr := leaseManager.Create(
		nsCtx,
		leases.WithID(fmt.Sprintf("pull-%s-%d", imageRef, time.Now().UnixNano())),
	)
	if leaseErr != nil {
		c.logger.WarnContext(
			c.ctx,
			"failed to create lease for image pull, continuing without lease",
			"image",
			imageRef,
			"err",
			formatError(leaseErr),
		)
		// Continue without lease - some containerd setups may not require it
	} else {
		// Use lease context for pull
		leaseCtx := leases.WithLease(nsCtx, lease.ID)
		defer func() {
			// Clean up lease after pull
			if deleteErr := leaseManager.Delete(nsCtx, lease); deleteErr != nil {
				c.logger.WarnContext(c.ctx, "failed to delete lease after image pull", "lease", lease.ID, "err", formatError(deleteErr))
			}
		}()
		nsCtx = leaseCtx
	}

	// Use default platform for pull
	// The image will be unpacked separately after pull
	platform := platforms.DefaultSpec()

	// Build pull options with resolver if credentials are available
	pullOpts := []containerd.RemoteOpt{
		containerd.WithPlatform(platforms.Format(platform)),
	}

	// Use credentials passed as parameter
	if len(creds) > 0 {
		resolver := buildResolver(creds)
		pullOpts = append(pullOpts, containerd.WithResolver(resolver))
		c.logger.DebugContext(c.ctx, "pulling image with credentials", "image", imageRef, "creds_count", len(creds))
	} else {
		c.logger.DebugContext(c.ctx, "pulling image anonymously", "image", imageRef)
	}

	image, err = cc.Pull(nsCtx, imageRef, pullOpts...)
	if err != nil {
		c.logger.ErrorContext(c.ctx, "failed to pull image", "image", imageRef, "err", formatError(err))
		return nil, fmt.Errorf("failed to pull image %s: %w", imageRef, err)
	}

	return image, nil
}

// LoadImage imports an OCI/docker image tarball into the specified
// containerd namespace and returns the names of the imported images.
//
// WithSkipMissing() mirrors `ctr images import`'s tolerance: multi-arch
// tarballs produced by `docker save` reference platform-specific blobs the
// docker daemon does not always include. Skipping missing blobs lets the
// host-arch manifest land while ignoring the others.
func (c *client) LoadImage(namespace string, reader io.Reader) ([]string, error) {
	nsCtx := c.namespaceCtx(namespace)

	imgs, err := c.conn().Import(nsCtx, reader, containerd.WithSkipMissing())
	if err != nil {
		c.logger.ErrorContext(
			c.ctx,
			"failed to import image tarball",
			"namespace",
			namespace,
			"err",
			formatError(err),
		)
		return nil, fmt.Errorf("failed to import image tarball: %w", err)
	}

	names := make([]string, 0, len(imgs))
	for _, img := range imgs {
		names = append(names, img.Name)
	}
	c.logger.DebugContext(c.ctx, "imported image tarball", "namespace", namespace, "images", names)
	return names, nil
}

// ListImages enumerates images in the specified containerd namespace.
// Size is best-effort: when containerd cannot resolve an image's size (e.g.
// because content is missing locally), the entry is still surfaced with
// Size=-1 so listing degrades gracefully instead of failing the whole call.
func (c *client) ListImages(namespace string) ([]ImageInfo, error) {
	nsCtx := c.namespaceCtx(namespace)

	imgs, err := c.conn().ListImages(nsCtx)
	if err != nil {
		c.logger.ErrorContext(
			c.ctx,
			"failed to list images",
			"namespace",
			namespace,
			"err",
			formatError(err),
		)
		return nil, fmt.Errorf("%w: %w", internalerrdefs.ErrListImages, err)
	}

	out := make([]ImageInfo, 0, len(imgs))
	for _, img := range imgs {
		out = append(out, c.imageToInfo(namespace, img))
	}
	return out, nil
}

// GetImage returns metadata for the named image ref in the specified
// containerd namespace. Returns errdefs.ErrImageNotFound (the kukeon
// sentinel) when containerd reports the ref absent so upper layers can map
// to a clean error message.
func (c *client) GetImage(namespace, ref string) (ImageInfo, error) {
	nsCtx := c.namespaceCtx(namespace)

	img, err := c.conn().GetImage(nsCtx, ref)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return ImageInfo{}, fmt.Errorf("%w: %s", internalerrdefs.ErrImageNotFound, ref)
		}
		c.logger.ErrorContext(
			c.ctx,
			"failed to get image",
			"namespace",
			namespace,
			"ref",
			ref,
			"err",
			formatError(err),
		)
		return ImageInfo{}, fmt.Errorf("%w: %w", internalerrdefs.ErrGetImage, err)
	}
	return c.imageToInfo(namespace, img), nil
}

// DeleteImage removes the named image ref from the specified containerd
// namespace and triggers a synchronous content/snapshot GC sweep so layers
// exclusive to the deleted image are reclaimed before the call returns
// (#1037). Without the sweep, ImageService().Delete only unlinks the
// metadata image bucket entry — exclusive layers stay pinned by GC
// references the deleted image was their last root for, so the operator
// sees no disk freed.
//
// images.SynchronousDelete() flips the image-service RPC's Sync flag,
// which the containerd-side handler honors by calling ScheduleAndWait on
// the GC scheduler after the metadata removal (containerd v2
// plugins/services/images/local.go Delete). The sweep walks every GC root
// (live images, leases, container snapshots) and reclaims content and
// snapshots no surviving root references — so layers shared with another
// tagged image or a running container survive on their own refcount,
// satisfying the AC's "shared layers preserved" guarantee without any
// per-layer accounting on our side.
//
// Mirrors the leases.SynchronousDelete pattern in drainLeases (see
// CleanupNamespaceResources's GC-sweep rationale). The pull-time lease
// pullImage creates is dropped by its own defer, so the regular pull
// path leaves no orphaned lease for this sweep to step around;
// kukebuild's build-time orphaned leases are tracked separately in
// #1038 and would survive this sweep regardless.
//
// The kukeon ErrImageNotFound sentinel is returned when containerd
// reports the ref absent so upper layers can map to a clean error
// message; other errors are wrapped with ErrDeleteImage.
func (c *client) DeleteImage(namespace, ref string) error {
	nsCtx := c.namespaceCtx(namespace)
	return c.deleteImage(nsCtx, namespace, ref, c.conn().ImageService())
}

// deleteImage is the body of DeleteImage split out so unit tests can drive
// the image-delete path with a fake images.Store that captures the
// DeleteOpt set — the SynchronousDelete opt is load-bearing for layer
// reclaim and is therefore asserted in TestDeleteImagePassesSynchronousDelete.
func (c *client) deleteImage(nsCtx context.Context, namespace, ref string, store images.Store) error {
	if err := store.Delete(nsCtx, ref, images.SynchronousDelete()); err != nil {
		if errdefs.IsNotFound(err) {
			return fmt.Errorf("%w: %s", internalerrdefs.ErrImageNotFound, ref)
		}
		c.logger.ErrorContext(
			c.ctx,
			"failed to delete image",
			"namespace",
			namespace,
			"ref",
			ref,
			"err",
			formatError(err),
		)
		return fmt.Errorf("%w: %w", internalerrdefs.ErrDeleteImage, err)
	}
	return nil
}

// ImageChainID returns the chainID the image at ref would unpack to today,
// computed from its current rootfs DiffIDs in the namespace's content store.
// The chainID is the canonical content-addressed identity of the layer
// stack a fresh container would be anchored on, so a difference between
// this value and a container's existing snapshot Parent is a precise
// signal that the image at ref has been re-pointed since the container
// was created (the digest-drift signal #915 defect 2 was missing).
//
// Returns internalerrdefs.ErrImageNotFound when ref is absent in
// namespace (matches GetImage's not-found mapping so upper layers stay
// uniform); operational failures wrap internalerrdefs.ErrGetImage.
func (c *client) ImageChainID(namespace, ref string) (string, error) {
	nsCtx := c.namespaceCtx(namespace)

	img, err := c.conn().GetImage(nsCtx, ref)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return "", fmt.Errorf("%w: %s", internalerrdefs.ErrImageNotFound, ref)
		}
		return "", fmt.Errorf("%w: %w", internalerrdefs.ErrGetImage, err)
	}

	diffIDs, err := img.RootFS(nsCtx)
	if err != nil {
		return "", fmt.Errorf("%w: rootfs for %s: %w", internalerrdefs.ErrGetImage, ref, err)
	}
	if len(diffIDs) == 0 {
		return "", nil
	}
	return identity.ChainID(diffIDs).String(), nil
}

// ContainerRootChainID returns the chainID of the parent layer stack the
// container's root snapshot was committed against — i.e. the image content
// the container is anchored on at the moment of the call, regardless of
// what the image ref by the same name resolves to today. Returned as a
// string so callers comparing it against ImageChainID (issue #915 defect
// 2) need no third-party type.
//
// The snapshotter key defaults to the container's ID (see container.go
// CreateContainerFromSpec, which falls back to spec.ID when SnapshotKey is
// unset); we honor the explicit field too in case a future caller customizes
// it. Returns internalerrdefs.ErrContainerNotFound when the container is
// absent in namespace.
func (c *client) ContainerRootChainID(namespace, containerID string) (string, error) {
	container, err := c.loadContainer(namespace, containerID)
	if err != nil {
		return "", err
	}

	nsCtx := c.namespaceCtx(namespace)
	info, err := container.Info(nsCtx)
	if err != nil {
		return "", fmt.Errorf("failed to get container info for %s: %w", containerID, err)
	}

	snapshotKey := info.SnapshotKey
	if snapshotKey == "" {
		snapshotKey = containerID
	}
	snapshotter := info.Snapshotter
	if snapshotter == "" {
		snapshotter = defaults.DefaultSnapshotter
	}

	snapInfo, err := c.conn().SnapshotService(snapshotter).Stat(nsCtx, snapshotKey)
	if err != nil {
		return "", fmt.Errorf("failed to stat snapshot %s/%s: %w", snapshotter, snapshotKey, err)
	}
	return snapInfo.Parent, nil
}

// imageToInfo extracts the ImageInfo subset from a containerd Image. Size is
// resolved via the platform-default Size() helper; failure leaves Size=-1
// rather than aborting because partial-content tarballs are common with
// `docker save` (see LoadImage's WithSkipMissing rationale).
func (c *client) imageToInfo(namespace string, img containerd.Image) ImageInfo {
	nsCtx := c.namespaceCtx(namespace)
	meta := img.Metadata()
	target := img.Target()

	size := int64(-1)
	if s, err := img.Size(nsCtx); err == nil {
		size = s
	} else {
		c.logger.DebugContext(
			c.ctx,
			"failed to resolve image size",
			"namespace",
			namespace,
			"image",
			img.Name(),
			"err",
			formatError(err),
		)
	}

	return ImageInfo{
		Name:      img.Name(),
		Size:      size,
		CreatedAt: meta.CreatedAt,
		Digest:    target.Digest.String(),
		MediaType: target.MediaType,
		Labels:    meta.Labels,
	}
}
