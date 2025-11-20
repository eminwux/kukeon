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
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/metadata"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// GetRealm retrieves a single realm by name.
func (b *Exec) GetRealm(name string) (*v1beta1.RealmDoc, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errdefs.ErrRealmNameRequired
	}

	doc := &v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{
			Name: name,
		},
	}

	return b.runner.GetRealm(doc)
}

// ListRealms lists all realms.
func (b *Exec) ListRealms() ([]*v1beta1.RealmDoc, error) {
	realmsDir := filepath.Join(b.opts.RunPath, consts.KukeonRealmMetadataSubDir)
	return listResources[v1beta1.RealmDoc](b.ctx, b.logger, realmsDir)
}

// GetSpace retrieves a single space by name and realm.
func (b *Exec) GetSpace(name, realmName string) (*v1beta1.SpaceDoc, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errdefs.ErrSpaceNameRequired
	}
	realmName = strings.TrimSpace(realmName)
	if realmName == "" {
		return nil, errdefs.ErrRealmNameRequired
	}

	doc := &v1beta1.SpaceDoc{
		Metadata: v1beta1.SpaceMetadata{
			Name: name,
		},
	}

	spaceDoc, err := b.runner.GetSpace(doc)
	if err != nil {
		return nil, err
	}

	// Verify realm matches
	if spaceDoc.Spec.RealmID != realmName {
		return nil, fmt.Errorf("space %q not found in realm %q (found in realm %q) at run-path %q",
			name, realmName, spaceDoc.Spec.RealmID, b.opts.RunPath)
	}

	return spaceDoc, nil
}

// ListSpaces lists all spaces, optionally filtered by realm.
func (b *Exec) ListSpaces(realmName string) ([]*v1beta1.SpaceDoc, error) {
	spacesDir := filepath.Join(b.opts.RunPath, consts.KukeonSpaceMetadataSubDir)
	spaces, err := listResources[v1beta1.SpaceDoc](b.ctx, b.logger, spacesDir)
	if err != nil {
		return nil, err
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

// GetStack retrieves a single stack by name, realm, and space.
func (b *Exec) GetStack(name, realmName, spaceName string) (*v1beta1.StackDoc, error) {
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

	doc := &v1beta1.StackDoc{
		Metadata: v1beta1.StackMetadata{
			Name: name,
		},
	}

	stackDoc, err := b.runner.GetStack(doc)
	if err != nil {
		return nil, err
	}

	// Verify realm and space match
	if stackDoc.Spec.RealmID != realmName {
		return nil, fmt.Errorf("stack %q not found in realm %q (found in realm %q) at run-path %q",
			name, realmName, stackDoc.Spec.RealmID, b.opts.RunPath)
	}
	if stackDoc.Spec.SpaceID != spaceName {
		return nil, fmt.Errorf("stack %q not found in space %q (found in space %q) at run-path %q",
			name, spaceName, stackDoc.Spec.SpaceID, b.opts.RunPath)
	}

	return stackDoc, nil
}

// ListStacks lists all stacks, optionally filtered by realm and/or space.
func (b *Exec) ListStacks(realmName, spaceName string) ([]*v1beta1.StackDoc, error) {
	stacksDir := filepath.Join(b.opts.RunPath, consts.KukeonStackMetadataSubDir)
	stacks, err := listResources[v1beta1.StackDoc](b.ctx, b.logger, stacksDir)
	if err != nil {
		return nil, err
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

// GetCell retrieves a single cell by name, realm, space, and stack.
func (b *Exec) GetCell(name, realmName, spaceName, stackName string) (*v1beta1.CellDoc, error) {
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

	doc := &v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{
			Name: name,
		},
	}

	cellDoc, err := b.runner.GetCell(doc)
	if err != nil {
		return nil, err
	}

	// Verify realm, space, and stack match
	if cellDoc.Spec.RealmID != realmName {
		return nil, fmt.Errorf("cell %q not found in realm %q (found in realm %q) at run-path %q",
			name, realmName, cellDoc.Spec.RealmID, b.opts.RunPath)
	}
	if cellDoc.Spec.SpaceID != spaceName {
		return nil, fmt.Errorf("cell %q not found in space %q (found in space %q) at run-path %q",
			name, spaceName, cellDoc.Spec.SpaceID, b.opts.RunPath)
	}
	if cellDoc.Spec.StackID != stackName {
		return nil, fmt.Errorf("cell %q not found in stack %q (found in stack %q) at run-path %q",
			name, stackName, cellDoc.Spec.StackID, b.opts.RunPath)
	}

	return cellDoc, nil
}

// ListCells lists all cells, optionally filtered by realm, space, and/or stack.
func (b *Exec) ListCells(realmName, spaceName, stackName string) ([]*v1beta1.CellDoc, error) {
	cellsDir := filepath.Join(b.opts.RunPath, consts.KukeonCellMetadataSubDir)
	cells, err := listResources[v1beta1.CellDoc](b.ctx, b.logger, cellsDir)
	if err != nil {
		return nil, err
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

// GetContainer retrieves a single container by name from a cell.
func (b *Exec) GetContainer(name, realmName, spaceName, stackName, cellName string) (*v1beta1.ContainerSpec, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errdefs.ErrContainerNameRequired
	}

	cellDoc, err := b.GetCell(cellName, realmName, spaceName, stackName)
	if err != nil {
		return nil, fmt.Errorf("failed to get cell %q: %w", cellName, err)
	}

	// Find container in cell spec by name (ID now stores just the container name)
	for _, container := range cellDoc.Spec.Containers {
		if container.ID == name {
			return &container, nil
		}
	}

	return nil, fmt.Errorf("container %q not found in cell %q at run-path %q", name, cellName, b.opts.RunPath)
}

// ListContainers lists all containers, optionally filtered by realm, space, stack, and/or cell.
func (b *Exec) ListContainers(realmName, spaceName, stackName, cellName string) ([]*v1beta1.ContainerSpec, error) {
	var cells []*v1beta1.CellDoc
	var err error

	if cellName != "" {
		// Get specific cell
		cell, err := b.GetCell(cellName, realmName, spaceName, stackName)
		if err != nil {
			return nil, err
		}
		cells = []*v1beta1.CellDoc{cell}
	} else {
		// List all cells matching filters
		cells, err = b.ListCells(realmName, spaceName, stackName)
		if err != nil {
			return nil, err
		}
	}

	// Extract containers from cells
	containers := make([]*v1beta1.ContainerSpec, 0)
	for _, cell := range cells {
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
		doc, err := metadata.ReadMetadata[T](ctx, logger, metadataPath)
		if err != nil {
			// Skip files that can't be read (might be incomplete or corrupted)
			logger.DebugContext(ctx, "skipping metadata file", "path", metadataPath, "error", err)
			continue
		}

		results = append(results, &doc)
	}

	return results, nil
}
