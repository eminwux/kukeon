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
	defer b.runner.Close()
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

	// Build container spec to merge into cell
	newContainerSpec := intmodel.ContainerSpec{
		ID:        container.Spec.ID,
		RealmName: realm,
		SpaceName: space,
		StackName: stack,
		CellName:  cellName,
		Image:     image,
		Command:   container.Spec.Command,
		Args:      container.Spec.Args,
	}

	// Build lookup cell for pre-state check and CreateContainer call
	lookupCell := intmodel.Cell{
		Metadata: intmodel.CellMetadata{
			Name: cellName,
		},
		Spec: intmodel.CellSpec{
			RealmName: realm,
			SpaceName: space,
			StackName: stack,
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

	// Check pre-state
	internalCellPre, err := b.runner.GetCell(lookupCell)
	if err != nil {
		if errors.Is(err, errdefs.ErrCellNotFound) {
			return res, fmt.Errorf("cell %q not found", cellName)
		}
		return res, fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
	}

	res.CellMetadataExistsPre = true
	res.ContainerExistsPre = containerSpecExistsInternal(internalCellPre.Spec.Containers, containerName)

	// Log before calling CreateContainer
	b.logger.DebugContext(
		b.ctx,
		"calling CreateContainer to merge container",
		"containerName", containerName,
		"cell", cellName,
		"containerExistsPre", res.ContainerExistsPre,
	)

	// CreateContainer returns the Cell with merged container
	resultCell, err := b.runner.CreateContainer(lookupCell, newContainerSpec)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrCreateCell, err)
	}

	// Log after CreateContainer returns
	b.logger.DebugContext(
		b.ctx,
		"CreateContainer returned successfully",
		"containerName", containerName,
		"cell", cellName,
		"containersInCreatedCell", len(resultCell.Spec.Containers),
	)

	// Use StartContainer to start only the specific container
	b.logger.DebugContext(
		b.ctx,
		"calling StartContainer to start container",
		"containerName", containerName,
		"cell", cellName,
	)

	// Start only the specific container
	if err = b.runner.StartContainer(resultCell, containerName); err != nil {
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

		// Query actual container state from containerd
		var actualState intmodel.ContainerState
		actualState, err = b.runner.GetContainerState(internalCellPost, containerName)
		if err != nil {
			// Log error but continue with Ready state (container was just started)
			b.logger.DebugContext(b.ctx, "failed to get container state from containerd after creation",
				"container", containerName,
				"cell", cellName,
				"error", err)
			actualState = intmodel.ContainerStateReady // Default to Ready since we just started it
		}

		res.Container = intmodel.Container{
			Metadata: intmodel.ContainerMetadata{
				Name:   containerName,
				Labels: labels,
			},
			Spec: *containerSpec,
			Status: intmodel.ContainerStatus{
				State: actualState,
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
