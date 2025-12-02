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
	"time"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/platforms"
)

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
