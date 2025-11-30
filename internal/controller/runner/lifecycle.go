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
	"fmt"
	"strings"
	"syscall"

	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
)

// detachRootContainerFromCNI detaches the root container from the CNI network.
// It handles the case where the container might not exist or might already be detached.
// This function logs warnings for non-critical failures but continues execution.
func (r *Exec) detachRootContainerFromCNI(
	ctrCtx context.Context,
	rootContainerID, cniConfigPath, cellID, cellName, spaceID, realmID, realmNamespace string,
) {
	// Get root container to check if it exists and get its task
	rootContainer, err := r.ctrClient.GetContainer(ctrCtx, rootContainerID)
	if err == nil {
		// Container exists, try to get its task to detach from CNI
		nsCtx := namespaces.WithNamespace(ctrCtx, realmNamespace)
		rootTask, taskErr := rootContainer.Task(nsCtx, nil)
		if taskErr == nil {
			// Task exists, get PID and detach from CNI
			rootPID := rootTask.Pid()
			if rootPID > 0 {
				netnsPath := fmt.Sprintf("/proc/%d/ns/net", rootPID)

				// Create CNI manager and detach from network
				cniMgr, mgrErr := cni.NewManager(
					r.cniConf.CniBinDir,
					r.cniConf.CniConfigDir,
					r.cniConf.CniCacheDir,
				)
				if mgrErr == nil {
					if loadErr := cniMgr.LoadNetworkConfigList(cniConfigPath); loadErr == nil {
						if delErr := cniMgr.DelContainerFromNetwork(ctrCtx, rootContainerID, netnsPath); delErr != nil {
							// Log warning but continue - network might already be detached
							fields := appendCellLogFields([]any{"id", rootContainerID}, cellID, cellName)
							fields = append(
								fields,
								"space",
								spaceID,
								"realm",
								realmID,
								"netns",
								netnsPath,
								"err",
								fmt.Sprintf("%v", delErr),
							)
							r.logger.WarnContext(
								r.ctx,
								"failed to detach root container from network, continuing",
								fields...,
							)
						} else {
							fields := appendCellLogFields([]any{"id", rootContainerID}, cellID, cellName)
							fields = append(fields, "space", spaceID, "realm", realmID, "netns", netnsPath)
							r.logger.InfoContext(
								r.ctx,
								"detached root container from network",
								fields...,
							)
						}
					}
				}
			}
		}
	}
}

// killContainerTask directly kills a container task by sending SIGKILL immediately.
// This is a helper method used by KillCell and KillContainer.
func (r *Exec) killContainerTask(containerID, realmNamespace string) error {
	// Get the container
	container, err := r.ctrClient.GetContainer(r.ctx, containerID)
	if err != nil {
		return fmt.Errorf("failed to get container %s: %w", containerID, err)
	}

	// Create namespace context
	nsCtx := namespaces.WithNamespace(r.ctx, realmNamespace)

	// Get the task
	task, err := container.Task(nsCtx, nil)
	if err != nil {
		// Task might not exist, which is fine for kill operation
		r.logger.DebugContext(
			r.ctx,
			"task not found for container, may already be stopped",
			"container",
			containerID,
			"error",
			err,
		)
		return nil // Don't fail if task doesn't exist
	}

	// Immediately send SIGKILL
	err = task.Kill(nsCtx, syscall.SIGKILL)
	if err != nil {
		// If task is already stopped, that's fine
		r.logger.DebugContext(
			r.ctx,
			"failed to kill task, may already be stopped",
			"container",
			containerID,
			"error",
			err,
		)
		// Don't return error if task is already stopped
		return nil
	}

	return nil
}

func (r *Exec) resolveSpaceCNIConfigPath(realmID, spaceID string) (string, error) {
	lookupSpace := intmodel.Space{
		Metadata: intmodel.SpaceMetadata{
			Name: spaceID,
		},
		Spec: intmodel.SpaceSpec{
			RealmName: realmID,
		},
	}
	internalSpace, err := r.GetSpace(lookupSpace)
	if err != nil {
		return "", fmt.Errorf("%w: %w", errdefs.ErrGetSpace, err)
	}

	// Convert internal space back to external for accessing CNIConfigPath
	spaceDoc, convertErr := apischeme.BuildSpaceExternalFromInternal(internalSpace, apischeme.VersionV1Beta1)
	if convertErr != nil {
		return "", fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, convertErr)
	}

	confPath := strings.TrimSpace(spaceDoc.Spec.CNIConfigPath)
	if confPath != "" {
		return confPath, nil
	}

	confPath, err = fs.SpaceNetworkConfigPath(r.opts.RunPath, realmID, spaceDoc.Metadata.Name)
	if err != nil {
		return "", fmt.Errorf("failed to build default space CNI config path: %w", err)
	}
	return confPath, nil
}
