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
	"strings"

	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/metadata"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/cgroups"
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
	if err != nil {
		if errors.Is(err, errdefs.ErrCellNotFound) {
			// Idempotent: cell doesn't exist, consider it deleted
			return nil
		}
		return fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
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

	// Delete all containers in the cell (workload + root)
	ctrCtx := context.Background()
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

		// Build container ID using hierarchical format
		var containerID string
		containerID, err = naming.BuildContainerName(
			containerSpaceName,
			containerStackName,
			containerCellName,
			containerSpec.ID,
		)
		if err != nil {
			r.logger.WarnContext(
				r.ctx,
				"failed to build container name, skipping",
				"container",
				containerSpec.ID,
				"error",
				err,
			)
			continue
		}

		// Use container name with UUID for containerd operations
		// Stop and delete the container
		_, err = r.ctrClient.StopContainer(ctrCtx, containerID, ctr.StopContainerOptions{})
		if err != nil {
			r.logger.WarnContext(
				r.ctx,
				"failed to stop container, continuing with deletion",
				"container",
				containerID,
				"error",
				err,
			)
		}

		err = r.ctrClient.DeleteContainer(ctrCtx, containerID, ctr.ContainerDeleteOptions{
			SnapshotCleanup: true,
		})
		if err != nil {
			r.logger.WarnContext(r.ctx, "failed to delete container", "container", containerID, "error", err)
			// Continue with other containers
		}
	}

	// Delete root container
	rootContainerID, err := naming.BuildRootContainerName(cellSpaceName, cellStackName, cellID)
	if err != nil {
		return fmt.Errorf("failed to build root container name: %w", err)
	}

	// Clean up CNI network configuration before stopping/deleting the root container
	// Try to get the task to retrieve the netns path
	container, loadErr := r.ctrClient.GetContainer(ctrCtx, rootContainerID)
	if loadErr == nil {
		// Try to get the task to get PID and netns path
		task, taskErr := container.Task(ctrCtx, nil)
		if taskErr == nil {
			pid := task.Pid()
			if pid > 0 {
				netnsPath := fmt.Sprintf("/proc/%d/ns/net", pid)

				// Get CNI config path
				cniConfigPath, cniErr := r.resolveSpaceCNIConfigPath(internalCell.Spec.RealmName, cellSpaceName)
				if cniErr == nil {
					// Create CNI manager and remove container from network
					cniMgr, mgrErr := cni.NewManager(
						r.cniConf.CniBinDir,
						r.cniConf.CniConfigDir,
						r.cniConf.CniCacheDir,
					)
					if mgrErr == nil {
						if configLoadErr := cniMgr.LoadNetworkConfigList(cniConfigPath); configLoadErr == nil {
							delErr := cniMgr.DelContainerFromNetwork(ctrCtx, rootContainerID, netnsPath)
							if delErr != nil {
								r.logger.WarnContext(
									r.ctx,
									"failed to remove root container from CNI network, continuing with deletion",
									"container",
									rootContainerID,
									"netns",
									netnsPath,
									"error",
									delErr,
								)
							} else {
								r.logger.InfoContext(
									r.ctx,
									"removed root container from CNI network",
									"container",
									rootContainerID,
									"netns",
									netnsPath,
								)
							}
						} else {
							r.logger.WarnContext(
								r.ctx,
								"failed to load CNI config for cleanup",
								"container",
								rootContainerID,
								"config",
								cniConfigPath,
								"error",
								configLoadErr,
							)
						}
					} else {
						r.logger.WarnContext(
							r.ctx,
							"failed to create CNI manager for cleanup",
							"container",
							rootContainerID,
							"error",
							mgrErr,
						)
					}
				} else {
					r.logger.WarnContext(
						r.ctx,
						"failed to resolve CNI config path for cleanup",
						"container",
						rootContainerID,
						"error",
						cniErr,
					)
				}
			}
		} else {
			r.logger.DebugContext(
				r.ctx,
				"root container task not found, skipping CNI cleanup",
				"container",
				rootContainerID,
				"error",
				taskErr,
			)
		}
	} else {
		r.logger.DebugContext(
			r.ctx,
			"root container not found, skipping CNI cleanup",
			"container",
			rootContainerID,
			"error",
			loadErr,
		)
	}

	_, err = r.ctrClient.StopContainer(ctrCtx, rootContainerID, ctr.StopContainerOptions{})
	if err != nil {
		r.logger.WarnContext(
			r.ctx,
			"failed to stop root container, continuing with deletion",
			"container",
			rootContainerID,
			"error",
			err,
		)
	}

	err = r.ctrClient.DeleteContainer(ctrCtx, rootContainerID, ctr.ContainerDeleteOptions{
		SnapshotCleanup: true,
	})
	if err != nil {
		r.logger.WarnContext(r.ctx, "failed to delete root container", "container", rootContainerID, "error", err)
		// Continue with cgroup and metadata deletion
	}

	// Delete cell cgroup
	spec := cgroups.DefaultCellSpec(internalCell)
	mountpoint := r.ctrClient.GetCgroupMountpoint()
	err = r.ctrClient.DeleteCgroup(spec.Group, mountpoint)
	if err != nil {
		r.logger.WarnContext(r.ctx, "failed to delete cell cgroup", "cgroup", spec.Group, "error", err)
		// Continue with metadata deletion
	}

	// Delete cell metadata
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

	return nil
}
