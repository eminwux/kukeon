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

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/naming"
)

// GetContainerState queries containerd for the actual task status of a container
// and converts it to the internal ContainerState.
func (r *Exec) GetContainerState(cell intmodel.Cell, containerID string) (intmodel.ContainerState, error) {
	containerID = strings.TrimSpace(containerID)
	if containerID == "" {
		return intmodel.ContainerStateUnknown, errors.New("container ID is required")
	}

	cellName := strings.TrimSpace(cell.Metadata.Name)
	if cellName == "" {
		return intmodel.ContainerStateUnknown, errdefs.ErrCellNotFound
	}

	realmName := strings.TrimSpace(cell.Spec.RealmName)
	if realmName == "" {
		return intmodel.ContainerStateUnknown, errdefs.ErrRealmNameRequired
	}

	spaceName := strings.TrimSpace(cell.Spec.SpaceName)
	if spaceName == "" {
		return intmodel.ContainerStateUnknown, errdefs.ErrSpaceNameRequired
	}

	stackName := strings.TrimSpace(cell.Spec.StackName)
	if stackName == "" {
		return intmodel.ContainerStateUnknown, errdefs.ErrStackNameRequired
	}

	if err := r.ensureClientConnected(); err != nil {
		r.logger.InfoContext(r.ctx, "failed to connect to containerd",
			"container", containerID,
			"error", err)
		return intmodel.ContainerStateUnknown, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}

	// Get realm to access namespace
	lookupRealm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: realmName,
		},
	}
	internalRealm, err := r.GetRealm(lookupRealm)
	if err != nil {
		r.logger.InfoContext(r.ctx, "failed to get realm for container state",
			"container", containerID,
			"realm", realmName,
			"error", err)
		return intmodel.ContainerStateUnknown, fmt.Errorf("failed to get realm: %w", err)
	}

	namespace := internalRealm.Spec.Namespace
	if namespace == "" {
		// Fallback to realm name if namespace is not set
		namespace = realmName
		r.logger.DebugContext(r.ctx, "realm has no namespace, using realm name as fallback",
			"realm", realmName,
			"namespace", namespace)
	}

	// Set namespace for containerd operations
	if r.ctrClient != nil {
		r.ctrClient.SetNamespace(namespace)
		r.logger.DebugContext(r.ctx, "set containerd namespace",
			"namespace", namespace,
			"realm", realmName)
	}

	// Find container in cell spec to get ContainerdID
	var foundContainerSpec *intmodel.ContainerSpec
	for i := range cell.Spec.Containers {
		if cell.Spec.Containers[i].ID == containerID {
			foundContainerSpec = &cell.Spec.Containers[i]
			break
		}
	}

	if foundContainerSpec == nil {
		r.logger.InfoContext(r.ctx, "container not found in cell spec",
			"container", containerID,
			"cell", cellName,
			"containersInCell", len(cell.Spec.Containers))
		return intmodel.ContainerStateUnknown, nil
	}

	// Get cell ID (use Spec.ID if available, otherwise fall back to Metadata.Name)
	cellID := strings.TrimSpace(cell.Spec.ID)
	if cellID == "" {
		cellID = strings.TrimSpace(cell.Metadata.Name)
	}
	if cellID == "" {
		return intmodel.ContainerStateUnknown, errdefs.ErrCellIDRequired
	}

	// Get containerd ID
	containerdID := foundContainerSpec.ContainerdID
	if containerdID == "" {
		// Build containerd ID if not set
		if foundContainerSpec.Root {
			containerdID, err = naming.BuildRootContainerdID(
				cell.Spec.SpaceName,
				cell.Spec.StackName,
				cellID,
			)
		} else {
			containerdID, err = naming.BuildContainerdID(
				cell.Spec.SpaceName,
				cell.Spec.StackName,
				cellID,
				foundContainerSpec.ID,
			)
		}
		if err != nil {
			return intmodel.ContainerStateUnknown, fmt.Errorf("failed to build containerd ID: %w", err)
		}
	}

	r.logger.DebugContext(r.ctx, "querying container state",
		"container", containerID,
		"containerdID", containerdID,
		"namespace", namespace)

	// Check if container exists in containerd
	// Note: ensureClientConnected() should have initialized the client, but check anyway
	if r.ctrClient == nil {
		r.logger.InfoContext(r.ctx, "containerd client not available after connection attempt",
			"container", containerID,
			"containerdID", containerdID)
		return intmodel.ContainerStateUnknown, errors.New("containerd client not available")
	}

	containerExists, err := r.ctrClient.ExistsContainer(containerdID)
	if err != nil {
		r.logger.InfoContext(r.ctx, "failed to check container existence",
			"container", containerID,
			"containerdID", containerdID,
			"error", err)
		// If we can't check existence, return Unknown
		return intmodel.ContainerStateStopped, nil
	}

	if !containerExists {
		// Container doesn't exist in containerd
		r.logger.InfoContext(r.ctx, "container does not exist in containerd",
			"container", containerID,
			"containerdID", containerdID,
			"namespace", namespace)
		return intmodel.ContainerStateUnknown, nil
	}

	// Get task status if container exists
	taskStatus, err := r.ctrClient.TaskStatus(containerdID)
	if err != nil {
		// Task might not exist even if container does (container was stopped/deleted)
		r.logger.DebugContext(r.ctx, "failed to get task status (container exists but task may not)",
			"container", containerID,
			"containerdID", containerdID,
			"error", err)
		// Container exists but no task - container is stopped, return Stopped
		return intmodel.ContainerStateStopped, nil
	}

	// Convert containerd status to internal container state
	state := convertContainerdStatusToContainerState(taskStatus, true)
	r.logger.InfoContext(r.ctx, "container state determined",
		"container", containerID,
		"containerdID", containerdID,
		"namespace", namespace,
		"taskStatus", taskStatus.Status,
		"internalState", state)
	return state, nil
}

// convertContainerdStatusToContainerState converts a containerd task status to internal ContainerState.
func convertContainerdStatusToContainerState(status containerd.Status, hasTask bool) intmodel.ContainerState {
	if !hasTask {
		return intmodel.ContainerStateUnknown
	}

	switch status.Status {
	case containerd.Running:
		return intmodel.ContainerStateReady
	case containerd.Stopped:
		return intmodel.ContainerStateStopped
	case containerd.Created:
		return intmodel.ContainerStatePending
	case containerd.Unknown:
		return intmodel.ContainerStateUnknown
	case containerd.Paused:
		return intmodel.ContainerStatePaused
	case containerd.Pausing:
		return intmodel.ContainerStatePausing
	default:
		return intmodel.ContainerStateUnknown
	}
}
