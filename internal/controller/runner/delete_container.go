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

// DeleteContainer stops and deletes a specific container in a cell from containerd.
func (r *Exec) DeleteContainer(cell intmodel.Cell, containerID string) error {
	containerID = strings.TrimSpace(containerID)
	if containerID == "" {
		return errors.New("container ID is required")
	}

	cellName := strings.TrimSpace(cell.Metadata.Name)
	if cellName == "" {
		return errdefs.ErrCellNameRequired
	}

	cellID := strings.TrimSpace(cell.Spec.ID)
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

	stackName := strings.TrimSpace(cell.Spec.StackName)
	if stackName == "" {
		return errdefs.ErrStackNameRequired
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

	// Use ContainerdID from spec
	containerdID := foundContainerSpec.ContainerdID
	if containerdID == "" {
		return fmt.Errorf("container %q has empty ContainerdID", containerID)
	}

	// Get space for network name
	lookupSpace := intmodel.Space{
		Metadata: intmodel.SpaceMetadata{
			Name: spaceName,
		},
		Spec: intmodel.SpaceSpec{
			RealmName: realmName,
		},
	}
	space, spaceErr := r.GetSpace(lookupSpace)
	var networkName string
	if spaceErr == nil {
		networkName, _ = r.getSpaceNetworkName(space)
	}

	// Comprehensive CNI cleanup before stopping/deleting
	netnsPath, _ := r.getContainerNetnsPath(containerdID)
	_ = r.purgeCNIForContainer(containerdID, netnsPath, networkName)

	// Stop the container using containerd ID
	_, err = r.ctrClient.StopContainer(containerdID, ctr.StopContainerOptions{})
	if err != nil {
		r.logger.WarnContext(
			r.ctx,
			"failed to stop container, continuing with deletion",
			"container",
			containerID,
			"containerdID",
			containerdID,
			"error",
			err,
		)
	}

	// Delete the container from containerd using containerd ID
	err = r.ctrClient.DeleteContainer(containerdID, ctr.ContainerDeleteOptions{
		SnapshotCleanup: true,
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
			"failed to delete container",
			fields...,
		)
		return fmt.Errorf("failed to delete container %s: %w", containerID, err)
	}

	fields := appendCellLogFields([]any{"id", containerdID}, cellID, cellName)
	fields = append(fields, "space", spaceName, "realm", realmName, "containerName", containerID)
	r.logger.InfoContext(
		r.ctx,
		"deleted container",
		fields...,
	)

	return nil
}
