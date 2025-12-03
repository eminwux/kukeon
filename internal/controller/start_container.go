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

// StartContainerResult reports the outcome of starting a container.
type StartContainerResult struct {
	Container intmodel.Container
	Started   bool
}

// StartContainer starts a specific container in a cell and updates the cell metadata.
func (b *Exec) StartContainer(container intmodel.Container) (StartContainerResult, error) {
	defer b.runner.Close()
	var res StartContainerResult

	name := strings.TrimSpace(container.Metadata.Name)
	if name == "" {
		name = strings.TrimSpace(container.Spec.ID)
	}
	name = strings.TrimSpace(name)
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

	// Find container in cell spec by name (ID now stores just the container name)
	var foundContainerSpec *intmodel.ContainerSpec
	for i := range internalCell.Spec.Containers {
		if internalCell.Spec.Containers[i].ID == name {
			foundContainerSpec = &internalCell.Spec.Containers[i]
			break
		}
	}

	if foundContainerSpec == nil {
		return res, fmt.Errorf("container %q not found in cell %q", name, cellName)
	}

	// Start the container using runner.StartContainer which checks if root container is running
	// If root container is not running, runner.StartContainer will return an error telling user to start the cell first
	if err = b.runner.StartContainer(internalCell, name); err != nil {
		return res, fmt.Errorf("failed to start container %s: %w", name, err)
	}

	// Update cell state to Ready
	internalCell.Status.State = intmodel.CellStateReady

	// Update cell metadata state to Ready
	if err = b.runner.UpdateCellMetadata(internalCell); err != nil {
		return res, fmt.Errorf("failed to update cell metadata: %w", err)
	}

	// Query actual container state from containerd
	actualState, err := b.runner.GetContainerState(internalCell, name)
	if err != nil {
		// Log error but continue with Ready state (container was just started)
		b.logger.DebugContext(b.ctx, "failed to get container state from containerd after start",
			"container", name,
			"cell", cellName,
			"error", err)
		actualState = intmodel.ContainerStateReady // Default to Ready since we just started it
	}

	// Construct result container
	labels := container.Metadata.Labels
	if labels == nil {
		labels = make(map[string]string)
	}

	res.Container = intmodel.Container{
		Metadata: intmodel.ContainerMetadata{
			Name:   name,
			Labels: labels,
		},
		Spec: *foundContainerSpec,
		Status: intmodel.ContainerStatus{
			State: actualState,
		},
	}
	res.Started = true

	return res, nil
}
