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

	// Set namespace to realm namespace
	r.ctrClient.SetNamespace(internalRealm.Spec.Namespace)

	cellSpaceName := internalCell.Spec.SpaceName
	cellStackName := internalCell.Spec.StackName
	cellID := internalCell.Spec.ID
	if strings.TrimSpace(cellID) == "" {
		cellID = internalCell.Metadata.Name
	}

	// Get space for network name
	lookupSpace := intmodel.Space{
		Metadata: intmodel.SpaceMetadata{
			Name: cellSpaceName,
		},
		Spec: intmodel.SpaceSpec{
			RealmName: internalCell.Spec.RealmName,
		},
	}
	space, spaceErr := r.GetSpace(lookupSpace)
	var networkName string
	if spaceErr == nil {
		networkName, _ = r.getSpaceNetworkName(space)
	}

	// Delete all containers in the cell (workload + root)
	for _, containerSpec := range internalCell.Spec.Containers {
		containerSpaceName := containerSpec.SpaceName
		if strings.TrimSpace(containerSpaceName) == "" {
			containerSpaceName = cellSpaceName
		}
		containerStackName := containerSpec.StackName
		if strings.TrimSpace(containerStackName) == "" {
			containerStackName = cellStackName
		}
		containerCellName := containerSpec.CellName
		if strings.TrimSpace(containerCellName) == "" {
			containerCellName = cellID
		}

		// Use ContainerdID from spec
		containerID := containerSpec.ContainerdID
		if containerID == "" {
			r.logger.WarnContext(
				r.ctx,
				"container has empty ContainerdID, skipping",
				"container",
				containerSpec.ID,
			)
			continue
		}

		// Get netns path and purge CNI before stopping/deleting
		netnsPath, _ := r.getContainerNetnsPath(containerID)
		_ = r.purgeCNIForContainer(containerID, netnsPath, networkName)

		// Use container name with UUID for containerd operations
		// Stop and delete the container
		_, err = r.ctrClient.StopContainer(containerID, ctr.StopContainerOptions{})
		if err != nil {
			// Check if container/task doesn't exist - this is idempotent (already stopped/deleted)
			if errors.Is(err, errdefs.ErrContainerNotFound) || errors.Is(err, errdefs.ErrTaskNotFound) {
				r.logger.DebugContext(
					r.ctx,
					"container or task already stopped/deleted, continuing",
					"container",
					containerID,
				)
			} else {
				r.logger.WarnContext(
					r.ctx,
					"failed to stop container, continuing with deletion",
					"container",
					containerID,
					"error",
					err,
				)
			}
		}

		err = r.ctrClient.DeleteContainer(containerID, ctr.ContainerDeleteOptions{
			SnapshotCleanup: true,
		})
		if err != nil {
			// Check if container doesn't exist - this is idempotent (already deleted)
			if errors.Is(err, errdefs.ErrContainerNotFound) {
				r.logger.DebugContext(r.ctx, "container already deleted", "container", containerID)
				// Continue with other containers
			} else {
				r.logger.WarnContext(r.ctx, "failed to delete container", "container", containerID, "error", err)
				// Continue with other containers
			}
		}
	}

	// Delete root container
	rootContainerID, err := naming.BuildRootContainerdID(cellSpaceName, cellStackName, cellID)
	if err != nil {
		return fmt.Errorf("failed to build root container containerd ID: %w", err)
	}

	// Comprehensive CNI cleanup for root container before stopping/deleting
	netnsPath, _ := r.getContainerNetnsPath(rootContainerID)
	_ = r.purgeCNIForContainer(rootContainerID, netnsPath, networkName)

	_, err = r.ctrClient.StopContainer(rootContainerID, ctr.StopContainerOptions{Force: true})
	if err != nil {
		// Check if container/task doesn't exist - this is idempotent (already stopped/deleted)
		if errors.Is(err, errdefs.ErrContainerNotFound) || errors.Is(err, errdefs.ErrTaskNotFound) {
			r.logger.DebugContext(
				r.ctx,
				"root container or task already stopped/deleted, continuing",
				"container",
				rootContainerID,
			)
		} else {
			r.logger.WarnContext(
				r.ctx,
				"failed to stop root container, continuing with deletion",
				"container",
				rootContainerID,
				"error",
				err,
			)
		}
	}

	err = r.ctrClient.DeleteContainer(rootContainerID, ctr.ContainerDeleteOptions{
		SnapshotCleanup: true,
	})
	if err != nil {
		// Check if container doesn't exist - this is idempotent (already deleted)
		if errors.Is(err, errdefs.ErrContainerNotFound) {
			r.logger.DebugContext(r.ctx, "root container already deleted", "container", rootContainerID)
			// Continue with cgroup and metadata deletion
		} else {
			r.logger.WarnContext(r.ctx, "failed to delete root container", "container", rootContainerID, "error", err)
			// Continue with cgroup and metadata deletion
		}
	}

	// Always run comprehensive CNI cleanup after root container deletion as a safety net
	// This ensures IPAM allocations are cleaned up even if earlier cleanup succeeded or failed
	// Clear netns path since container is now deleted
	netnsPath = ""
	if networkName != "" {
		_ = r.purgeCNIForContainer(rootContainerID, netnsPath, networkName)
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
