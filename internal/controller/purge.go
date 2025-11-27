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

// PurgeRealmResult reports what was purged during realm purging.
type PurgeRealmResult struct {
	Realm          intmodel.Realm
	RealmDeleted   bool     // Whether realm deletion succeeded
	PurgeSucceeded bool     // Whether comprehensive purge succeeded
	Force          bool     // Force flag that was used
	Cascade        bool     // Cascade flag that was used
	Deleted        []string // Resources that were deleted (standard cleanup)
	Purged         []string // Additional resources purged (CNI, orphaned containers, etc.)
}

// PurgeSpaceResult reports what was purged during space purging.
type PurgeSpaceResult struct {
	Space intmodel.Space

	MetadataDeleted   bool
	CgroupDeleted     bool
	CNINetworkDeleted bool
	PurgeSucceeded    bool
	Force             bool
	Cascade           bool

	Deleted []string // Resources that were deleted (standard cleanup)
	Purged  []string // Additional resources purged (CNI, orphaned containers, etc.)
}

// PurgeStackResult reports what was purged during stack purging.
type PurgeStackResult struct {
	Stack   intmodel.Stack
	Deleted []string // Resources that were deleted (standard cleanup)
	Purged  []string // Additional resources purged (CNI, orphaned containers, etc.)
}

// PurgeCellResult reports what was purged during cell purging.
type PurgeCellResult struct {
	Cell              intmodel.Cell
	ContainersDeleted bool
	CgroupDeleted     bool
	MetadataDeleted   bool
	PurgeSucceeded    bool
	Force             bool
	Cascade           bool
	Deleted           []string // Resources that were deleted (standard cleanup)
	Purged            []string // Additional resources purged (CNI, orphaned containers, etc.)
}

// PurgeContainerResult reports what was purged during container purging.
type PurgeContainerResult struct {
	Container          intmodel.Container
	CellMetadataExists bool
	ContainerExists    bool
	Deleted            []string // Resources that were deleted (standard cleanup)
	Purged             []string // Additional resources purged (CNI, orphaned containers, etc.)
}

// PurgeRealm purges a realm with comprehensive cleanup. If cascade is true, purges all spaces first.
// If force is true, skips validation of child resources.
func (b *Exec) PurgeRealm(realm intmodel.Realm, force, cascade bool) (PurgeRealmResult, error) {
	var result PurgeRealmResult

	name := strings.TrimSpace(realm.Metadata.Name)
	if name == "" {
		return result, errdefs.ErrRealmNameRequired
	}

	// Build lookup realm for GetRealm
	lookupRealm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: name,
		},
	}
	getResult, err := b.GetRealm(lookupRealm)
	if err != nil {
		if errors.Is(err, errdefs.ErrRealmNotFound) {
			return result, fmt.Errorf("realm %q not found", name)
		}
		return result, err
	}
	if !getResult.MetadataExists {
		return result, fmt.Errorf("realm %q not found", name)
	}

	internalRealm := getResult.Realm

	// Initialize result with realm and flags
	result = PurgeRealmResult{
		Realm:   internalRealm,
		Force:   force,
		Cascade: cascade,
		Deleted: []string{},
		Purged:  []string{},
	}

	// If cascade, purge all spaces first
	if cascade {
		var spaces []intmodel.Space
		spaces, err = b.ListSpaces(name)
		if err != nil {
			return result, fmt.Errorf("failed to list spaces: %w", err)
		}
		for _, spaceInternal := range spaces {
			_, err = b.PurgeSpace(spaceInternal, force, cascade)
			if err != nil {
				return result, fmt.Errorf("failed to purge space %q: %w", spaceInternal.Metadata.Name, err)
			}
			result.Deleted = append(result.Deleted, fmt.Sprintf("space:%s", spaceInternal.Metadata.Name))
		}
	} else if !force {
		// Validate no spaces exist
		var spaces []intmodel.Space
		spaces, err = b.ListSpaces(name)
		if err != nil {
			return result, fmt.Errorf("failed to list spaces: %w", err)
		}
		if len(spaces) > 0 {
			return result, fmt.Errorf("%w: realm %q has %d space(s). Use --cascade to purge them or --force to skip validation", errdefs.ErrResourceHasDependencies, name, len(spaces))
		}
	}

	// Perform standard delete first
	deleteResult, err := b.DeleteRealm(internalRealm, force, cascade)
	if err != nil {
		// Log but continue with purge
		result.Purged = append(result.Purged, fmt.Sprintf("delete-warning:%v", err))
		result.RealmDeleted = false
	} else {
		result.Deleted = append(result.Deleted, deleteResult.Deleted...)
		result.RealmDeleted = true
		// Update result realm with deleted realm
		result.Realm = deleteResult.Realm
	}

	// Perform comprehensive purge
	if err = b.runner.PurgeRealm(internalRealm); err != nil {
		result.Purged = append(result.Purged, fmt.Sprintf("purge-error:%v", err))
		result.PurgeSucceeded = false
	} else {
		result.Purged = append(result.Purged, "orphaned-containers", "cni-resources", "all-metadata")
		result.PurgeSucceeded = true
	}

	return result, nil
}

