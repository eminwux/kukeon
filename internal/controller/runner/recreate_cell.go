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
func (r *Exec) RecreateCell(desired intmodel.Cell) (_ intmodel.Cell, retErr error) {
	defer r.lockCell(desired)()

	// Get existing cell
	existing, err := r.GetCell(desired)
	if err != nil {
		return intmodel.Cell{}, fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
	}

	// Once the destructive recreate begins creating fresh containers, any later
	// failure must roll the cell back — tear down the containers/IPAM already
	// created and stamp Failed — so a half-recreated cell never lingers as Ready
	// or Unknown (issue #718). Mirrors StartCell's provisionStarted gate (issue
	// #407). Armed immediately before createCellContainers and disarmed before
	// startCellLocked, which owns its own markCellFailed("StartCellFailed") for
	// the start phase; leaving the gate armed across it would re-run a redundant
	// kill cycle.
	var provisionStarted bool
	cellForCleanup := existing
	defer func() {
		if retErr != nil && provisionStarted {
			r.markCellFailed(cellForCleanup, "RecreateCellFailed", retErr)
		}
	}()

	// Stop all containers in the cell
	_, stopErr := r.stopCellLocked(existing)
	if stopErr != nil {
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

	// Delete all containers in the cell
	for _, containerSpec := range existing.Spec.Containers {
		containerID := containerSpec.ContainerdID
		if containerID == "" {
			continue
		}

		// Stop container
		_, err = r.ctrClient.StopContainer(internalRealm.Spec.Namespace, containerID, ctr.StopContainerOptions{})
		if err != nil {
			r.logger.WarnContext(
				r.ctx,
				"failed to stop container, continuing with deletion",
				"container", containerID,
				"error", err,
			)
		}

		// Delete container
		err = r.ctrClient.DeleteContainer(internalRealm.Spec.Namespace, containerID, ctr.ContainerDeleteOptions{
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

	// Release the root container's CNI/IPAM reservation before deleting it.
	// createCellContainers below rebuilds the root container record under this
	// same deterministic containerd ID, and the StartCell call at the end of
	// RecreateCell re-runs CNI ADD against it; without releasing first,
	// host-local IPAM rejects the re-ADD as a duplicate allocation (issue #630).
	// releaseCNI runs before the stop/delete so the netns is still valid for CNI
	// DEL where applicable; purgeCNI scrubs the residual allocation file afterward.
	// Resolve the purge's network name deterministically from realm+space so the
	// scrub runs even when space metadata is gone or corrupt (issue #685), matching
	// the stop/kill/delete teardown paths; the name is a pure function of (realm,
	// space).
	cellName := strings.TrimSpace(existing.Metadata.Name)
	cniConfigPath, _ := r.ResolveSpaceCNIConfigPath(realmName, spaceName)
	networkName := r.buildRootCNINetworkName(realmName, spaceName)
	_ = teardownRootContainerCNI(
		func() {
			r.detachRootContainerFromNetwork(
				rootContainerID, cniConfigPath, internalRealm.Spec.Namespace, cellID, cellName, spaceName, realmName,
			)
		},
		func() error {
			if _, rootStopErr := r.ctrClient.StopContainer(
				internalRealm.Spec.Namespace, rootContainerID, ctr.StopContainerOptions{Force: true},
			); rootStopErr != nil {
				r.logger.WarnContext(
					r.ctx,
					"failed to stop root container, continuing with deletion",
					"container", rootContainerID,
					"error", rootStopErr,
				)
			}

			delErr := r.ctrClient.DeleteContainer(
				internalRealm.Spec.Namespace,
				rootContainerID,
				ctr.ContainerDeleteOptions{
					SnapshotCleanup: true,
				},
			)
			if delErr != nil {
				r.logger.WarnContext(
					r.ctx,
					"failed to delete root container, continuing",
					"container", rootContainerID,
					"error", delErr,
				)
			}
			return delErr
		},
		func() {
			if networkName != "" {
				_ = r.purgeCNIForContainer(rootContainerID, "", networkName)
			}
		},
	)

	// Clear containerd IDs from desired cell so containers will be recreated
	for i := range desired.Spec.Containers {
		desired.Spec.Containers[i].ContainerdID = ""
	}

	// Preserve existing metadata and status where appropriate
	desired.Status.CgroupPath = existing.Status.CgroupPath

	// Past this point a failure has created fresh containers (and possibly an
	// IPAM reservation); arm the rollback defer so they don't leak.
	provisionStarted = true

	// Recreate cell containers (this will create root container and all child containers)
	_, err = r.createCellContainers(&desired)
	if err != nil {
		return intmodel.Cell{}, fmt.Errorf("failed to recreate cell containers: %w", err)
	}

	// Persist the recreated container records (new root spec + freshly assigned
	// containerd IDs) so StartCell's GetCell reads them. Ready is intentionally
	// NOT stamped here: createCellContainers only created container *records* —
	// no task is running and CNI ADD has not run yet (issue #682). Stamping
	// Ready now would leave the cell reporting Ready over created-but-not-started
	// containers if StartCell never ran.
	if updateErr := r.UpdateCellMetadata(desired); updateErr != nil {
		return intmodel.Cell{}, fmt.Errorf("%w: %w", errdefs.ErrUpdateCellMetadata, updateErr)
	}

	// Bring the recreated cell to the same running state a fresh create -> start
	// produces before Ready is stamped — mirrors the create branch in
	// apply/reconcile.go (CreateCell -> StartCell). StartCell starts the tasks,
	// runs CNI ADD against the deterministic root ID, and stamps + persists Ready
	// only once the cell is actually up.
	// startCellLocked owns its own failure cleanup (markCellFailed with reason
	// "StartCellFailed", issue #407), so disarm the local gate to avoid a
	// redundant kill cycle on the start path.
	provisionStarted = false
	started, startErr := r.startCellLocked(desired)
	if startErr != nil {
		return intmodel.Cell{}, fmt.Errorf("failed to start recreated cell: %w", startErr)
	}

	return started, nil
}
