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
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/metadata"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// GetRealmResult reports the current state of a realm.
type GetRealmResult struct {
	Realm                     intmodel.Realm
	MetadataExists            bool
	CgroupExists              bool
	ContainerdNamespaceExists bool
}

// GetSpaceResult reports the current state of a space.
type GetSpaceResult struct {
	Space            intmodel.Space
	MetadataExists   bool
	CgroupExists     bool
	CNINetworkExists bool
}

// GetStackResult reports the current state of a stack.
type GetStackResult struct {
	Stack          intmodel.Stack
	MetadataExists bool
	CgroupExists   bool
}

// GetContainerResult reports the current state of a container.
type GetContainerResult struct {
	Container          intmodel.Container
	CellMetadataExists bool
	ContainerExists    bool
}

// GetCellResult reports the current state of a cell.
type GetCellResult struct {
	Cell                intmodel.Cell
	MetadataExists      bool
	CgroupExists        bool
	RootContainerExists bool
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
func (b *Exec) ListRealms() ([]*v1beta1.RealmDoc, error) {
	realmsDir := b.opts.RunPath
	return listResources[v1beta1.RealmDoc](b.ctx, b.logger, realmsDir)
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
func (b *Exec) ListSpaces(realmName string) ([]*v1beta1.SpaceDoc, error) {
	realmDirs, err := b.listRealmDirs(realmName)
	if err != nil {
		return nil, err
	}

	spaces := make([]*v1beta1.SpaceDoc, 0)
	for _, dir := range realmDirs {
		items, listErr := listResources[v1beta1.SpaceDoc](b.ctx, b.logger, dir)
		if listErr != nil {
			return nil, listErr
		}
		spaces = append(spaces, items...)
	}

	// Filter by realm if specified
	if realmName != "" {
		filtered := make([]*v1beta1.SpaceDoc, 0)
		for _, space := range spaces {
			if space.Spec.RealmID == realmName {
				filtered = append(filtered, space)
			}
		}
		return filtered, nil
	}

	return spaces, nil
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
func (b *Exec) ListStacks(realmName, spaceName string) ([]*v1beta1.StackDoc, error) {
	spaceDirs, err := b.listSpaceDirs(realmName, spaceName)
	if err != nil {
		return nil, err
	}

	stacks := make([]*v1beta1.StackDoc, 0)
	for _, dir := range spaceDirs {
		items, listErr := listResources[v1beta1.StackDoc](b.ctx, b.logger, dir)
		if listErr != nil {
			return nil, listErr
		}
		stacks = append(stacks, items...)
	}

	// Filter by realm and/or space if specified
	if realmName != "" || spaceName != "" {
		filtered := make([]*v1beta1.StackDoc, 0)
		for _, stack := range stacks {
			realmMatch := realmName == "" || stack.Spec.RealmID == realmName
			spaceMatch := spaceName == "" || stack.Spec.SpaceID == spaceName
			if realmMatch && spaceMatch {
				filtered = append(filtered, stack)
			}
		}
		return filtered, nil
	}

	return stacks, nil
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
func (b *Exec) ListCells(realmName, spaceName, stackName string) ([]*v1beta1.CellDoc, error) {
	stackDirs, err := b.listStackDirs(realmName, spaceName, stackName)
	if err != nil {
		return nil, err
	}

	cells := make([]*v1beta1.CellDoc, 0)
	for _, dir := range stackDirs {
		items, listErr := listResources[v1beta1.CellDoc](b.ctx, b.logger, dir)
		if listErr != nil {
			return nil, listErr
		}
		cells = append(cells, items...)
	}

	// Filter by realm, space, and/or stack if specified
	if realmName != "" || spaceName != "" || stackName != "" {
		filtered := make([]*v1beta1.CellDoc, 0)
		for _, cell := range cells {
			realmMatch := realmName == "" || cell.Spec.RealmID == realmName
			spaceMatch := spaceName == "" || cell.Spec.SpaceID == spaceName
			stackMatch := stackName == "" || cell.Spec.StackID == stackName
			if realmMatch && spaceMatch && stackMatch {
				filtered = append(filtered, cell)
			}
		}
		return filtered, nil
	}

	return cells, nil
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
func (b *Exec) ListContainers(realmName, spaceName, stackName, cellName string) ([]*v1beta1.ContainerSpec, error) {
	var cells []*v1beta1.CellDoc

	if cellName != "" {
		// For autocomplete, we can read directly from metadata files without calling containerd
		// This avoids the containerd connection that GetCell would trigger via ExistsCellRootContainer
		cellDir := fs.CellMetadataDir(b.opts.RunPath, realmName, spaceName, stackName, cellName)
		metadataPath := filepath.Join(cellDir, consts.KukeonMetadataFile)

		// Try to read cell metadata directly
		cell, readErr := metadata.ReadMetadata[v1beta1.CellDoc](b.ctx, b.logger, metadataPath)
		if readErr != nil {
			// If metadata file doesn't exist, return empty list (not an error for autocomplete)
			if os.IsNotExist(readErr) {
				return []*v1beta1.ContainerSpec{}, nil
			}
			// For other errors, fall back to GetCell (which may call containerd)
			// This preserves existing behavior for non-autocomplete use cases
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
			result, getErr := b.GetCell(lookupCell)
			if getErr != nil {
				return nil, getErr
			}
			if !result.MetadataExists {
				return nil, fmt.Errorf("cell %q not found", cellName)
			}
			// Convert back to external for cells list (temporary until ListContainers is refactored)
			cellDoc, convertErr := apischeme.BuildCellExternalFromInternal(result.Cell, apischeme.VersionV1Beta1)
			if convertErr != nil {
				return nil, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, convertErr)
			}
			cells = []*v1beta1.CellDoc{&cellDoc}
		} else {
			// Successfully read from metadata file - use it directly without containerd
			cells = []*v1beta1.CellDoc{&cell}
		}
	} else {
		// List all cells matching filters
		var listErr error
		cells, listErr = b.ListCells(realmName, spaceName, stackName)
		if listErr != nil {
			return nil, listErr
		}
	}

	// Extract containers from cells
	containers := make([]*v1beta1.ContainerSpec, 0)
	for _, cell := range cells {
		if cell.Spec.RootContainer != nil {
			cell.Spec.RootContainer.Root = true
			containers = append(containers, cell.Spec.RootContainer)
		}
		for i := range cell.Spec.Containers {
			containers = append(containers, &cell.Spec.Containers[i])
		}
	}

	return containers, nil
}

// listResources is a generic helper to list resources from a metadata directory.
func listResources[T any](ctx context.Context, logger *slog.Logger, dir string) ([]*T, error) {
	var results []*T

	// Check if directory exists
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return results, nil // Return empty list if directory doesn't exist
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory %q: %w", dir, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		metadataPath := filepath.Join(dir, entry.Name(), consts.KukeonMetadataFile)
		doc, readErr := metadata.ReadMetadata[T](ctx, logger, metadataPath)
		if readErr != nil {
			// Skip files that can't be read (might be incomplete or corrupted)
			logger.DebugContext(ctx, "skipping metadata file", "path", metadataPath, "error", readErr)
			continue
		}

		results = append(results, &doc)
	}

	return results, nil
}

func (b *Exec) listRealmDirs(realmName string) ([]string, error) {
	base := b.opts.RunPath
	if strings.TrimSpace(realmName) != "" {
		dir := fs.RealmMetadataDir(b.opts.RunPath, realmName)
		info, err := os.Stat(dir)
		if err != nil {
			if os.IsNotExist(err) {
				return []string{}, nil
			}
			return nil, fmt.Errorf("failed to stat realm dir %q: %w", dir, err)
		}
		if !info.IsDir() {
			return []string{}, nil
		}
		return []string{dir}, nil
	}

	entries, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("failed to read realm directory %q: %w", base, err)
	}

	dirs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, filepath.Join(base, entry.Name()))
		}
	}
	return dirs, nil
}

