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

	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// StopCell gracefully stops every task in the cell (workload containers
// first, then the root container) and deletes them, including tasks that had
// already exited. Each task gets SIGTERM, then SIGKILL after the timeout. The
// root is detached from the CNI network before it is stopped, while its
// network namespace is still valid, and the IPAM lease is purged afterwards.
// See classifyOperatorStop for the label the cell is left with.
func (r *Exec) StopCell(cell intmodel.Cell) (intmodel.Cell, error) {
	defer r.lockCell(cell)()
	return r.stopCellLocked(cell)
}

// stopCellLocked is the body of StopCell. The caller must hold the per-cell
// lifecycle lock (the StopCell wrapper, or a nesting op such as RecreateCell
// that already holds it). It must not be called without the lock held.
func (r *Exec) stopCellLocked(cell intmodel.Cell) (intmodel.Cell, error) {
	return r.operatorTeardownLocked(cell, teardownGraceful)
}

// StopContainer stops a specific container in a cell.
func (r *Exec) StopContainer(cell intmodel.Cell, containerID string) error {
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

	namespace := internalRealm.Spec.Namespace
	if namespace == "" {
		return fmt.Errorf("realm %q has no namespace", realmName)
	}

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

	// Root container cannot be stopped directly - it must be stopped by stopping the cell
	if foundContainerSpec.Root {
		return fmt.Errorf(
			"root container cannot be stopped directly, stop the cell instead using 'kuke stop %s'",
			cellName,
		)
	}

	// Use ContainerdID from spec
	containerdID := foundContainerSpec.ContainerdID
	if containerdID == "" {
		r.logger.WarnContext(
			r.ctx,
			"container has empty ContainerdID, skipping",
			"container",
			containerID,
		)
		return nil
	}

	// Use containerd ID for containerd operations
	timeout := 5 * time.Second
	_, err = r.ctrClient.StopContainer(namespace, containerdID, ctr.StopContainerOptions{
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

	// Issue #867: leave the containerd container record (and snapshot) in
	// place. The cell-wide cleanup comments in stopCellLocked above carry
	// the full rationale; this single-container path mirrors the cell-wide
	// stop's contract.
	return nil
}
