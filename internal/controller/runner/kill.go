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

	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// KillCell immediately force-kills all containers in a cell (workload containers first, then root container).
// It detaches the root container from the CNI network before killing it.
func (r *Exec) KillCell(cell intmodel.Cell) (intmodel.Cell, error) {
	cellName := strings.TrimSpace(cell.Metadata.Name)
	if cellName == "" {
		return intmodel.Cell{}, errdefs.ErrCellNameRequired
	}
	realmName := strings.TrimSpace(cell.Spec.RealmName)
	if realmName == "" {
		return intmodel.Cell{}, errdefs.ErrRealmNameRequired
	}
	spaceName := strings.TrimSpace(cell.Spec.SpaceName)
	if spaceName == "" {
		return intmodel.Cell{}, errdefs.ErrSpaceNameRequired
	}
	stackName := strings.TrimSpace(cell.Spec.StackName)
	if stackName == "" {
		return intmodel.Cell{}, errdefs.ErrStackNameRequired
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
		return intmodel.Cell{}, fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
	}

	cellID := strings.TrimSpace(internalCell.Spec.ID)
	if cellID == "" {
		return intmodel.Cell{}, errdefs.ErrCellIDRequired
	}

	realmID := strings.TrimSpace(internalCell.Spec.RealmName)
	if realmID == "" {
		return intmodel.Cell{}, errdefs.ErrRealmNameRequired
	}

	spaceID := strings.TrimSpace(internalCell.Spec.SpaceName)
	if spaceID == "" {
		return intmodel.Cell{}, errdefs.ErrSpaceNameRequired
	}

	stackID := strings.TrimSpace(internalCell.Spec.StackName)
	if stackID == "" {
		return intmodel.Cell{}, errdefs.ErrStackNameRequired
	}

	cniConfigPath, cniErr := r.resolveSpaceCNIConfigPath(realmID, spaceID)
	if cniErr != nil {
		return intmodel.Cell{}, fmt.Errorf("failed to resolve space CNI config: %w", cniErr)
	}

	if err = r.ensureClientConnected(); err != nil {
		return intmodel.Cell{}, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}

	// Get realm to access namespace
	lookupRealm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: realmID,
		},
	}
	internalRealm, err := r.GetRealm(lookupRealm)
	if err != nil {
		return intmodel.Cell{}, fmt.Errorf("failed to get realm: %w", err)
	}

	// Set namespace to realm namespace
	r.ctrClient.SetNamespace(internalRealm.Spec.Namespace)

	// Kill all workload containers first
	for _, containerSpec := range internalCell.Spec.Containers {
		// Skip root container - it's handled separately afterwards
		if containerSpec.Root {
			continue
		}

		containerCellName := strings.TrimSpace(containerSpec.CellName)
		if containerCellName == "" {
			containerCellName = cellID
		}
		// Use ContainerdID from spec
		containerID := containerSpec.ContainerdID
		if containerID == "" {
			return intmodel.Cell{}, fmt.Errorf("container %q has empty ContainerdID", containerSpec.ID)
		}

		// Use container name with UUID for containerd operations
		err = r.killContainerTask(containerID, internalRealm.Spec.Namespace)
		if err != nil {
			// Log warning but continue with other containers
			fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
			fields = append(fields, "space", spaceID, "realm", realmID, "err", fmt.Sprintf("%v", err))
			r.logger.WarnContext(
				r.ctx,
				"failed to kill container, continuing",
				fields...,
			)
			// Continue with other containers
			continue
		}

		fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
		fields = append(fields, "space", spaceID, "realm", realmID)
		r.logger.InfoContext(
			r.ctx,
			"killed container",
			fields...,
		)

		// Delete container after killing
		err = r.ctrClient.DeleteContainer(containerID, ctr.ContainerDeleteOptions{
			SnapshotCleanup: true,
		})
		if err != nil {
			// Log warning but don't fail - container might already be deleted
			fields = appendCellLogFields([]any{"id", containerID}, cellID, cellName)
			fields = append(fields, "space", spaceID, "realm", realmID, "err", fmt.Sprintf("%v", err))
			r.logger.WarnContext(
				r.ctx,
				"failed to delete container, continuing",
				fields...,
			)
		} else {
			fields = appendCellLogFields([]any{"id", containerID}, cellID, cellName)
			fields = append(fields, "space", spaceID, "realm", realmID)
			r.logger.InfoContext(
				r.ctx,
				"deleted container",
				fields...,
			)
		}
	}

	// Kill root container last and detach from CNI
	rootContainerID, err := r.getRootContainerContainerdID(internalCell)
	if err != nil {
		return intmodel.Cell{}, err
	}

	// Get space to resolve network name for fallback cleanup
	var networkName string
	lookupSpace := intmodel.Space{
		Metadata: intmodel.SpaceMetadata{
			Name: spaceID,
		},
		Spec: intmodel.SpaceSpec{
			RealmName: realmID,
		},
	}
	internalSpace, spaceErr := r.GetSpace(lookupSpace)
	if spaceErr == nil {
		networkName, _ = r.getSpaceNetworkName(internalSpace)
	}

	// Detach root container from CNI network before killing (needed for CNI detach)
	r.detachRootContainerFromNetwork(
		rootContainerID,
		cniConfigPath,
		internalRealm.Spec.Namespace,
		cellID,
		cellName,
		spaceID,
		realmID,
	)

	// Kill root container
	err = r.killContainerTask(rootContainerID, internalRealm.Spec.Namespace)
	if err != nil {
		fields := appendCellLogFields([]any{"id", rootContainerID}, cellID, cellName)
		fields = append(fields, "space", spaceID, "realm", realmID, "err", fmt.Sprintf("%v", err))
		r.logger.WarnContext(
			r.ctx,
			"failed to kill root container",
			fields...,
		)
		// Don't fail the whole operation if root container kill fails
	} else {
		fields := appendCellLogFields([]any{"id", rootContainerID}, cellID, cellName)
		fields = append(fields, "space", spaceID, "realm", realmID)
		r.logger.InfoContext(
			r.ctx,
			"killed root container",
			fields...,
		)
	}

	// Delete root container after killing
	err = r.ctrClient.DeleteContainer(rootContainerID, ctr.ContainerDeleteOptions{
		SnapshotCleanup: true,
	})
	if err != nil {
		// Log warning but don't fail - container might already be deleted
		fields := appendCellLogFields([]any{"id", rootContainerID}, cellID, cellName)
		fields = append(fields, "space", spaceID, "realm", realmID, "err", fmt.Sprintf("%v", err))
		r.logger.WarnContext(
			r.ctx,
			"failed to delete root container, continuing",
			fields...,
		)
	} else {
		fields := appendCellLogFields([]any{"id", rootContainerID}, cellID, cellName)
		fields = append(fields, "space", spaceID, "realm", realmID)
		r.logger.InfoContext(
			r.ctx,
			"deleted root container",
			fields...,
		)
	}

	// Always run comprehensive CNI cleanup after container deletion as a safety net
	// This ensures IPAM allocations are cleaned up even if CNI DEL succeeded or failed
	// Clear netns path since container is now deleted
	var netnsPath string
	if networkName != "" {
		if purgeErr := r.purgeCNIForContainer(rootContainerID, netnsPath, networkName); purgeErr != nil {
			fields := appendCellLogFields([]any{"id", rootContainerID}, cellID, cellName)
			fields = append(
				fields,
				"space",
				spaceID,
				"realm",
				realmID,
				"network",
				networkName,
				"err",
				fmt.Sprintf("%v", purgeErr),
			)
			r.logger.WarnContext(
				r.ctx,
				"final CNI cleanup had errors, but continuing",
				fields...,
			)
		} else {
			fields := appendCellLogFields([]any{"id", rootContainerID}, cellID, cellName)
			fields = append(fields, "space", spaceID, "realm", realmID, "network", networkName)
			r.logger.DebugContext(
				r.ctx,
				"completed final CNI cleanup after container deletion",
				fields...,
			)
		}
	} else {
		fields := appendCellLogFields([]any{"id", rootContainerID}, cellID, cellName)
		fields = append(fields, "space", spaceID, "realm", realmID)
		r.logger.WarnContext(
			r.ctx,
			"cannot perform final CNI cleanup: network name not resolved",
			fields...,
		)
	}

	// Update cell state in internal model
	internalCell.Status.State = intmodel.CellStateStopped

	// Populate container statuses after killing cell and persist them
	if err = r.PopulateAndPersistCellContainerStatuses(&internalCell); err != nil {
		r.logger.WarnContext(r.ctx, "failed to populate container statuses",
			"cell", cellName,
			"error", err)
		// Continue anyway - status population is best-effort
	}

	return internalCell, nil
}

