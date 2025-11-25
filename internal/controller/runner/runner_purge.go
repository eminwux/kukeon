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

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/util/cgroups"
	"github.com/eminwux/kukeon/internal/util/fs"
	"github.com/eminwux/kukeon/internal/util/naming"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// PurgeCell performs comprehensive cleanup of a cell, including CNI resources and orphaned containers.
func (r *Exec) PurgeCell(doc *v1beta1.CellDoc) error {
	if doc == nil {
		return errdefs.ErrCellNotFound
	}

	// First, perform standard delete
	if err := r.DeleteCell(doc); err != nil {
		// Log but continue with purge even if delete fails
		r.logger.WarnContext(r.ctx, "delete failed, continuing with purge", "error", err)
	}

	// Get cell document to access containers and metadata
	cellDoc, err := r.GetCell(doc)
	if err != nil && !errors.Is(err, errdefs.ErrCellNotFound) {
		return fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
	}

	// If cell doesn't exist, still try to purge orphaned resources
	if errors.Is(err, errdefs.ErrCellNotFound) {
		cellDoc = doc // Use provided doc for pattern matching
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
	var realmDoc *v1beta1.RealmDoc
	realmDoc, err = r.GetRealm(&v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{
			Name: cellDoc.Spec.RealmID,
		},
	})
	if err != nil {
		r.logger.WarnContext(r.ctx, "failed to get realm for purge", "error", err)
		return nil // Continue anyway
	}

	// Set namespace
	r.logger.DebugContext(r.ctx, "setting namespace for cell purge", "namespace", realmDoc.Spec.Namespace)
	r.ctrClient.SetNamespace(realmDoc.Spec.Namespace)

	// Get space for network name
	var spaceDoc *v1beta1.SpaceDoc
	spaceDoc, err = r.GetSpace(&v1beta1.SpaceDoc{
		Metadata: v1beta1.SpaceMetadata{
			Name: cellDoc.Spec.SpaceID,
		},
		Spec: v1beta1.SpaceSpec{
			RealmID: cellDoc.Spec.RealmID,
		},
	})
	if err != nil {
		r.logger.WarnContext(r.ctx, "failed to get space for purge", "error", err)
	} else {
		// Convert to internal model
		space, convertErr := apischeme.ConvertSpaceDocToInternal(*spaceDoc)
		if convertErr != nil {
			r.logger.WarnContext(r.ctx, "failed to convert space to internal model", "error", convertErr)
		} else {
			var networkName string
			networkName, err = r.getSpaceNetworkName(space)
			if err != nil {
				r.logger.WarnContext(r.ctx, "failed to get network name", "error", err)
			} else {
				// Build container names from the cell document using the same naming scheme as creation
				// This ensures we match the actual container names (using underscores, not hyphens)
				var containerIDs []string

				// Get IDs from cell document, with fallbacks
				spaceID := cellDoc.Spec.SpaceID
				stackID := cellDoc.Spec.StackID
				cellID := cellDoc.Spec.ID
				if cellID == "" {
					cellID = cellDoc.Metadata.Name
				}

				// Add root container
				var rootContainerID string
				rootContainerID, err = naming.BuildRootContainerName(spaceID, stackID, cellID)
				if err != nil {
					r.logger.WarnContext(r.ctx, "failed to build root container name", "error", err)
				} else {
					containerIDs = append(containerIDs, rootContainerID)
				}

				// Add all containers from the cell spec
				for _, containerSpec := range cellDoc.Spec.Containers {
					// Use container spec IDs if available, otherwise fall back to cell doc IDs
					containerSpaceID := containerSpec.SpaceID
					if containerSpaceID == "" {
						containerSpaceID = spaceID
					}
					containerStackID := containerSpec.StackID
					if containerStackID == "" {
						containerStackID = stackID
					}
					containerCellID := containerSpec.CellID
					if containerCellID == "" {
						containerCellID = cellID
					}
					containerName := containerSpec.ID
					if containerName == "" {
						r.logger.WarnContext(r.ctx, "container spec has empty ID, skipping", "index", len(containerIDs))
						continue
					}

					var containerID string
					containerID, err = naming.BuildContainerName(containerSpaceID, containerStackID, containerCellID, containerName)
					if err != nil {
						r.logger.WarnContext(r.ctx, "failed to build container name", "container", containerName, "error", err)
						continue
					}
					containerIDs = append(containerIDs, containerID)
				}

				r.logger.DebugContext(r.ctx, "built container IDs from cell document", "count", len(containerIDs))
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
	}

	// Force remove cell cgroup if it still exists
	stackDoc, err := r.GetStack(&v1beta1.StackDoc{
		Metadata: v1beta1.StackMetadata{
			Name: cellDoc.Spec.StackID,
		},
		Spec: v1beta1.StackSpec{
			RealmID: cellDoc.Spec.RealmID,
			SpaceID: cellDoc.Spec.SpaceID,
		},
	})
	if err == nil && spaceDoc != nil {
		spec := cgroups.DefaultCellSpec(realmDoc, spaceDoc, stackDoc, cellDoc)
		mountpoint := r.ctrClient.GetCgroupMountpoint()
		_ = r.ctrClient.DeleteCgroup(spec.Group, mountpoint)
	}

	// Remove metadata directory completely
	metadataRunPath := fs.CellMetadataDir(
		r.opts.RunPath,
		cellDoc.Spec.RealmID,
		cellDoc.Spec.SpaceID,
		cellDoc.Spec.StackID,
		cellDoc.Metadata.Name,
	)
	_ = os.RemoveAll(metadataRunPath)

	return nil
}

// PurgeSpace performs comprehensive cleanup of a space, including CNI resources and orphaned containers.
func (r *Exec) PurgeSpace(doc *v1beta1.SpaceDoc) error {
	if doc == nil {
		return errdefs.ErrSpaceNotFound
	}

	// First, perform standard delete
	if err := r.DeleteSpace(doc); err != nil {
		r.logger.WarnContext(r.ctx, "delete failed, continuing with purge", "error", err)
	}

	// Get space document
	spaceDoc, err := r.GetSpace(doc)
	if err != nil && !errors.Is(err, errdefs.ErrSpaceNotFound) {
		return fmt.Errorf("%w: %w", errdefs.ErrGetSpace, err)
	}

	if errors.Is(err, errdefs.ErrSpaceNotFound) {
		spaceDoc = doc
	}

	// Get realm
	realmDoc, err := r.GetRealm(&v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{
			Name: spaceDoc.Spec.RealmID,
		},
	})
	if err != nil {
		r.logger.WarnContext(r.ctx, "failed to get realm for purge", "error", err)
		return nil
	}

	// Get network name
	var networkName string
	space, convertErr := apischeme.ConvertSpaceDocToInternal(*spaceDoc)
	if convertErr != nil {
		r.logger.WarnContext(r.ctx, "failed to convert space to internal model", "error", convertErr)
	} else {
		networkName, err = r.getSpaceNetworkName(space)
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
		r.ctrClient.SetNamespace(realmDoc.Spec.Namespace)

		pattern := fmt.Sprintf("%s-%s", spaceDoc.Spec.RealmID, spaceDoc.Metadata.Name)
		var containers []string
		containers, err = r.findContainersByPattern(r.ctx, realmDoc.Spec.Namespace, pattern)
		if err == nil {
			ctrCtx := context.Background()
			for _, containerID := range containers {
				netnsPath, _ := r.getContainerNetnsPath(ctrCtx, containerID)
				_ = r.purgeCNIForContainer(ctrCtx, containerID, netnsPath, networkName)
			}
		}
	}

	// Force remove space cgroup
	spec := cgroups.DefaultSpaceSpec(realmDoc, spaceDoc)
	if r.ctrClient != nil {
		mountpoint := r.ctrClient.GetCgroupMountpoint()
		_ = r.ctrClient.DeleteCgroup(spec.Group, mountpoint)
	}

	// Remove metadata directory completely
	metadataRunPath := fs.SpaceMetadataDir(
		r.opts.RunPath,
		spaceDoc.Spec.RealmID,
		spaceDoc.Metadata.Name,
	)
	_ = os.RemoveAll(metadataRunPath)

	return nil
}

// PurgeStack performs comprehensive cleanup of a stack, including CNI resources and orphaned containers.
func (r *Exec) PurgeStack(doc *v1beta1.StackDoc) error {
	if doc == nil {
		return errdefs.ErrStackNotFound
	}

	// First, perform standard delete
	if err := r.DeleteStack(doc); err != nil {
		r.logger.WarnContext(r.ctx, "delete failed, continuing with purge", "error", err)
	}

	// Get stack document
	stackDoc, err := r.GetStack(doc)
	if err != nil && !errors.Is(err, errdefs.ErrStackNotFound) {
		return fmt.Errorf("%w: %w", errdefs.ErrGetStack, err)
	}

	if errors.Is(err, errdefs.ErrStackNotFound) {
		stackDoc = doc
	}

	// Get realm and space
	realmDoc, err := r.GetRealm(&v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{
			Name: stackDoc.Spec.RealmID,
		},
	})
	if err != nil {
		r.logger.WarnContext(r.ctx, "failed to get realm for purge", "error", err)
		return nil
	}

	spaceDoc, err := r.GetSpace(&v1beta1.SpaceDoc{
		Metadata: v1beta1.SpaceMetadata{
			Name: stackDoc.Spec.SpaceID,
		},
		Spec: v1beta1.SpaceSpec{
			RealmID: stackDoc.Spec.RealmID,
		},
	})
	if err != nil {
		r.logger.WarnContext(r.ctx, "failed to get space for purge", "error", err)
		return nil
	}

	// Convert to internal model
	space, convertErr := apischeme.ConvertSpaceDocToInternal(*spaceDoc)
	if convertErr != nil {
		r.logger.WarnContext(r.ctx, "failed to convert space to internal model", "error", convertErr)
		return nil
	}
	networkName, _ := r.getSpaceNetworkName(space)

	// Find all containers in stack
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err = r.ctrClient.Connect(); err == nil {
		defer r.ctrClient.Close()
		r.ctrClient.SetNamespace(realmDoc.Spec.Namespace)

		pattern := fmt.Sprintf("%s-%s-%s", stackDoc.Spec.RealmID, stackDoc.Spec.SpaceID, stackDoc.Metadata.Name)
		var containers []string
		containers, err = r.findContainersByPattern(r.ctx, realmDoc.Spec.Namespace, pattern)
		if err == nil {
			ctrCtx := context.Background()
			for _, containerID := range containers {
				netnsPath, _ := r.getContainerNetnsPath(ctrCtx, containerID)
				_ = r.purgeCNIForContainer(ctrCtx, containerID, netnsPath, networkName)
			}
		}
	}

	// Force remove stack cgroup
	spec := cgroups.DefaultStackSpec(realmDoc, spaceDoc, stackDoc)
	if r.ctrClient != nil {
		mountpoint := r.ctrClient.GetCgroupMountpoint()
		_ = r.ctrClient.DeleteCgroup(spec.Group, mountpoint)
	}

	// Remove metadata directory completely
	metadataRunPath := fs.StackMetadataDir(
		r.opts.RunPath,
		stackDoc.Spec.RealmID,
		stackDoc.Spec.SpaceID,
		stackDoc.Metadata.Name,
	)
	_ = os.RemoveAll(metadataRunPath)

	return nil
}

