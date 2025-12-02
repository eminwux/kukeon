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
	"fmt"

	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// RefreshResult contains the summary of the refresh operation.
type RefreshResult struct {
	RealmsFound       []string
	SpacesFound       []string
	StacksFound       []string
	CellsFound        []string
	ContainersFound   []string
	RealmsUpdated     []string
	SpacesUpdated     []string
	StacksUpdated     []string
	CellsUpdated      []string
	ContainersUpdated []string
	Errors            []string
}

// RefreshAll refreshes all metadata entities by introspecting containerd and CNI.
func (b *Exec) RefreshAll() (RefreshResult, error) {
	defer b.runner.Close()
	result := RefreshResult{
		RealmsFound:       []string{},
		SpacesFound:       []string{},
		StacksFound:       []string{},
		CellsFound:        []string{},
		ContainersFound:   []string{},
		RealmsUpdated:     []string{},
		SpacesUpdated:     []string{},
		StacksUpdated:     []string{},
		CellsUpdated:      []string{},
		ContainersUpdated: []string{},
		Errors:            []string{},
	}

	// Ensure containerd client is connected (via runner)
	// This is done implicitly when refresh methods are called

	// List and refresh realms
	realms, err := b.runner.ListRealms()
	if err != nil {
		b.logger.ErrorContext(b.ctx, "failed to list realms", "error", err)
		result.Errors = append(result.Errors, fmt.Sprintf("failed to list realms: %v", err))
		return result, nil
	}

	for _, realm := range realms {
		b.refreshRealmAndChildren(realm, &result)
	}

	return result, nil
}

// refreshRealmAndChildren refreshes a realm and all its child resources.
func (b *Exec) refreshRealmAndChildren(realm intmodel.Realm, result *RefreshResult) {
	realmName := realm.Metadata.Name
	result.RealmsFound = append(result.RealmsFound, realmName)

	// Refresh realm
	updatedRealm, realmUpdated, refreshErr := b.runner.RefreshRealm(realm)
	if refreshErr != nil {
		b.logger.WarnContext(b.ctx, "failed to refresh realm status", "realm", realmName, "error", refreshErr)
		result.Errors = append(result.Errors, fmt.Sprintf("failed to refresh realm '%s': %v", realmName, refreshErr))
	} else if realmUpdated {
		result.RealmsUpdated = append(result.RealmsUpdated, realmName)
		_ = updatedRealm // Updated realm is persisted by RefreshRealm
	}

	// List and refresh spaces in this realm
	spaces, err := b.runner.ListSpaces(realmName)
	if err != nil {
		b.logger.WarnContext(b.ctx, "failed to list spaces", "realm", realmName, "error", err)
		result.Errors = append(result.Errors, fmt.Sprintf("failed to list spaces in realm '%s': %v", realmName, err))
		return
	}

	for _, space := range spaces {
		b.refreshSpaceAndChildren(realmName, space, result)
	}
}

// refreshSpaceAndChildren refreshes a space and all its child resources.
func (b *Exec) refreshSpaceAndChildren(realmName string, space intmodel.Space, result *RefreshResult) {
	spaceName := space.Metadata.Name
	result.SpacesFound = append(result.SpacesFound, spaceName)

	// Refresh space
	updatedSpace, spaceUpdated, spaceErr := b.runner.RefreshSpace(space)
	if spaceErr != nil {
		b.logger.WarnContext(
			b.ctx,
			"failed to refresh space status",
			"realm",
			realmName,
			"space",
			spaceName,
			"error",
			spaceErr,
		)
		result.Errors = append(
			result.Errors,
			fmt.Sprintf("failed to refresh space '%s' in realm '%s': %v", spaceName, realmName, spaceErr),
		)
	} else if spaceUpdated {
		result.SpacesUpdated = append(result.SpacesUpdated, spaceName)
		_ = updatedSpace // Updated space is persisted by RefreshSpace
	}

	// List and refresh stacks in this space
	stacks, err := b.runner.ListStacks(realmName, spaceName)
	if err != nil {
		b.logger.WarnContext(b.ctx, "failed to list stacks", "realm", realmName, "space", spaceName, "error", err)
		result.Errors = append(
			result.Errors,
			fmt.Sprintf("failed to list stacks in realm '%s', space '%s': %v", realmName, spaceName, err),
		)
		return
	}

	for _, stack := range stacks {
		b.refreshStackAndChildren(realmName, spaceName, stack, result)
	}
}

