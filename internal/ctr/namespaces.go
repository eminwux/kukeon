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
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/opencontainers/go-digest"
)

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

// CleanupNamespaceResources removes all images and snapshots from a namespace
// This must be called before deleting the namespace, as containerd requires
// namespaces to be empty before deletion.
func (c *client) CleanupNamespaceResources(ctx context.Context, namespace, snapshotter string) error {
	// Ensure client is connected
	if c.cClient == nil {
		if err := c.Connect(); err != nil {
			return fmt.Errorf("failed to connect to containerd: %w", err)
		}
	}

	// Set namespace context for operations
	nsCtx := namespaces.WithNamespace(ctx, namespace)

	// Clean up images
	c.logger.DebugContext(c.ctx, "listing images in namespace", "namespace", namespace)
	images, err := c.cClient.ListImages(nsCtx)
	if err != nil {
		c.logger.WarnContext(c.ctx, "failed to list images", "namespace", namespace, "error", err)
	} else {
		c.logger.DebugContext(c.ctx, "found images in namespace", "namespace", namespace, "count", len(images))
		for _, image := range images {
			imageName := image.Name()
			if imageName == "" {
				// If image has no name, use target digest
				if target := image.Target(); target.Digest.String() != "" {
					imageName = target.Digest.String()
				} else {
					c.logger.WarnContext(c.ctx, "skipping image with no name or digest", "namespace", namespace)
					continue
				}
			}
			c.logger.DebugContext(c.ctx, "deleting image", "namespace", namespace, "image", imageName)
			if deleteErr := c.cClient.ImageService().Delete(nsCtx, imageName); deleteErr != nil {
				c.logger.WarnContext(c.ctx, "failed to delete image", "namespace", namespace, "image", imageName, "error", deleteErr)
				// Continue with other images
			} else {
				c.logger.DebugContext(c.ctx, "deleted image", "namespace", namespace, "image", imageName)
			}
		}
	}

	// Clean up snapshots
	if snapshotter == "" {
		snapshotter = "overlayfs" // Default snapshotter
	}
	c.logger.DebugContext(c.ctx, "cleaning up snapshots", "namespace", namespace, "snapshotter", snapshotter)
	snapshotService := c.cClient.SnapshotService(snapshotter)

	// Walk all snapshots and remove them
	var snapshotKeys []string
	err = snapshotService.Walk(nsCtx, func(_ context.Context, info snapshots.Info) error {
		snapshotKeys = append(snapshotKeys, info.Name)
		return nil
	})
	if err != nil {
		c.logger.WarnContext(
			c.ctx,
			"failed to walk snapshots",
			"namespace",
			namespace,
			"snapshotter",
			snapshotter,
			"error",
			err,
		)
	} else {
		c.logger.DebugContext(c.ctx, "found snapshots", "namespace", namespace, "snapshotter", snapshotter, "count", len(snapshotKeys))
		// Delete snapshots in reverse order (children before parents)
		for i := len(snapshotKeys) - 1; i >= 0; i-- {
			key := snapshotKeys[i]
			c.logger.DebugContext(c.ctx, "removing snapshot", "namespace", namespace, "snapshotter", snapshotter, "key", key)
			if removeErr := snapshotService.Remove(nsCtx, key); removeErr != nil {
				c.logger.WarnContext(c.ctx, "failed to remove snapshot", "namespace", namespace, "snapshotter", snapshotter, "key", key, "error", removeErr)
				// Continue with other snapshots
			} else {
				c.logger.DebugContext(c.ctx, "removed snapshot", "namespace", namespace, "snapshotter", snapshotter, "key", key)
			}
		}
	}

	// Clean up blobs (content)
	c.logger.DebugContext(c.ctx, "cleaning up blobs", "namespace", namespace)
	contentStore := c.cClient.ContentStore()
	var blobDigests []digest.Digest
	err = contentStore.Walk(nsCtx, func(info content.Info) error {
		blobDigests = append(blobDigests, info.Digest)
		return nil
	})
	if err != nil {
		c.logger.WarnContext(c.ctx, "failed to walk blobs", "namespace", namespace, "error", err)
	} else {
		c.logger.DebugContext(c.ctx, "found blobs", "namespace", namespace, "count", len(blobDigests))
		for _, dgst := range blobDigests {
			c.logger.DebugContext(c.ctx, "deleting blob", "namespace", namespace, "digest", dgst.String())
			if deleteErr := contentStore.Delete(nsCtx, dgst); deleteErr != nil {
				c.logger.WarnContext(c.ctx, "failed to delete blob", "namespace", namespace, "digest", dgst.String(), "error", deleteErr)
				// Continue with other blobs
			} else {
				c.logger.DebugContext(c.ctx, "deleted blob", "namespace", namespace, "digest", dgst.String())
			}
		}
	}

	return nil
}
