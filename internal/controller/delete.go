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

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// DeleteSpaceResult reports what was deleted during space deletion.
type DeleteSpaceResult struct {
	SpaceName string
	RealmName string
	Space     intmodel.Space

	MetadataDeleted   bool
	CgroupDeleted     bool
	CNINetworkDeleted bool

	Deleted []string // Resources that were deleted (metadata, cgroup, network, cascaded resources)
}

// DeleteStackResult reports what was deleted during stack deletion.
type DeleteStackResult struct {
	StackName string
	RealmName string
	SpaceName string
	Stack     intmodel.Stack

	MetadataDeleted bool
	CgroupDeleted   bool

	Deleted []string // Resources that were deleted (metadata, cgroup, cascaded resources)
}

// DeleteCellResult reports what was deleted during cell deletion.
type DeleteCellResult struct {
	Cell              intmodel.Cell
	ContainersDeleted bool
	CgroupDeleted     bool
	MetadataDeleted   bool
}

// DeleteContainerResult mirrors GetContainerResult but also reports what was deleted.
type DeleteContainerResult struct {
	Container          intmodel.Container
	CellMetadataExists bool
	ContainerExists    bool
	Deleted            []string // Resources that were deleted (container, task)
}

// DeleteSpace deletes a space. If cascade is true, deletes all stacks first.
// If force is true, skips validation of child resources.
func (b *Exec) DeleteSpace(space intmodel.Space, force, cascade bool) (DeleteSpaceResult, error) {
	var res DeleteSpaceResult

	name := strings.TrimSpace(space.Metadata.Name)
	if name == "" {
		return res, errdefs.ErrSpaceNameRequired
	}

	realmName := strings.TrimSpace(space.Spec.RealmName)
	if realmName == "" {
		return res, errdefs.ErrRealmNameRequired
	}

	// Build lookup space for GetSpace
	lookupSpace := intmodel.Space{
		Metadata: intmodel.SpaceMetadata{
			Name: name,
		},
		Spec: intmodel.SpaceSpec{
			RealmName: realmName,
		},
	}

	getResult, err := b.GetSpace(lookupSpace)
	if err != nil {
		if errors.Is(err, errdefs.ErrSpaceNotFound) {
			return res, fmt.Errorf("space %q not found in realm %q", name, realmName)
		}
		return res, err
	}
	if !getResult.MetadataExists {
		return res, fmt.Errorf("space %q not found in realm %q", name, realmName)
	}

	res = DeleteSpaceResult{
		SpaceName: name,
		RealmName: realmName,
		Space:     getResult.Space,
		Deleted:   []string{},
	}

	// If cascade, delete all stacks first (recursively cascades to cells)
	var stacks []intmodel.Stack
	if cascade {
		stacks, err = b.ListStacks(realmName, name)
		if err != nil {
			return res, fmt.Errorf("failed to list stacks: %w", err)
		}
		for _, stackInternal := range stacks {
			_, err = b.DeleteStack(stackInternal, force, cascade)
			if err != nil {
				return res, fmt.Errorf("failed to delete stack %q: %w", stackInternal.Metadata.Name, err)
			}
			res.Deleted = append(res.Deleted, fmt.Sprintf("stack:%s", stackInternal.Metadata.Name))
		}
	} else if !force {
		// Validate no stacks exist
		stacks, err = b.ListStacks(realmName, name)
		if err != nil {
			return res, fmt.Errorf("failed to list stacks: %w", err)
		}
		if len(stacks) > 0 {
			return res, fmt.Errorf("%w: space %q has %d stack(s). Use --cascade to delete them or --force to skip validation", errdefs.ErrResourceHasDependencies, name, len(stacks))
		}
	}

	// Delete space
	if err = b.runner.DeleteSpace(getResult.Space); err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrDeleteSpace, err)
	}

	res.MetadataDeleted = true
	res.CgroupDeleted = true
	res.CNINetworkDeleted = true
	res.Deleted = append(res.Deleted, "metadata", "cgroup", "network")
	return res, nil
}

