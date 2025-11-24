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

// KillCellResult reports the outcome of killing a cell.
type KillCellResult struct {
	CellDoc *v1beta1.CellDoc
	Killed  bool
}

// KillContainerResult reports the outcome of killing a container.
type KillContainerResult struct {
	ContainerDoc *v1beta1.ContainerDoc
	Killed       bool
}

// KillCell immediately force-kills all containers in a cell and updates the cell metadata state.
func (b *Exec) KillCell(doc *v1beta1.CellDoc) (KillCellResult, error) {
	var res KillCellResult
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

	// Kill all containers in the cell
	if err = b.runner.KillCell(cellDoc); err != nil {
		return res, fmt.Errorf("failed to kill cell containers: %w", err)
	}

	// Update cell metadata state to Pending (killed)
	cellDoc.Status.State = v1beta1.CellStatePending
	if err = b.runner.UpdateCellMetadata(cellDoc); err != nil {
		return res, fmt.Errorf("failed to update cell metadata: %w", err)
	}

	res.Killed = true
	return res, nil
}

// KillContainer immediately force-kills a specific container in a cell and updates the cell metadata.
func (b *Exec) KillContainer(doc *v1beta1.ContainerDoc) (KillContainerResult, error) {
	var res KillContainerResult
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
	name = strings.TrimSpace(name)
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

	// Get cell document
	cellLookup := &v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{
			Name: cellName,
		},
		Spec: v1beta1.CellSpec{
			RealmID: realmName,
			SpaceID: spaceName,
			StackID: stackName,
		},
	}
	getResult, err := b.GetCell(cellLookup)
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
	cellDoc := getResult.CellDoc
	if cellDoc == nil {
		return res, fmt.Errorf("cell %q not found", cellName)
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
		return res, fmt.Errorf("container %q not found in cell %q", name, cellName)
	}

	// Update the doc spec to match the stored container details.
	doc.Spec = *foundContainer
	doc.Spec.ID = name
	doc.Spec.RealmID = realmName
	doc.Spec.SpaceID = spaceName
	doc.Spec.StackID = stackName
	doc.Spec.CellID = cellName

	// Kill the specific container (pass container name, runner will build full ID)
	if err = b.runner.KillContainer(cellDoc, name); err != nil {
		return res, fmt.Errorf("failed to kill container %s: %w", name, err)
	}

	// Update cell metadata (state remains Ready if other containers are running)
	// The state management can be enhanced later to track individual container states
	if err = b.runner.UpdateCellMetadata(cellDoc); err != nil {
		return res, fmt.Errorf("failed to update cell metadata: %w", err)
	}

	res.Killed = true
	return res, nil
}
