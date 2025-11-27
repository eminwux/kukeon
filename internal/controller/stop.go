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

// StopCellResult reports the outcome of stopping a cell.
type StopCellResult struct {
	CellDoc *v1beta1.CellDoc
	Stopped bool
}

// StopContainerResult reports the outcome of stopping a container.
type StopContainerResult struct {
	ContainerDoc *v1beta1.ContainerDoc
	Stopped      bool
}

// StopCell stops all containers in a cell and updates the cell metadata state.
func (b *Exec) StopCell(doc *v1beta1.CellDoc) (StopCellResult, error) {
	var result StopCellResult
	if doc == nil {
		return result, errdefs.ErrCellNameRequired
	}

	if doc.Metadata.Labels == nil {
		doc.Metadata.Labels = make(map[string]string)
	}
	if doc.APIVersion == "" {
		doc.APIVersion = v1beta1.APIVersionV1Beta1
	}
	if doc.Kind == "" {
		doc.Kind = v1beta1.KindCell
	}

	name := strings.TrimSpace(doc.Metadata.Name)
	if name == "" {
		name = strings.TrimSpace(doc.Spec.ID)
	}
	if name == "" {
		return result, errdefs.ErrCellNameRequired
	}
	doc.Metadata.Name = name
	doc.Spec.ID = name

	realmName := strings.TrimSpace(doc.Spec.RealmID)
	if realmName == "" {
		return result, errdefs.ErrRealmNameRequired
	}
	doc.Spec.RealmID = realmName

	spaceName := strings.TrimSpace(doc.Spec.SpaceID)
	if spaceName == "" {
		return result, errdefs.ErrSpaceNameRequired
	}
	doc.Spec.SpaceID = spaceName

	stackName := strings.TrimSpace(doc.Spec.StackID)
	if stackName == "" {
		return result, errdefs.ErrStackNameRequired
	}
	doc.Spec.StackID = stackName

	_, _, _, _, cellDoc, err := b.validateAndGetCell(name, realmName, spaceName, stackName)
	if err != nil {
		return result, err
	}
	result.CellDoc = cellDoc

	// Convert external cell to internal for runner.StopCell
	internalCell, err := apischeme.ConvertCellDocToInternal(*cellDoc)
	if err != nil {
		return result, fmt.Errorf("failed to convert cell to internal model: %w", err)
	}

	// Stop all containers in the cell
	if err = b.runner.StopCell(internalCell); err != nil {
		return result, fmt.Errorf("failed to stop cell containers: %w", err)
	}

	// Use the same internal cell for UpdateCellMetadata
	cell := internalCell
	cell.Status.State = intmodel.CellStatePending

	// Update cell metadata state to Pending (stopped)
	if err = b.runner.UpdateCellMetadata(cell); err != nil {
		return result, fmt.Errorf("failed to update cell metadata: %w", err)
	}

	// Update cellDoc for response
	cellDoc.Status.State = v1beta1.CellStatePending

	result.Stopped = true
	return result, nil
}

// StopContainer stops a specific container in a cell and updates the cell metadata.
func (b *Exec) StopContainer(doc *v1beta1.ContainerDoc) (StopContainerResult, error) {
	var res StopContainerResult
	if doc == nil {
		return res, errdefs.ErrContainerNameRequired
	}

	if doc.Metadata.Labels == nil {
		doc.Metadata.Labels = make(map[string]string)
	}
	if doc.APIVersion == "" {
		doc.APIVersion = v1beta1.APIVersionV1Beta1
	}
	if doc.Kind == "" {
		doc.Kind = v1beta1.KindContainer
	}

	name := strings.TrimSpace(doc.Metadata.Name)
	if name == "" {
		name = strings.TrimSpace(doc.Spec.ID)
	}
	if name == "" {
		return res, errdefs.ErrContainerNameRequired
	}
	doc.Metadata.Name = name
	doc.Spec.ID = name

	realmName := strings.TrimSpace(doc.Spec.RealmID)
	if realmName == "" {
		return res, errdefs.ErrRealmNameRequired
	}
	doc.Spec.RealmID = realmName

	spaceName := strings.TrimSpace(doc.Spec.SpaceID)
	if spaceName == "" {
		return res, errdefs.ErrSpaceNameRequired
	}
	doc.Spec.SpaceID = spaceName

	stackName := strings.TrimSpace(doc.Spec.StackID)
	if stackName == "" {
		return res, errdefs.ErrStackNameRequired
	}
	doc.Spec.StackID = stackName

	cellName := strings.TrimSpace(doc.Spec.CellID)
	if cellName == "" {
		return res, errdefs.ErrCellNameRequired
	}
	doc.Spec.CellID = cellName

	res.ContainerDoc = doc

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

	// Convert external cell to internal for runner.StopContainer
	internalCell, err := apischeme.ConvertCellDocToInternal(*cellDoc)
	if err != nil {
		return res, fmt.Errorf("failed to convert cell to internal model: %w", err)
	}

	// Stop the specific container (pass container name, runner will build full ID)
	if err = b.runner.StopContainer(internalCell, name); err != nil {
		return res, fmt.Errorf("failed to stop container %s: %w", name, err)
	}

	// Use the same internal cell for UpdateCellMetadata
	cell := internalCell

	// Update cell metadata (state remains Ready if other containers are running)
	// The state management can be enhanced later to track individual container states
	if err = b.runner.UpdateCellMetadata(cell); err != nil {
		return res, fmt.Errorf("failed to update cell metadata: %w", err)
	}

	res.Stopped = true
	return res, nil
}
