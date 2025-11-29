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

package controller

import (
	"errors"
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// CreateCellResult reports reconciliation outcomes for a cell.
type CreateCellResult struct {
	Cell intmodel.Cell

	MetadataExistsPre       bool
	MetadataExistsPost      bool
	CgroupExistsPre         bool
	CgroupExistsPost        bool
	CgroupCreated           bool
	RootContainerExistsPre  bool
	RootContainerExistsPost bool
	RootContainerCreated    bool
	StartedPre              bool
	StartedPost             bool
	Started                 bool
	Created                 bool

	Containers []ContainerCreationOutcome
}

type ContainerCreationOutcome struct {
	Name       string
	ExistsPre  bool
	ExistsPost bool
	Created    bool
}

// CreateCell creates a new cell or ensures an existing cell's resources exist.
// It returns a CreateCellResult and an error.
// The CreateCellResult reports the state of cell resources before and after the operation,
// indicating what was created vs what already existed, including container-level outcomes.
// The error is returned if the cell name is required, the realm name is required,
// the space name is required, the stack name is required, the cell cgroup does not exist,
// the root container does not exist, or the cell creation fails.
func (b *Exec) CreateCell(cell intmodel.Cell) (CreateCellResult, error) {
	var res CreateCellResult

	name := strings.TrimSpace(cell.Metadata.Name)
	if name == "" {
		return res, errdefs.ErrCellNameRequired
	}
	realm := strings.TrimSpace(cell.Spec.RealmName)
	if realm == "" {
		return res, errdefs.ErrRealmNameRequired
	}
	space := strings.TrimSpace(cell.Spec.SpaceName)
	if space == "" {
		return res, errdefs.ErrSpaceNameRequired
	}
	stack := strings.TrimSpace(cell.Spec.StackName)
	if stack == "" {
		return res, errdefs.ErrStackNameRequired
	}

	// Ensure default labels are set
	if cell.Metadata.Labels == nil {
		cell.Metadata.Labels = make(map[string]string)
	}
	if _, exists := cell.Metadata.Labels[consts.KukeonRealmLabelKey]; !exists {
		cell.Metadata.Labels[consts.KukeonRealmLabelKey] = realm
	}
	if _, exists := cell.Metadata.Labels[consts.KukeonSpaceLabelKey]; !exists {
		cell.Metadata.Labels[consts.KukeonSpaceLabelKey] = space
	}
	if _, exists := cell.Metadata.Labels[consts.KukeonStackLabelKey]; !exists {
		cell.Metadata.Labels[consts.KukeonStackLabelKey] = stack
	}
	if _, exists := cell.Metadata.Labels[consts.KukeonCellLabelKey]; !exists {
		cell.Metadata.Labels[consts.KukeonCellLabelKey] = name
	}

	// Ensure Spec.ID is set
	if cell.Spec.ID == "" {
		cell.Spec.ID = name
	}

	// Ensure container ownership (work with internal types)
	cell.Spec.Containers = ensureContainerOwnershipInternal(cell.Spec.Containers, realm, space, stack, name)

	preContainerExists := make(map[string]bool)

	// Build minimal internal cell for GetCell lookup
	lookupCell := intmodel.Cell{
		Metadata: intmodel.CellMetadata{
			Name: name,
		},
		Spec: intmodel.CellSpec{
			RealmName: realm,
			SpaceName: space,
			StackName: stack,
		},
	}

	// Check if cell already exists
	internalCellPre, err := b.runner.GetCell(lookupCell)
	var resultCell intmodel.Cell
	var wasCreated bool

	if err != nil {
		// Cell not found, create new cell
		if errors.Is(err, errdefs.ErrCellNotFound) {
			res.MetadataExistsPre = false
		} else {
			return res, fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
		}

		// Create new cell
		resultCell, err = b.runner.CreateCell(cell)
		if err != nil {
			return res, fmt.Errorf("%w: %w", errdefs.ErrCreateCell, err)
		}

		wasCreated = true
	} else {
		// Cell found, check pre-state for result reporting (EnsureCell will also check internally)
		res.MetadataExistsPre = true
		res.CgroupExistsPre, err = b.runner.ExistsCgroup(internalCellPre)
		if err != nil {
			return res, fmt.Errorf("failed to check if cell cgroup exists: %w", err)
		}
		res.RootContainerExistsPre, err = b.runner.ExistsCellRootContainer(internalCellPre)
		if err != nil {
			return res, fmt.Errorf("failed to check root container: %w", err)
		}
		for _, container := range internalCellPre.Spec.Containers {
			id := strings.TrimSpace(container.ID)
			if id != "" {
				preContainerExists[id] = true
			}
		}
		res.StartedPre = false

		// Ensure resources exist (EnsureCell checks/ensures internally)
		resultCell, err = b.runner.EnsureCell(internalCellPre)
		if err != nil {
			return res, fmt.Errorf("%w: %w", errdefs.ErrCreateCell, err)
		}

		wasCreated = false
	}

	// Start cell containers (both new and existing cells need to be started)
	if err = b.runner.StartCell(resultCell); err != nil {
		return res, fmt.Errorf("failed to start cell containers: %w", err)
	}

	// Build post-container existence map from resultCell directly
	postContainerExists := make(map[string]bool)
	for _, container := range resultCell.Spec.Containers {
		id := strings.TrimSpace(container.ID)
		if id != "" {
			postContainerExists[id] = true
		}
	}

	// Set result fields
	res.Cell = resultCell
	res.MetadataExistsPost = true
	// After CreateCell/EnsureCell, cgroup and root container are guaranteed to exist
	res.CgroupExistsPost = true
	res.RootContainerExistsPost = true
	res.StartedPost = true
	res.Created = wasCreated
	if wasCreated {
		// New cell: all resources were created
		res.CgroupCreated = true
		res.RootContainerCreated = true
		res.Started = true
	} else {
		// Existing cell: resources were created only if they didn't exist before
		res.CgroupCreated = !res.CgroupExistsPre
		res.RootContainerCreated = !res.RootContainerExistsPre
		res.Started = !res.StartedPre
	}

	// Build container creation outcomes
	for _, container := range cell.Spec.Containers {
		id := strings.TrimSpace(container.ID)
		if id == "" {
			continue
		}
		created := !preContainerExists[id] && postContainerExists[id]
		res.Containers = append(res.Containers, ContainerCreationOutcome{
			Name:       id,
			ExistsPre:  preContainerExists[id],
			ExistsPost: postContainerExists[id],
			Created:    created,
		})
	}

	return res, nil
}
