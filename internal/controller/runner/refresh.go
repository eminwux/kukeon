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

	containerd "github.com/containerd/containerd/v2/client"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/naming"
)

// RefreshRealm refreshes the status of a realm by checking cgroup and containerd namespace.
// Returns the updated realm, whether it was updated, and any error.
func (r *Exec) RefreshRealm(realm intmodel.Realm) (intmodel.Realm, bool, error) {
	originalStatus := realm.Status
	newStatus := intmodel.RealmStatus{
		State:      intmodel.RealmStateUnknown, // Default to Unknown - will be updated if checks succeed
		CgroupPath: originalStatus.CgroupPath,
	}

	// Check cgroup existence
	cgroupExists, err := r.ExistsCgroup(realm)
	switch {
	case err != nil:
		r.logger.DebugContext(r.ctx, "failed to check realm cgroup", "realm", realm.Metadata.Name, "error", err)
		// If check fails, state remains Unknown (already set)
	case cgroupExists:
		newStatus.State = intmodel.RealmStateReady
	default:
		// Cgroup doesn't exist - realm is unknown
		newStatus.State = intmodel.RealmStateUnknown
	}

	// Check containerd namespace (only if cgroup check succeeded without error)
	if err == nil {
		namespace := realm.Spec.Namespace
		if namespace == "" {
			namespace = realm.Metadata.Name
		}
		namespaceExists, namespaceErr := r.ExistsRealmContainerdNamespace(namespace)
		switch {
		case namespaceErr != nil:
			r.logger.DebugContext(
				r.ctx,
				"failed to check realm namespace",
				"realm",
				realm.Metadata.Name,
				"error",
				namespaceErr,
			)
			// If namespace check fails, set state to Unknown (cannot determine state)
			newStatus.State = intmodel.RealmStateUnknown
		case namespaceExists && newStatus.State == intmodel.RealmStateReady:
			// Namespace exists and cgroup exists - realm is ready
			newStatus.State = intmodel.RealmStateReady
		case !namespaceExists && newStatus.State == intmodel.RealmStateReady:
			// Cgroup exists but namespace doesn't - realm is in inconsistent state
			newStatus.State = intmodel.RealmStateUnknown
		}
	}

	// Update if status changed
	updated := false
	if newStatus.State != originalStatus.State || newStatus.CgroupPath != originalStatus.CgroupPath {
		realm.Status = newStatus
		if updateErr := r.UpdateRealmMetadata(realm); updateErr != nil {
			return realm, false, fmt.Errorf("failed to update realm metadata: %w", updateErr)
		}
		updated = true
	}

	return realm, updated, nil
}

// RefreshSpace refreshes the status of a space by checking CNI config.
// Returns the updated space, whether it was updated, and any error.
func (r *Exec) RefreshSpace(space intmodel.Space) (intmodel.Space, bool, error) {
	originalStatus := space.Status
	newStatus := intmodel.SpaceStatus{
		State:      intmodel.SpaceStateUnknown, // Default to Unknown - will be updated if checks succeed
		CgroupPath: originalStatus.CgroupPath,
	}

	// Check CNI config existence
	cniExists, err := r.ExistsSpaceCNIConfig(space)
	switch {
	case err != nil:
		r.logger.DebugContext(r.ctx, "failed to check space CNI config", "space", space.Metadata.Name, "error", err)
		// If check fails, state remains Unknown (already set)
	case cniExists:
		newStatus.State = intmodel.SpaceStateReady
	default:
		// CNI config doesn't exist - space is unknown
		newStatus.State = intmodel.SpaceStateUnknown
	}

	// Check cgroup existence (only if CNI check succeeded without error)
	if err == nil {
		cgroupExists, cgroupErr := r.ExistsCgroup(space)
		switch {
		case cgroupErr != nil:
			r.logger.DebugContext(
				r.ctx,
				"failed to check space cgroup",
				"space",
				space.Metadata.Name,
				"error",
				cgroupErr,
			)
			// If cgroup check fails, set state to Unknown (cannot determine state)
			newStatus.State = intmodel.SpaceStateUnknown
		case cgroupExists && newStatus.State == intmodel.SpaceStateReady:
			// Both CNI and cgroup exist - space is ready
			newStatus.State = intmodel.SpaceStateReady
		case !cgroupExists && newStatus.State == intmodel.SpaceStateReady:
			// CNI exists but cgroup doesn't - space is in inconsistent state
			newStatus.State = intmodel.SpaceStateUnknown
		}
	}

	// Update if status changed
	updated := false
	if newStatus.State != originalStatus.State || newStatus.CgroupPath != originalStatus.CgroupPath {
		space.Status = newStatus
		if updateErr := r.UpdateSpaceMetadata(space); updateErr != nil {
			return space, false, fmt.Errorf("failed to update space metadata: %w", updateErr)
		}
		updated = true
	}

	return space, updated, nil
}

// RefreshStack refreshes the status of a stack by checking cgroup.
// Returns the updated stack, whether it was updated, and any error.
func (r *Exec) RefreshStack(stack intmodel.Stack) (intmodel.Stack, bool, error) {
	originalStatus := stack.Status
	newStatus := intmodel.StackStatus{
		State:      intmodel.StackStateUnknown, // Default to Unknown - will be updated if checks succeed
		CgroupPath: originalStatus.CgroupPath,
	}

	// Check cgroup existence
	cgroupExists, err := r.ExistsCgroup(stack)
	switch {
	case err != nil:
		r.logger.DebugContext(r.ctx, "failed to check stack cgroup", "stack", stack.Metadata.Name, "error", err)
		// If check fails, state remains Unknown (already set)
	case cgroupExists:
		newStatus.State = intmodel.StackStateReady
	default:
		// Cgroup doesn't exist - stack is unknown
		newStatus.State = intmodel.StackStateUnknown
	}

	// Update if status changed
	updated := false
	if newStatus.State != originalStatus.State || newStatus.CgroupPath != originalStatus.CgroupPath {
		stack.Status = newStatus
		if updateErr := r.UpdateStackMetadata(stack); updateErr != nil {
			return stack, false, fmt.Errorf("failed to update stack metadata: %w", updateErr)
		}
		updated = true
	}

	return stack, updated, nil
}

