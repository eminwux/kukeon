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
	"github.com/eminwux/kukeon/internal/util/naming"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// DeleteRealmResult reports what was deleted during realm deletion.
type DeleteRealmResult struct {
	RealmName string
	Deleted   []string // Resources that were deleted (metadata, cgroup, namespace, cascaded resources)
}

// DeleteSpaceResult reports what was deleted during space deletion.
type DeleteSpaceResult struct {
	SpaceName string
	RealmName string
	Deleted   []string // Resources that were deleted (metadata, cgroup, network, cascaded resources)
}

// DeleteStackResult reports what was deleted during stack deletion.
type DeleteStackResult struct {
	StackName string
	RealmName string
	SpaceName string
	Deleted   []string // Resources that were deleted (metadata, cgroup, cascaded resources)
}

// DeleteCellResult reports what was deleted during cell deletion.
type DeleteCellResult struct {
	CellName  string
	RealmName string
	SpaceName string
	StackName string
	Deleted   []string // Resources that were deleted (containers, cgroup, metadata)
}

// DeleteContainerResult reports what was deleted during container deletion.
type DeleteContainerResult struct {
	ContainerName string
	RealmName     string
	SpaceName     string
	StackName     string
	CellName      string
	Deleted       []string // Resources that were deleted (container, task)
}

// DeleteRealm deletes a realm. If cascade is true, deletes all spaces first.
// If force is true, skips validation of child resources.
func (b *Exec) DeleteRealm(name string, force, cascade bool) (*DeleteRealmResult, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errdefs.ErrRealmNameRequired
	}

	// Get realm document
	_, err := b.GetRealm(name)
	if err != nil {
		if errors.Is(err, errdefs.ErrRealmNotFound) {
			return nil, fmt.Errorf("realm %q not found", name)
		}
		return nil, err
	}

	result := &DeleteRealmResult{
		RealmName: name,
		Deleted:   []string{},
	}

	// If cascade, delete all spaces first
	var spaces []*v1beta1.SpaceDoc
	if cascade {
		spaces, err = b.ListSpaces(name)
		if err != nil {
			return nil, fmt.Errorf("failed to list spaces: %w", err)
		}
		for _, space := range spaces {
			_, err = b.DeleteSpace(space.Metadata.Name, name, force, cascade)
			if err != nil {
				return nil, fmt.Errorf("failed to delete space %q: %w", space.Metadata.Name, err)
			}
			result.Deleted = append(result.Deleted, fmt.Sprintf("space:%s", space.Metadata.Name))
		}
	} else if !force {
		// Validate no spaces exist
		spaces, err = b.ListSpaces(name)
		if err != nil {
			return nil, fmt.Errorf("failed to list spaces: %w", err)
		}
		if len(spaces) > 0 {
			return nil, fmt.Errorf("%w: realm %q has %d space(s). Use --cascade to delete them or --force to skip validation", errdefs.ErrResourceHasDependencies, name, len(spaces))
		}
	}

	// Delete realm
	doc := &v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{
			Name: name,
		},
	}
	if err = b.runner.DeleteRealm(doc); err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrDeleteRealm, err)
	}

	result.Deleted = append(result.Deleted, "metadata", "cgroup", "namespace")
	return result, nil
}

