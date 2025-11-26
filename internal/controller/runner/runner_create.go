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

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

func (r *Exec) CreateRealm(realm intmodel.Realm) (intmodel.Realm, error) {
	r.logger.Debug("run-path", "run-path", r.opts.RunPath)

	// Build minimal internal realm for GetRealm lookup
	lookupRealm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: realm.Metadata.Name,
		},
	}

	// Get existing realm (returns internal model)
	existingRealm, err := r.GetRealm(lookupRealm)
	if err != nil && !errors.Is(err, errdefs.ErrRealmNotFound) {
		return intmodel.Realm{}, fmt.Errorf("%w: %w", errdefs.ErrGetRealm, err)
	}

	// Realm found, check if namespace exists
	if err == nil {
		ensuredRealm, ensureErr := r.ensureRealmContainerdNamespace(existingRealm)
		if ensureErr != nil {
			return intmodel.Realm{}, ensureErr
		}

		ensuredRealm, ensureErr = r.ensureRealmCgroup(ensuredRealm)
		if ensureErr != nil {
			return intmodel.Realm{}, ensureErr
		}

		return ensuredRealm, nil
	}

	// Realm not found, create new realm
	realm.Status.State = intmodel.RealmStateCreating
	resultRealm, err := r.provisionNewRealm(realm)
	if err != nil {
		return intmodel.Realm{}, err
	}

	return resultRealm, nil
}

func (r *Exec) CreateStack(stack intmodel.Stack) (intmodel.Stack, error) {
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}

	// Build minimal internal stack for GetStack lookup
	lookupStack := intmodel.Stack{
		Metadata: intmodel.StackMetadata{
			Name: stack.Metadata.Name,
		},
		Spec: intmodel.StackSpec{
			RealmName: stack.Spec.RealmName,
			SpaceName: stack.Spec.SpaceName,
		},
	}

	// Get existing stack (returns internal model)
	existingStack, err := r.GetStack(lookupStack)
	if err != nil && !errors.Is(err, errdefs.ErrStackNotFound) {
		return intmodel.Stack{}, fmt.Errorf("%w: %w", errdefs.ErrGetStack, err)
	}

	// Stack found, ensure cgroup exists
	if err == nil {
		ensuredStack, ensureErr := r.ensureStackCgroup(existingStack)
		if ensureErr != nil {
			return intmodel.Stack{}, ensureErr
		}

		return ensuredStack, nil
	}

	// Stack not found, create new stack
	resultStack, err := r.provisionNewStack(stack)
	if err != nil {
		return intmodel.Stack{}, err
	}

	return resultStack, nil
}

