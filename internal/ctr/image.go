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
	"fmt"
	"io"
	"time"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/errdefs"
	"github.com/containerd/platforms"
	internalerrdefs "github.com/eminwux/kukeon/internal/errdefs"
)

// ImageInfo is the ctr-layer view of a containerd image. The fields are the
// common subset surfaced to operators by `kuke image get`; downstream layers
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
func (c *client) ensureImageUnpacked(image containerd.Image, snapshotter string) error {
	nsCtx := c.namespaceCtx()

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
func (c *client) pullImage(imageRef string) (containerd.Image, error) {
	nsCtx := c.namespaceCtx()

	// Try to get the image locally first
	image, err := c.cClient.GetImage(nsCtx, imageRef)
	if err == nil {
		return image, nil
	}

	// Image not found locally, pull it
	c.logger.DebugContext(c.ctx, "image not found locally, pulling", "image", imageRef)

	// Create a lease for the pull operation to avoid lease management issues
	// The lease will be automatically cleaned up when the context is done
	leaseManager := c.cClient.LeasesService()
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

	// Get credentials from client (set when namespace was configured)
	creds := c.GetRegistryCredentials()
	if len(creds) > 0 {
		resolver := buildResolver(creds)
		pullOpts = append(pullOpts, containerd.WithResolver(resolver))
		c.logger.DebugContext(c.ctx, "pulling image with credentials", "image", imageRef, "creds_count", len(creds))
	} else {
		c.logger.DebugContext(c.ctx, "pulling image anonymously", "image", imageRef)
	}

	image, err = c.cClient.Pull(nsCtx, imageRef, pullOpts...)
	if err != nil {
		c.logger.ErrorContext(c.ctx, "failed to pull image", "image", imageRef, "err", formatError(err))
		return nil, fmt.Errorf("failed to pull image %s: %w", imageRef, err)
	}

	return image, nil
}

// LoadImage imports an OCI/docker image tarball into the client's current
// containerd namespace and returns the names of the imported images. The
// caller sets the target namespace via SetNamespace; namespaceCtx() then
// scopes the import to that namespace.
//
// WithSkipMissing() mirrors `ctr images import`'s tolerance: multi-arch
// tarballs produced by `docker save` reference platform-specific blobs the
// docker daemon does not always include. Skipping missing blobs lets the
// host-arch manifest land while ignoring the others.
func (c *client) LoadImage(reader io.Reader) ([]string, error) {
	nsCtx := c.namespaceCtx()

	imgs, err := c.cClient.Import(nsCtx, reader, containerd.WithSkipMissing())
	if err != nil {
		c.logger.ErrorContext(
			c.ctx,
			"failed to import image tarball",
			"namespace",
			c.Namespace(),
			"err",
			formatError(err),
		)
		return nil, fmt.Errorf("failed to import image tarball: %w", err)
	}

	names := make([]string, 0, len(imgs))
	for _, img := range imgs {
		names = append(names, img.Name)
	}
	c.logger.DebugContext(c.ctx, "imported image tarball", "namespace", c.Namespace(), "images", names)
	return names, nil
}

// ListImages enumerates images in the client's current containerd namespace.
// Size is best-effort: when containerd cannot resolve an image's size (e.g.
// because content is missing locally), the entry is still surfaced with
// Size=-1 so listing degrades gracefully instead of failing the whole call.
func (c *client) ListImages() ([]ImageInfo, error) {
	nsCtx := c.namespaceCtx()

	imgs, err := c.cClient.ListImages(nsCtx)
	if err != nil {
		c.logger.ErrorContext(
			c.ctx,
			"failed to list images",
			"namespace",
			c.Namespace(),
			"err",
			formatError(err),
		)
		return nil, fmt.Errorf("%w: %w", internalerrdefs.ErrListImages, err)
	}

	out := make([]ImageInfo, 0, len(imgs))
	for _, img := range imgs {
		out = append(out, c.imageToInfo(img))
	}
	return out, nil
}

// GetImage returns metadata for the named image ref in the client's current
// containerd namespace. Returns errdefs.ErrImageNotFound (the kukeon
// sentinel) when containerd reports the ref absent so upper layers can map
// to a clean error message.
func (c *client) GetImage(ref string) (ImageInfo, error) {
	nsCtx := c.namespaceCtx()

	img, err := c.cClient.GetImage(nsCtx, ref)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return ImageInfo{}, fmt.Errorf("%w: %s", internalerrdefs.ErrImageNotFound, ref)
		}
		c.logger.ErrorContext(
			c.ctx,
			"failed to get image",
			"namespace",
			c.Namespace(),
			"ref",
			ref,
			"err",
			formatError(err),
		)
		return ImageInfo{}, fmt.Errorf("%w: %w", internalerrdefs.ErrGetImage, err)
	}
	return c.imageToInfo(img), nil
}

// DeleteImage removes the named image ref from the client's current
// containerd namespace. The kukeon ErrImageNotFound sentinel is returned
// when containerd reports the ref absent so upper layers can map to a
// clean error message; other errors are wrapped with ErrDeleteImage.
func (c *client) DeleteImage(ref string) error {
	nsCtx := c.namespaceCtx()

	if err := c.cClient.ImageService().Delete(nsCtx, ref); err != nil {
		if errdefs.IsNotFound(err) {
			return fmt.Errorf("%w: %s", internalerrdefs.ErrImageNotFound, ref)
		}
		c.logger.ErrorContext(
			c.ctx,
			"failed to delete image",
			"namespace",
			c.Namespace(),
			"ref",
			ref,
			"err",
			formatError(err),
		)
		return fmt.Errorf("%w: %w", internalerrdefs.ErrDeleteImage, err)
	}
	return nil
}

// imageToInfo extracts the ImageInfo subset from a containerd Image. Size is
// resolved via the platform-default Size() helper; failure leaves Size=-1
// rather than aborting because partial-content tarballs are common with
// `docker save` (see LoadImage's WithSkipMissing rationale).
func (c *client) imageToInfo(img containerd.Image) ImageInfo {
	nsCtx := c.namespaceCtx()
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
			c.Namespace(),
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
