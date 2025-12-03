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

// StopContainerResult reports the outcome of stopping a container.
type StopContainerResult struct {
	Container intmodel.Container
	Stopped   bool
}

// StopContainer stops a specific container in a cell and updates the cell metadata.
func (b *Exec) StopContainer(container intmodel.Container) (StopContainerResult, error) {
	defer b.runner.Close()
	var res StopContainerResult

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

	// Find container in cell spec to construct result container
	var foundContainerSpec *intmodel.ContainerSpec
	for i := range internalCell.Spec.Containers {
		if internalCell.Spec.Containers[i].ID == name {
			foundContainerSpec = &internalCell.Spec.Containers[i]
			break
		}
	}

	// Stop the specific container (pass container name, runner will build full ID)
	if err = b.runner.StopContainer(internalCell, name); err != nil {
		return res, fmt.Errorf("failed to stop container %s: %w", name, err)
	}

	// Update cell metadata (state remains Ready if other containers are running)
	// The state management can be enhanced later to track individual container states
	if err = b.runner.UpdateCellMetadata(internalCell); err != nil {
		return res, fmt.Errorf("failed to update cell metadata: %w", err)
	}

	// Query actual container state from containerd
	var actualState intmodel.ContainerState
	if foundContainerSpec != nil {
		var state intmodel.ContainerState
		state, err = b.runner.GetContainerState(internalCell, name)
		if err != nil {
			// Log error but continue with Pending state (container was just stopped)
			b.logger.DebugContext(b.ctx, "failed to get container state from containerd after stop",
				"container", name,
				"cell", cellName,
				"error", err)
			actualState = intmodel.ContainerStateStopped // Default to Stopped since we just stopped it
		} else {
			actualState = state
		}
	}

	// Construct result container
	if foundContainerSpec != nil {
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
	} else {
		// If container not found in spec, use the input container
		res.Container = container
	}

	res.Stopped = true
	return res, nil
}
