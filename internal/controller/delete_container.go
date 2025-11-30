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

// DeleteContainerResult mirrors GetContainerResult but also reports what was deleted.
type DeleteContainerResult struct {
	Container          intmodel.Container
	CellMetadataExists bool
	ContainerExists    bool
	Deleted            []string // Resources that were deleted (container, task)
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

	// Build lookup cell for runner
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
	internalCell, err := b.runner.GetCell(lookupCell)
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
	res.CellMetadataExists = true

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

	// Use private internal method for deletion
	if err = b.deleteContainerInternal(internalCell, name); err != nil {
		return res, err
	}

	res.Deleted = append(res.Deleted, "container", "task")
	return res, nil
}

// deleteContainerInternal handles the core container deletion logic using runner methods directly.
// It deletes the container from containerd and updates the cell metadata to remove the container.
// It returns an error if deletion fails, but does not return result types.
func (b *Exec) deleteContainerInternal(cell intmodel.Cell, containerName string) error {
	// Delete container from containerd (via runner)
	if err := b.runner.DeleteContainer(cell, containerName); err != nil {
		return fmt.Errorf("failed to delete container %s: %w", containerName, err)
	}

	// Remove container from cell's Spec.Containers list
	var updatedContainers []intmodel.ContainerSpec
	for _, containerSpec := range cell.Spec.Containers {
		if containerSpec.ID != containerName {
			updatedContainers = append(updatedContainers, containerSpec)
		}
	}
	cell.Spec.Containers = updatedContainers

	// Update cell metadata to persist the change
	if err := b.runner.UpdateCellMetadata(cell); err != nil {
		return fmt.Errorf("failed to update cell metadata: %w", err)
	}

	return nil
}
