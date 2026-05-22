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

	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/metadata"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
	"github.com/eminwux/kukeon/internal/util/naming"
)

func (r *Exec) DeleteCell(cell intmodel.Cell) error {
	cellName := strings.TrimSpace(cell.Metadata.Name)
	if cellName == "" {
		return errdefs.ErrCellNameRequired
	}
	realmName := strings.TrimSpace(cell.Spec.RealmName)
	if realmName == "" {
		return errdefs.ErrRealmNameRequired
	}
	spaceName := strings.TrimSpace(cell.Spec.SpaceName)
	if spaceName == "" {
		return errdefs.ErrSpaceNameRequired
	}
	stackName := strings.TrimSpace(cell.Spec.StackName)
	if stackName == "" {
		return errdefs.ErrStackNameRequired
	}

	// Get the cell document to access all containers
	internalCell, err := r.GetCell(cell)
	if err != nil {
		if errors.Is(err, errdefs.ErrCellNotFound) {
			// Idempotent: cell doesn't exist, consider it deleted
			return nil
		}
		return fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
	}

	if err = r.ensureClientConnected(); err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}

	// Get realm to access namespace
	lookupRealm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: internalCell.Spec.RealmName,
		},
	}
	internalRealm, err := r.GetRealm(lookupRealm)
	if err != nil {
		return fmt.Errorf("failed to get realm: %w", err)
	}
	namespace := internalRealm.Spec.Namespace

	cellSpaceName := internalCell.Spec.SpaceName
	cellStackName := internalCell.Spec.StackName
	cellID := internalCell.Spec.ID
	if strings.TrimSpace(cellID) == "" {
		cellID = internalCell.Metadata.Name
	}

	// Resolve the network name deterministically from realm+space so the
	// post-delete CNI/IPAM purge below runs even when space metadata is gone or
	// corrupt (issue #685). The name is a pure function of (realm, space).
	networkName := r.buildRootCNINetworkName(internalCell.Spec.RealmName, cellSpaceName)

	// Aggregate container-deletion failures rather than swallow them. Issue
	// #371: a single warn-and-continue on a non-NotFound DeleteContainer
	// error used to slip past the metadata-removal step, leaving an orphan
	// containerd container behind while the controller declared the cell
	// gone. The next `kuke init` then collided on the surviving ID.
	var deleteErrors []error

	// Workload containers tracked on the cell spec.
	for _, containerSpec := range internalCell.Spec.Containers {
		// Unlink the per-container SUN_PATH-safe socket symlink staged at
		// provision time (issue #521) before tearing down the container.
		// The symlink lives at <RunPath>/s/<short>, outside the
		// CellMetadataDir tree that the RemoveAll below would otherwise
		// reach — leaving it behind would accumulate stale symlinks
		// pointing at vanished bind-mount sources across re-creates. The
		// helper short-circuits on non-Attachable specs.
		if symlinkErr := removeAttachableSocketSymlink(r.opts.RunPath, containerSpec); symlinkErr != nil {
			r.logger.WarnContext(
				r.ctx,
				"failed to remove socket symlink",
				"container", containerSpec.ID,
				"error", symlinkErr,
			)
		}

		containerID := containerSpec.ContainerdID
		if containerID == "" {
			// A partial init can persist the cell document before the
			// runner has filled in ContainerdID. Don't silently skip —
			// the name-based scan below will catch any container that
			// was already created in containerd.
			r.logger.WarnContext(
				r.ctx,
				"container has empty ContainerdID; relying on name-based orphan scan",
				"container", containerSpec.ID,
			)
			continue
		}
		if delErr := r.stopAndDeleteContainer(namespace, containerID, networkName, false); delErr != nil {
			deleteErrors = append(deleteErrors,
				fmt.Errorf("delete container %q: %w", containerID, delErr))
		}
	}

	// Root container.
	rootContainerID, err := naming.BuildRootContainerdID(cellSpaceName, cellStackName, cellID)
	if err != nil {
		return fmt.Errorf("failed to build root container containerd ID: %w", err)
	}
	if delErr := r.stopAndDeleteContainer(namespace, rootContainerID, networkName, true); delErr != nil {
		deleteErrors = append(deleteErrors,
			fmt.Errorf("delete root container %q: %w", rootContainerID, delErr))
	}

	// Belt-and-suspenders: enumerate every containerd container in the realm
	// namespace whose ID matches `<space>_<stack>_<cell>_*` and tear it down
	// too. Catches the partial-init case where the cell spec was persisted
	// with an empty ContainerdID, and the rare race where a non-NotFound
	// DeleteContainer error above leaves a survivor we then mop up here.
	namePrefix := fmt.Sprintf("%s_%s_%s_", cellSpaceName, cellStackName, cellID)
	orphans, scanErr := r.findOrphanContainerIDs(namespace, namePrefix)
	if scanErr != nil {
		deleteErrors = append(deleteErrors,
			fmt.Errorf("scan orphan containers with prefix %q: %w", namePrefix, scanErr))
	}
	for _, orphanID := range orphans {
		r.logger.InfoContext(
			r.ctx,
			"reconciling orphan container left by partial init or prior failed delete",
			"container", orphanID,
			"namespace", namespace,
		)
		if delErr := r.stopAndDeleteContainer(namespace, orphanID, networkName, true); delErr != nil {
			deleteErrors = append(deleteErrors,
				fmt.Errorf("delete orphan container %q: %w", orphanID, delErr))
		}
	}

	// Always run comprehensive CNI cleanup after root container deletion as a safety net.
	// This ensures IPAM allocations are cleaned up even if earlier cleanup succeeded or failed.
	if networkName != "" {
		_ = r.purgeCNIForContainer(rootContainerID, "", networkName)
	}

	// If any container-tier failure remains, refuse to remove the cell
	// cgroup or metadata: leaving the metadata file in place is what makes
	// the next `kuke daemon reset` find the cell document and retry the
	// teardown instead of declaring success on a still-broken host.
	if len(deleteErrors) > 0 {
		return fmt.Errorf("%w: %w", errdefs.ErrDeleteCell, errors.Join(deleteErrors...))
	}

	// Delete cell cgroup
	// Use the stored CgroupPath from cell status (includes full hierarchy path)
	// instead of rebuilding from DefaultCellSpec which only has the relative path
	mountpoint := r.ctrClient.GetCgroupMountpoint()
	cgroupGroup := internalCell.Status.CgroupPath
	if cgroupGroup == "" {
		// Fallback to DefaultCellSpec if CgroupPath is not set (for backwards compatibility)
		spec := ctr.DefaultCellSpec(internalCell)
		cgroupGroup = spec.Group
	}
	err = r.ctrClient.DeleteCgroup(cgroupGroup, mountpoint)
	if err != nil {
		r.logger.WarnContext(r.ctx, "failed to delete cell cgroup", "cgroup", cgroupGroup, "error", err)
		// Continue with metadata deletion
	}

	// Delete cell metadata file
	metadataFilePath := fs.CellMetadataPath(
		r.opts.RunPath,
		internalCell.Spec.RealmName,
		cellSpaceName,
		cellStackName,
		internalCell.Metadata.Name,
	)
	if err = metadata.DeleteMetadata(r.ctx, r.logger, metadataFilePath); err != nil {
		return fmt.Errorf("%w: failed to delete cell metadata: %w", errdefs.ErrDeleteCell, err)
	}

	// Remove metadata directory completely
	metadataRunPath := fs.CellMetadataDir(
		r.opts.RunPath,
		internalCell.Spec.RealmName,
		cellSpaceName,
		cellStackName,
		internalCell.Metadata.Name,
	)
	_ = os.RemoveAll(metadataRunPath)

	return nil
}

