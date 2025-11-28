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

// CreateStackResult reports reconciliation outcomes for a stack.
type CreateStackResult struct {
	Stack intmodel.Stack

	MetadataExistsPre  bool
	MetadataExistsPost bool
	CgroupExistsPre    bool
	CgroupExistsPost   bool
	CgroupCreated      bool
	Created            bool
}

func (b *Exec) CreateStack(stack intmodel.Stack) (CreateStackResult, error) {
	var res CreateStackResult

	name := strings.TrimSpace(stack.Metadata.Name)
	if name == "" {
		return res, errdefs.ErrStackNameRequired
	}
	realm := strings.TrimSpace(stack.Spec.RealmName)
	if realm == "" {
		return res, errdefs.ErrRealmNameRequired
	}
	space := strings.TrimSpace(stack.Spec.SpaceName)
	if space == "" {
		return res, errdefs.ErrSpaceNameRequired
	}

	// Ensure default labels are set
	if stack.Metadata.Labels == nil {
		stack.Metadata.Labels = make(map[string]string)
	}
	if _, exists := stack.Metadata.Labels[consts.KukeonRealmLabelKey]; !exists {
		stack.Metadata.Labels[consts.KukeonRealmLabelKey] = realm
	}
	if _, exists := stack.Metadata.Labels[consts.KukeonSpaceLabelKey]; !exists {
		stack.Metadata.Labels[consts.KukeonSpaceLabelKey] = space
	}
	if _, exists := stack.Metadata.Labels[consts.KukeonStackLabelKey]; !exists {
		stack.Metadata.Labels[consts.KukeonStackLabelKey] = name
	}

	// Ensure Spec.ID is set
	if stack.Spec.ID == "" {
		stack.Spec.ID = name
	}

	// Build minimal internal stack for GetStack lookup
	lookupStackPre := intmodel.Stack{
		Metadata: intmodel.StackMetadata{
			Name: name,
		},
		Spec: intmodel.StackSpec{
			RealmName: realm,
			SpaceName: space,
		},
	}
	internalStackPre, err := b.runner.GetStack(lookupStackPre)
	if err != nil {
		if errors.Is(err, errdefs.ErrStackNotFound) {
			res.MetadataExistsPre = false
		} else {
			return res, fmt.Errorf("%w: %w", errdefs.ErrGetStack, err)
		}
	} else {
		res.MetadataExistsPre = true
		// Verify space exists before checking cgroup to provide better error message
		verifySpace := intmodel.Space{
			Metadata: intmodel.SpaceMetadata{
				Name: space,
			},
			Spec: intmodel.SpaceSpec{
				RealmName: realm,
			},
		}
		_, spaceErr := b.runner.GetSpace(verifySpace)
		if spaceErr != nil {
			return res, fmt.Errorf("space %q not found at run-path %q: %w", space, b.opts.RunPath, spaceErr)
		}
		res.CgroupExistsPre, err = b.runner.ExistsCgroup(internalStackPre)
		if err != nil {
			return res, fmt.Errorf("failed to check if stack cgroup exists: %w", err)
		}
	}

	// Call runner with internal type
	resultStack, err := b.runner.CreateStack(stack)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrCreateStack, err)
	}

	// Build minimal internal stack for GetStack lookup
	lookupStackPost := intmodel.Stack{
		Metadata: intmodel.StackMetadata{
			Name: name,
		},
		Spec: intmodel.StackSpec{
			RealmName: realm,
			SpaceName: space,
		},
	}
	internalStackPost, err := b.runner.GetStack(lookupStackPost)
	if err != nil {
		if errors.Is(err, errdefs.ErrStackNotFound) {
			res.MetadataExistsPost = false
		} else {
			return res, fmt.Errorf("%w: %w", errdefs.ErrGetStack, err)
		}
	} else {
		res.MetadataExistsPost = true
		// Verify space exists before checking cgroup to provide better error message
		verifySpace := intmodel.Space{
			Metadata: intmodel.SpaceMetadata{
				Name: space,
			},
			Spec: intmodel.SpaceSpec{
				RealmName: realm,
			},
		}
		_, spaceErr := b.runner.GetSpace(verifySpace)
		if spaceErr != nil {
			return res, fmt.Errorf("space %q not found at run-path %q: %w", space, b.opts.RunPath, spaceErr)
		}
		res.CgroupExistsPost, err = b.runner.ExistsCgroup(internalStackPost)
		if err != nil {
			return res, fmt.Errorf("failed to check if stack cgroup exists: %w", err)
		}
		// Use the result from CreateStack instead of GetStack to ensure consistency
		res.Stack = resultStack
	}

	res.Created = !res.MetadataExistsPre && res.MetadataExistsPost
	res.CgroupCreated = !res.CgroupExistsPre && res.CgroupExistsPost

	return res, nil
}

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

	lookupCellPre := intmodel.Cell{
		Metadata: intmodel.CellMetadata{
			Name: name,
		},
		Spec: intmodel.CellSpec{
			RealmName: realm,
			SpaceName: space,
			StackName: stack,
		},
	}
	internalCellPre, err := b.runner.GetCell(lookupCellPre)
	if err != nil {
		if errors.Is(err, errdefs.ErrCellNotFound) {
			res.MetadataExistsPre = false
		} else {
			return res, fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
		}
	} else {
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
	}

	// Call runner with internal type
	resultCell, err := b.runner.CreateCell(cell)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrCreateCell, err)
	}

	// Convert external cell to internal for runner.StartCell (use the same internal cell from CreateCell)
	// Since resultCell is already internal, we can use it directly
	if err = b.runner.StartCell(resultCell); err != nil {
		return res, fmt.Errorf("failed to start cell containers: %w", err)
	}

	postContainerExists := make(map[string]bool)

	lookupCellPost := intmodel.Cell{
		Metadata: intmodel.CellMetadata{
			Name: name,
		},
		Spec: intmodel.CellSpec{
			RealmName: realm,
			SpaceName: space,
			StackName: stack,
		},
	}
	internalCellPost, err := b.runner.GetCell(lookupCellPost)
	if err != nil {
		if errors.Is(err, errdefs.ErrCellNotFound) {
			res.MetadataExistsPost = false
		} else {
			return res, fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
		}
	} else {
		res.MetadataExistsPost = true
		res.CgroupExistsPost, err = b.runner.ExistsCgroup(internalCellPost)
		if err != nil {
			return res, fmt.Errorf("failed to check if cell cgroup exists: %w", err)
		}
		res.RootContainerExistsPost, err = b.runner.ExistsCellRootContainer(internalCellPost)
		if err != nil {
			return res, fmt.Errorf("failed to check root container: %w", err)
		}
		for _, container := range internalCellPost.Spec.Containers {
			id := strings.TrimSpace(container.ID)
			if id != "" {
				postContainerExists[id] = true
			}
		}
		res.StartedPost = true
		// Use the result from CreateCell instead of GetCell to ensure consistency
		res.Cell = resultCell
	}

	res.Created = !res.MetadataExistsPre && res.MetadataExistsPost
	res.CgroupCreated = !res.CgroupExistsPre && res.CgroupExistsPost
	res.RootContainerCreated = !res.RootContainerExistsPre && res.RootContainerExistsPost
	res.Started = !res.StartedPre && res.StartedPost

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

