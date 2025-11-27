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

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// DeleteRealmResult reports what was deleted during realm deletion.
type DeleteRealmResult struct {
	Realm                      intmodel.Realm
	Deleted                    []string // Resources that were deleted (metadata, cgroup, namespace, cascaded resources)
	MetadataDeleted            bool
	CgroupDeleted              bool
	ContainerdNamespaceDeleted bool
}

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

// DeleteRealm deletes a realm. If cascade is true, deletes all spaces first.
// If force is true, skips validation of child resources.
func (b *Exec) DeleteRealm(realm intmodel.Realm, force, cascade bool) (DeleteRealmResult, error) {
	var res DeleteRealmResult

	name := strings.TrimSpace(realm.Metadata.Name)
	if name == "" {
		return res, errdefs.ErrRealmNameRequired
	}

	// Ensure realm exists and capture its latest state
	lookupRealm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: name,
		},
	}
	getResult, err := b.GetRealm(lookupRealm)
	if err != nil {
		if errors.Is(err, errdefs.ErrRealmNotFound) {
			return res, fmt.Errorf("realm %q not found", name)
		}
		return res, err
	}
	if !getResult.MetadataExists {
		return res, fmt.Errorf("realm %q not found", name)
	}

	res = DeleteRealmResult{
		Realm:   getResult.Realm,
		Deleted: []string{},
	}

	// If cascade, delete all spaces first
	var spaces []*v1beta1.SpaceDoc
	if cascade {
		spaces, err = b.ListSpaces(name)
		if err != nil {
			return res, fmt.Errorf("failed to list spaces: %w", err)
		}
		for _, spaceDoc := range spaces {
			// Convert external space to internal at boundary
			spaceInternal, _, convertErr := apischeme.NormalizeSpace(*spaceDoc)
			if convertErr != nil {
				return res, fmt.Errorf("failed to convert space %q: %w", spaceDoc.Metadata.Name, convertErr)
			}
			_, err = b.DeleteSpace(spaceInternal, force, cascade)
			if err != nil {
				return res, fmt.Errorf("failed to delete space %q: %w", spaceDoc.Metadata.Name, err)
			}
			res.Deleted = append(res.Deleted, fmt.Sprintf("space:%s", spaceDoc.Metadata.Name))
		}
	} else if !force {
		// Validate no spaces exist
		spaces, err = b.ListSpaces(name)
		if err != nil {
			return res, fmt.Errorf("failed to list spaces: %w", err)
		}
		if len(spaces) > 0 {
			return res, fmt.Errorf("%w: realm %q has %d space(s). Use --cascade to delete them or --force to skip validation", errdefs.ErrResourceHasDependencies, name, len(spaces))
		}
	}

	// Delete realm via runner and capture detailed outcome
	outcome, err := b.runner.DeleteRealm(getResult.Realm)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrDeleteRealm, err)
	}

	res.MetadataDeleted = outcome.MetadataDeleted
	res.CgroupDeleted = outcome.CgroupDeleted
	res.ContainerdNamespaceDeleted = outcome.ContainerdNamespaceDeleted

	if outcome.MetadataDeleted {
		res.Deleted = append(res.Deleted, "metadata")
	}
	if outcome.CgroupDeleted {
		res.Deleted = append(res.Deleted, "cgroup")
	}
	if outcome.ContainerdNamespaceDeleted {
		res.Deleted = append(res.Deleted, "namespace")
	}

	return res, nil
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
	var stacks []*v1beta1.StackDoc
	if cascade {
		stacks, err = b.ListStacks(realmName, name)
		if err != nil {
			return res, fmt.Errorf("failed to list stacks: %w", err)
		}
		for _, stackDoc := range stacks {
			if stackDoc == nil {
				continue
			}
			// Convert external stack to internal at boundary
			stackInternal, _, convertErr := apischeme.NormalizeStack(*stackDoc)
			if convertErr != nil {
				return res, fmt.Errorf("failed to convert stack %q: %w", stackDoc.Metadata.Name, convertErr)
			}
			_, err = b.DeleteStack(stackInternal, force, cascade)
			if err != nil {
				return res, fmt.Errorf("failed to delete stack %q: %w", stackDoc.Metadata.Name, err)
			}
			res.Deleted = append(res.Deleted, fmt.Sprintf("stack:%s", stackDoc.Metadata.Name))
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
	var cells []*v1beta1.CellDoc
	if cascade {
		cells, err = b.ListCells(realmName, spaceName, name)
		if err != nil {
			return res, fmt.Errorf("failed to list cells: %w", err)
		}
		for _, cellDoc := range cells {
			// Convert external cell to internal at boundary
			cellInternal, _, convertErr := apischeme.NormalizeCell(*cellDoc)
			if convertErr != nil {
				return res, fmt.Errorf("failed to convert cell %q: %w", cellDoc.Metadata.Name, convertErr)
			}
			_, err = b.DeleteCell(cellInternal)
			if err != nil {
				return res, fmt.Errorf("failed to delete cell %q: %w", cellDoc.Metadata.Name, err)
			}
			res.Deleted = append(res.Deleted, fmt.Sprintf("cell:%s", cellDoc.Metadata.Name))
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
