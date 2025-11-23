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
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// StartCellResult reports the outcome of starting a cell.
type StartCellResult struct {
	CellName  string
	RealmName string
	SpaceName string
	StackName string
	Started   bool
}

// StartContainerResult reports the outcome of starting a container.
type StartContainerResult struct {
	ContainerName string
	RealmName     string
	SpaceName     string
	StackName     string
	CellName      string
	Started       bool
}

// validateAndGetCell validates and trims cell parameters, then retrieves the cell document.
// Returns the validated parameters and the cell document, or an error if validation or retrieval fails.
func (b *Exec) validateAndGetCell(
	name, realmName, spaceName, stackName string,
) (string, string, string, string, *v1beta1.CellDoc, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", "", "", "", nil, errdefs.ErrCellNameRequired
	}
	realmName = strings.TrimSpace(realmName)
	if realmName == "" {
		return "", "", "", "", nil, errdefs.ErrRealmNameRequired
	}
	spaceName = strings.TrimSpace(spaceName)
	if spaceName == "" {
		return "", "", "", "", nil, errdefs.ErrSpaceNameRequired
	}
	stackName = strings.TrimSpace(stackName)
	if stackName == "" {
		return "", "", "", "", nil, errdefs.ErrStackNameRequired
	}

	// Get cell document
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
	result, err := b.GetCell(doc)
	if err != nil {
		if errors.Is(err, errdefs.ErrCellNotFound) {
			return "", "", "", "", nil, fmt.Errorf(
				"cell %q not found in realm %q, space %q, stack %q",
				name,
				realmName,
				spaceName,
				stackName,
			)
		}
		return "", "", "", "", nil, err
	}
	cellDoc := result.CellDoc
	if cellDoc == nil {
		return "", "", "", "", nil, fmt.Errorf("cell %q not found", name)
	}

	return name, realmName, spaceName, stackName, cellDoc, nil
}

// StartCell starts all containers in a cell and updates the cell metadata state.
func (b *Exec) StartCell(name, realmName, spaceName, stackName string) (*StartCellResult, error) {
	name, realmName, spaceName, stackName, cellDoc, err := b.validateAndGetCell(name, realmName, spaceName, stackName)
	if err != nil {
		return nil, err
	}

	result := &StartCellResult{
		CellName:  name,
		RealmName: realmName,
		SpaceName: spaceName,
		StackName: stackName,
	}

	// Start all containers in the cell
	if err = b.runner.StartCell(cellDoc); err != nil {
		return nil, fmt.Errorf("failed to start cell containers: %w", err)
	}

	// Update cell metadata state to Ready
	cellDoc.Status.State = v1beta1.CellStateReady
	if err = b.runner.UpdateCellMetadata(cellDoc); err != nil {
		return nil, fmt.Errorf("failed to update cell metadata: %w", err)
	}

	result.Started = true
	return result, nil
}

// StartContainer starts a specific container in a cell and updates the cell metadata.
func (b *Exec) StartContainer(name, realmName, spaceName, stackName, cellName string) (*StartContainerResult, error) {
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

	result := &StartContainerResult{
		ContainerName: name,
		RealmName:     realmName,
		SpaceName:     spaceName,
		StackName:     stackName,
		CellName:      cellName,
	}

	// Get cell document
	doc := &v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{
			Name: cellName,
		},
		Spec: v1beta1.CellSpec{
			RealmID: realmName,
			SpaceID: spaceName,
			StackID: stackName,
		},
	}
	getResult, err := b.GetCell(doc)
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
	cellDoc := getResult.CellDoc
	if cellDoc == nil {
		return nil, fmt.Errorf("cell %q not found", cellName)
	}

	// Find container in cell spec by name (ID now stores just the container name)
	var foundContainer *v1beta1.ContainerSpec
	for i := range cellDoc.Spec.Containers {
		if cellDoc.Spec.Containers[i].ID == name {
			foundContainer = &cellDoc.Spec.Containers[i]
			break
		}
	}

	if foundContainer == nil {
		return nil, fmt.Errorf("container %q not found in cell %q", name, cellName)
	}

	// Start the cell (which will start all containers including this one)
	// The StartCell method handles starting individual containers if they exist
	doc = &v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{
			Name: cellName,
		},
		Spec: v1beta1.CellSpec{
			ID:      cellName,
			RealmID: realmName,
			SpaceID: spaceName,
			StackID: stackName,
			Containers: []v1beta1.ContainerSpec{
				*foundContainer,
			},
		},
	}
	if err = b.runner.StartCell(doc); err != nil {
		return nil, fmt.Errorf("failed to start container %s: %w", name, err)
	}

	// Update cell metadata state to Ready
	cellDoc.Status.State = v1beta1.CellStateReady
	if err = b.runner.UpdateCellMetadata(cellDoc); err != nil {
		return nil, fmt.Errorf("failed to update cell metadata: %w", err)
	}

	result.Started = true
	return result, nil
}