func (b *Exec) listSpaceDirs(realmName, spaceName string) ([]string, error) {
	realmDirs, err := b.listRealmDirs(realmName)
	if err != nil {
		return nil, err
	}

	dirs := make([]string, 0)
	for _, realmDir := range realmDirs {
		if strings.TrimSpace(spaceName) != "" {
			dir := filepath.Join(realmDir, spaceName)
			info, statErr := os.Stat(dir)
			if statErr != nil {
				if os.IsNotExist(statErr) {
					continue
				}
				return nil, fmt.Errorf("failed to stat space dir %q: %w", dir, statErr)
			}
			if info.IsDir() {
				dirs = append(dirs, dir)
			}
			continue
		}

		entries, readErr := os.ReadDir(realmDir)
		if readErr != nil {
			if os.IsNotExist(readErr) {
				continue
			}
			return nil, fmt.Errorf("failed to read realm dir %q: %w", realmDir, readErr)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				dirs = append(dirs, filepath.Join(realmDir, entry.Name()))
			}
		}
	}
	return dirs, nil
}

func (b *Exec) listStackDirs(realmName, spaceName, stackName string) ([]string, error) {
	spaceDirs, err := b.listSpaceDirs(realmName, spaceName)
	if err != nil {
		return nil, err
	}

	dirs := make([]string, 0)
	for _, spaceDir := range spaceDirs {
		if strings.TrimSpace(stackName) != "" {
			dir := filepath.Join(spaceDir, stackName)
			info, statErr := os.Stat(dir)
			if statErr != nil {
				if os.IsNotExist(statErr) {
					continue
				}
				return nil, fmt.Errorf("failed to stat stack dir %q: %w", dir, statErr)
			}
			if info.IsDir() {
				dirs = append(dirs, dir)
			}
			continue
		}

		entries, readErr := os.ReadDir(spaceDir)
		if readErr != nil {
			if os.IsNotExist(readErr) {
				continue
			}
			return nil, fmt.Errorf("failed to read space dir %q: %w", spaceDir, readErr)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				dirs = append(dirs, filepath.Join(spaceDir, entry.Name()))
			}
		}
	}
	return dirs, nil
}