// RefreshCell refreshes the status of a cell and its containers.
// Returns the updated cell, number of containers updated, and any error.
func (r *Exec) RefreshCell(cell intmodel.Cell) (intmodel.Cell, int, error) {
	originalStatus := cell.Status
	newStatus := intmodel.CellStatus{
		State:      intmodel.CellStateUnknown, // Default to Unknown - will be updated if checks succeed
		CgroupPath: originalStatus.CgroupPath,
	}

	// Check cgroup existence
	cgroupExists, err := r.ExistsCgroup(cell)
	switch {
	case err != nil:
		r.logger.DebugContext(r.ctx, "failed to check cell cgroup", "cell", cell.Metadata.Name, "error", err)
		// If check fails, state remains Unknown (already set)
	case cgroupExists:
		newStatus.State = intmodel.CellStateReady
	default:
		// Cgroup doesn't exist - cell is unknown
		newStatus.State = intmodel.CellStateUnknown
	}

	// Refresh all containers in the cell (always attempt, regardless of cgroup check result)
	containersUpdated := 0
	for i := range cell.Spec.Containers {
		containerSpec := &cell.Spec.Containers[i]
		updated, containerErr := r.refreshContainerStatus(cell, containerSpec)
		if containerErr != nil {
			r.logger.WarnContext(r.ctx, "failed to refresh container status",
				"cell", cell.Metadata.Name,
				"container", containerSpec.ID,
				"error", containerErr)
			// Continue with other containers
		} else if updated {
			containersUpdated++
		}
	}

	// Update cell metadata if status changed or containers were updated
	cellUpdated := false
	if newStatus.State != originalStatus.State || newStatus.CgroupPath != originalStatus.CgroupPath {
		cell.Status = newStatus
		cellUpdated = true
	}

	if cellUpdated || containersUpdated > 0 {
		if updateErr := r.UpdateCellMetadata(cell); updateErr != nil {
			return cell, containersUpdated, fmt.Errorf("failed to update cell metadata: %w", updateErr)
		}
		return cell, containersUpdated, nil
	}

	return cell, containersUpdated, nil
}

// refreshContainerStatus refreshes the status of a container by introspecting containerd.
func (r *Exec) refreshContainerStatus(cell intmodel.Cell, containerSpec *intmodel.ContainerSpec) (bool, error) {
	// Get realm to access namespace
	lookupRealm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: cell.Spec.RealmName,
		},
	}
	internalRealm, err := r.GetRealm(lookupRealm)
	if err != nil {
		return false, fmt.Errorf("failed to get realm: %w", err)
	}

	namespace := internalRealm.Spec.Namespace
	if namespace == "" {
		namespace = internalRealm.Metadata.Name
	}

	// Set namespace for containerd operations
	if r.ctrClient != nil {
		r.ctrClient.SetNamespace(namespace)
	}

	// Get containerd ID
	containerdID := containerSpec.ContainerdID
	if containerdID == "" {
		// Build containerd ID if not set
		if containerSpec.Root {
			containerdID, err = naming.BuildRootContainerdID(
				cell.Spec.SpaceName,
				cell.Spec.StackName,
				cell.Spec.ID,
			)
		} else {
			containerdID, err = naming.BuildContainerdID(
				cell.Spec.SpaceName,
				cell.Spec.StackName,
				cell.Spec.ID,
				containerSpec.ID,
			)
		}
		if err != nil {
			return false, fmt.Errorf("failed to build containerd ID: %w", err)
		}
	}

	// Check if container exists in containerd
	containerExists := false
	if r.ctrClient != nil {
		containerExists, err = r.ExistsContainer(containerdID)
		if err != nil {
			r.logger.DebugContext(r.ctx, "failed to check container existence",
				"container", containerSpec.ID,
				"containerdID", containerdID,
				"error", err)
			// Continue - assume container doesn't exist
		}
	}

	// Get task status if container exists
	var taskStatus containerd.Status
	taskExists := false
	if containerExists && r.ctrClient != nil {
		taskStatus, err = r.ctrClient.TaskStatus(containerdID)
		if err != nil {
			// Task might not exist even if container does
			r.logger.DebugContext(r.ctx, "failed to get task status",
				"container", containerSpec.ID,
				"containerdID", containerdID,
				"error", err)
		} else {
			taskExists = true
		}
	}

	// Log container state for debugging
	switch {
	case taskExists:
		r.logger.DebugContext(r.ctx, "container task status",
			"container", containerSpec.ID,
			"containerdID", containerdID,
			"status", taskStatus.Status)
	case containerExists:
		r.logger.DebugContext(r.ctx, "container exists but no task",
			"container", containerSpec.ID,
			"containerdID", containerdID)
	default:
		r.logger.DebugContext(r.ctx, "container not found in containerd",
			"container", containerSpec.ID,
			"containerdID", containerdID)
	}

	// Update container's ContainerdID if it was missing and we successfully built it
	updated := false
	if containerSpec.ContainerdID == "" && containerdID != "" {
		containerSpec.ContainerdID = containerdID
		updated = true
	}

	return updated, nil
}