// CreateContainerResult reports reconciliation outcomes for container creation within a cell.
type CreateContainerResult struct {
	Container intmodel.Container

	CellMetadataExistsPre  bool
	CellMetadataExistsPost bool
	ContainerExistsPre     bool
	ContainerExistsPost    bool
	ContainerCreated       bool
	Started                bool
}

func (b *Exec) CreateContainer(container intmodel.Container) (CreateContainerResult, error) {
	var res CreateContainerResult

	containerName := strings.TrimSpace(container.Metadata.Name)
	if containerName == "" {
		containerName = strings.TrimSpace(container.Spec.ID)
	}
	if containerName == "" {
		return res, errdefs.ErrContainerNameRequired
	}
	if strings.TrimSpace(container.Spec.ID) == "" {
		container.Spec.ID = containerName
	}

	realm := strings.TrimSpace(container.Spec.RealmName)
	if realm == "" {
		return res, errdefs.ErrRealmNameRequired
	}
	space := strings.TrimSpace(container.Spec.SpaceName)
	if space == "" {
		return res, errdefs.ErrSpaceNameRequired
	}
	stack := strings.TrimSpace(container.Spec.StackName)
	if stack == "" {
		return res, errdefs.ErrStackNameRequired
	}
	cellName := strings.TrimSpace(container.Spec.CellName)
	if cellName == "" {
		return res, errdefs.ErrCellNameRequired
	}
	image := strings.TrimSpace(container.Spec.Image)
	if image == "" {
		return res, errdefs.ErrInvalidImage
	}

	// Build internal Cell with container spec to merge
	cellInternal := intmodel.Cell{
		Metadata: intmodel.CellMetadata{
			Name: cellName,
		},
		Spec: intmodel.CellSpec{
			ID:        cellName,
			RealmName: realm,
			SpaceName: space,
			StackName: stack,
			Containers: []intmodel.ContainerSpec{
				{
					ID:        container.Spec.ID,
					RealmName: realm,
					SpaceName: space,
					StackName: stack,
					CellName:  cellName,
					Image:     image,
					Command:   container.Spec.Command,
					Args:      container.Spec.Args,
				},
			},
		},
	}

	// Log the container spec being created
	b.logger.DebugContext(
		b.ctx,
		"creating container in cell",
		"containerName", containerName,
		"cell", cellName,
		"realm", realm,
		"space", space,
		"stack", stack,
		"image", image,
		"command", container.Spec.Command,
		"containerSpecID", container.Spec.ID,
	)

	lookupCellPre := intmodel.Cell{
		Metadata: intmodel.CellMetadata{
			Name: cellName,
		},
		Spec: intmodel.CellSpec{
			RealmName: realm,
			SpaceName: space,
			StackName: stack,
		},
	}
	internalCellPre, err := b.runner.GetCell(lookupCellPre)
	if err != nil {
		if errors.Is(err, errdefs.ErrCellNotFound) {
			return res, fmt.Errorf("cell %q not found", cellName)
		}
		return res, fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
	}

	res.CellMetadataExistsPre = true
	res.ContainerExistsPre = containerSpecExistsInternal(internalCellPre.Spec.Containers, containerName)

	// Log before calling CreateCell
	b.logger.DebugContext(
		b.ctx,
		"calling CreateCell to merge container",
		"containerName", containerName,
		"cell", cellName,
		"containerExistsPre", res.ContainerExistsPre,
		"containersInCell", len(cellInternal.Spec.Containers),
	)

	// CreateCell returns the Cell with merged containers - we must use this
	// returned cell for StartCell to ensure we're starting the containers
	// that were actually created
	resultCell, err := b.runner.CreateCell(cellInternal)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrCreateCell, err)
	}

	// Log after CreateCell returns
	b.logger.DebugContext(
		b.ctx,
		"CreateCell returned successfully",
		"containerName", containerName,
		"cell", cellName,
		"containersInCreatedCell", len(resultCell.Spec.Containers),
	)

	// Use the CellDoc returned from CreateCell, which has the containers properly merged
	b.logger.DebugContext(
		b.ctx,
		"calling StartCell to start containers",
		"containerName", containerName,
		"cell", cellName,
		"containersToStart", len(resultCell.Spec.Containers),
	)

	// Use the same internal cell from CreateCell for runner.StartCell
	if err = b.runner.StartCell(resultCell); err != nil {
		return res, fmt.Errorf("failed to start container %s: %w", containerName, err)
	}

	lookupCellPost := intmodel.Cell{
		Metadata: intmodel.CellMetadata{
			Name: cellName,
		},
		Spec: intmodel.CellSpec{
			RealmName: realm,
			SpaceName: space,
			StackName: stack,
		},
	}
	internalCellPost, err := b.runner.GetCell(lookupCellPost)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
	}

	res.CellMetadataExistsPost = true
	res.ContainerExistsPost = containerSpecExistsInternal(internalCellPost.Spec.Containers, containerName)
	res.ContainerCreated = !res.ContainerExistsPre && res.ContainerExistsPost
	res.Started = true

	// Construct Container from the created container spec
	var containerSpec *intmodel.ContainerSpec
	for i := range internalCellPost.Spec.Containers {
		if internalCellPost.Spec.Containers[i].ID == containerName {
			containerSpec = &internalCellPost.Spec.Containers[i]
			break
		}
	}

	if containerSpec != nil {
		// Use labels from container if provided, otherwise empty map
		labels := container.Metadata.Labels
		if labels == nil {
			labels = make(map[string]string)
		}

		res.Container = intmodel.Container{
			Metadata: intmodel.ContainerMetadata{
				Name:   containerName,
				Labels: labels,
			},
			Spec: *containerSpec,
			Status: intmodel.ContainerStatus{
				State: intmodel.ContainerStateReady,
			},
		}
	}

	return res, nil
}

func ensureContainerOwnershipInternal(
	containers []intmodel.ContainerSpec,
	realm, space, stack, cell string,
) []intmodel.ContainerSpec {
	if len(containers) == 0 {
		return containers
	}
	result := make([]intmodel.ContainerSpec, len(containers))
	for i, c := range containers {
		c.RealmName = realm
		c.SpaceName = space
		c.StackName = stack
		c.CellName = cell
		result[i] = c
	}
	return result
}

func containerSpecExistsInternal(specs []intmodel.ContainerSpec, id string) bool {
	for _, spec := range specs {
		if spec.ID == id {
			return true
		}
	}
	return false
}
