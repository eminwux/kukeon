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
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/naming"
)

// RecreateCell stops all containers in the cell, deletes them, and recreates the cell
// with the new root container spec. This is used when the root container spec changes
// (image, command, or args).
func (r *Exec) RecreateCell(desired intmodel.Cell) (intmodel.Cell, error) {
	// Get existing cell
	existing, err := r.GetCell(desired)
	if err != nil {
		return intmodel.Cell{}, fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
	}

	// Stop all containers in the cell
	if stopErr := r.StopCell(existing); stopErr != nil {
		r.logger.WarnContext(
			r.ctx,
			"failed to stop cell containers, continuing with deletion",
			"cell", existing.Metadata.Name,
			"error", stopErr,
		)
	}

	// Delete all containers
	if connectErr := r.ensureClientConnected(); connectErr != nil {
		return intmodel.Cell{}, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, connectErr)
	}

	cellID := existing.Spec.ID
	if cellID == "" {
		cellID = existing.Metadata.Name
	}

	realmName := strings.TrimSpace(existing.Spec.RealmName)
	spaceName := strings.TrimSpace(existing.Spec.SpaceName)
	stackName := strings.TrimSpace(existing.Spec.StackName)

	// Get realm to access namespace
	lookupRealm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: realmName,
		},
	}
	internalRealm, err := r.GetRealm(lookupRealm)
	if err != nil {
		return intmodel.Cell{}, fmt.Errorf("failed to get realm: %w", err)
	}

	r.ctrClient.SetNamespace(internalRealm.Spec.Namespace)

	// Delete all containers in the cell
	for _, containerSpec := range existing.Spec.Containers {
		containerID := containerSpec.ContainerdID
		if containerID == "" {
			continue
		}

		// Stop container
		_, err = r.ctrClient.StopContainer(containerID, ctr.StopContainerOptions{})
		if err != nil {
			r.logger.WarnContext(
				r.ctx,
				"failed to stop container, continuing with deletion",
				"container", containerID,
				"error", err,
			)
		}

		// Delete container
		err = r.ctrClient.DeleteContainer(containerID, ctr.ContainerDeleteOptions{
			SnapshotCleanup: true,
		})
		if err != nil {
			r.logger.WarnContext(
				r.ctx,
				"failed to delete container, continuing",
				"container", containerID,
				"error", err,
			)
		}
	}

	// Delete root container
	rootContainerID, err := naming.BuildRootContainerdID(spaceName, stackName, cellID)
	if err != nil {
		return intmodel.Cell{}, fmt.Errorf("failed to build root container containerd ID: %w", err)
	}

	_, err = r.ctrClient.StopContainer(rootContainerID, ctr.StopContainerOptions{Force: true})
	if err != nil {
		r.logger.WarnContext(
			r.ctx,
			"failed to stop root container, continuing with deletion",
			"container", rootContainerID,
			"error", err,
		)
	}

	err = r.ctrClient.DeleteContainer(rootContainerID, ctr.ContainerDeleteOptions{
		SnapshotCleanup: true,
	})
	if err != nil {
		r.logger.WarnContext(
			r.ctx,
			"failed to delete root container, continuing",
			"container", rootContainerID,
			"error", err,
		)
	}

	// Clear containerd IDs from desired cell so containers will be recreated
	for i := range desired.Spec.Containers {
		desired.Spec.Containers[i].ContainerdID = ""
	}

	// Preserve existing metadata and status where appropriate
	desired.Status.CgroupPath = existing.Status.CgroupPath

	// Recreate cell containers (this will create root container and all child containers)
	_, err = r.createCellContainers(&desired)
	if err != nil {
		return intmodel.Cell{}, fmt.Errorf("failed to recreate cell containers: %w", err)
	}

	// Update cell state
	desired.Status.State = intmodel.CellStateReady

	// Update metadata
	if updateErr := r.UpdateCellMetadata(desired); updateErr != nil {
		return intmodel.Cell{}, fmt.Errorf("%w: %w", errdefs.ErrUpdateCellMetadata, updateErr)
	}

	return desired, nil
}
