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

// PurgeRealmResult reports what was purged during realm purging.
type PurgeRealmResult struct {
	RealmName string
	Deleted   []string // Resources that were deleted (standard cleanup)
	Purged    []string // Additional resources purged (CNI, orphaned containers, etc.)
}

// PurgeSpaceResult reports what was purged during space purging.
type PurgeSpaceResult struct {
	SpaceName string
	RealmName string
	Deleted   []string // Resources that were deleted (standard cleanup)
	Purged    []string // Additional resources purged (CNI, orphaned containers, etc.)
}

// PurgeStackResult reports what was purged during stack purging.
type PurgeStackResult struct {
	StackName string
	RealmName string
	SpaceName string
	Deleted   []string // Resources that were deleted (standard cleanup)
	Purged    []string // Additional resources purged (CNI, orphaned containers, etc.)
}

// PurgeCellResult reports what was purged during cell purging.
type PurgeCellResult struct {
	CellName  string
	RealmName string
	SpaceName string
	StackName string
	Deleted   []string // Resources that were deleted (standard cleanup)
	Purged    []string // Additional resources purged (CNI, orphaned containers, etc.)
}

// PurgeContainerResult reports what was purged during container purging.
type PurgeContainerResult struct {
	ContainerName string
	RealmName     string
	SpaceName     string
	StackName     string
	CellName      string
	Deleted       []string // Resources that were deleted (standard cleanup)
	Purged        []string // Additional resources purged (CNI, orphaned containers, etc.)
}

// PurgeRealm purges a realm with comprehensive cleanup. If cascade is true, purges all spaces first.
// If force is true, skips validation of child resources.
func (b *Exec) PurgeRealm(name string, force, cascade bool) (*PurgeRealmResult, error) {
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

	result := &PurgeRealmResult{
		RealmName: name,
		Deleted:   []string{},
		Purged:    []string{},
	}

	// If cascade, purge all spaces first
	if cascade {
		var spaces []*v1beta1.SpaceDoc
		spaces, err = b.ListSpaces(name)
		if err != nil {
			return nil, fmt.Errorf("failed to list spaces: %w", err)
		}
		for _, space := range spaces {
			_, err = b.PurgeSpace(space.Metadata.Name, name, force, cascade)
			if err != nil {
				return nil, fmt.Errorf("failed to purge space %q: %w", space.Metadata.Name, err)
			}
			result.Deleted = append(result.Deleted, fmt.Sprintf("space:%s", space.Metadata.Name))
		}
	} else if !force {
		// Validate no spaces exist
		var spaces []*v1beta1.SpaceDoc
		spaces, err = b.ListSpaces(name)
		if err != nil {
			return nil, fmt.Errorf("failed to list spaces: %w", err)
		}
		if len(spaces) > 0 {
			return nil, fmt.Errorf("%w: realm %q has %d space(s). Use --cascade to purge them or --force to skip validation", errdefs.ErrResourceHasDependencies, name, len(spaces))
		}
	}

	// Perform standard delete first
	deleteResult, err := b.DeleteRealm(name, force, cascade)
	if err != nil {
		// Log but continue with purge
		result.Purged = append(result.Purged, fmt.Sprintf("delete-warning:%v", err))
	} else {
		result.Deleted = deleteResult.Deleted
	}

	// Perform comprehensive purge
	doc := &v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{
			Name: name,
		},
	}
	if err = b.runner.PurgeRealm(doc); err != nil {
		result.Purged = append(result.Purged, fmt.Sprintf("purge-error:%v", err))
	} else {
		result.Purged = append(result.Purged, "orphaned-containers", "cni-resources", "all-metadata")
	}

	return result, nil
}

// PurgeSpace purges a space with comprehensive cleanup. If cascade is true, purges all stacks first.
// If force is true, skips validation of child resources.
func (b *Exec) PurgeSpace(name, realmName string, force, cascade bool) (*PurgeSpaceResult, error) {
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

	result := &PurgeSpaceResult{
		SpaceName: name,
		RealmName: realmName,
		Deleted:   []string{},
		Purged:    []string{},
	}

	// If cascade, purge all stacks first (recursively cascades to cells)
	if cascade {
		var stacks []*v1beta1.StackDoc
		stacks, err = b.ListStacks(realmName, name)
		if err != nil {
			return nil, fmt.Errorf("failed to list stacks: %w", err)
		}
		for _, stack := range stacks {
			_, err = b.PurgeStack(stack.Metadata.Name, realmName, name, force, cascade)
			if err != nil {
				return nil, fmt.Errorf("failed to purge stack %q: %w", stack.Metadata.Name, err)
			}
			result.Deleted = append(result.Deleted, fmt.Sprintf("stack:%s", stack.Metadata.Name))
		}
	} else if !force {
		// Validate no stacks exist
		var stacks []*v1beta1.StackDoc
		stacks, err = b.ListStacks(realmName, name)
		if err != nil {
			return nil, fmt.Errorf("failed to list stacks: %w", err)
		}
		if len(stacks) > 0 {
			return nil, fmt.Errorf("%w: space %q has %d stack(s). Use --cascade to purge them or --force to skip validation", errdefs.ErrResourceHasDependencies, name, len(stacks))
		}
	}

	// Perform standard delete first
	deleteResult, err := b.DeleteSpace(name, realmName, force, cascade)
	if err != nil {
		result.Purged = append(result.Purged, fmt.Sprintf("delete-warning:%v", err))
	} else {
		result.Deleted = deleteResult.Deleted
	}

	// Perform comprehensive purge
	doc := &v1beta1.SpaceDoc{
		Metadata: v1beta1.SpaceMetadata{
			Name: name,
		},
		Spec: v1beta1.SpaceSpec{
			RealmID: realmName,
		},
	}
	if err = b.runner.PurgeSpace(doc); err != nil {
		result.Purged = append(result.Purged, fmt.Sprintf("purge-error:%v", err))
	} else {
		result.Purged = append(result.Purged, "cni-network", "cni-cache", "orphaned-containers", "all-metadata")
	}

	return result, nil
}