// stopAndDeleteContainer purges CNI state, stops the task (best effort), then
// deletes the containerd container. Stop failures are logged but not surfaced
// — they don't gate delete. Delete is the gate: a non-NotFound error is
// returned so the caller can refuse to remove cell metadata while an orphan
// container still owns the containerd ID we'd re-derive on the next init.
//
// force selects SIGKILL escalation on the stop step (root containers and
// orphans use force=true; workload containers preserve the historical
// SIGTERM-only default).
func (r *Exec) stopAndDeleteContainer(namespace, containerID, networkName string, force bool) error {
	netnsPath, _ := r.getContainerNetnsPath(namespace, containerID)
	_ = r.purgeCNIForContainer(containerID, netnsPath, networkName)

	if _, stopErr := r.ctrClient.StopContainer(
		namespace, containerID, ctr.StopContainerOptions{Force: force},
	); stopErr != nil {
		if errors.Is(stopErr, errdefs.ErrContainerNotFound) ||
			errors.Is(stopErr, errdefs.ErrTaskNotFound) {
			r.logger.DebugContext(
				r.ctx,
				"container or task already stopped/deleted, continuing",
				"container", containerID,
			)
		} else {
			r.logger.WarnContext(
				r.ctx,
				"failed to stop container, continuing with deletion",
				"container", containerID,
				"error", stopErr,
			)
		}
	}

	delErr := r.ctrClient.DeleteContainer(namespace, containerID, ctr.ContainerDeleteOptions{
		SnapshotCleanup: true,
	})
	if delErr == nil {
		return nil
	}
	if errors.Is(delErr, errdefs.ErrContainerNotFound) {
		r.logger.DebugContext(r.ctx, "container already deleted", "container", containerID)
		return nil
	}
	return delErr
}

// findOrphanContainerIDs returns every container in namespace whose ID begins
// with namePrefix. The prefix `<space>_<stack>_<cell>_` matches both the
// `<...>_root` root container and every `<...>_<containerName>` workload
// container produced by naming.BuildContainerdID / BuildRootContainerdID, so
// a single scan covers both partial-init leftovers and prior-reset survivors.
func (r *Exec) findOrphanContainerIDs(namespace, namePrefix string) ([]string, error) {
	containers, err := r.ctrClient.ListContainers(namespace)
	if err != nil {
		return nil, err
	}
	var orphans []string
	for _, c := range containers {
		id := c.ID()
		if strings.HasPrefix(id, namePrefix) {
			orphans = append(orphans, id)
		}
	}
	return orphans, nil
}
