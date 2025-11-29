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

package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/cgroups"
	"github.com/eminwux/kukeon/internal/util/fs"
)

// PurgeRealm performs comprehensive cleanup of a realm, including all child resources, CNI resources, and orphaned containers.
func (r *Exec) PurgeRealm(realm intmodel.Realm) error {
	realmName := strings.TrimSpace(realm.Metadata.Name)
	if realmName == "" {
		return errdefs.ErrRealmNameRequired
	}

	// Get realm via internal model to ensure metadata accuracy (if available)
	// Note: DeleteRealm is handled at the controller level, this function focuses on comprehensive cleanup
	internalRealm, err := r.GetRealm(realm)
	if err != nil && !errors.Is(err, errdefs.ErrRealmNotFound) {
		return fmt.Errorf("%w: %w", errdefs.ErrGetRealm, err)
	}

	// Use internalRealm if available, otherwise use provided realm as fallback
	realmForOps := internalRealm
	realmNotFound := errors.Is(err, errdefs.ErrRealmNotFound)
	if realmNotFound {
		realmForOps = realm
	}

	// Initialize ctr client for comprehensive cleanup
	r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	if err = r.ctrClient.Connect(); err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	// List ALL containers in namespace (even orphaned ones)
	r.logger.DebugContext(r.ctx, "starting to find orphaned containers", "namespace", realmForOps.Spec.Namespace)
	containers, err := r.findOrphanedContainers(r.ctx, realmForOps.Spec.Namespace, "")
	if err != nil {
		r.logger.WarnContext(r.ctx, "failed to find orphaned containers", "error", err)
	} else {
		r.logger.DebugContext(r.ctx, "found orphaned containers", "count", len(containers))
		if len(containers) > 0 {
			r.logger.InfoContext(r.ctx, "processing orphaned containers for deletion", "count", len(containers))
			ctrCtx := context.Background()
			for i, containerID := range containers {
				r.logger.DebugContext(r.ctx, "processing container", "index", i+1, "total", len(containers), "id", containerID)
				// Try to delete container
				r.logger.DebugContext(r.ctx, "stopping container", "id", containerID)
				_, _ = r.ctrClient.StopContainer(ctrCtx, containerID, ctr.StopContainerOptions{})
				r.logger.DebugContext(r.ctx, "deleting container", "id", containerID)
				_ = r.ctrClient.DeleteContainer(ctrCtx, containerID, ctr.ContainerDeleteOptions{
					SnapshotCleanup: true,
				})

				// Get netns and purge CNI
				r.logger.DebugContext(r.ctx, "getting container netns path", "id", containerID)
				netnsPath, _ := r.getContainerNetnsPath(ctrCtx, containerID)
				// Try to determine network name from container ID pattern
				// Container ID format: realm-space-cell-container
				parts := strings.Split(containerID, "-")
				if len(parts) >= 2 {
					networkName := fmt.Sprintf("%s-%s", parts[0], parts[1])
					r.logger.DebugContext(r.ctx, "purging CNI resources for container", "id", containerID, "network", networkName)
					_ = r.purgeCNIForContainer(ctrCtx, containerID, netnsPath, networkName)
				}
				r.logger.DebugContext(r.ctx, "completed processing container", "id", containerID)
			}
			r.logger.InfoContext(r.ctx, "completed processing all orphaned containers", "count", len(containers))
		}
	}

	// Purge all CNI networks for this realm
	// Network names follow the pattern: {realmName}-{spaceName}
	// Scan /var/lib/cni/networks/ for all networks starting with realm name
	cniNetworksDir := cni.CNINetworksDir
	entries, err := os.ReadDir(cniNetworksDir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			r.logger.WarnContext(r.ctx, "failed to read CNI networks directory", "dir", cniNetworksDir, "error", err)
		}
	} else {
		realmPrefix := realmForOps.Metadata.Name + "-"
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			networkName := entry.Name()
			// Check if network name starts with realm name prefix
			if strings.HasPrefix(networkName, realmPrefix) {
				r.logger.DebugContext(r.ctx, "purging CNI network for realm", "realm", realmForOps.Metadata.Name, "network", networkName)
				_ = r.purgeCNIForNetwork(r.ctx, networkName)
			}
		}
	}

	// Clean up all namespace resources (images, snapshots) before deleting namespace
	// Namespace must be empty before deletion
	ctrCtx := context.Background()
	if err = r.ctrClient.CleanupNamespaceResources(ctrCtx, realmForOps.Spec.Namespace, "overlayfs"); err != nil {
		r.logger.WarnContext(
			r.ctx,
			"failed to cleanup namespace resources",
			"namespace",
			realmForOps.Spec.Namespace,
			"error",
			err,
		)
		// Continue with namespace deletion attempt anyway
	}

	// Delete containerd namespace as part of comprehensive cleanup
	if err = r.ctrClient.DeleteNamespace(realmForOps.Spec.Namespace); err != nil {
		r.logger.WarnContext(
			r.ctx,
			"failed to delete containerd namespace",
			"namespace",
			realmForOps.Spec.Namespace,
			"error",
			err,
		)
		// Continue with other cleanup
	}

	// Remove all metadata directories for realm and children
	metadataRunPath := fs.RealmMetadataDir(r.opts.RunPath, realmForOps.Metadata.Name)
	_ = os.RemoveAll(metadataRunPath)

	// Force remove realm cgroup
	spec := cgroups.DefaultRealmSpec(realmForOps)
	mountpoint := r.ctrClient.GetCgroupMountpoint()
	_ = r.ctrClient.DeleteCgroup(spec.Group, mountpoint)

	return nil
}
