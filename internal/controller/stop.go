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

// StopCellResult reports the outcome of stopping a cell.
type StopCellResult struct {
	CellName  string
	RealmName string
	SpaceName string
	StackName string
	Stopped   bool
}

// StopContainerResult reports the outcome of stopping a container.
type StopContainerResult struct {
	ContainerName string
	RealmName     string
	SpaceName     string
	StackName     string
	CellName      string
	Stopped       bool
}

// StopCell stops all containers in a cell and updates the cell metadata state.
func (b *Exec) StopCell(name, realmName, spaceName, stackName string) (*StopCellResult, error) {
	name, realmName, spaceName, stackName, cellDoc, err := b.validateAndGetCell(name, realmName, spaceName, stackName)
	if err != nil {
		return nil, err
	}

	result := &StopCellResult{
		CellName:  name,
		RealmName: realmName,
		SpaceName: spaceName,
		StackName: stackName,
	}

	// Stop all containers in the cell
	if err = b.runner.StopCell(cellDoc); err != nil {
		return nil, fmt.Errorf("failed to stop cell containers: %w", err)
	}

	// Update cell metadata state to Pending (stopped)
	cellDoc.Status.State = v1beta1.CellStatePending
	if err = b.runner.UpdateCellMetadata(cellDoc); err != nil {
		return nil, fmt.Errorf("failed to update cell metadata: %w", err)
	}

	result.Stopped = true
	return result, nil
}

// StopContainer stops a specific container in a cell and updates the cell metadata.
func (b *Exec) StopContainer(name, realmName, spaceName, stackName, cellName string) (*StopContainerResult, error) {
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

	result := &StopContainerResult{
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

	// Stop the specific container (pass container name, runner will build full ID)
	if err = b.runner.StopContainer(cellDoc, name); err != nil {
		return nil, fmt.Errorf("failed to stop container %s: %w", name, err)
	}

	// Update cell metadata (state remains Ready if other containers are running)
	// The state management can be enhanced later to track individual container states
	if err = b.runner.UpdateCellMetadata(cellDoc); err != nil {
		return nil, fmt.Errorf("failed to update cell metadata: %w", err)
	}

	result.Stopped = true
	return result, nil
}
