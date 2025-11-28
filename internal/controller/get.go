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

// GetRealmResult reports the current state of a realm.
type GetRealmResult struct {
	Realm                     intmodel.Realm
	MetadataExists            bool
	CgroupExists              bool
	ContainerdNamespaceExists bool
}

// GetRealm retrieves a single realm and reports its current state.
func (b *Exec) GetRealm(realm intmodel.Realm) (GetRealmResult, error) {
	var res GetRealmResult

	name := strings.TrimSpace(realm.Metadata.Name)
	if name == "" {
		return res, errdefs.ErrRealmNameRequired
	}

	namespace := strings.TrimSpace(realm.Spec.Namespace)
	if namespace == "" {
		namespace = name
	}

	// Build lookup realm for runner
	lookupRealm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: name,
		},
	}

	// Call runner with internal type
	internalRealm, err := b.runner.GetRealm(lookupRealm)
	if err != nil {
		if errors.Is(err, errdefs.ErrRealmNotFound) {
			res.MetadataExists = false
		} else {
			return res, fmt.Errorf("%w: %w", errdefs.ErrGetRealm, err)
		}
	} else {
		res.MetadataExists = true

		res.CgroupExists, err = b.runner.ExistsCgroup(internalRealm)
		if err != nil {
			return res, fmt.Errorf("failed to check if realm cgroup exists: %w", err)
		}
		res.Realm = internalRealm
	}

	res.ContainerdNamespaceExists, err = b.runner.ExistsRealmContainerdNamespace(namespace)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrCheckNamespaceExists, err)
	}

	return res, nil
}

// ListRealms lists all realms.
func (b *Exec) ListRealms() ([]intmodel.Realm, error) {
	return b.runner.ListRealms()
}

// GetSpaceResult reports the current state of a space.
type GetSpaceResult struct {
	Space            intmodel.Space
	MetadataExists   bool
	CgroupExists     bool
	CNINetworkExists bool
}

// GetSpace retrieves a single space and reports its current state.
func (b *Exec) GetSpace(space intmodel.Space) (GetSpaceResult, error) {
	var res GetSpaceResult

	name := strings.TrimSpace(space.Metadata.Name)
	if name == "" {
		return res, errdefs.ErrSpaceNameRequired
	}
	realmName := strings.TrimSpace(space.Spec.RealmName)
	if realmName == "" {
		return res, errdefs.ErrRealmNameRequired
	}

	// Build lookup space for runner
	lookupSpace := intmodel.Space{
		Metadata: intmodel.SpaceMetadata{
			Name: name,
		},
		Spec: intmodel.SpaceSpec{
			RealmName: realmName,
		},
	}

	// Call runner with internal type
	internalSpace, err := b.runner.GetSpace(lookupSpace)
	if err != nil {
		if errors.Is(err, errdefs.ErrSpaceNotFound) {
			res.MetadataExists = false
		} else {
			return res, fmt.Errorf("%w: %w", errdefs.ErrGetSpace, err)
		}
	} else {
		res.MetadataExists = true

		res.CgroupExists, err = b.runner.ExistsCgroup(internalSpace)
		if err != nil {
			return res, fmt.Errorf("failed to check if space cgroup exists: %w", err)
		}
		res.Space = internalSpace
	}

	res.CNINetworkExists, err = b.runner.ExistsSpaceCNIConfig(lookupSpace)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, err)
	}

	return res, nil
}

// ListSpaces lists all spaces, optionally filtered by realm.
func (b *Exec) ListSpaces(realmName string) ([]intmodel.Space, error) {
	return b.runner.ListSpaces(realmName)
}

// GetStackResult reports the current state of a stack.
type GetStackResult struct {
	Stack          intmodel.Stack
	MetadataExists bool
	CgroupExists   bool
}

// GetStack retrieves a single stack and reports its current state.
func (b *Exec) GetStack(stack intmodel.Stack) (GetStackResult, error) {
	var res GetStackResult

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

	// Build lookup stack for runner
	lookupStack := intmodel.Stack{
		Metadata: intmodel.StackMetadata{
			Name: name,
		},
		Spec: intmodel.StackSpec{
			RealmName: realmName,
			SpaceName: spaceName,
		},
	}

	internalStack, err := b.runner.GetStack(lookupStack)
	if err != nil {
		if errors.Is(err, errdefs.ErrStackNotFound) {
			res.MetadataExists = false
		} else {
			return res, fmt.Errorf("%w: %w", errdefs.ErrGetStack, err)
		}
	} else {
		res.MetadataExists = true
		res.CgroupExists, err = b.runner.ExistsCgroup(internalStack)
		if err != nil {
			return res, fmt.Errorf("failed to check if stack cgroup exists: %w", err)
		}
		res.Stack = internalStack
	}

	return res, nil
}

// ListStacks lists all stacks, optionally filtered by realm and/or space.
func (b *Exec) ListStacks(realmName, spaceName string) ([]intmodel.Stack, error) {
	return b.runner.ListStacks(realmName, spaceName)
}

