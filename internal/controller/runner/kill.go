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

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// KillCell SIGKILLs every task in the cell (workload containers first, then
// the root container), waits (bounded) for each to exit and deletes it,
// including tasks that had already exited. It differs from StopCell only in
// the first signal, so both verbs leave the same container end state (#1557).
// See classifyOperatorStop for the label the cell is left with.
func (r *Exec) KillCell(cell intmodel.Cell) (intmodel.Cell, error) {
	defer r.lockCell(cell)()
	return r.killCellLocked(cell)
}

// killCellLocked is the body of KillCell. The caller must hold the per-cell
// lifecycle lock (the KillCell wrapper, or a nesting op such as StartCell's
// failure cleanup or ReconcileCell's auto-delete that already holds it). It
// must not be called without the lock held.
func (r *Exec) killCellLocked(cell intmodel.Cell) (intmodel.Cell, error) {
	return r.operatorTeardownLocked(cell, teardownKill)
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
			"root container cannot be killed directly, kill the cell instead using 'kuke kill %s'",
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
	err = r.killContainerTask(internalRealm.Spec.Namespace, containerdID)
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

	// Issue #867: leave the containerd container record (and snapshot) in
	// place. The cell-wide cleanup comments in killCellLocked above carry
	// the full rationale; this single-container path mirrors the cell-wide
	// kill's contract.
	return nil
}
