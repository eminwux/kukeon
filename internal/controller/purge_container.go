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

// PurgeContainerResult reports what was purged during container purging.
type PurgeContainerResult struct {
	Container          intmodel.Container
	CellMetadataExists bool
	ContainerExists    bool
	Deleted            []string // Resources that were deleted (standard cleanup)
	Purged             []string // Additional resources purged (CNI, orphaned containers, etc.)
}

// PurgeContainer purges a single container with comprehensive cleanup. Cascade flag is not applicable.
func (b *Exec) PurgeContainer(container intmodel.Container) (PurgeContainerResult, error) {
	defer b.runner.Close()
	var result PurgeContainerResult

	name := strings.TrimSpace(container.Metadata.Name)
	if name == "" {
		name = strings.TrimSpace(container.Spec.ID)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return result, errdefs.ErrContainerNameRequired
	}

	realmName := strings.TrimSpace(container.Spec.RealmName)
	if realmName == "" {
		return result, errdefs.ErrRealmNameRequired
	}

	spaceName := strings.TrimSpace(container.Spec.SpaceName)
	if spaceName == "" {
		return result, errdefs.ErrSpaceNameRequired
	}

	stackName := strings.TrimSpace(container.Spec.StackName)
	if stackName == "" {
		return result, errdefs.ErrStackNameRequired
	}

	cellName := strings.TrimSpace(container.Spec.CellName)
	if cellName == "" {
		return result, errdefs.ErrCellNameRequired
	}

	// Initialize result
	result = PurgeContainerResult{
		Container: container,
		Deleted:   []string{},
		Purged:    []string{},
	}

	// Get cell to find container metadata
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
			result.CellMetadataExists = false
			return result, fmt.Errorf(
				"cell %q not found in realm %q, space %q, stack %q",
				cellName,
				realmName,
				spaceName,
				stackName,
			)
		}
		return result, err
	}
	result.CellMetadataExists = true

	// Check if container exists in cell metadata by name (ID now stores just the container name)
	var foundContainerSpec *intmodel.ContainerSpec
	for i := range internalCell.Spec.Containers {
		if internalCell.Spec.Containers[i].ID == name {
			foundContainerSpec = &internalCell.Spec.Containers[i]
			result.ContainerExists = true
			break
		}
	}

	// Track what will be deleted before calling private method and build result container
	if foundContainerSpec != nil {
		result.Deleted = append(result.Deleted, "container", "task")
		// Build result container from found container spec
		result.Container = intmodel.Container{
			Metadata: intmodel.ContainerMetadata{
				Name:   name,
				Labels: container.Metadata.Labels,
			},
			Spec: *foundContainerSpec,
			Status: intmodel.ContainerStatus{
				Name:  name,
				ID:    name,
				State: intmodel.ContainerStateReady,
			},
		}
	}

	// Get realm to pass to purgeContainerInternal
	lookupRealm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: realmName,
		},
	}
	internalRealm, err := b.runner.GetRealm(lookupRealm)
	if err != nil {
		if errors.Is(err, errdefs.ErrRealmNotFound) {
			return result, fmt.Errorf("realm %q not found", realmName)
		}
		return result, fmt.Errorf("failed to get realm: %w", err)
	}

	// Call private method for deletion and purging
	if err = b.purgeContainerInternal(internalCell, name, internalRealm); err != nil {
		result.Purged = append(result.Purged, fmt.Sprintf("purge-error:%v", err))
	} else {
		result.Purged = append(result.Purged, "cni-resources", "ipam-allocation", "cache-entries")
	}

	return result, nil
}

// purgeContainerInternal handles container deletion and purging logic using runner methods directly.
// It returns an error if deletion/purging fails, but does not return result types.
func (b *Exec) purgeContainerInternal(cell intmodel.Cell, containerName string, realm intmodel.Realm) error {
	// Check if container exists in cell metadata
	var foundContainerSpec *intmodel.ContainerSpec
	for i := range cell.Spec.Containers {
		if cell.Spec.Containers[i].ID == containerName {
			foundContainerSpec = &cell.Spec.Containers[i]
			break
		}
	}

	// Perform standard delete if container exists in metadata
	if foundContainerSpec != nil {
		if err := b.deleteContainerInternal(cell, containerName); err != nil {
			return fmt.Errorf("failed to delete container: %w", err)
		}

		// Use ContainerdID from spec for purge
		containerdID := foundContainerSpec.ContainerdID
		if containerdID != "" {
			// Perform comprehensive purge via runner using ContainerdID
			return b.runner.PurgeContainer(realm, containerdID)
		}
	}

	// If container not found in metadata or ContainerdID is empty, still try to purge by name
	// (for orphaned containers that might exist in containerd but not in metadata)
	return b.runner.PurgeContainer(realm, containerName)
}