// GetCellResult reports the current state of a cell.
type GetCellResult struct {
	Cell                intmodel.Cell
	MetadataExists      bool
	CgroupExists        bool
	RootContainerExists bool
}

// GetCell retrieves a single cell and reports its current state.
func (b *Exec) GetCell(cell intmodel.Cell) (GetCellResult, error) {
	var res GetCellResult

	name := strings.TrimSpace(cell.Metadata.Name)
	if name == "" {
		return res, errdefs.ErrCellNameRequired
	}
	realmName := strings.TrimSpace(cell.Spec.RealmName)
	if realmName == "" {
		return res, errdefs.ErrRealmNameRequired
	}
	spaceName := strings.TrimSpace(cell.Spec.SpaceName)
	if spaceName == "" {
		return res, errdefs.ErrSpaceNameRequired
	}
	stackName := strings.TrimSpace(cell.Spec.StackName)
	if stackName == "" {
		return res, errdefs.ErrStackNameRequired
	}

	// Build lookup cell for runner
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

	internalCell, err := b.runner.GetCell(lookupCell)
	if err != nil {
		if errors.Is(err, errdefs.ErrCellNotFound) {
			res.MetadataExists = false
		} else {
			return res, fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
		}
	} else {
		res.MetadataExists = true
		// Verify realm, space, and stack match
		if internalCell.Spec.RealmName != realmName {
			return res, fmt.Errorf("cell %q not found in realm %q (found in realm %q) at run-path %q",
				name, realmName, internalCell.Spec.RealmName, b.opts.RunPath)
		}
		if internalCell.Spec.SpaceName != spaceName {
			return res, fmt.Errorf("cell %q not found in space %q (found in space %q) at run-path %q",
				name, spaceName, internalCell.Spec.SpaceName, b.opts.RunPath)
		}
		if internalCell.Spec.StackName != stackName {
			return res, fmt.Errorf("cell %q not found in stack %q (found in stack %q) at run-path %q",
				name, stackName, internalCell.Spec.StackName, b.opts.RunPath)
		}
		res.CgroupExists, err = b.runner.ExistsCgroup(internalCell)
		if err != nil {
			return res, fmt.Errorf("failed to check if cell cgroup exists: %w", err)
		}
		res.RootContainerExists, err = b.runner.ExistsCellRootContainer(internalCell)
		if err != nil {
			return res, fmt.Errorf("failed to check root container: %w", err)
		}
		res.Cell = internalCell
	}

	return res, nil
}

// ListCells lists all cells, optionally filtered by realm, space, and/or stack.
func (b *Exec) ListCells(realmName, spaceName, stackName string) ([]intmodel.Cell, error) {
	return b.runner.ListCells(realmName, spaceName, stackName)
}

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
		if errors.Is(err, errdefs.ErrCellNotFound) {
			res.CellMetadataExists = false
		} else {
			return res, fmt.Errorf("failed to get cell %q: %w", cellName, err)
		}
	} else {
		res.CellMetadataExists = true
		if !cellResult.MetadataExists {
			return res, fmt.Errorf("cell %q not found", cellName)
		}

		// Find container in cell spec by name (ID now stores just the container name)
		var foundContainerSpec *intmodel.ContainerSpec

		// Check root container first
		if cellResult.Cell.Spec.RootContainer != nil && cellResult.Cell.Spec.RootContainer.ID == name {
			foundContainerSpec = cellResult.Cell.Spec.RootContainer
		} else {
			// Check regular containers
			for i := range cellResult.Cell.Spec.Containers {
				if cellResult.Cell.Spec.Containers[i].ID == name {
					foundContainerSpec = &cellResult.Cell.Spec.Containers[i]
					break
				}
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

// validateAndGetCell validates cell input parameters and retrieves the cell.
// It performs validation of name, realmName, spaceName, and stackName,
// builds a lookup cell, calls GetCell, and handles errors appropriately.
func (b *Exec) validateAndGetCell(cell intmodel.Cell) (intmodel.Cell, error) {
	name := strings.TrimSpace(cell.Metadata.Name)
	if name == "" {
		return intmodel.Cell{}, errdefs.ErrCellNameRequired
	}

	realmName := strings.TrimSpace(cell.Spec.RealmName)
	if realmName == "" {
		return intmodel.Cell{}, errdefs.ErrRealmNameRequired
	}

	spaceName := strings.TrimSpace(cell.Spec.SpaceName)
	if spaceName == "" {
		return intmodel.Cell{}, errdefs.ErrSpaceNameRequired
	}

	stackName := strings.TrimSpace(cell.Spec.StackName)
	if stackName == "" {
		return intmodel.Cell{}, errdefs.ErrStackNameRequired
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
			return intmodel.Cell{}, fmt.Errorf(
				"cell %q not found in realm %q, space %q, stack %q",
				name,
				realmName,
				spaceName,
				stackName,
			)
		}
		return intmodel.Cell{}, err
	}
	if !getResult.MetadataExists {
		return intmodel.Cell{}, fmt.Errorf("cell %q not found", name)
	}

	return getResult.Cell, nil
}
