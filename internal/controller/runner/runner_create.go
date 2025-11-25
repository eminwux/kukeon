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
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func (r *Exec) CreateRealm(doc *v1beta1.RealmDoc) (*v1beta1.RealmDoc, error) {
	r.logger.Debug("run-path", "run-path", r.opts.RunPath)

	// Convert input to internal model at boundary
	realm, version, err := apischeme.NormalizeRealm(*doc)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	// Get existing realm (returns external model)
	rDoc, err := r.GetRealm(doc)
	if err != nil && !errors.Is(err, errdefs.ErrRealmNotFound) {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrGetRealm, err)
	}

	// Realm found, check if namespace exists
	if rDoc != nil {
		existingRealm, _, normalizeErr := apischeme.NormalizeRealm(*rDoc)
		if normalizeErr != nil {
			return nil, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, normalizeErr)
		}

		ensuredRealm, ensureErr := r.ensureRealmContainerdNamespace(existingRealm)
		if ensureErr != nil {
			return nil, ensureErr
		}

		ensuredRealm, ensureErr = r.ensureRealmCgroup(ensuredRealm)
		if ensureErr != nil {
			return nil, ensureErr
		}

		// Convert to external model for return
		ensuredRealmDoc, buildErr := apischeme.BuildRealmExternalFromInternal(ensuredRealm, version)
		if buildErr != nil {
			return nil, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, buildErr)
		}

		return &ensuredRealmDoc, nil
	}

	// Realm not found, create new realm
	realm.Status.State = intmodel.RealmStateCreating
	resultRealm, err := r.provisionNewRealm(realm)
	if err != nil {
		return nil, err
	}

	// Convert result back to external model at boundary
	resultDoc, err := apischeme.BuildRealmExternalFromInternal(resultRealm, version)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	return &resultDoc, nil
}

func (r *Exec) CreateStack(doc *v1beta1.StackDoc) (*v1beta1.StackDoc, error) {
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}

	// Convert input to internal model at boundary
	stack, version, err := apischeme.NormalizeStack(*doc)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	// Get existing stack (returns external model)
	sDoc, err := r.GetStack(doc)
	if err != nil && !errors.Is(err, errdefs.ErrStackNotFound) {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrGetStack, err)
	}

	// Stack found, ensure cgroup exists
	if sDoc != nil {
		existingStack, _, normalizeErr := apischeme.NormalizeStack(*sDoc)
		if normalizeErr != nil {
			return nil, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, normalizeErr)
		}

		ensuredStack, ensureErr := r.ensureStackCgroup(existingStack)
		if ensureErr != nil {
			return nil, ensureErr
		}

		// Convert to external model for return
		ensuredStackDoc, buildErr := apischeme.BuildStackExternalFromInternal(ensuredStack, version)
		if buildErr != nil {
			return nil, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, buildErr)
		}

		return &ensuredStackDoc, nil
	}

	// Stack not found, create new stack
	resultStack, err := r.provisionNewStack(stack)
	if err != nil {
		return nil, err
	}

	// Convert result back to external model at boundary
	resultDoc, err := apischeme.BuildStackExternalFromInternal(resultStack, version)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	return &resultDoc, nil
}

func (r *Exec) CreateCell(doc *v1beta1.CellDoc) (*v1beta1.CellDoc, error) {
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}

	// Convert input to internal model at boundary
	cell, version, err := apischeme.NormalizeCell(*doc)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	// Get existing cell (returns external model)
	cDoc, err := r.GetCell(doc)
	if err != nil && !errors.Is(err, errdefs.ErrCellNotFound) {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
	}

	// Cell found, ensure cgroup exists
	if cDoc != nil {
		existingCell, _, normalizeErr := apischeme.NormalizeCell(*cDoc)
		if normalizeErr != nil {
			return nil, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, normalizeErr)
		}

		ensuredCell, ensureErr := r.ensureCellCgroup(existingCell)
		if ensureErr != nil {
			return nil, ensureErr
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
			return nil, ensureErr
		}

		// Update metadata to persist the merged containers
		if err = r.UpdateCellMetadata(ensuredCell); err != nil {
			return nil, fmt.Errorf("%w: %w", errdefs.ErrUpdateCellMetadata, err)
		}

		// Convert to external model for return
		ensuredCellDoc, buildErr := apischeme.BuildCellExternalFromInternal(ensuredCell, version)
		if buildErr != nil {
			return nil, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, buildErr)
		}

		// Return external model
		return &ensuredCellDoc, nil
	}

	// Cell not found, create new cell
	resultCell, err := r.provisionNewCell(cell)
	if err != nil {
		return nil, err
	}

	// Convert result back to external model at boundary
	resultDoc, err := apischeme.BuildCellExternalFromInternal(resultCell, version)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	return &resultDoc, nil
}

func (r *Exec) CreateSpace(doc *v1beta1.SpaceDoc) (*v1beta1.SpaceDoc, error) {
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}

	// Convert input to internal model at boundary
	space, version, err := apischeme.NormalizeSpace(*doc)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	// Get existing space (returns external model)
	sDoc, err := r.GetSpace(doc)
	if err != nil && !errors.Is(err, errdefs.ErrSpaceNotFound) {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrGetSpace, err)
	}

	realmName := space.Spec.RealmName
	if sDoc != nil {
		existingSpace, _, normalizeErr := apischeme.NormalizeSpace(*sDoc)
		if normalizeErr != nil {
			return nil, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, normalizeErr)
		}
		if existingSpace.Spec.RealmName != "" {
			realmName = existingSpace.Spec.RealmName
		}
	}
	if realmName == "" {
		return nil, errdefs.ErrRealmNameRequired
	}
	realmDoc, realmErr := r.GetRealm(&v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{
			Name: realmName,
		},
	})
	if realmErr != nil {
		return nil, realmErr
	}

	// Space found, ensure CNI config exists
	if sDoc != nil {
		existingSpace, _, normalizeErr := apischeme.NormalizeSpace(*sDoc)
		if normalizeErr != nil {
			return nil, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, normalizeErr)
		}

		ensuredSpace, ensureErr := r.ensureSpaceCNIConfig(existingSpace)
		if ensureErr != nil {
			return nil, ensureErr
		}

		ensuredSpace, ensureErr = r.ensureSpaceCgroup(ensuredSpace, realmDoc)
		if ensureErr != nil {
			return nil, ensureErr
		}

		// Convert to external model for return
		ensuredSpaceDoc, buildErr := apischeme.BuildSpaceExternalFromInternal(ensuredSpace, version)
		if buildErr != nil {
			return nil, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, buildErr)
		}

		return &ensuredSpaceDoc, nil
	}

	// Space not found, create new space
	space.Status.State = intmodel.SpaceStateCreating
	resultSpace, err := r.provisionNewSpace(space)
	if err != nil {
		return nil, err
	}

	// Convert result back to external model at boundary
	resultDoc, err := apischeme.BuildSpaceExternalFromInternal(resultSpace, version)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	return &resultDoc, nil
}
