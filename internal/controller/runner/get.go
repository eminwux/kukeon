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

package runner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
)

func (r *Exec) GetRealm(realm intmodel.Realm) (intmodel.Realm, error) {
	// Get realm metadata
	metadataFilePath := fs.RealmMetadataPath(r.opts.RunPath, realm.Metadata.Name)
	internalRealm, err := r.readRealmInternal(metadataFilePath)
	if err != nil {
		switch {
		case errors.Is(err, errdefs.ErrMissingMetadataFile):
			return intmodel.Realm{}, errdefs.ErrRealmNotFound
		case errors.Is(err, errdefs.ErrConversionFailed):
			return intmodel.Realm{}, err
		default:
			return intmodel.Realm{}, fmt.Errorf("%w: %w", errdefs.ErrGetRealm, err)
		}
	}

	return internalRealm, nil
}

func (r *Exec) GetSpace(space intmodel.Space) (intmodel.Space, error) {
	realmName := strings.TrimSpace(space.Spec.RealmName)
	if realmName == "" {
		return intmodel.Space{}, errdefs.ErrRealmNameRequired
	}
	spaceName := strings.TrimSpace(space.Metadata.Name)
	if spaceName == "" {
		return intmodel.Space{}, errdefs.ErrSpaceNameRequired
	}
	// Get space metadata
	metadataFilePath := fs.SpaceMetadataPath(r.opts.RunPath, realmName, spaceName)
	internalSpace, err := r.readSpaceInternal(metadataFilePath)
	if err != nil {
		switch {
		case errors.Is(err, errdefs.ErrMissingMetadataFile):
			return intmodel.Space{}, errdefs.ErrSpaceNotFound
		case errors.Is(err, errdefs.ErrConversionFailed):
			return intmodel.Space{}, err
		default:
			return intmodel.Space{}, fmt.Errorf("%w: %w", errdefs.ErrGetSpace, err)
		}
	}

	return internalSpace, nil
}

func (r *Exec) GetStack(stack intmodel.Stack) (intmodel.Stack, error) {
	realmName := strings.TrimSpace(stack.Spec.RealmName)
	if realmName == "" {
		return intmodel.Stack{}, errdefs.ErrRealmNameRequired
	}
	spaceName := strings.TrimSpace(stack.Spec.SpaceName)
	if spaceName == "" {
		return intmodel.Stack{}, errdefs.ErrSpaceNameRequired
	}
	stackName := strings.TrimSpace(stack.Metadata.Name)
	if stackName == "" {
		return intmodel.Stack{}, errdefs.ErrStackNameRequired
	}
	// Get stack metadata
	metadataFilePath := fs.StackMetadataPath(r.opts.RunPath, realmName, spaceName, stackName)
	internalStack, err := r.readStackInternal(metadataFilePath)
	if err != nil {
		switch {
		case errors.Is(err, errdefs.ErrMissingMetadataFile):
			return intmodel.Stack{}, errdefs.ErrStackNotFound
		case errors.Is(err, errdefs.ErrConversionFailed):
			return intmodel.Stack{}, err
		default:
			return intmodel.Stack{}, fmt.Errorf("%w: %w", errdefs.ErrGetStack, err)
		}
	}

	return internalStack, nil
}

func (r *Exec) GetCell(cell intmodel.Cell) (intmodel.Cell, error) {
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
	cellName := strings.TrimSpace(cell.Metadata.Name)
	if cellName == "" {
		return intmodel.Cell{}, errdefs.ErrCellNameRequired
	}
	// Get cell metadata
	metadataFilePath := fs.CellMetadataPath(r.opts.RunPath, realmName, spaceName, stackName, cellName)
	internalCell, err := r.readCellInternal(metadataFilePath)
	if err != nil {
		switch {
		case errors.Is(err, errdefs.ErrMissingMetadataFile):
			return intmodel.Cell{}, errdefs.ErrCellNotFound
		case errors.Is(err, errdefs.ErrConversionFailed):
			return intmodel.Cell{}, err
		default:
			return intmodel.Cell{}, fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
		}
	}

	return internalCell, nil
}

