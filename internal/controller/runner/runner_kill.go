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
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/naming"
)

// KillCell immediately force-kills all containers in a cell (workload containers first, then root container).
// It detaches the root container from the CNI network before killing it.
func (r *Exec) KillCell(cell intmodel.Cell) error {
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

	// Convert internal cell back to external for accessing container specs
	cellDoc, convertErr := apischeme.BuildCellExternalFromInternal(internalCell, apischeme.VersionV1Beta1)
	if convertErr != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, convertErr)
	}

	cellID := cellDoc.Spec.ID
	if cellID == "" {
		return errdefs.ErrCellIDRequired
	}

	realmID := cellDoc.Spec.RealmID
	spaceID := cellDoc.Spec.SpaceID
	stackID := cellDoc.Spec.StackID

	cniConfigPath, cniErr := r.resolveSpaceCNIConfigPath(realmID, spaceID)
	if cniErr != nil {
		return fmt.Errorf("failed to resolve space CNI config: %w", cniErr)
	}

	// Create a background context for containerd operations
	ctrCtx := context.Background()

	// Always create a fresh client with background context to avoid cancellation issues
	if r.ctrClient != nil {
		_ = r.ctrClient.Close() // Ignore errors when closing old client
		r.ctrClient = nil
	}
	r.ctrClient = ctr.NewClient(context.Background(), r.logger, r.opts.ContainerdSocket)

	err = r.ctrClient.Connect()
	if err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	// Get realm to access namespace
	lookupRealm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: realmID,
		},
	}
	internalRealm, err := r.GetRealm(lookupRealm)
	if err != nil {
		return fmt.Errorf("failed to get realm: %w", err)
	}

	// Set namespace to realm namespace
	r.ctrClient.SetNamespace(internalRealm.Spec.Namespace)

	// Kill all workload containers first
	for _, containerSpec := range cellDoc.Spec.Containers {
		// Build container ID using hierarchical format
		var containerID string
		containerID, err = naming.BuildContainerName(
			containerSpec.SpaceID,
			containerSpec.StackID,
			containerSpec.CellID,
			containerSpec.ID,
		)
		if err != nil {
			return fmt.Errorf("failed to build container name: %w", err)
		}

		// Use container name with UUID for containerd operations
		err = r.killContainerTask(ctrCtx, containerID, internalRealm.Spec.Namespace)
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
	}

	// Kill root container last and detach from CNI
	rootContainerID, err := naming.BuildRootContainerName(spaceID, stackID, cellID)
	if err != nil {
		return fmt.Errorf("failed to build root container name: %w", err)
	}

	// Detach root container from CNI network
	r.detachRootContainerFromCNI(
		ctrCtx,
		rootContainerID,
		cniConfigPath,
		cellID,
		cellName,
		spaceID,
		realmID,
		internalRealm.Spec.Namespace,
	)

	// Kill root container
	err = r.killContainerTask(ctrCtx, rootContainerID, internalRealm.Spec.Namespace)
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

	return nil
}

// KillContainer immediately force-kills a specific container in a cell.
func (r *Exec) KillContainer(cell intmodel.Cell, containerID string) error {
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

	// Create a background context for containerd operations
	ctrCtx := context.Background()

	// Always create a fresh client with background context to avoid cancellation issues
	if r.ctrClient != nil {
		_ = r.ctrClient.Close() // Ignore errors when closing old client
		r.ctrClient = nil
	}
	r.ctrClient = ctr.NewClient(context.Background(), r.logger, r.opts.ContainerdSocket)

	err := r.ctrClient.Connect()
	if err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

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

	// Use container name directly for containerd operations
	err = r.killContainerTask(ctrCtx, containerID, internalRealm.Spec.Namespace)
	if err != nil {
		fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
		fields = append(fields, "space", spaceName, "realm", realmName, "err", fmt.Sprintf("%v", err))
		r.logger.ErrorContext(
			r.ctx,
			"failed to kill container",
			fields...,
		)
		return fmt.Errorf("failed to kill container %s: %w", containerID, err)
	}

	fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
	fields = append(fields, "space", spaceName, "realm", realmName)
	r.logger.InfoContext(
		r.ctx,
		"killed container",
		fields...,
	)

	return nil
}
