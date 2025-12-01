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
	"strings"
	"time"

	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/naming"
)

// StopCell stops all containers in the cell (workload containers first, then root container).
// It detaches the root container from the CNI network before stopping it.
func (r *Exec) StopCell(cell intmodel.Cell) error {
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
		return fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
	}

	cellID := strings.TrimSpace(internalCell.Spec.ID)
	if cellID == "" {
		cellID = strings.TrimSpace(internalCell.Metadata.Name)
	}
	if cellID == "" {
		return errdefs.ErrCellIDRequired
	}

	if specRealm := strings.TrimSpace(internalCell.Spec.RealmName); specRealm != "" {
		realmName = specRealm
	}
	if specSpace := strings.TrimSpace(internalCell.Spec.SpaceName); specSpace != "" {
		spaceName = specSpace
	}
	if specStack := strings.TrimSpace(internalCell.Spec.StackName); specStack != "" {
		stackName = specStack
	}

	cniConfigPath, cniErr := r.resolveSpaceCNIConfigPath(realmName, spaceName)
	if cniErr != nil {
		return fmt.Errorf("failed to resolve space CNI config: %w", cniErr)
	}

	if err = r.ensureClientConnected(); err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
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

	// Set namespace to realm namespace
	r.ctrClient.SetNamespace(namespace)

	// Stop all workload containers first
	for _, containerSpec := range internalCell.Spec.Containers {
		containerSpaceName := strings.TrimSpace(containerSpec.SpaceName)
		if containerSpaceName == "" {
			containerSpaceName = spaceName
		}
		containerStackName := strings.TrimSpace(containerSpec.StackName)
		if containerStackName == "" {
			containerStackName = stackName
		}
		containerCellName := strings.TrimSpace(containerSpec.CellName)
		if containerCellName == "" {
			containerCellName = cellID
		}

		// Use ContainerdID from spec
		containerID := containerSpec.ContainerdID
		if containerID == "" {
			return fmt.Errorf("container %q has empty ContainerdID", containerSpec.ID)
		}

		// Use container name with UUID for containerd operations
		timeout := 5 * time.Second
		_, err = r.ctrClient.StopContainer(containerID, ctr.StopContainerOptions{
			Force:   true,
			Timeout: &timeout,
		})
		if err != nil {
			// Log warning but continue with other containers
			fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
			fields = append(fields, "space", spaceName, "realm", realmName, "err", fmt.Sprintf("%v", err))
			r.logger.WarnContext(
				r.ctx,
				"failed to stop container, continuing",
				fields...,
			)
			// Continue with other containers
			continue
		}

		fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
		fields = append(fields, "space", spaceName, "realm", realmName)
		r.logger.InfoContext(
			r.ctx,
			"stopped container",
			fields...,
		)
	}

	// Stop root container last (after all workload containers are stopped)
	rootContainerID, err := naming.BuildRootContainerdID(spaceName, stackName, cellID)
	if err != nil {
		return fmt.Errorf("failed to build root container containerd ID: %w", err)
	}

	// Get root container's PID before stopping (needed for CNI detach)
	var rootPID uint32
	rootContainer, err := r.ctrClient.GetContainer(rootContainerID)
	if err == nil {
		nsCtx := namespaces.WithNamespace(r.ctx, namespace)
		rootTask, taskErr := rootContainer.Task(nsCtx, nil)
		if taskErr == nil {
			rootPID = rootTask.Pid()
		}
	}

	// Stop root container
	timeout := 5 * time.Second
	_, err = r.ctrClient.StopContainer(rootContainerID, ctr.StopContainerOptions{
		Force:   true,
		Timeout: &timeout,
	})
	if err != nil {
		fields := appendCellLogFields([]any{"id", rootContainerID}, cellID, cellName)
		fields = append(fields, "space", spaceName, "realm", realmName, "err", fmt.Sprintf("%v", err))
		r.logger.WarnContext(
			r.ctx,
			"failed to stop root container",
			fields...,
		)
		// Don't fail the whole operation if root container stop fails
	} else {
		fields := appendCellLogFields([]any{"id", rootContainerID}, cellID, cellName)
		fields = append(fields, "space", spaceName, "realm", realmName)
		r.logger.InfoContext(
			r.ctx,
			"stopped root container",
			fields...,
		)
	}

	// Detach root container from CNI network after it's stopped
	// Use the PID we captured before stopping
	if rootPID > 0 {
		netnsPath := fmt.Sprintf("/proc/%d/ns/net", rootPID)
		cniMgr, mgrErr := cni.NewManager(
			r.cniConf.CniBinDir,
			r.cniConf.CniConfigDir,
			r.cniConf.CniCacheDir,
		)
		if mgrErr == nil {
			if loadErr := cniMgr.LoadNetworkConfigList(cniConfigPath); loadErr == nil {
				if delErr := cniMgr.DelContainerFromNetwork(r.ctx, rootContainerID, netnsPath); delErr != nil {
					// Log warning but continue - network might already be detached
					fields := appendCellLogFields([]any{"id", rootContainerID}, cellID, cellName)
					fields = append(
						fields,
						"space",
						spaceName,
						"realm",
						realmName,
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
					fields = append(fields, "space", spaceName, "realm", realmName, "netns", netnsPath)
					r.logger.InfoContext(
						r.ctx,
						"detached root container from network",
						fields...,
					)
				}
			}
		}
	}

	return nil
}

