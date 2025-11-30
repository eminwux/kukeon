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
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// GetContainerResult reports the current state of a container.
type GetContainerResult struct {
	Container          intmodel.Container
	CellMetadataExists bool
	ContainerExists    bool
}

// GetContainer retrieves a single container and reports its current state.
func (b *Exec) GetContainer(container intmodel.Container) (GetContainerResult, error) {
	var res GetContainerResult

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
	cellResult, err := b.GetCell(lookupCell)
	if err != nil {
		return res, fmt.Errorf("failed to get cell %q: %w", cellName, err)
	}
	// Check MetadataExists regardless of error (GetCell returns nil error when cell not found)
	if !cellResult.MetadataExists {
		res.CellMetadataExists = false
		return res, fmt.Errorf("failed to get cell %q: cell not found", cellName)
	}
	res.CellMetadataExists = true

	// Find container in cell spec by name (ID now stores just the container name)
	var foundContainerSpec *intmodel.ContainerSpec
	for i := range cellResult.Cell.Spec.Containers {
		if cellResult.Cell.Spec.Containers[i].ID == name {
			foundContainerSpec = &cellResult.Cell.Spec.Containers[i]
			break
		}
	}

	if foundContainerSpec != nil {
		res.ContainerExists = true
		// Construct Container from the found container spec
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
				State: intmodel.ContainerStateReady,
			},
		}
	} else {
		res.ContainerExists = false
	}

	if !res.ContainerExists {
		return res, fmt.Errorf("container %q not found in cell %q at run-path %q", name, cellName, b.opts.RunPath)
	}

	return res, nil
}

// ListContainers lists all containers, optionally filtered by realm, space, stack, and/or cell.
func (b *Exec) ListContainers(realmName, spaceName, stackName, cellName string) ([]intmodel.ContainerSpec, error) {
	return b.runner.ListContainers(realmName, spaceName, stackName, cellName)
}