func (r *Exec) ListSpaces(realmName string) ([]intmodel.Space, error) {
	var results []intmodel.Space

	// Get realm directories to search in
	realmDirs, err := r.resolveRealmDirs(realmName)
	if err != nil {
		return nil, err
	}

	// For each realm directory, read spaces
	for _, realmDir := range realmDirs {
		realmNameFromDir := filepath.Base(realmDir)

		var entries []os.DirEntry
		entries, err = os.ReadDir(realmDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("failed to read realm dir %q: %w", realmDir, err)
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			spaceName := entry.Name()
			metadataPath := fs.SpaceMetadataPath(r.opts.RunPath, realmNameFromDir, spaceName)
			space, readErr := r.readSpaceInternal(metadataPath)
			if readErr != nil {
				// Skip directories that can't be read (might be incomplete or corrupted)
				r.logger.DebugContext(r.ctx, "skipping space metadata file", "path", metadataPath, "error", readErr)
				continue
			}

			// Filter by realm if specified (compare with internal type's RealmName)
			if realmName != "" && space.Spec.RealmName != realmName {
				continue
			}

			results = append(results, space)
		}
	}

	return results, nil
}

// resolveRealmDirs resolves realm directories to search based on realmName.
// If realmName is empty, returns all realm directories. If realmName is specified,
// returns a single realm directory if it exists. Returns empty slice if realm/base
// doesn't exist (graceful handling for listing operations).
func (r *Exec) resolveRealmDirs(realmName string) ([]string, error) {
	base := r.opts.RunPath
	var realmDirs []string

	if strings.TrimSpace(realmName) != "" {
		// Single realm specified
		dir := fs.RealmMetadataDir(r.opts.RunPath, realmName)
		info, err := os.Stat(dir)
		if err != nil {
			if os.IsNotExist(err) {
				return []string{}, nil // Return empty list if realm doesn't exist
			}
			return nil, fmt.Errorf("failed to stat realm dir %q: %w", dir, err)
		}
		if !info.IsDir() {
			return []string{}, nil
		}
		realmDirs = []string{dir}
	} else {
		// All realms
		if _, err := os.Stat(base); os.IsNotExist(err) {
			return []string{}, nil // Return empty list if base directory doesn't exist
		}

		entries, err := os.ReadDir(base)
		if err != nil {
			return nil, fmt.Errorf("failed to read realm directory %q: %w", base, err)
		}

		realmDirs = make([]string, 0, len(entries))
		for _, entry := range entries {
			if entry.IsDir() {
				realmDirs = append(realmDirs, filepath.Join(base, entry.Name()))
			}
		}
	}

	return realmDirs, nil
}