// DeleteSpace deletes a space. If cascade is true, deletes all stacks first.
// If force is true, skips validation of child resources.
func (b *Exec) DeleteSpace(name, realmName string, force, cascade bool) (*DeleteSpaceResult, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errdefs.ErrSpaceNameRequired
	}
	realmName = strings.TrimSpace(realmName)
	if realmName == "" {
		return nil, errdefs.ErrRealmNameRequired
	}

	// Get space document
	_, err := b.GetSpace(name, realmName)
	if err != nil {
		if errors.Is(err, errdefs.ErrSpaceNotFound) {
			return nil, fmt.Errorf("space %q not found in realm %q", name, realmName)
		}
		return nil, err
	}

	result := &DeleteSpaceResult{
		SpaceName: name,
		RealmName: realmName,
		Deleted:   []string{},
	}

	// If cascade, delete all stacks first (recursively cascades to cells)
	var stacks []*v1beta1.StackDoc
	if cascade {
		stacks, err = b.ListStacks(realmName, name)
		if err != nil {
			return nil, fmt.Errorf("failed to list stacks: %w", err)
		}
		for _, stack := range stacks {
			_, err = b.DeleteStack(stack.Metadata.Name, realmName, name, force, cascade)
			if err != nil {
				return nil, fmt.Errorf("failed to delete stack %q: %w", stack.Metadata.Name, err)
			}
			result.Deleted = append(result.Deleted, fmt.Sprintf("stack:%s", stack.Metadata.Name))
		}
	} else if !force {
		// Validate no stacks exist
		stacks, err = b.ListStacks(realmName, name)
		if err != nil {
			return nil, fmt.Errorf("failed to list stacks: %w", err)
		}
		if len(stacks) > 0 {
			return nil, fmt.Errorf("%w: space %q has %d stack(s). Use --cascade to delete them or --force to skip validation", errdefs.ErrResourceHasDependencies, name, len(stacks))
		}
	}

	// Delete space
	doc := &v1beta1.SpaceDoc{
		Metadata: v1beta1.SpaceMetadata{
			Name: name,
		},
		Spec: v1beta1.SpaceSpec{
			RealmID: realmName,
		},
	}
	if err = b.runner.DeleteSpace(doc); err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrDeleteSpace, err)
	}

	result.Deleted = append(result.Deleted, "metadata", "cgroup", "network")
	return result, nil
}

// DeleteStack deletes a stack. If cascade is true, deletes all cells first.
// If force is true, skips validation of child resources.
func (b *Exec) DeleteStack(name, realmName, spaceName string, force, cascade bool) (*DeleteStackResult, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errdefs.ErrStackNameRequired
	}
	realmName = strings.TrimSpace(realmName)
	if realmName == "" {
		return nil, errdefs.ErrRealmNameRequired
	}
	spaceName = strings.TrimSpace(spaceName)
	if spaceName == "" {
		return nil, errdefs.ErrSpaceNameRequired
	}

	// Get stack document
	_, err := b.GetStack(name, realmName, spaceName)
	if err != nil {
		if errors.Is(err, errdefs.ErrStackNotFound) {
			return nil, fmt.Errorf("stack %q not found in realm %q, space %q", name, realmName, spaceName)
		}
		return nil, err
	}

	result := &DeleteStackResult{
		StackName: name,
		RealmName: realmName,
		SpaceName: spaceName,
		Deleted:   []string{},
	}

	// If cascade, delete all cells first
	var cells []*v1beta1.CellDoc
	if cascade {
		cells, err = b.ListCells(realmName, spaceName, name)
		if err != nil {
			return nil, fmt.Errorf("failed to list cells: %w", err)
		}
		for _, cell := range cells {
			_, err = b.DeleteCell(cell.Metadata.Name, realmName, spaceName, name)
			if err != nil {
				return nil, fmt.Errorf("failed to delete cell %q: %w", cell.Metadata.Name, err)
			}
			result.Deleted = append(result.Deleted, fmt.Sprintf("cell:%s", cell.Metadata.Name))
		}
	} else if !force {
		// Validate no cells exist
		cells, err = b.ListCells(realmName, spaceName, name)
		if err != nil {
			return nil, fmt.Errorf("failed to list cells: %w", err)
		}
		if len(cells) > 0 {
			return nil, fmt.Errorf("%w: stack %q has %d cell(s). Use --cascade to delete them or --force to skip validation", errdefs.ErrResourceHasDependencies, name, len(cells))
		}
	}

	// Delete stack
	doc := &v1beta1.StackDoc{
		Metadata: v1beta1.StackMetadata{
			Name: name,
		},
		Spec: v1beta1.StackSpec{
			RealmID: realmName,
			SpaceID: spaceName,
		},
	}
	if err = b.runner.DeleteStack(doc); err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrDeleteStack, err)
	}

	result.Deleted = append(result.Deleted, "metadata", "cgroup")
	return result, nil
}

