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

	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/cgroups"
	"github.com/eminwux/kukeon/internal/util/fs"
	"github.com/eminwux/kukeon/internal/util/naming"
)

// PurgeCell performs comprehensive cleanup of a cell, including CNI resources and orphaned containers.
func (r *Exec) PurgeCell(cell intmodel.Cell) error {
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

	// First, perform standard delete
	if err := r.DeleteCell(cell); err != nil {
		// Log but continue with purge even if delete fails
		r.logger.WarnContext(r.ctx, "delete failed, continuing with purge", "error", err)
	}

	// Get cell to access containers and metadata
	internalCell, err := r.GetCell(cell)
	if err != nil && !errors.Is(err, errdefs.ErrCellNotFound) {
		return fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
	}

	// Use internalCell if available, otherwise use provided cell as fallback
	cellForOps := internalCell
	cellNotFound := errors.Is(err, errdefs.ErrCellNotFound)
	if cellNotFound {
		cellForOps = cell
	}

	// Initialize ctr client if needed
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err = r.ctrClient.Connect(); err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	// Get realm to access namespace
	lookupRealm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: cellForOps.Spec.RealmName,
		},
	}
	internalRealm, err := r.GetRealm(lookupRealm)
	if err != nil {
		r.logger.WarnContext(r.ctx, "failed to get realm for purge", "error", err)
		return nil // Continue anyway
	}

	// Set namespace
	r.logger.DebugContext(r.ctx, "setting namespace for cell purge", "namespace", internalRealm.Spec.Namespace)
	r.ctrClient.SetNamespace(internalRealm.Spec.Namespace)

	// Get space for network name
	lookupSpace := intmodel.Space{
		Metadata: intmodel.SpaceMetadata{
			Name: cellForOps.Spec.SpaceName,
		},
		Spec: intmodel.SpaceSpec{
			RealmName: cellForOps.Spec.RealmName,
		},
	}
	space, err := r.GetSpace(lookupSpace)
	if err != nil {
		r.logger.WarnContext(r.ctx, "failed to get space for purge", "error", err)
	} else {
		var networkName string
		networkName, err = r.getSpaceNetworkName(space)
		if err != nil {
			r.logger.WarnContext(r.ctx, "failed to get network name", "error", err)
		} else {
			// Build container names from the cell using the same naming scheme as creation
			// This ensures we match the actual container names (using underscores, not hyphens)
			var containerIDs []string

			// Get names from cell spec, with fallbacks
			cellSpaceName := cellForOps.Spec.SpaceName
			cellStackName := cellForOps.Spec.StackName
			cellID := cellForOps.Spec.ID
			if cellID == "" {
				cellID = cellForOps.Metadata.Name
			}

			// Add root container
			var rootContainerID string
			rootContainerID, err = naming.BuildRootContainerdID(cellSpaceName, cellStackName, cellID)
			if err != nil {
				r.logger.WarnContext(r.ctx, "failed to build root container containerd ID", "error", err)
			} else {
				containerIDs = append(containerIDs, rootContainerID)
			}

			// Add all containers from the cell spec
			for _, containerSpec := range cellForOps.Spec.Containers {
				// Use container spec names if available, otherwise fall back to cell spec names
				containerSpaceName := containerSpec.SpaceName
				if containerSpaceName == "" {
					containerSpaceName = cellSpaceName
				}
				containerStackName := containerSpec.StackName
				if containerStackName == "" {
					containerStackName = cellStackName
				}
				containerCellName := containerSpec.CellName
				if containerCellName == "" {
					containerCellName = cellID
				}
				containerName := containerSpec.ID
				if containerName == "" {
					r.logger.WarnContext(r.ctx, "container spec has empty ID, skipping", "index", len(containerIDs))
					continue
				}

				// Use ContainerdID from spec
				containerID := containerSpec.ContainerdID
				if containerID == "" {
					r.logger.WarnContext(r.ctx, "container has empty ContainerdID, skipping", "container", containerName)
					continue
				}
				containerIDs = append(containerIDs, containerID)
			}

			r.logger.DebugContext(r.ctx, "built container IDs from cell", "count", len(containerIDs))
			if len(containerIDs) > 0 {
				r.logger.InfoContext(r.ctx, "purging CNI resources for cell containers", "count", len(containerIDs))
				ctrCtx := context.Background()
				for i, containerID := range containerIDs {
					r.logger.DebugContext(r.ctx, "processing container for CNI purge", "index", i+1, "total", len(containerIDs), "id", containerID)
					// Try to get netns path
					r.logger.DebugContext(r.ctx, "getting container netns path", "id", containerID)
					netnsPath, _ := r.getContainerNetnsPath(ctrCtx, containerID)
					// Purge CNI resources
					r.logger.DebugContext(r.ctx, "purging CNI resources for container", "id", containerID, "network", networkName)
					_ = r.purgeCNIForContainer(ctrCtx, containerID, netnsPath, networkName)
				}
				r.logger.InfoContext(r.ctx, "completed purging CNI resources for cell containers", "count", len(containerIDs))
			}
		}
	}

	// Force remove cell cgroup if it still exists
	spec := cgroups.DefaultCellSpec(cellForOps)
	mountpoint := r.ctrClient.GetCgroupMountpoint()
	_ = r.ctrClient.DeleteCgroup(spec.Group, mountpoint)

	// Remove metadata directory completely
	metadataRunPath := fs.CellMetadataDir(
		r.opts.RunPath,
		cellForOps.Spec.RealmName,
		cellForOps.Spec.SpaceName,
		cellForOps.Spec.StackName,
		cellForOps.Metadata.Name,
	)
	_ = os.RemoveAll(metadataRunPath)

	return nil
}