// KillContainer immediately force-kills a specific container in a cell.
func (r *Exec) KillContainer(cell intmodel.Cell, containerID string) error {
	containerID = strings.TrimSpace(containerID)
	if containerID == "" {
		return errors.New("container ID is required")
	}

	cellName := strings.TrimSpace(cell.Metadata.Name)
	if cellName == "" {
		return errdefs.ErrCellNameRequired
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

	// Set namespace to realm namespace
	r.ctrClient.SetNamespace(internalRealm.Spec.Namespace)

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

	// Root container cannot be killed directly - it must be killed by killing the cell
	if foundContainerSpec.Root {
		return fmt.Errorf(
			"root container cannot be killed directly, kill the cell instead using 'kuke kill cell %s'",
			cellName,
		)
	}

	// Use ContainerdID from spec
	containerdID := foundContainerSpec.ContainerdID
	if containerdID == "" {
		return fmt.Errorf("container %q has empty ContainerdID", containerID)
	}

	// Use containerd ID for containerd operations
	err = r.killContainerTask(containerdID, internalRealm.Spec.Namespace)
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
			"failed to kill container",
			fields...,
		)
		return fmt.Errorf("failed to kill container %s: %w", containerID, err)
	}

	fields := appendCellLogFields([]any{"id", containerdID}, cellID, cellName)
	fields = append(fields, "space", spaceName, "realm", realmName, "containerName", containerID)
	r.logger.InfoContext(
		r.ctx,
		"killed container",
		fields...,
	)

	// Delete container after killing
	err = r.ctrClient.DeleteContainer(containerdID, ctr.ContainerDeleteOptions{
		SnapshotCleanup: true,
	})
	if err != nil {
		// Log warning but don't fail - container might already be deleted
		fields = appendCellLogFields([]any{"id", containerdID}, cellID, cellName)
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
		r.logger.WarnContext(
			r.ctx,
			"failed to delete container, continuing",
			fields...,
		)
	} else {
		fields = appendCellLogFields([]any{"id", containerdID}, cellID, cellName)
		fields = append(fields, "space", spaceName, "realm", realmName, "containerName", containerID)
		r.logger.InfoContext(
			r.ctx,
			"deleted container",
			fields...,
		)
	}

	return nil
}
