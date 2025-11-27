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
	lookupCellForDelete := intmodel.Cell{
		Metadata: intmodel.CellMetadata{
			Name: cellName,
		},
		Spec: intmodel.CellSpec{
			RealmName: realmName,
			SpaceName: spaceName,
			StackName: stackName,
		},
	}
	if err := r.DeleteCell(lookupCellForDelete); err != nil {
		// Log but continue with purge even if delete fails
		r.logger.WarnContext(r.ctx, "delete failed, continuing with purge", "error", err)
	}

	// Get cell to access containers and metadata
	lookupCell := intmodel.Cell{
		Metadata: intmodel.CellMetadata{
			Name: cellName,
		},
		Spec: intmodel.CellSpec{
			RealmName: realmName,
			SpaceName: spaceName,
			StackName: stackName,
		},
	}
	internalCell, err := r.GetCell(lookupCell)
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
			rootContainerID, err = naming.BuildRootContainerName(cellSpaceName, cellStackName, cellID)
			if err != nil {
				r.logger.WarnContext(r.ctx, "failed to build root container name", "error", err)
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

				var containerID string
				containerID, err = naming.BuildContainerName(containerSpaceName, containerStackName, containerCellName, containerName)
				if err != nil {
					r.logger.WarnContext(r.ctx, "failed to build container name", "container", containerName, "error", err)
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

// PurgeSpace performs comprehensive cleanup of a space, including CNI resources and orphaned containers.
func (r *Exec) PurgeSpace(space intmodel.Space) error {
	spaceName := strings.TrimSpace(space.Metadata.Name)
	if spaceName == "" {
		return errdefs.ErrSpaceNameRequired
	}
	realmName := strings.TrimSpace(space.Spec.RealmName)
	if realmName == "" {
		return errdefs.ErrRealmNameRequired
	}

	// First, perform standard delete
	lookupSpaceForDelete := intmodel.Space{
		Metadata: intmodel.SpaceMetadata{
			Name: spaceName,
		},
		Spec: intmodel.SpaceSpec{
			RealmName: realmName,
		},
	}
	if err := r.DeleteSpace(lookupSpaceForDelete); err != nil {
		r.logger.WarnContext(r.ctx, "delete failed, continuing with purge", "error", err)
	}

	// Get space via internal model to ensure metadata accuracy
	lookupSpace := intmodel.Space{
		Metadata: intmodel.SpaceMetadata{
			Name: spaceName,
		},
		Spec: intmodel.SpaceSpec{
			RealmName: realmName,
		},
	}
	internalSpace, err := r.GetSpace(lookupSpace)
	if err != nil && !errors.Is(err, errdefs.ErrSpaceNotFound) {
		return fmt.Errorf("%w: %w", errdefs.ErrGetSpace, err)
	}

	// Use internalSpace if available, otherwise use provided space as fallback
	spaceForOps := internalSpace
	spaceNotFound := errors.Is(err, errdefs.ErrSpaceNotFound)
	if spaceNotFound {
		spaceForOps = space
	}

	// Get realm
	lookupRealm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: spaceForOps.Spec.RealmName,
		},
	}
	internalRealm, err := r.GetRealm(lookupRealm)
	if err != nil {
		r.logger.WarnContext(r.ctx, "failed to get realm for purge", "error", err)
		return nil
	}

	// Get network name
	var networkName string
	if !spaceNotFound {
		networkName, err = r.getSpaceNetworkName(internalSpace)
		if err == nil {
			// Purge entire network
			_ = r.purgeCNIForNetwork(r.ctx, networkName)
		}
	}

	// Find all containers in space
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err = r.ctrClient.Connect(); err == nil {
		defer r.ctrClient.Close()
		r.ctrClient.SetNamespace(internalRealm.Spec.Namespace)

		pattern := fmt.Sprintf("%s-%s", spaceForOps.Spec.RealmName, spaceForOps.Metadata.Name)
		var containers []string
		containers, err = r.findContainersByPattern(r.ctx, internalRealm.Spec.Namespace, pattern)
		if err == nil {
			ctrCtx := context.Background()
			for _, containerID := range containers {
				netnsPath, _ := r.getContainerNetnsPath(ctrCtx, containerID)
				_ = r.purgeCNIForContainer(ctrCtx, containerID, netnsPath, networkName)
			}
		}
	}

	// Force remove space cgroup
	spec := cgroups.DefaultSpaceSpec(spaceForOps)
	if r.ctrClient != nil {
		mountpoint := r.ctrClient.GetCgroupMountpoint()
		_ = r.ctrClient.DeleteCgroup(spec.Group, mountpoint)
	}

	// Remove metadata directory completely
	metadataRunPath := fs.SpaceMetadataDir(
		r.opts.RunPath,
		spaceForOps.Spec.RealmName,
		spaceForOps.Metadata.Name,
	)
	_ = os.RemoveAll(metadataRunPath)

	return nil
}