func (r *Exec) ListStacks(realmName, spaceName string) ([]intmodel.Stack, error) {
	// Get realm directories to search in
	realmDirs, err := r.resolveRealmDirs(realmName)
	if err != nil {
		return nil, err
	}

	// Get space directories from realm directories
	var spaceDirs []string
	for _, realmDir := range realmDirs {
		if strings.TrimSpace(spaceName) != "" {
			// Single space specified
			dir := filepath.Join(realmDir, spaceName)
			var info os.FileInfo
			info, err = os.Stat(dir)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, fmt.Errorf("failed to stat space dir %q: %w", dir, err)
			}
			if info.IsDir() {
				spaceDirs = append(spaceDirs, dir)
			}
		} else {
			// All spaces in this realm
			var entries []os.DirEntry
			entries, err = os.ReadDir(realmDir)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, fmt.Errorf("failed to read realm dir %q: %w", realmDir, err)
			}
			for _, entry := range entries {
				if entry.IsDir() {
					spaceDirs = append(spaceDirs, filepath.Join(realmDir, entry.Name()))
				}
			}
		}
	}

	// For each space directory, read stacks
	var stackResults []intmodel.Stack
	for _, spaceDir := range spaceDirs {
		realmNameFromPath := filepath.Base(filepath.Dir(spaceDir))
		spaceNameFromPath := filepath.Base(spaceDir)

		var entries []os.DirEntry
		entries, err = os.ReadDir(spaceDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("failed to read space dir %q: %w", spaceDir, err)
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			stackName := entry.Name()
			metadataPath := fs.StackMetadataPath(r.opts.RunPath, realmNameFromPath, spaceNameFromPath, stackName)
			stack, readErr := r.readStackInternal(metadataPath)
			if readErr != nil {
				// Skip directories that can't be read (might be incomplete or corrupted)
				r.logger.DebugContext(r.ctx, "skipping stack metadata file", "path", metadataPath, "error", readErr)
				continue
			}

			// Filter by realm and/or space if specified (compare with internal type's RealmName/SpaceName)
			if realmName != "" && stack.Spec.RealmName != realmName {
				continue
			}
			if spaceName != "" && stack.Spec.SpaceName != spaceName {
				continue
			}

			stackResults = append(stackResults, stack)
		}
	}

	return stackResults, nil
}

func (r *Exec) ListCells(realmName, spaceName, stackName string) ([]intmodel.Cell, error) {
	// Get realm directories to search in
	realmDirs, err := r.resolveRealmDirs(realmName)
	if err != nil {
		return nil, err
	}

	// Get space directories from realm directories
	var spaceDirs []string
	for _, realmDir := range realmDirs {
		if strings.TrimSpace(spaceName) != "" {
			// Single space specified
			dir := filepath.Join(realmDir, spaceName)
			var info os.FileInfo
			info, err = os.Stat(dir)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, fmt.Errorf("failed to stat space dir %q: %w", dir, err)
			}
			if info.IsDir() {
				spaceDirs = append(spaceDirs, dir)
			}
		} else {
			// All spaces in this realm
			var entries []os.DirEntry
			entries, err = os.ReadDir(realmDir)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, fmt.Errorf("failed to read realm dir %q: %w", realmDir, err)
			}
			for _, entry := range entries {
				if entry.IsDir() {
					spaceDirs = append(spaceDirs, filepath.Join(realmDir, entry.Name()))
				}
			}
		}
	}

	// Get stack directories from space directories
	var stackDirs []string
	for _, spaceDir := range spaceDirs {
		if strings.TrimSpace(stackName) != "" {
			// Single stack specified
			dir := filepath.Join(spaceDir, stackName)
			var info os.FileInfo
			info, err = os.Stat(dir)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, fmt.Errorf("failed to stat stack dir %q: %w", dir, err)
			}
			if info.IsDir() {
				stackDirs = append(stackDirs, dir)
			}
		} else {
			// All stacks in this space
			var entries []os.DirEntry
			entries, err = os.ReadDir(spaceDir)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, fmt.Errorf("failed to read space dir %q: %w", spaceDir, err)
			}
			for _, entry := range entries {
				if entry.IsDir() {
					stackDirs = append(stackDirs, filepath.Join(spaceDir, entry.Name()))
				}
			}
		}
	}

	// For each stack directory, read cells
	var cellResults []intmodel.Cell
	for _, stackDir := range stackDirs {
		realmNameFromPath := filepath.Base(filepath.Dir(filepath.Dir(stackDir)))
		spaceNameFromPath := filepath.Base(filepath.Dir(stackDir))
		stackNameFromPath := filepath.Base(stackDir)

		var entries []os.DirEntry
		entries, err = os.ReadDir(stackDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("failed to read stack dir %q: %w", stackDir, err)
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			cellName := entry.Name()
			metadataPath := fs.CellMetadataPath(
				r.opts.RunPath,
				realmNameFromPath,
				spaceNameFromPath,
				stackNameFromPath,
				cellName,
			)
			cell, readErr := r.readCellInternal(metadataPath)
			if readErr != nil {
				// Skip directories that can't be read (might be incomplete or corrupted)
				r.logger.DebugContext(r.ctx, "skipping cell metadata file", "path", metadataPath, "error", readErr)
				continue
			}

			// Filter by realm, space, and/or stack if specified
			if realmName != "" && cell.Spec.RealmName != realmName {
				continue
			}
			if spaceName != "" && cell.Spec.SpaceName != spaceName {
				continue
			}
			if stackName != "" && cell.Spec.StackName != stackName {
				continue
			}

			cellResults = append(cellResults, cell)
		}
	}

	return cellResults, nil
}