// PurgeSpace purges a space with comprehensive cleanup. If cascade is true, purges all stacks first.
// If force is true, skips validation of child resources.
func (b *Exec) PurgeSpace(space intmodel.Space, force, cascade bool) (PurgeSpaceResult, error) {
	var result PurgeSpaceResult

	name := strings.TrimSpace(space.Metadata.Name)
	if name == "" {
		return result, errdefs.ErrSpaceNameRequired
	}

	realmName := strings.TrimSpace(space.Spec.RealmName)
	if realmName == "" {
		return result, errdefs.ErrRealmNameRequired
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
			return result, fmt.Errorf("space %q not found in realm %q", name, realmName)
		}
		return result, err
	}
	if !getResult.MetadataExists {
		return result, fmt.Errorf("space %q not found in realm %q", name, realmName)
	}

	internalSpace := getResult.Space

	// Initialize result with space and flags
	result = PurgeSpaceResult{
		Space:   internalSpace,
		Force:   force,
		Cascade: cascade,
		Deleted: []string{},
		Purged:  []string{},
	}

	// If cascade, purge all stacks first (recursively cascades to cells)
	if cascade {
		var stacks []intmodel.Stack
		stacks, err = b.ListStacks(realmName, name)
		if err != nil {
			return result, fmt.Errorf("failed to list stacks: %w", err)
		}
		for _, stackInternal := range stacks {
			_, err = b.PurgeStack(stackInternal, force, cascade)
			if err != nil {
				return result, fmt.Errorf("failed to purge stack %q: %w", stackInternal.Metadata.Name, err)
			}
			result.Deleted = append(result.Deleted, fmt.Sprintf("stack:%s", stackInternal.Metadata.Name))
		}
	} else if !force {
		// Validate no stacks exist
		var stacks []intmodel.Stack
		stacks, err = b.ListStacks(realmName, name)
		if err != nil {
			return result, fmt.Errorf("failed to list stacks: %w", err)
		}
		if len(stacks) > 0 {
			return result, fmt.Errorf("%w: space %q has %d stack(s). Use --cascade to purge them or --force to skip validation", errdefs.ErrResourceHasDependencies, name, len(stacks))
		}
	}

	// Perform standard delete first
	deleteResult, err := b.DeleteSpace(internalSpace, force, cascade)
	if err != nil {
		result.Purged = append(result.Purged, fmt.Sprintf("delete-warning:%v", err))
	} else {
		result.Deleted = append(result.Deleted, deleteResult.Deleted...)
		result.MetadataDeleted = deleteResult.MetadataDeleted
		result.CgroupDeleted = deleteResult.CgroupDeleted
		result.CNINetworkDeleted = deleteResult.CNINetworkDeleted
		// Update result space with deleted space
		result.Space = deleteResult.Space
	}

	// Perform comprehensive purge
	if err = b.runner.PurgeSpace(internalSpace); err != nil {
		result.Purged = append(result.Purged, fmt.Sprintf("purge-error:%v", err))
		result.PurgeSucceeded = false
	} else {
		result.Purged = append(result.Purged, "cni-network", "cni-cache", "orphaned-containers", "all-metadata")
		result.PurgeSucceeded = true
	}

	return result, nil
}