// PurgeStack performs comprehensive cleanup of a stack, including CNI resources and orphaned containers.
func (r *Exec) PurgeStack(stack intmodel.Stack) error {
	stackName := strings.TrimSpace(stack.Metadata.Name)
	if stackName == "" {
		return errdefs.ErrStackNameRequired
	}
	realmName := strings.TrimSpace(stack.Spec.RealmName)
	if realmName == "" {
		return errdefs.ErrRealmNameRequired
	}
	spaceName := strings.TrimSpace(stack.Spec.SpaceName)
	if spaceName == "" {
		return errdefs.ErrSpaceNameRequired
	}

	// First, perform standard delete
	lookupStackForDelete := intmodel.Stack{
		Metadata: intmodel.StackMetadata{
			Name: stackName,
		},
		Spec: intmodel.StackSpec{
			RealmName: realmName,
			SpaceName: spaceName,
		},
	}
	if err := r.DeleteStack(lookupStackForDelete); err != nil {
		r.logger.WarnContext(r.ctx, "delete failed, continuing with purge", "error", err)
	}

	// Get stack document via internal model to ensure metadata accuracy
	lookupStack := intmodel.Stack{
		Metadata: intmodel.StackMetadata{
			Name: stackName,
		},
		Spec: intmodel.StackSpec{
			RealmName: realmName,
			SpaceName: spaceName,
		},
	}
	internalStack, getStackErr := r.GetStack(lookupStack)
	stackForOps := stack
	if getStackErr == nil {
		stackForOps = internalStack
	} else if !errors.Is(getStackErr, errdefs.ErrStackNotFound) {
		return fmt.Errorf("%w: %w", errdefs.ErrGetStack, getStackErr)
	}

	// Get realm and space
	lookupRealmForStack := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: stackForOps.Spec.RealmName,
		},
	}
	internalRealmForStack, err := r.GetRealm(lookupRealmForStack)
	if err != nil {
		r.logger.WarnContext(r.ctx, "failed to get realm for purge", "error", err)
		return nil
	}
	realmNamespace := internalRealmForStack.Spec.Namespace

	lookupSpace := intmodel.Space{
		Metadata: intmodel.SpaceMetadata{
			Name: stackForOps.Spec.SpaceName,
		},
		Spec: intmodel.SpaceSpec{
			RealmName: stackForOps.Spec.RealmName,
		},
	}
	internalSpace, err := r.GetSpace(lookupSpace)
	if err != nil {
		r.logger.WarnContext(r.ctx, "failed to get space for purge", "error", err)
		return nil
	}
	networkName, _ := r.getSpaceNetworkName(internalSpace)

	// Find all containers in stack
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err = r.ctrClient.Connect(); err == nil {
		defer r.ctrClient.Close()
		r.ctrClient.SetNamespace(realmNamespace)

		pattern := fmt.Sprintf(
			"%s-%s-%s",
			stackForOps.Spec.RealmName,
			stackForOps.Spec.SpaceName,
			stackForOps.Metadata.Name,
		)
		var containers []string
		containers, err = r.findContainersByPattern(r.ctx, realmNamespace, pattern)
		if err == nil {
			ctrCtx := context.Background()
			for _, containerID := range containers {
				netnsPath, _ := r.getContainerNetnsPath(ctrCtx, containerID)
				_ = r.purgeCNIForContainer(ctrCtx, containerID, netnsPath, networkName)
			}
		}
	}

	// Force remove stack cgroup
	spec := cgroups.DefaultStackSpec(stackForOps)
	if r.ctrClient != nil {
		mountpoint := r.ctrClient.GetCgroupMountpoint()
		_ = r.ctrClient.DeleteCgroup(spec.Group, mountpoint)
	}

	// Remove metadata directory completely
	metadataRunPath := fs.StackMetadataDir(
		r.opts.RunPath,
		stackForOps.Spec.RealmName,
		stackForOps.Spec.SpaceName,
		stackForOps.Metadata.Name,
	)
	_ = os.RemoveAll(metadataRunPath)

	return nil
}