func (r *Exec) CreateCell(cell intmodel.Cell) (intmodel.Cell, error) {
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}

	// Build minimal internal cell for GetCell lookup
	lookupCell := intmodel.Cell{
		Metadata: intmodel.CellMetadata{
			Name: cell.Metadata.Name,
		},
		Spec: intmodel.CellSpec{
			RealmName: cell.Spec.RealmName,
			SpaceName: cell.Spec.SpaceName,
			StackName: cell.Spec.StackName,
		},
	}

	// Get existing cell (returns internal model)
	existingCell, err := r.GetCell(lookupCell)
	if err != nil && !errors.Is(err, errdefs.ErrCellNotFound) {
		return intmodel.Cell{}, fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
	}

	// Cell found, ensure cgroup exists
	if !errors.Is(err, errdefs.ErrCellNotFound) {
		ensuredCell, ensureErr := r.ensureCellCgroup(existingCell)
		if ensureErr != nil {
			return intmodel.Cell{}, ensureErr
		}

		// Merge containers from the new cell into the existing cell
		// This ensures containers specified in the new cell are created even if
		// they weren't in the stored cell document
		if len(cell.Spec.Containers) > 0 {
			// Log containers being merged
			r.logger.DebugContext(
				r.ctx,
				"merging containers into existing cell",
				"cell", ensuredCell.Metadata.Name,
				"existingContainerCount", len(ensuredCell.Spec.Containers),
				"newContainerCount", len(cell.Spec.Containers),
			)

			// Create a map of existing container IDs to avoid duplicates
			existingContainerIDs := make(map[string]bool)
			for _, container := range ensuredCell.Spec.Containers {
				existingContainerIDs[container.ID] = true
				r.logger.DebugContext(
					r.ctx,
					"existing container in cell",
					"cell", ensuredCell.Metadata.Name,
					"containerID", container.ID,
				)
			}
			// Add containers from the new cell that don't already exist
			for _, container := range cell.Spec.Containers {
				r.logger.DebugContext(
					r.ctx,
					"checking if container should be merged",
					"cell", ensuredCell.Metadata.Name,
					"containerID", container.ID,
					"alreadyExists", existingContainerIDs[container.ID],
				)
				if !existingContainerIDs[container.ID] {
					ensuredCell.Spec.Containers = append(ensuredCell.Spec.Containers, container)
					r.logger.DebugContext(
						r.ctx,
						"merged new container into cell",
						"cell", ensuredCell.Metadata.Name,
						"containerID", container.ID,
						"totalContainers", len(ensuredCell.Spec.Containers),
					)
				}
			}
		}

		// Log final container count before ensuring containers
		r.logger.DebugContext(
			r.ctx,
			"calling ensureCellContainers",
			"cell", ensuredCell.Metadata.Name,
			"containerCount", len(ensuredCell.Spec.Containers),
		)

		_, ensureErr = r.ensureCellContainers(ensuredCell)
		if ensureErr != nil {
			return intmodel.Cell{}, ensureErr
		}

		// Update metadata to persist the merged containers
		if err = r.UpdateCellMetadata(ensuredCell); err != nil {
			return intmodel.Cell{}, fmt.Errorf("%w: %w", errdefs.ErrUpdateCellMetadata, err)
		}

		return ensuredCell, nil
	}

	// Cell not found, create new cell
	resultCell, err := r.provisionNewCell(cell)
	if err != nil {
		return intmodel.Cell{}, err
	}

	return resultCell, nil
}

func (r *Exec) CreateSpace(space intmodel.Space) (intmodel.Space, error) {
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}

	// Build minimal internal space for GetSpace lookup
	lookupSpace := intmodel.Space{
		Metadata: intmodel.SpaceMetadata{
			Name: space.Metadata.Name,
		},
		Spec: intmodel.SpaceSpec{
			RealmName: space.Spec.RealmName,
		},
	}

	// Get existing space (returns internal model)
	existingSpace, err := r.GetSpace(lookupSpace)
	if err != nil && !errors.Is(err, errdefs.ErrSpaceNotFound) {
		return intmodel.Space{}, fmt.Errorf("%w: %w", errdefs.ErrGetSpace, err)
	}

	realmName := space.Spec.RealmName
	if err == nil {
		if existingSpace.Spec.RealmName != "" {
			realmName = existingSpace.Spec.RealmName
		}
	}
	if realmName == "" {
		return intmodel.Space{}, errdefs.ErrRealmNameRequired
	}

	// Build minimal internal realm for GetRealm lookup
	lookupRealm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: realmName,
		},
	}

	internalRealm, realmErr := r.GetRealm(lookupRealm)
	if realmErr != nil {
		return intmodel.Space{}, realmErr
	}

	// Convert internal realm back to external for ensureSpaceCgroup
	realmDoc, convertErr := apischeme.BuildRealmExternalFromInternal(internalRealm, apischeme.VersionV1Beta1)
	if convertErr != nil {
		return intmodel.Space{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, convertErr)
	}

	// Space found, ensure CNI config exists
	if err == nil {
		ensuredSpace, ensureErr := r.ensureSpaceCNIConfig(existingSpace)
		if ensureErr != nil {
			return intmodel.Space{}, ensureErr
		}

		ensuredSpace, ensureErr = r.ensureSpaceCgroup(ensuredSpace, &realmDoc)
		if ensureErr != nil {
			return intmodel.Space{}, ensureErr
		}

		return ensuredSpace, nil
	}

	// Space not found, create new space
	space.Status.State = intmodel.SpaceStateCreating
	resultSpace, err := r.provisionNewSpace(space)
	if err != nil {
		return intmodel.Space{}, err
	}

	return resultSpace, nil
}
