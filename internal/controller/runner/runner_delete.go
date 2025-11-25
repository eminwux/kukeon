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
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/metadata"
	"github.com/eminwux/kukeon/internal/util/cgroups"
	"github.com/eminwux/kukeon/internal/util/fs"
	"github.com/eminwux/kukeon/internal/util/naming"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// DeleteContainer stops and deletes a specific container in a cell from containerd.
func (r *Exec) DeleteContainer(doc *v1beta1.CellDoc, containerID string) error {
	if doc == nil {
		return errdefs.ErrCellNotFound
	}

	containerID = strings.TrimSpace(containerID)
	if containerID == "" {
		return errors.New("container ID is required")
	}

	cellName := strings.TrimSpace(doc.Metadata.Name)
	if cellName == "" {
		return errdefs.ErrCellNotFound
	}

	cellID := doc.Spec.ID
	if cellID == "" {
		return errdefs.ErrCellIDRequired
	}

	realmID := doc.Spec.RealmID
	if realmID == "" {
		return errdefs.ErrRealmNameRequired
	}

	spaceID := doc.Spec.SpaceID
	if spaceID == "" {
		return errdefs.ErrSpaceNameRequired
	}

	// Initialize ctr client if needed
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err := r.ctrClient.Connect(); err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	// Get realm to access namespace
	realmDoc, err := r.GetRealm(&v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{
			Name: realmID,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to get realm: %w", err)
	}

	// Set namespace to realm namespace
	r.ctrClient.SetNamespace(realmDoc.Spec.Namespace)

	// Build hierarchical container ID for containerd operations
	stackID := doc.Spec.StackID
	if stackID == "" {
		return errdefs.ErrStackNameRequired
	}

	hierarchicalContainerID, err := naming.BuildContainerName(
		spaceID,
		stackID,
		cellID,
		containerID,
	)
	if err != nil {
		return fmt.Errorf("failed to build container name: %w", err)
	}

	// Create a background context for containerd operations
	ctrCtx := context.Background()

	// Stop the container using hierarchical ID
	_, err = r.ctrClient.StopContainer(ctrCtx, hierarchicalContainerID, ctr.StopContainerOptions{})
	if err != nil {
		r.logger.WarnContext(
			r.ctx,
			"failed to stop container, continuing with deletion",
			"container",
			containerID,
			"hierarchicalID",
			hierarchicalContainerID,
			"error",
			err,
		)
	}

	// Delete the container from containerd using hierarchical ID
	err = r.ctrClient.DeleteContainer(ctrCtx, hierarchicalContainerID, ctr.ContainerDeleteOptions{
		SnapshotCleanup: true,
	})
	if err != nil {
		fields := appendCellLogFields([]any{"id", hierarchicalContainerID}, cellID, cellName)
		fields = append(
			fields,
			"space",
			spaceID,
			"realm",
			realmID,
			"containerName",
			containerID,
			"err",
			fmt.Sprintf("%v", err),
		)
		r.logger.ErrorContext(
			r.ctx,
			"failed to delete container",
			fields...,
		)
		return fmt.Errorf("failed to delete container %s: %w", containerID, err)
	}

	fields := appendCellLogFields([]any{"id", hierarchicalContainerID}, cellID, cellName)
	fields = append(fields, "space", spaceID, "realm", realmID, "containerName", containerID)
	r.logger.InfoContext(
		r.ctx,
		"deleted container",
		fields...,
	)

	return nil
}

func (r *Exec) DeleteCell(doc *v1beta1.CellDoc) error {
	if doc == nil {
		return errdefs.ErrCellNotFound
	}

	// Get the cell document to access all containers
	cellDoc, err := r.GetCell(doc)
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
	var realmDoc *v1beta1.RealmDoc
	realmDoc, err = r.GetRealm(&v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{
			Name: cellDoc.Spec.RealmID,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to get realm: %w", err)
	}

	// Set namespace to realm namespace
	r.ctrClient.SetNamespace(realmDoc.Spec.Namespace)

	// Delete all containers in the cell (workload + root)
	ctrCtx := context.Background()
	for _, containerSpec := range cellDoc.Spec.Containers {
		// Build container ID using hierarchical format
		var containerID string
		containerID, err = naming.BuildContainerName(
			containerSpec.SpaceID,
			containerSpec.StackID,
			containerSpec.CellID,
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
	rootContainerID, err := naming.BuildRootContainerName(cellDoc.Spec.SpaceID, cellDoc.Spec.StackID, cellDoc.Spec.ID)
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
				cniConfigPath, cniErr := r.resolveSpaceCNIConfigPath(cellDoc.Spec.RealmID, cellDoc.Spec.SpaceID)
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
	// Get space and stack to build proper cgroup spec
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
		r.logger.WarnContext(r.ctx, "failed to get space for cgroup deletion", "error", err)
	} else {
		var stackDoc *v1beta1.StackDoc
		stackDoc, err = r.GetStack(&v1beta1.StackDoc{
			Metadata: v1beta1.StackMetadata{
				Name: cellDoc.Spec.StackID,
			},
			Spec: v1beta1.StackSpec{
				RealmID: cellDoc.Spec.RealmID,
				SpaceID: cellDoc.Spec.SpaceID,
			},
		})
		if err != nil {
			r.logger.WarnContext(r.ctx, "failed to get stack for cgroup deletion", "error", err)
		} else {
			spec := cgroups.DefaultCellSpec(realmDoc, spaceDoc, stackDoc, cellDoc)
			mountpoint := r.ctrClient.GetCgroupMountpoint()
			err = r.ctrClient.DeleteCgroup(spec.Group, mountpoint)
			if err != nil {
				r.logger.WarnContext(r.ctx, "failed to delete cell cgroup", "cgroup", spec.Group, "error", err)
				// Continue with metadata deletion
			}
		}
	}

	// Delete cell metadata
	metadataFilePath := fs.CellMetadataPath(
		r.opts.RunPath,
		cellDoc.Spec.RealmID,
		cellDoc.Spec.SpaceID,
		cellDoc.Spec.StackID,
		cellDoc.Metadata.Name,
	)
	if err = metadata.DeleteMetadata(r.ctx, r.logger, metadataFilePath); err != nil {
		return fmt.Errorf("%w: failed to delete cell metadata: %w", errdefs.ErrDeleteCell, err)
	}

	return nil
}

func (r *Exec) DeleteStack(doc *v1beta1.StackDoc) error {
	if doc == nil {
		return errdefs.ErrStackNotFound
	}

	// Get the stack document
	stackDoc, err := r.GetStack(doc)
	if err != nil {
		if errors.Is(err, errdefs.ErrStackNotFound) {
			// Idempotent: stack doesn't exist, consider it deleted
			return nil
		}
		return fmt.Errorf("%w: %w", errdefs.ErrGetStack, err)
	}

	// Initialize ctr client if needed
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err = r.ctrClient.Connect(); err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	// Get realm and space to build cgroup spec
	realmDoc, err := r.GetRealm(&v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{
			Name: stackDoc.Spec.RealmID,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to get realm: %w", err)
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
		return fmt.Errorf("failed to get space: %w", err)
	}

	// Delete stack cgroup
	spec := cgroups.DefaultStackSpec(realmDoc, spaceDoc, stackDoc)
	mountpoint := r.ctrClient.GetCgroupMountpoint()
	err = r.ctrClient.DeleteCgroup(spec.Group, mountpoint)
	if err != nil {
		r.logger.WarnContext(r.ctx, "failed to delete stack cgroup", "cgroup", spec.Group, "error", err)
		// Continue with metadata deletion
	}

	// Delete stack metadata
	metadataFilePath := fs.StackMetadataPath(
		r.opts.RunPath,
		stackDoc.Spec.RealmID,
		stackDoc.Spec.SpaceID,
		stackDoc.Metadata.Name,
	)
	if err = metadata.DeleteMetadata(r.ctx, r.logger, metadataFilePath); err != nil {
		return fmt.Errorf("%w: failed to delete stack metadata: %w", errdefs.ErrDeleteStack, err)
	}

	return nil
}

func (r *Exec) DeleteSpace(doc *v1beta1.SpaceDoc) error {
	if doc == nil {
		return errdefs.ErrSpaceNotFound
	}

	// Get the space document
	spaceDoc, err := r.GetSpace(doc)
	if err != nil {
		if errors.Is(err, errdefs.ErrSpaceNotFound) {
			// Idempotent: space doesn't exist, consider it deleted
			return nil
		}
		return fmt.Errorf("%w: %w", errdefs.ErrGetSpace, err)
	}

	// Initialize ctr client if needed
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err = r.ctrClient.Connect(); err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	// Get realm to build cgroup spec
	realmDoc, err := r.GetRealm(&v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{
			Name: spaceDoc.Spec.RealmID,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to get realm: %w", err)
	}

	// Delete CNI network config
	var networkName string
	realmID := spaceDoc.Spec.RealmID
	if realmID == "" && spaceDoc.Metadata.Labels != nil {
		if realmLabel, ok := spaceDoc.Metadata.Labels[consts.KukeonRealmLabelKey]; ok &&
			strings.TrimSpace(realmLabel) != "" {
			realmID = strings.TrimSpace(realmLabel)
		}
	}
	networkName, err = naming.BuildSpaceNetworkName(realmID, spaceDoc.Metadata.Name)
	if err != nil {
		r.logger.WarnContext(r.ctx, "failed to build network name, skipping CNI config deletion", "error", err)
	} else {
		var confPath string
		confPath, err = r.resolveSpaceCNIConfigPath(spaceDoc.Spec.RealmID, spaceDoc.Metadata.Name)
		if err == nil {
			var mgr *cni.Manager
			mgr, err = cni.NewManager(
				r.cniConf.CniBinDir,
				r.cniConf.CniConfigDir,
				r.cniConf.CniCacheDir,
			)
			if err == nil {
				if err = mgr.DeleteNetwork(networkName, confPath); err != nil {
					r.logger.WarnContext(r.ctx, "failed to delete CNI network config", "network", networkName, "error", err)
					// Continue with cgroup and metadata deletion
				}
			}
		}
	}

	// Delete space cgroup
	spec := cgroups.DefaultSpaceSpec(realmDoc, spaceDoc)
	mountpoint := r.ctrClient.GetCgroupMountpoint()
	err = r.ctrClient.DeleteCgroup(spec.Group, mountpoint)
	if err != nil {
		r.logger.WarnContext(r.ctx, "failed to delete space cgroup", "cgroup", spec.Group, "error", err)
		// Continue with metadata deletion
	}

	// Delete space metadata
	metadataFilePath := fs.SpaceMetadataPath(
		r.opts.RunPath,
		spaceDoc.Spec.RealmID,
		spaceDoc.Metadata.Name,
	)
	if err = metadata.DeleteMetadata(r.ctx, r.logger, metadataFilePath); err != nil {
		return fmt.Errorf("%w: failed to delete space metadata: %w", errdefs.ErrDeleteSpace, err)
	}

	return nil
}

func (r *Exec) DeleteRealm(doc *v1beta1.RealmDoc) (DeleteRealmOutcome, error) {
	var outcome DeleteRealmOutcome

	if doc == nil {
		return outcome, errdefs.ErrRealmNotFound
	}

	// Get the realm document
	realmDoc, err := r.GetRealm(doc)
	if err != nil {
		if errors.Is(err, errdefs.ErrRealmNotFound) {
			// Idempotent: realm doesn't exist, consider it deleted
			return outcome, nil
		}
		return outcome, fmt.Errorf("%w: %w", errdefs.ErrGetRealm, err)
	}

	// Initialize ctr client if needed
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err = r.ctrClient.Connect(); err != nil {
		return outcome, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	// Delete realm cgroup
	spec := cgroups.DefaultRealmSpec(realmDoc)
	mountpoint := r.ctrClient.GetCgroupMountpoint()
	err = r.ctrClient.DeleteCgroup(spec.Group, mountpoint)
	if err != nil {
		r.logger.WarnContext(r.ctx, "failed to delete realm cgroup", "cgroup", spec.Group, "error", err)
		// Continue with namespace and metadata deletion
	} else {
		outcome.CgroupDeleted = true
	}

	// Delete containerd namespace
	if err = r.ctrClient.DeleteNamespace(realmDoc.Spec.Namespace); err != nil {
		r.logger.WarnContext(
			r.ctx,
			"failed to delete containerd namespace",
			"namespace",
			realmDoc.Spec.Namespace,
			"error",
			err,
		)
		// Continue with metadata deletion
	} else {
		outcome.ContainerdNamespaceDeleted = true
	}

	// Delete realm metadata
	metadataFilePath := fs.RealmMetadataPath(r.opts.RunPath, realmDoc.Metadata.Name)
	if err = metadata.DeleteMetadata(r.ctx, r.logger, metadataFilePath); err != nil {
		return outcome, fmt.Errorf("%w: failed to delete realm metadata: %w", errdefs.ErrDeleteRealm, err)
	}
	outcome.MetadataDeleted = true

	return outcome, nil
}
