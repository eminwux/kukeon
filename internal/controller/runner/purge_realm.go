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
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/eminwux/kukeon/internal/cni"
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

	if err = r.ensureClientConnected(); err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}

	// List ALL containers in namespace (even orphaned ones)
	r.logger.DebugContext(r.ctx, "starting to find orphaned containers", "namespace", realmForOps.Spec.Namespace)
	containers, err := r.findOrphanedContainers(realmForOps.Spec.Namespace, "")
	if err != nil {
		r.logger.WarnContext(r.ctx, "failed to find orphaned containers", "error", err)
	} else {
		r.logger.DebugContext(r.ctx, "found orphaned containers", "count", len(containers))
		r.processOrphanedContainers(r.ctx, containers)
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
				_ = r.purgeCNIForNetwork(networkName)
			}
		}
	}

	// Clean up all namespace resources (images, snapshots) before deleting namespace
	// Namespace must be empty before deletion
	if err = r.ctrClient.CleanupNamespaceResources(r.ctx, realmForOps.Spec.Namespace, "overlayfs"); err != nil {
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
	// Try to use stored CgroupPath first, fallback to DefaultRealmSpec if not available
	mountpoint := r.ctrClient.GetCgroupMountpoint()
	cgroupGroup := realmForOps.Status.CgroupPath
	if cgroupGroup == "" {
		// Fallback to DefaultRealmSpec if CgroupPath is not set
		spec := cgroups.DefaultRealmSpec(realmForOps)
		cgroupGroup = spec.Group
	}
	// DeleteCgroup is now idempotent - it will return nil if cgroup doesn't exist
	if err = r.ctrClient.DeleteCgroup(cgroupGroup, mountpoint); err != nil {
		r.logger.WarnContext(r.ctx, "failed to delete realm cgroup during purge", "cgroup", cgroupGroup, "error", err)
		// Continue with cleanup even if cgroup deletion fails
	}

	return nil
}