// PurgeStack purges a stack with comprehensive cleanup. If cascade is true, purges all cells first.
// If force is true, skips validation of child resources.
func (b *Exec) PurgeStack(name, realmName, spaceName string, force, cascade bool) (*PurgeStackResult, error) {
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

	result := &PurgeStackResult{
		StackName: name,
		RealmName: realmName,
		SpaceName: spaceName,
		Deleted:   []string{},
		Purged:    []string{},
	}

	// If cascade, purge all cells first
	if cascade {
		var cells []*v1beta1.CellDoc
		cells, err = b.ListCells(realmName, spaceName, name)
		if err != nil {
			return nil, fmt.Errorf("failed to list cells: %w", err)
		}
		for _, cell := range cells {
			_, err = b.PurgeCell(cell.Metadata.Name, realmName, spaceName, name, force, false)
			if err != nil {
				return nil, fmt.Errorf("failed to purge cell %q: %w", cell.Metadata.Name, err)
			}
			result.Deleted = append(result.Deleted, fmt.Sprintf("cell:%s", cell.Metadata.Name))
		}
	} else if !force {
		// Validate no cells exist
		var cells []*v1beta1.CellDoc
		cells, err = b.ListCells(realmName, spaceName, name)
		if err != nil {
			return nil, fmt.Errorf("failed to list cells: %w", err)
		}
		if len(cells) > 0 {
			return nil, fmt.Errorf("%w: stack %q has %d cell(s). Use --cascade to purge them or --force to skip validation", errdefs.ErrResourceHasDependencies, name, len(cells))
		}
	}

	// Perform standard delete first
	deleteResult, err := b.DeleteStack(name, realmName, spaceName, force, cascade)
	if err != nil {
		result.Purged = append(result.Purged, fmt.Sprintf("delete-warning:%v", err))
	} else {
		result.Deleted = deleteResult.Deleted
	}

	// Perform comprehensive purge
	doc := &v1beta1.StackDoc{
		Metadata: v1beta1.StackMetadata{
			Name: name,
		},
		Spec: v1beta1.StackSpec{
			RealmID: realmName,
			SpaceID: spaceName,
		},
	}
	if err = b.runner.PurgeStack(doc); err != nil {
		result.Purged = append(result.Purged, fmt.Sprintf("purge-error:%v", err))
	} else {
		result.Purged = append(result.Purged, "cni-resources", "orphaned-containers", "all-metadata")
	}

	return result, nil
}

// PurgeCell purges a cell with comprehensive cleanup. Always purges all containers first.
// If force is true, skips validation.
func (b *Exec) PurgeCell(name, realmName, spaceName, stackName string, force, cascade bool) (*PurgeCellResult, error) {
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
	_, err := b.GetCell(name, realmName, spaceName, stackName)
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

	result := &PurgeCellResult{
		CellName:  name,
		RealmName: realmName,
		SpaceName: spaceName,
		StackName: stackName,
		Deleted:   []string{},
		Purged:    []string{},
	}

	// Perform standard delete first
	deleteResult, err := b.DeleteCell(name, realmName, spaceName, stackName)
	if err != nil {
		result.Purged = append(result.Purged, fmt.Sprintf("delete-warning:%v", err))
	} else {
		result.Deleted = deleteResult.Deleted
	}

	// Perform comprehensive purge
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
	if err = b.runner.PurgeCell(doc); err != nil {
		result.Purged = append(result.Purged, fmt.Sprintf("purge-error:%v", err))
	} else {
		result.Purged = append(result.Purged, "cni-resources", "orphaned-containers", "all-metadata")
	}

	return result, nil
}

// PurgeContainer purges a single container with comprehensive cleanup. Cascade flag is not applicable.
func (b *Exec) PurgeContainer(name, realmName, spaceName, stackName, cellName string) (*PurgeContainerResult, error) {
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

	// Get realm to get namespace
	realmDoc, err := b.GetRealm(realmName)
	if err != nil {
		return nil, fmt.Errorf("failed to get realm: %w", err)
	}

	result := &PurgeContainerResult{
		ContainerName: name,
		RealmName:     realmName,
		SpaceName:     spaceName,
		StackName:     stackName,
		CellName:      cellName,
		Deleted:       []string{},
		Purged:        []string{},
	}

	// Build container ID
	containerID := naming.BuildContainerName(realmName, spaceName, cellName, name)

	// Check if container exists in cell metadata
	var foundContainer *v1beta1.ContainerSpec
	for i := range cellDoc.Spec.Containers {
		if cellDoc.Spec.Containers[i].ID == containerID {
			foundContainer = &cellDoc.Spec.Containers[i]
			break
		}
	}

	// Perform standard delete if container is in metadata
	if foundContainer != nil {
		var deleteResult *DeleteContainerResult
		deleteResult, err = b.DeleteContainer(name, realmName, spaceName, stackName, cellName)
		if err != nil {
			result.Purged = append(result.Purged, fmt.Sprintf("delete-warning:%v", err))
		} else {
			result.Deleted = deleteResult.Deleted
		}
	}

	// Perform comprehensive purge (works even if container not in metadata)
	if err = b.runner.PurgeContainer(containerID, realmDoc.Spec.Namespace); err != nil {
		result.Purged = append(result.Purged, fmt.Sprintf("purge-error:%v", err))
	} else {
		result.Purged = append(result.Purged, "cni-resources", "ipam-allocation", "cache-entries")
	}

	return result, nil
}