// PurgeRealm performs comprehensive cleanup of a realm, including all child resources, CNI resources, and orphaned containers.
func (r *Exec) PurgeRealm(doc *v1beta1.RealmDoc) error {
	if doc == nil {
		return errdefs.ErrRealmNotFound
	}

	// First, perform standard delete
	if _, err := r.DeleteRealm(doc); err != nil {
		r.logger.WarnContext(r.ctx, "delete failed, continuing with purge", "error", err)
	}

	// Get realm document
	realmDoc, err := r.GetRealm(doc)
	if err != nil && !errors.Is(err, errdefs.ErrRealmNotFound) {
		return fmt.Errorf("%w: %w", errdefs.ErrGetRealm, err)
	}

	if errors.Is(err, errdefs.ErrRealmNotFound) {
		realmDoc = doc
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
	r.logger.DebugContext(r.ctx, "starting to find orphaned containers", "namespace", realmDoc.Spec.Namespace)
	containers, err := r.findOrphanedContainers(r.ctx, realmDoc.Spec.Namespace, "")
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
	metadataRunPath := fs.RealmMetadataDir(r.opts.RunPath, realmDoc.Metadata.Name)
	_ = os.RemoveAll(metadataRunPath)

	// Force remove realm cgroup
	spec := cgroups.DefaultRealmSpec(realmDoc)
	mountpoint := r.ctrClient.GetCgroupMountpoint()
	_ = r.ctrClient.DeleteCgroup(spec.Group, mountpoint)

	return nil
}

// PurgeContainer performs comprehensive cleanup of a container, including CNI resources.
func (r *Exec) PurgeContainer(containerID string, namespace string) error {
	if containerID == "" {
		return errdefs.ErrContainerNameRequired
	}

	// Initialize ctr client
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err := r.ctrClient.Connect(); err != nil {
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
