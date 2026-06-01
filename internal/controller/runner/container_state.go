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

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/naming"
)

// ContainerObservation bundles the slice of containerd task state the runner
// needs to surface in ContainerStatus and drive lifecycle decisions: the
// internal-state mapping plus the exit code reported by the task. ExitCode is
// only meaningful on the TaskStatus-success branch — on every other branch
// (NotCreated, ErrTaskNotFound, transient error, fallback Unknown) it stays
// zero.
type ContainerObservation struct {
	State    intmodel.ContainerState
	ExitCode int
}

// GetContainerState queries containerd for the actual task status of a container
// and converts it to the internal ContainerState. Thin wrapper around
// GetContainerObservation for callers that only need the state column.
func (r *Exec) GetContainerState(cell intmodel.Cell, containerID string) (intmodel.ContainerState, error) {
	obs, err := r.GetContainerObservation(cell, containerID)
	return obs.State, err
}

// GetContainerObservation is GetContainerState plus the task's exit code.
// Behaves identically on the existence/namespace/task-not-found edges so
// callers can swap from GetContainerState without observability gaps; the
// ExitCode field is only populated on the TaskStatus-success branch (every
// other return path leaves it zero).
func (r *Exec) GetContainerObservation(cell intmodel.Cell, containerID string) (ContainerObservation, error) {
	containerID = strings.TrimSpace(containerID)
	if containerID == "" {
		return ContainerObservation{State: intmodel.ContainerStateUnknown}, errors.New("container ID is required")
	}

	cellName := strings.TrimSpace(cell.Metadata.Name)
	if cellName == "" {
		return ContainerObservation{State: intmodel.ContainerStateUnknown}, errdefs.ErrCellNotFound
	}

	realmName := strings.TrimSpace(cell.Spec.RealmName)
	if realmName == "" {
		return ContainerObservation{State: intmodel.ContainerStateUnknown}, errdefs.ErrRealmNameRequired
	}

	spaceName := strings.TrimSpace(cell.Spec.SpaceName)
	if spaceName == "" {
		return ContainerObservation{State: intmodel.ContainerStateUnknown}, errdefs.ErrSpaceNameRequired
	}

	stackName := strings.TrimSpace(cell.Spec.StackName)
	if stackName == "" {
		return ContainerObservation{State: intmodel.ContainerStateUnknown}, errdefs.ErrStackNameRequired
	}

	if err := r.ensureClientConnected(); err != nil {
		r.logger.InfoContext(r.ctx, "failed to connect to containerd",
			"container", containerID,
			"error", err)
		return ContainerObservation{State: intmodel.ContainerStateUnknown},
			fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
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
		return ContainerObservation{State: intmodel.ContainerStateUnknown},
			fmt.Errorf("failed to get realm: %w", err)
	}

	namespace := internalRealm.Spec.Namespace
	if namespace == "" {
		// Fallback to <realm>.kukeon.io if namespace is not set
		namespace = consts.RealmNamespace(realmName)
		r.logger.DebugContext(r.ctx, "realm has no namespace, deriving from realm name",
			"realm", realmName,
			"namespace", namespace)
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
		return ContainerObservation{State: intmodel.ContainerStateUnknown}, nil
	}

	// Get cell ID (use Spec.ID if available, otherwise fall back to Metadata.Name)
	cellID := strings.TrimSpace(cell.Spec.ID)
	if cellID == "" {
		cellID = strings.TrimSpace(cell.Metadata.Name)
	}
	if cellID == "" {
		return ContainerObservation{State: intmodel.ContainerStateUnknown}, errdefs.ErrCellIDRequired
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
			return ContainerObservation{State: intmodel.ContainerStateUnknown},
				fmt.Errorf("failed to build containerd ID: %w", err)
		}
	}

	r.logger.DebugContext(r.ctx, "querying container state",
		"container", containerID,
		"containerdID", containerdID,
		"namespace", namespace)

	// Check if container exists in containerd
	containerExists, err := r.ctrClient.ExistsContainer(namespace, containerdID)
	if err != nil {
		r.logger.InfoContext(r.ctx, "failed to check container existence",
			"container", containerID,
			"containerdID", containerdID,
			"error", err)
		return ContainerObservation{State: intmodel.ContainerStateUnknown}, nil
	}

	if !containerExists {
		// No containerd record at all — distinct from Stopped (a record that
		// exists but whose task is gone, handled below). Reporting must not
		// collapse the two: an absent record means the container was never
		// realized or was reaped/lost, which an operator needs to tell apart
		// from a normal clean stop (#670). The reconciler treats NotCreated
		// exactly like Stopped for its lifecycle decisions — see refresh.go.
		r.logger.InfoContext(r.ctx, "container does not exist in containerd",
			"container", containerID,
			"containerdID", containerdID,
			"namespace", namespace)
		return ContainerObservation{State: intmodel.ContainerStateNotCreated}, nil
	}

	// Get container state using TaskStatus from ctr package
	taskStatus, taskStatusErr := r.ctrClient.TaskStatus(namespace, containerdID)
	if taskStatusErr == nil {
		// TaskStatus succeeded - convert and return. ExitStatus is the
		// uint32 exit code containerd records for the task; it is only
		// meaningful on Stopped tasks (Running/Created/Paused leave the
		// field unobserved) but the value is safe to surface unconditionally
		// — non-terminal callers read the State column and ignore ExitCode.
		state := ctr.ConvertContainerdStatusToContainerState(taskStatus)
		exitCode := int(taskStatus.ExitStatus)
		r.logger.InfoContext(r.ctx, "container state determined via TaskStatus",
			"container", containerID,
			"containerdID", containerdID,
			"namespace", namespace,
			"taskStatus", taskStatus.Status,
			"internalState", state,
			"exitCode", exitCode)
		return ContainerObservation{State: state, ExitCode: exitCode}, nil
	}

	// TaskStatus failed against an existing container: the container record
	// survived but its task is gone. The headline case is a host reboot
	// (cgroups + tasks wiped, container records survive in containerd's
	// boltdb): without this branch the cell-level derivation can never see
	// past Unknown and the reconciler leaves every previously-Ready cell
	// stuck (#543). A wrapped ErrTaskNotFound is the "no task" signal
	// loadTask emits via container.Task() — anything else is treated as a
	// transient containerd RPC blip and stays Unknown to avoid reaping a
	// cell on a misread.
	if errors.Is(taskStatusErr, errdefs.ErrTaskNotFound) {
		r.logger.InfoContext(r.ctx, "container exists but task is gone; reporting Stopped",
			"container", containerID,
			"containerdID", containerdID,
			"namespace", namespace,
			"error", taskStatusErr)
		return ContainerObservation{State: intmodel.ContainerStateStopped}, nil
	}

	// TaskStatus failed - return Unknown since we can't determine the state
	r.logger.InfoContext(r.ctx, "failed to get container state via TaskStatus",
		"container", containerID,
		"containerdID", containerdID,
		"namespace", namespace,
		"error", taskStatusErr)
	return ContainerObservation{State: intmodel.ContainerStateUnknown}, nil
}