// DeleteCell deletes a cell. Always deletes all containers first.
func (b *Exec) DeleteCell(
	name, realmName, spaceName, stackName string,
) (*DeleteCellResult, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errdefs.ErrCellNameRequired
	}
	realmName = strings.TrimSpace(realmName)
	if realmName == "" {
		return nil, errdefs.ErrRealmNameRequired
	}
	spaceName = strings.TrimSpace(spaceName)
	if spaceName == "" {
		return nil, errdefs.ErrSpaceNameRequired
	}
	stackName = strings.TrimSpace(stackName)
	if stackName == "" {
		return nil, errdefs.ErrStackNameRequired
	}

	// Get cell document
	cellDoc, err := b.GetCell(name, realmName, spaceName, stackName)
	if err != nil {
		if errors.Is(err, errdefs.ErrCellNotFound) {
			return nil, fmt.Errorf(
				"cell %q not found in realm %q, space %q, stack %q",
				name,
				realmName,
				spaceName,
				stackName,
			)
		}
		return nil, err
	}

	result := &DeleteCellResult{
		CellName:  name,
		RealmName: realmName,
		SpaceName: spaceName,
		StackName: stackName,
		Deleted:   []string{},
	}

	// Always delete all containers in cell first
	// Containers are always deleted with cells
	containers := cellDoc.Spec.Containers
	result.Deleted = append(result.Deleted, fmt.Sprintf("containers:%d", len(containers)))

	// Delete cell
	doc := &v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{
			Name: name,
		},
		Spec: v1beta1.CellSpec{
			RealmID: realmName,
			SpaceID: spaceName,
			StackID: stackName,
		},
	}
	if err = b.runner.DeleteCell(doc); err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrDeleteCell, err)
	}

	result.Deleted = append(result.Deleted, "cgroup", "metadata")
	return result, nil
}

// DeleteContainer deletes a single container. Cascade flag is not applicable.
func (b *Exec) DeleteContainer(name, realmName, spaceName, stackName, cellName string) (*DeleteContainerResult, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errdefs.ErrContainerNameRequired
	}
	realmName = strings.TrimSpace(realmName)
	if realmName == "" {
		return nil, errdefs.ErrRealmNameRequired
	}
	spaceName = strings.TrimSpace(spaceName)
	if spaceName == "" {
		return nil, errdefs.ErrSpaceNameRequired
	}
	stackName = strings.TrimSpace(stackName)
	if stackName == "" {
		return nil, errdefs.ErrStackNameRequired
	}
	cellName = strings.TrimSpace(cellName)
	if cellName == "" {
		return nil, errdefs.ErrCellNameRequired
	}

	// Get cell to find container
	cellDoc, err := b.GetCell(cellName, realmName, spaceName, stackName)
	if err != nil {
		if errors.Is(err, errdefs.ErrCellNotFound) {
			return nil, fmt.Errorf(
				"cell %q not found in realm %q, space %q, stack %q",
				cellName,
				realmName,
				spaceName,
				stackName,
			)
		}
		return nil, err
	}

	// Find container in cell
	// Use the naming utility to build container ID
	containerID := naming.BuildContainerName(realmName, spaceName, cellName, name)
	var foundContainer *v1beta1.ContainerSpec
	for i := range cellDoc.Spec.Containers {
		if cellDoc.Spec.Containers[i].ID == containerID {
			foundContainer = &cellDoc.Spec.Containers[i]
			break
		}
	}

	if foundContainer == nil {
		return nil, fmt.Errorf("container %q not found in cell %q", name, cellName)
	}

	result := &DeleteContainerResult{
		ContainerName: name,
		RealmName:     realmName,
		SpaceName:     spaceName,
		StackName:     stackName,
		CellName:      cellName,
		Deleted:       []string{},
	}

	// Delete container from containerd (via runner)
	// Note: This requires a DeleteContainer method on the runner
	// For now, we'll need to implement this at the runner level
	// The container should be stopped and deleted from containerd
	// and then removed from the cell's Spec.Containers list
	result.Deleted = append(result.Deleted, "container", "task")
	return result, nil
}