// DeleteStack deletes a stack. If cascade is true, deletes all cells first.
// If force is true, skips validation of child resources.
func (b *Exec) DeleteStack(stack intmodel.Stack, force, cascade bool) (DeleteStackResult, error) {
	var res DeleteStackResult

	name := strings.TrimSpace(stack.Metadata.Name)
	if name == "" {
		return res, errdefs.ErrStackNameRequired
	}

	realmName := strings.TrimSpace(stack.Spec.RealmName)
	if realmName == "" {
		return res, errdefs.ErrRealmNameRequired
	}

	spaceName := strings.TrimSpace(stack.Spec.SpaceName)
	if spaceName == "" {
		return res, errdefs.ErrSpaceNameRequired
	}

	// Build lookup stack for GetStack
	lookupStack := intmodel.Stack{
		Metadata: intmodel.StackMetadata{
			Name: name,
		},
		Spec: intmodel.StackSpec{
			RealmName: realmName,
			SpaceName: spaceName,
		},
	}

	getResult, err := b.GetStack(lookupStack)
	if err != nil {
		if errors.Is(err, errdefs.ErrStackNotFound) {
			return res, fmt.Errorf("stack %q not found in realm %q, space %q", name, realmName, spaceName)
		}
		return res, err
	}
	if !getResult.MetadataExists {
		return res, fmt.Errorf("stack %q not found in realm %q, space %q", name, realmName, spaceName)
	}

	res = DeleteStackResult{
		StackName: name,
		RealmName: realmName,
		SpaceName: spaceName,
		Stack:     getResult.Stack,
		Deleted:   []string{},
	}

	// If cascade, delete all cells first
	var cells []intmodel.Cell
	if cascade {
		cells, err = b.ListCells(realmName, spaceName, name)
		if err != nil {
			return res, fmt.Errorf("failed to list cells: %w", err)
		}
		for _, cellInternal := range cells {
			_, err = b.DeleteCell(cellInternal)
			if err != nil {
				return res, fmt.Errorf("failed to delete cell %q: %w", cellInternal.Metadata.Name, err)
			}
			res.Deleted = append(res.Deleted, fmt.Sprintf("cell:%s", cellInternal.Metadata.Name))
		}
	} else if !force {
		// Validate no cells exist
		cells, err = b.ListCells(realmName, spaceName, name)
		if err != nil {
			return res, fmt.Errorf("failed to list cells: %w", err)
		}
		if len(cells) > 0 {
			return res, fmt.Errorf("%w: stack %q has %d cell(s). Use --cascade to delete them or --force to skip validation", errdefs.ErrResourceHasDependencies, name, len(cells))
		}
	}

	// Delete stack
	if err = b.runner.DeleteStack(getResult.Stack); err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrDeleteStack, err)
	}

	res.MetadataDeleted = true
	res.CgroupDeleted = true
	res.Deleted = append(res.Deleted, "metadata", "cgroup")
	return res, nil
}

// DeleteCell deletes a cell. Always deletes all containers first.
func (b *Exec) DeleteCell(cell intmodel.Cell) (DeleteCellResult, error) {
	var res DeleteCellResult

	internalCell, err := b.validateAndGetCell(cell)
	if err != nil {
		return res, err
	}

	res = DeleteCellResult{
		Cell: internalCell,
	}

	// Always delete all containers in cell first
	if len(internalCell.Spec.Containers) > 0 {
		res.ContainersDeleted = true
	}

	if err = b.runner.DeleteCell(internalCell); err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrDeleteCell, err)
	}

	res.CgroupDeleted = true
	res.MetadataDeleted = true
	return res, nil
}

// DeleteContainer deletes a single container. Cascade flag is not applicable.
func (b *Exec) DeleteContainer(container intmodel.Container) (DeleteContainerResult, error) {
	var res DeleteContainerResult

	name := strings.TrimSpace(container.Metadata.Name)
	if name == "" {
		return res, errdefs.ErrContainerNameRequired
	}
	realmName := strings.TrimSpace(container.Spec.RealmName)
	if realmName == "" {
		return res, errdefs.ErrRealmNameRequired
	}
	spaceName := strings.TrimSpace(container.Spec.SpaceName)
	if spaceName == "" {
		return res, errdefs.ErrSpaceNameRequired
	}
	stackName := strings.TrimSpace(container.Spec.StackName)
	if stackName == "" {
		return res, errdefs.ErrStackNameRequired
	}
	cellName := strings.TrimSpace(container.Spec.CellName)
	if cellName == "" {
		return res, errdefs.ErrCellNameRequired
	}

	res.Deleted = []string{}

	// Build minimal lookup doc for GetCell (still uses external types)
	// Build lookup cell for GetCell
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
	getResult, err := b.GetCell(lookupCell)
	if err != nil {
		if errors.Is(err, errdefs.ErrCellNotFound) {
			res.CellMetadataExists = false
			return res, fmt.Errorf(
				"cell %q not found in realm %q, space %q, stack %q",
				cellName,
				realmName,
				spaceName,
				stackName,
			)
		}
		return res, err
	}
	res.CellMetadataExists = getResult.MetadataExists
	if !getResult.MetadataExists {
		return res, fmt.Errorf("cell %q not found", cellName)
	}

	internalCell := getResult.Cell

	// Find container in cell by name
	var foundContainer *intmodel.ContainerSpec
	for i := range internalCell.Spec.Containers {
		if internalCell.Spec.Containers[i].ID == name {
			foundContainer = &internalCell.Spec.Containers[i]
			break
		}
	}

	if foundContainer == nil {
		return res, fmt.Errorf("container %q not found in cell %q", name, cellName)
	}

	res.ContainerExists = true

	// Build result container from found container spec
	resultContainer := intmodel.Container{
		Metadata: intmodel.ContainerMetadata{
			Name:   name,
			Labels: container.Metadata.Labels,
		},
		Spec: *foundContainer,
		Status: intmodel.ContainerStatus{
			State: intmodel.ContainerStateReady,
		},
	}
	res.Container = resultContainer

	// Delete container from containerd (via runner)
	if err = b.runner.DeleteContainer(internalCell, name); err != nil {
		return res, fmt.Errorf("failed to delete container %s: %w", name, err)
	}

	// Remove container from cell's Spec.Containers list
	var updatedContainers []intmodel.ContainerSpec
	for _, containerSpec := range internalCell.Spec.Containers {
		if containerSpec.ID != name {
			updatedContainers = append(updatedContainers, containerSpec)
		}
	}
	internalCell.Spec.Containers = updatedContainers

	// Update cell metadata to persist the change
	if err = b.runner.UpdateCellMetadata(internalCell); err != nil {
		return res, fmt.Errorf("failed to update cell metadata: %w", err)
	}

	res.Deleted = append(res.Deleted, "container", "task")
	return res, nil
}