// PurgeStack purges a stack with comprehensive cleanup. If cascade is true, purges all cells first.
// If force is true, skips validation of child resources.
func (b *Exec) PurgeStack(stack intmodel.Stack, force, cascade bool) (PurgeStackResult, error) {
	var result PurgeStackResult

	name := strings.TrimSpace(stack.Metadata.Name)
	if name == "" {
		return result, errdefs.ErrStackNameRequired
	}

	realmName := strings.TrimSpace(stack.Spec.RealmName)
	if realmName == "" {
		return result, errdefs.ErrRealmNameRequired
	}

	spaceName := strings.TrimSpace(stack.Spec.SpaceName)
	if spaceName == "" {
		return result, errdefs.ErrSpaceNameRequired
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
		return result, err
	}
	if !getResult.MetadataExists {
		return result, fmt.Errorf("stack %q not found in realm %q, space %q", name, realmName, spaceName)
	}

	internalStack := getResult.Stack

	// Initialize result with stack and flags
	result = PurgeStackResult{
		Stack:   internalStack,
		Deleted: []string{},
		Purged:  []string{},
	}

	// If cascade, purge all cells first
	if cascade {
		var cells []intmodel.Cell
		cells, err = b.ListCells(realmName, spaceName, name)
		if err != nil {
			return result, fmt.Errorf("failed to list cells: %w", err)
		}
		for _, cellInternal := range cells {
			_, err = b.PurgeCell(cellInternal, force, false)
			if err != nil {
				return result, fmt.Errorf("failed to purge cell %q: %w", cellInternal.Metadata.Name, err)
			}
			result.Deleted = append(result.Deleted, fmt.Sprintf("cell:%s", cellInternal.Metadata.Name))
		}
	} else if !force {
		// Validate no cells exist
		var cells []intmodel.Cell
		cells, err = b.ListCells(realmName, spaceName, name)
		if err != nil {
			return result, fmt.Errorf("failed to list cells: %w", err)
		}
		if len(cells) > 0 {
			return result, fmt.Errorf("%w: stack %q has %d cell(s). Use --cascade to purge them or --force to skip validation", errdefs.ErrResourceHasDependencies, name, len(cells))
		}
	}

	// Perform standard delete first
	deleteResult, err := b.DeleteStack(internalStack, force, cascade)
	if err != nil {
		result.Purged = append(result.Purged, fmt.Sprintf("delete-warning:%v", err))
	} else {
		result.Deleted = append(result.Deleted, deleteResult.Deleted...)
		// Update result stack with deleted stack
		result.Stack = deleteResult.Stack
	}

	// Perform comprehensive purge
	if err = b.runner.PurgeStack(internalStack); err != nil {
		result.Purged = append(result.Purged, fmt.Sprintf("purge-error:%v", err))
	} else {
		result.Purged = append(result.Purged, "cni-resources", "orphaned-containers", "all-metadata")
	}

	return result, nil
}