// refreshStackAndChildren refreshes a stack and all its child resources.
func (b *Exec) refreshStackAndChildren(realmName, spaceName string, stack intmodel.Stack, result *RefreshResult) {
	stackName := stack.Metadata.Name
	result.StacksFound = append(result.StacksFound, stackName)

	// Refresh stack
	updatedStack, stackUpdated, stackErr := b.runner.RefreshStack(stack)
	if stackErr != nil {
		b.logger.WarnContext(
			b.ctx,
			"failed to refresh stack status",
			"realm",
			realmName,
			"space",
			spaceName,
			"stack",
			stackName,
			"error",
			stackErr,
		)
		result.Errors = append(
			result.Errors,
			fmt.Sprintf(
				"failed to refresh stack '%s' in realm '%s', space '%s': %v",
				stackName,
				realmName,
				spaceName,
				stackErr,
			),
		)
	} else if stackUpdated {
		result.StacksUpdated = append(result.StacksUpdated, stackName)
		_ = updatedStack // Updated stack is persisted by RefreshStack
	}

	// List and refresh cells in this stack
	cells, err := b.runner.ListCells(realmName, spaceName, stackName)
	if err != nil {
		b.logger.WarnContext(
			b.ctx,
			"failed to list cells",
			"realm",
			realmName,
			"space",
			spaceName,
			"stack",
			stackName,
			"error",
			err,
		)
		result.Errors = append(
			result.Errors,
			fmt.Sprintf(
				"failed to list cells in realm '%s', space '%s', stack '%s': %v",
				realmName,
				spaceName,
				stackName,
				err,
			),
		)
		return
	}

	for _, cell := range cells {
		b.refreshCellAndContainers(realmName, spaceName, stackName, cell, result)
	}
}

// refreshCellAndContainers refreshes a cell and tracks its containers.
func (b *Exec) refreshCellAndContainers(
	realmName, spaceName, stackName string,
	cell intmodel.Cell,
	result *RefreshResult,
) {
	cellName := cell.Metadata.Name
	result.CellsFound = append(result.CellsFound, cellName)

	// Track all containers from cell spec with fully qualified names
	for _, containerSpec := range cell.Spec.Containers {
		containerName := fmt.Sprintf("%s/%s/%s/%s/%s", realmName, spaceName, stackName, cellName, containerSpec.ID)
		result.ContainersFound = append(result.ContainersFound, containerName)
	}

	// Store original cell status for comparison
	originalCellStatus := cell.Status

	updatedCell, containersUpdated, cellErr := b.runner.RefreshCell(cell)
	if cellErr != nil {
		b.logger.WarnContext(
			b.ctx,
			"failed to refresh cell status",
			"realm",
			realmName,
			"space",
			spaceName,
			"stack",
			stackName,
			"cell",
			cellName,
			"error",
			cellErr,
		)
		result.Errors = append(
			result.Errors,
			fmt.Sprintf(
				"failed to refresh cell '%s' in realm '%s', space '%s', stack '%s': %v",
				cellName,
				realmName,
				spaceName,
				stackName,
				cellErr,
			),
		)
		return
	}

	// If containers were updated, we add all containers from this cell to ContainersUpdated
	// since RefreshCell only returns a count, not which specific containers were updated
	if containersUpdated > 0 {
		for _, containerSpec := range updatedCell.Spec.Containers {
			containerName := fmt.Sprintf("%s/%s/%s/%s/%s", realmName, spaceName, stackName, cellName, containerSpec.ID)
			result.ContainersUpdated = append(result.ContainersUpdated, containerName)
		}
	}

	// Check if cell status changed or if containers were updated (which triggers metadata write)
	if originalCellStatus.State != updatedCell.Status.State ||
		originalCellStatus.CgroupPath != updatedCell.Status.CgroupPath ||
		containersUpdated > 0 {
		result.CellsUpdated = append(result.CellsUpdated, cellName)
	}
	_ = updatedCell // Updated cell is persisted by RefreshCell
}
