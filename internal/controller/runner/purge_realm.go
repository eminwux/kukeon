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
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
)

// PurgeRealm performs comprehensive cleanup of a realm, including all child
// resources, CNI resources, and orphaned containers.
//
// Returns (namespaceRemoved, err). namespaceRemoved is true iff the
// containerd namespace was actually removed (or was already gone). err is
// non-nil only for fatal precondition failures (missing name, GetRealm error,
// containerd connect error) or when DeleteNamespace itself failed — it is the
// load-bearing piece of "purge". Best-effort cleanups (cgroup removal, CNI
// teardown, orphaned-container drain) log warnings and do not surface as err
// so a fully-cleaned namespace is never reported as a failed purge.
func (r *Exec) PurgeRealm(realm intmodel.Realm) (bool, error) {
	realmName := strings.TrimSpace(realm.Metadata.Name)
	if realmName == "" {
		return false, errdefs.ErrRealmNameRequired
	}

	// Get realm via internal model to ensure metadata accuracy (if available)
	// Note: DeleteRealm is handled at the controller level, this function focuses on comprehensive cleanup
	internalRealm, err := r.GetRealm(realm)
	if err != nil && !errors.Is(err, errdefs.ErrRealmNotFound) {
		return false, fmt.Errorf("%w: %w", errdefs.ErrGetRealm, err)
	}

	// Use internalRealm if available, otherwise use provided realm as fallback
	realmForOps := internalRealm
	realmNotFound := errors.Is(err, errdefs.ErrRealmNotFound)
	if realmNotFound {
		realmForOps = realm
	}

	if err = r.ensureClientConnected(); err != nil {
		return false, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
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

	// Tear down each conflist + bridge link for this realm. Driven from
	// CniConfigDir because that is the source of truth for which bridges
	// were created — IPAM dirs under CNINetworksDir can be missing if a
	// space's containers never started.
	r.teardownRealmCNI(realmForOps.Metadata.Name)

	// Purge IPAM/cache state for any leftover networks the realm owned.
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
	// Pass "" to walk every known snapshotter (overlayfs/native/btrfs/zfs/...);
	// uninstall on a host that experimented with a non-default snapshotter
	// must not strand its leftover snapshots and surface "namespace not empty".
	if err = r.ctrClient.CleanupNamespaceResources(realmForOps.Spec.Namespace, ""); err != nil {
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

	// Delete containerd namespace as part of comprehensive cleanup. This is
	// the load-bearing step of a purge — surface its failure to the caller
	// so a leftover namespace renders as "FAILED" in the uninstall report
	// instead of a bogus "purged".
	namespaceRemoved := true
	var nsErr error
	if err = r.ctrClient.DeleteNamespace(realmForOps.Spec.Namespace); err != nil {
		namespaceRemoved = false
		// Re-list containers in the namespace so the error message names the
		// specific resources that survived the drain. The generic precondition
		// "must be empty" message from containerd hides which container is
		// pinning it; without the IDs an operator cannot tell whether the
		// orphan-drain misfired (issue #195's symptom) or a different actor
		// (image, snapshot, lease) is keeping the namespace alive.
		residual, listErr := r.findOrphanedContainers(realmForOps.Spec.Namespace, "")
		switch {
		case listErr != nil:
			nsErr = fmt.Errorf(
				"failed to delete containerd namespace %q (re-list of survivors failed: %w): %w",
				realmForOps.Spec.Namespace, listErr, err,
			)
		case len(residual) > 0:
			nsErr = fmt.Errorf(
				"failed to delete containerd namespace %q (residual containers: %s): %w",
				realmForOps.Spec.Namespace, strings.Join(residual, ", "), err,
			)
		default:
			nsErr = fmt.Errorf("failed to delete containerd namespace %q: %w", realmForOps.Spec.Namespace, err)
		}
		r.logger.WarnContext(
			r.ctx,
			"failed to delete containerd namespace",
			"namespace",
			realmForOps.Spec.Namespace,
			"residual_containers",
			residual,
			"error",
			err,
		)
		// Continue with other cleanup so a single failure does not strand
		// metadata/cgroup state on disk.
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
		spec := ctr.DefaultRealmSpec(realmForOps)
		cgroupGroup = spec.Group
	}
	// DeleteCgroup is now idempotent - it will return nil if cgroup doesn't exist
	if err = r.ctrClient.DeleteCgroup(cgroupGroup, mountpoint); err != nil {
		r.logger.WarnContext(r.ctx, "failed to delete realm cgroup during purge", "cgroup", cgroupGroup, "error", err)
		// Continue with cleanup even if cgroup deletion fails
	}

	return namespaceRemoved, nsErr
}