// PurgeCell purges a cell with comprehensive cleanup. Always purges all containers first.
// If force is true, skips validation (currently unused but recorded for auditing).
func (b *Exec) PurgeCell(cell intmodel.Cell, force, cascade bool) (PurgeCellResult, error) {
	var result PurgeCellResult

	name := strings.TrimSpace(cell.Metadata.Name)
	if name == "" {
		return result, errdefs.ErrCellNameRequired
	}

	realmName := strings.TrimSpace(cell.Spec.RealmName)
	if realmName == "" {
		return result, errdefs.ErrRealmNameRequired
	}

	spaceName := strings.TrimSpace(cell.Spec.SpaceName)
	if spaceName == "" {
		return result, errdefs.ErrSpaceNameRequired
	}

	stackName := strings.TrimSpace(cell.Spec.StackName)
	if stackName == "" {
		return result, errdefs.ErrStackNameRequired
	}

	// Build lookup cell for GetCell
	lookupCell := intmodel.Cell{
		Metadata: intmodel.CellMetadata{
			Name: name,
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
			return result, fmt.Errorf(
				"cell %q not found in realm %q, space %q, stack %q",
				name,
				realmName,
				spaceName,
				stackName,
			)
		}
		return result, err
	}

	internalCell := getResult.Cell

	// Initialize result with cell and flags
	result = PurgeCellResult{
		Cell:    internalCell,
		Force:   force,
		Cascade: cascade,
		Deleted: []string{},
		Purged:  []string{},
	}

	// Perform standard delete first
	deleteResult, err := b.DeleteCell(internalCell)
	if err != nil {
		result.Purged = append(result.Purged, fmt.Sprintf("delete-warning:%v", err))
	} else {
		result.ContainersDeleted = deleteResult.ContainersDeleted
		result.CgroupDeleted = deleteResult.CgroupDeleted
		result.MetadataDeleted = deleteResult.MetadataDeleted

		if deleteResult.ContainersDeleted {
			result.Deleted = append(result.Deleted, "containers")
		}
		if deleteResult.CgroupDeleted {
			result.Deleted = append(result.Deleted, "cgroup")
		}
		if deleteResult.MetadataDeleted {
			result.Deleted = append(result.Deleted, "metadata")
		}
		// Update result cell with deleted cell
		result.Cell = deleteResult.Cell
	}

	// Perform comprehensive purge
	if err = b.runner.PurgeCell(internalCell); err != nil {
		result.Purged = append(result.Purged, fmt.Sprintf("purge-error:%v", err))
		result.PurgeSucceeded = false
	} else {
		result.Purged = append(result.Purged, "cni-resources", "orphaned-containers", "all-metadata")
		result.PurgeSucceeded = true
	}

	return result, nil
}

// PurgeContainer purges a single container with comprehensive cleanup. Cascade flag is not applicable.
func (b *Exec) PurgeContainer(container intmodel.Container) (PurgeContainerResult, error) {
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
	getResult, err := b.GetCell(lookupCell)
	if err != nil {
		if errors.Is(err, errdefs.ErrCellNotFound) {
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
	result.CellMetadataExists = getResult.MetadataExists

	if !getResult.MetadataExists {
		return result, fmt.Errorf("cell %q not found", cellName)
	}

	internalCell := getResult.Cell

	// Check if container exists in cell metadata by name (ID now stores just the container name)
	var foundContainerSpec *intmodel.ContainerSpec

	// Check root container first
	if internalCell.Spec.RootContainer != nil && internalCell.Spec.RootContainer.ID == name {
		foundContainerSpec = internalCell.Spec.RootContainer
		result.ContainerExists = true
	} else {
		// Check regular containers
		for i := range internalCell.Spec.Containers {
			if internalCell.Spec.Containers[i].ID == name {
				foundContainerSpec = &internalCell.Spec.Containers[i]
				result.ContainerExists = true
				break
			}
		}
	}

	// Perform standard delete if container is in metadata
	if foundContainerSpec != nil {
		var deleteResult DeleteContainerResult
		deleteResult, err = b.DeleteContainer(container)
		if err != nil {
			result.Purged = append(result.Purged, fmt.Sprintf("delete-warning:%v", err))
		} else {
			result.Deleted = append(result.Deleted, deleteResult.Deleted...)
			result.CellMetadataExists = deleteResult.CellMetadataExists
			result.ContainerExists = deleteResult.ContainerExists
			// Update result container with deleted container
			result.Container = deleteResult.Container
		}
	}

	// Get realm to pass to runner.PurgeContainer
	lookupRealm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: realmName,
		},
	}
	realmGetResult, err := b.GetRealm(lookupRealm)
	if err != nil {
		return result, fmt.Errorf("failed to get realm: %w", err)
	}
	if !realmGetResult.MetadataExists {
		return result, fmt.Errorf("realm %q not found", realmName)
	}

	// Use internal realm directly for runner.PurgeContainer
	internalRealm := realmGetResult.Realm
	// Use container name directly for containerd operations
	if err = b.runner.PurgeContainer(internalRealm, name); err != nil {
		result.Purged = append(result.Purged, fmt.Sprintf("purge-error:%v", err))
	} else {
		result.Purged = append(result.Purged, "cni-resources", "ipam-allocation", "cache-entries")
	}

	return result, nil
}