func (r *Exec) ListContainers(realmName, spaceName, stackName, cellName string) ([]intmodel.ContainerSpec, error) {
	var cells []intmodel.Cell

	if cellName != "" {
		// For autocomplete, we can read directly from metadata files without calling containerd
		// This avoids the containerd connection that GetCell would trigger via ExistsCellRootContainer
		cellDir := fs.CellMetadataDir(r.opts.RunPath, realmName, spaceName, stackName, cellName)
		metadataPath := filepath.Join(cellDir, consts.KukeonMetadataFile)

		// Try to read cell metadata directly
		cell, readErr := r.readCellInternal(metadataPath)
		if readErr != nil {
			// If metadata file doesn't exist, return empty list (not an error for autocomplete)
			if errors.Is(readErr, errdefs.ErrMissingMetadataFile) || os.IsNotExist(readErr) {
				return []intmodel.ContainerSpec{}, nil
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
			internalCell, getErr := r.GetCell(lookupCell)
			if getErr != nil {
				if errors.Is(getErr, errdefs.ErrCellNotFound) {
					return []intmodel.ContainerSpec{}, nil
				}
				return nil, getErr
			}
			cells = []intmodel.Cell{internalCell}
		} else {
			// Successfully read from metadata file - use it directly without containerd
			cells = []intmodel.Cell{cell}
		}
	} else {
		// List all cells matching filters
		var listErr error
		cells, listErr = r.ListCells(realmName, spaceName, stackName)
		if listErr != nil {
			return nil, listErr
		}
	}

	// Extract containers from cells
	return r.ExtractContainersFromCells(cells), nil
}

func (r *Exec) ListRealms() ([]intmodel.Realm, error) {
	realmsDir := r.opts.RunPath
	var results []intmodel.Realm

	// Check if directory exists
	if _, err := os.Stat(realmsDir); os.IsNotExist(err) {
		return results, nil // Return empty list if directory doesn't exist
	}

	entries, err := os.ReadDir(realmsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory %q: %w", realmsDir, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		realmName := entry.Name()
		metadataPath := fs.RealmMetadataPath(r.opts.RunPath, realmName)
		realm, readErr := r.readRealmInternal(metadataPath)
		if readErr != nil {
			// Skip directories that can't be read (might be incomplete or corrupted)
			r.logger.DebugContext(r.ctx, "skipping realm metadata file", "path", metadataPath, "error", readErr)
			continue
		}

		results = append(results, realm)
	}

	return results, nil
}

// ExtractContainersFromCells extracts all containers from a list of cells.
// It returns both root containers and regular containers as internal ContainerSpec types.
func (r *Exec) ExtractContainersFromCells(cells []intmodel.Cell) []intmodel.ContainerSpec {
	var containers []intmodel.ContainerSpec

	for _, cell := range cells {
		// Extract all containers, marking root container if present
		for _, container := range cell.Spec.Containers {
			// Check if this is the root container
			if cell.Spec.RootContainerID != "" && container.ID == cell.Spec.RootContainerID {
				container.Root = true
			}
			containers = append(containers, container)
		}
	}

	return containers
}