// PurgeRealm performs comprehensive cleanup of a realm, including all child resources, CNI resources, and orphaned containers.
func (r *Exec) PurgeRealm(realm intmodel.Realm) error {
	realmName := strings.TrimSpace(realm.Metadata.Name)
	if realmName == "" {
		return errdefs.ErrRealmNameRequired
	}

	// First, perform standard delete
	lookupRealmForDelete := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: realmName,
		},
	}
	if _, err := r.DeleteRealm(lookupRealmForDelete); err != nil {
		r.logger.WarnContext(r.ctx, "delete failed, continuing with purge", "error", err)
	}

	// Get realm via internal model to ensure metadata accuracy
	lookupRealm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: realmName,
		},
	}
	internalRealm, err := r.GetRealm(lookupRealm)
	if err != nil && !errors.Is(err, errdefs.ErrRealmNotFound) {
		return fmt.Errorf("%w: %w", errdefs.ErrGetRealm, err)
	}

	// Use internalRealm if available, otherwise use provided realm as fallback
	realmForOps := internalRealm
	realmNotFound := errors.Is(err, errdefs.ErrRealmNotFound)
	if realmNotFound {
		realmForOps = realm
	}

	// Initialize ctr client
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
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

	// Remove all metadata directories for realm and children
	metadataRunPath := fs.RealmMetadataDir(r.opts.RunPath, realmForOps.Metadata.Name)
	_ = os.RemoveAll(metadataRunPath)

	// Force remove realm cgroup
	spec := cgroups.DefaultRealmSpec(realmForOps)
	mountpoint := r.ctrClient.GetCgroupMountpoint()
	_ = r.ctrClient.DeleteCgroup(spec.Group, mountpoint)

	return nil
}

// PurgeContainer performs comprehensive cleanup of a container, including CNI resources.
func (r *Exec) PurgeContainer(realm intmodel.Realm, containerID string) error {
	containerID = strings.TrimSpace(containerID)
	if containerID == "" {
		return errdefs.ErrContainerNameRequired
	}

	realmName := strings.TrimSpace(realm.Metadata.Name)
	if realmName == "" {
		return errdefs.ErrRealmNameRequired
	}

	// Get realm to access namespace
	lookupRealm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: realmName,
		},
	}
	internalRealm, err := r.GetRealm(lookupRealm)
	if err != nil {
		return fmt.Errorf("failed to get realm: %w", err)
	}

	namespace := internalRealm.Spec.Namespace
	if namespace == "" {
		return fmt.Errorf("realm %q has no namespace", realmName)
	}

	// Initialize ctr client
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err = r.ctrClient.Connect(); err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	r.ctrClient.SetNamespace(namespace)

	ctrCtx := context.Background()

	// Try to stop and delete container
	_, _ = r.ctrClient.StopContainer(ctrCtx, containerID, ctr.StopContainerOptions{})
	_ = r.ctrClient.DeleteContainer(ctrCtx, containerID, ctr.ContainerDeleteOptions{
		SnapshotCleanup: true,
	})

	// Get netns path if container is running
	netnsPath, _ := r.getContainerNetnsPath(ctrCtx, containerID)

	// Try to determine network name from container ID
	parts := strings.Split(containerID, "-")
	networkName := ""
	if len(parts) >= 2 {
		networkName = fmt.Sprintf("%s-%s", parts[0], parts[1])
	}

	// Purge CNI resources
	_ = r.purgeCNIForContainer(ctrCtx, containerID, netnsPath, networkName)

	return nil
}
