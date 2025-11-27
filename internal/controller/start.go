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

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// StartCellResult reports the outcome of starting a cell.
type StartCellResult struct {
	CellDoc *v1beta1.CellDoc
	Started bool
}

// StartContainerResult reports the outcome of starting a container.
type StartContainerResult struct {
	ContainerDoc *v1beta1.ContainerDoc
	Started      bool
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

	// Build lookup cell for GetCell (using internal types)
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
	result, err := b.GetCell(lookupCell)
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
	if !result.MetadataExists {
		return "", "", "", "", nil, fmt.Errorf("cell %q not found", name)
	}
	// Convert internal cell back to external for return value
	cellDocExternal, err := apischeme.BuildCellExternalFromInternal(result.Cell, apischeme.VersionV1Beta1)
	if err != nil {
		return "", "", "", "", nil, fmt.Errorf("failed to convert cell to external model: %w", err)
	}
	cellDoc := &cellDocExternal

	return name, realmName, spaceName, stackName, cellDoc, nil
}

// StartCell starts all containers in a cell and updates the cell metadata state.
func (b *Exec) StartCell(doc *v1beta1.CellDoc) (StartCellResult, error) {
	var res StartCellResult
	if doc == nil {
		return res, errdefs.ErrCellNameRequired
	}

	name := strings.TrimSpace(doc.Metadata.Name)
	realmName := strings.TrimSpace(doc.Spec.RealmID)
	spaceName := strings.TrimSpace(doc.Spec.SpaceID)
	stackName := strings.TrimSpace(doc.Spec.StackID)

	_, _, _, _, cellDoc, err := b.validateAndGetCell(name, realmName, spaceName, stackName)
	if err != nil {
		return res, err
	}

	res.CellDoc = cellDoc

	// Convert external cell to internal for runner.StartCell
	internalCell, err := apischeme.ConvertCellDocToInternal(*cellDoc)
	if err != nil {
		return res, fmt.Errorf("failed to convert cell to internal model: %w", err)
	}

	// Start all containers in the cell
	if err = b.runner.StartCell(internalCell); err != nil {
		return res, fmt.Errorf("failed to start cell containers: %w", err)
	}

	// Use the same internal cell for UpdateCellMetadata
	cell := internalCell
	cell.Status.State = intmodel.CellStateReady

	// Update cell metadata state to Ready
	if err = b.runner.UpdateCellMetadata(cell); err != nil {
		return res, fmt.Errorf("failed to update cell metadata: %w", err)
	}

	// Update cellDoc for response
	cellDoc.Status.State = v1beta1.CellStateReady

	res.Started = true
	return res, nil
}

// StartContainer starts a specific container in a cell and updates the cell metadata.
func (b *Exec) StartContainer(doc *v1beta1.ContainerDoc) (StartContainerResult, error) {
	var res StartContainerResult
	if doc == nil {
		return res, errdefs.ErrContainerNameRequired
	}

	name := strings.TrimSpace(doc.Metadata.Name)
	if name == "" {
		return res, errdefs.ErrContainerNameRequired
	}

	realmName := strings.TrimSpace(doc.Spec.RealmID)
	if realmName == "" {
		return res, errdefs.ErrRealmNameRequired
	}

	spaceName := strings.TrimSpace(doc.Spec.SpaceID)
	if spaceName == "" {
		return res, errdefs.ErrSpaceNameRequired
	}

	stackName := strings.TrimSpace(doc.Spec.StackID)
	if stackName == "" {
		return res, errdefs.ErrStackNameRequired
	}

	cellName := strings.TrimSpace(doc.Spec.CellID)
	if cellName == "" {
		return res, errdefs.ErrCellNameRequired
	}

	// Build lookup cell for GetCell (using internal types)
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
	if !getResult.MetadataExists {
		return res, fmt.Errorf("cell %q not found", cellName)
	}
	// Convert internal cell back to external
	cellDocExternal, err := apischeme.BuildCellExternalFromInternal(getResult.Cell, apischeme.VersionV1Beta1)
	if err != nil {
		return res, fmt.Errorf("failed to convert cell to external model: %w", err)
	}
	cellDoc := &cellDocExternal

	// Find container in cell spec by name (ID now stores just the container name)
	var foundContainer *v1beta1.ContainerSpec
	for i := range cellDoc.Spec.Containers {
		if cellDoc.Spec.Containers[i].ID == name {
			foundContainer = &cellDoc.Spec.Containers[i]
			break
		}
	}

	if foundContainer == nil {
		return res, fmt.Errorf("container %q not found in cell %q", name, cellName)
	}

	// Start the cell (which will start all containers including this one)
	// The StartCell method handles starting individual containers if they exist
	cellDocToStart := &v1beta1.CellDoc{
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
	// Convert external cell to internal for runner.StartCell
	internalCellToStart, convertErr := apischeme.ConvertCellDocToInternal(*cellDocToStart)
	if convertErr != nil {
		return res, fmt.Errorf("failed to convert cell to internal model: %w", convertErr)
	}

	if err = b.runner.StartCell(internalCellToStart); err != nil {
		return res, fmt.Errorf("failed to start container %s: %w", name, err)
	}

	// Convert to internal model for UpdateCellMetadata
	cell, err := apischeme.ConvertCellDocToInternal(*cellDoc)
	if err != nil {
		return res, fmt.Errorf("failed to convert cell to internal model: %w", err)
	}
	cell.Status.State = intmodel.CellStateReady

	// Update cell metadata state to Ready
	if err = b.runner.UpdateCellMetadata(cell); err != nil {
		return res, fmt.Errorf("failed to update cell metadata: %w", err)
	}

	// Update cellDoc for response
	cellDoc.Status.State = v1beta1.CellStateReady

	containerSpec := *foundContainer
	containerSpec.RealmID = realmName
	containerSpec.SpaceID = spaceName
	containerSpec.StackID = stackName
	containerSpec.CellID = cellName

	res.ContainerDoc = &v1beta1.ContainerDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindContainer,
		Metadata: v1beta1.ContainerMetadata{
			Name:   containerSpec.ID,
			Labels: map[string]string{},
		},
		Spec: containerSpec,
		Status: v1beta1.ContainerStatus{
			State: v1beta1.ContainerStateReady,
		},
	}
	res.Started = true

	return res, nil
}