// StopContainer stops a specific container in a cell.
func (r *Exec) StopContainer(cell intmodel.Cell, containerID string) error {
	containerID = strings.TrimSpace(containerID)
	if containerID == "" {
		return errors.New("container ID is required")
	}

	cellName := strings.TrimSpace(cell.Metadata.Name)
	if cellName == "" {
		return errdefs.ErrCellNotFound
	}

	cellID := cell.Spec.ID
	if cellID == "" {
		return errdefs.ErrCellIDRequired
	}

	realmName := strings.TrimSpace(cell.Spec.RealmName)
	if realmName == "" {
		return errdefs.ErrRealmNameRequired
	}

	spaceName := strings.TrimSpace(cell.Spec.SpaceName)
	if spaceName == "" {
		return errdefs.ErrSpaceNameRequired
	}

	if err := r.ensureClientConnected(); err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
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

	// Set namespace to realm namespace
	r.ctrClient.SetNamespace(namespace)

	// Find container in cell spec by ID (base name)
	var foundContainerSpec *intmodel.ContainerSpec
	for i := range cell.Spec.Containers {
		if cell.Spec.Containers[i].ID == containerID {
			foundContainerSpec = &cell.Spec.Containers[i]
			break
		}
	}

	if foundContainerSpec == nil {
		return fmt.Errorf("container %q not found in cell %q", containerID, cellName)
	}

	// Use ContainerdID from spec
	containerdID := foundContainerSpec.ContainerdID
	if containerdID == "" {
		return fmt.Errorf("container %q has empty ContainerdID", containerID)
	}

	// Use containerd ID for containerd operations
	timeout := 5 * time.Second
	_, err = r.ctrClient.StopContainer(containerdID, ctr.StopContainerOptions{
		Force:   true,
		Timeout: &timeout,
	})
	if err != nil {
		fields := appendCellLogFields([]any{"id", containerdID}, cellID, cellName)
		fields = append(
			fields,
			"space",
			spaceName,
			"realm",
			realmName,
			"containerName",
			containerID,
			"err",
			fmt.Sprintf("%v", err),
		)
		r.logger.ErrorContext(
			r.ctx,
			"failed to stop container",
			fields...,
		)
		return fmt.Errorf("failed to stop container %s: %w", containerID, err)
	}

	fields := appendCellLogFields([]any{"id", containerdID}, cellID, cellName)
	fields = append(fields, "space", spaceName, "realm", realmName, "containerName", containerID)
	r.logger.InfoContext(
		r.ctx,
		"stopped container",
		fields...,
	)

	return nil
}
