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
	CellName  string
	RealmName string
	SpaceName string
	StackName string
	Killed    bool
}

// KillContainerResult reports the outcome of killing a container.
type KillContainerResult struct {
	ContainerName string
	RealmName     string
	SpaceName     string
	StackName     string
	CellName      string
	Killed        bool
}

// KillCell immediately force-kills all containers in a cell and updates the cell metadata state.
func (b *Exec) KillCell(name, realmName, spaceName, stackName string) (*KillCellResult, error) {
	name, realmName, spaceName, stackName, cellDoc, err := b.validateAndGetCell(name, realmName, spaceName, stackName)
	if err != nil {
		return nil, err
	}

	result := &KillCellResult{
		CellName:  name,
		RealmName: realmName,
		SpaceName: spaceName,
		StackName: stackName,
	}

	// Kill all containers in the cell
	if err = b.runner.KillCell(cellDoc); err != nil {
		return nil, fmt.Errorf("failed to kill cell containers: %w", err)
	}

	// Update cell metadata state to Pending (killed)
	cellDoc.Status.State = v1beta1.CellStatePending
	if err = b.runner.UpdateCellMetadata(cellDoc); err != nil {
		return nil, fmt.Errorf("failed to update cell metadata: %w", err)
	}

	result.Killed = true
	return result, nil
}

// KillContainer immediately force-kills a specific container in a cell and updates the cell metadata.
func (b *Exec) KillContainer(name, realmName, spaceName, stackName, cellName string) (*KillContainerResult, error) {
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

	result := &KillContainerResult{
		ContainerName: name,
		RealmName:     realmName,
		SpaceName:     spaceName,
		StackName:     stackName,
		CellName:      cellName,
	}

	// Get cell document
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

	// Kill the specific container (pass container name, runner will build full ID)
	if err = b.runner.KillContainer(cellDoc, name); err != nil {
		return nil, fmt.Errorf("failed to kill container %s: %w", name, err)
	}

	// Update cell metadata (state remains Ready if other containers are running)
	// The state management can be enhanced later to track individual container states
	if err = b.runner.UpdateCellMetadata(cellDoc); err != nil {
		return nil, fmt.Errorf("failed to update cell metadata: %w", err)
	}

	result.Killed = true
	return result, nil
}
