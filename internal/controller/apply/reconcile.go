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

package apply

import (
	"errors"
	"fmt"

	"github.com/eminwux/kukeon/internal/controller/runner"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

const (
	actionCreated = "created"
	actionUpdated = "updated"
)

// ReconcileRealm reconciles a desired realm state with the actual state.
func ReconcileRealm(r runner.Runner, desired intmodel.Realm) (ReconcileResult, error) {
	result := ReconcileResult{
		Action: "unchanged",
		Kind:   "Realm",
		Name:   desired.Metadata.Name,
	}

	// Get actual state
	actual, err := r.GetRealm(desired)
	if err != nil {
		if errors.Is(err, errdefs.ErrRealmNotFound) {
			// Realm doesn't exist, create it
			created, createErr := r.CreateRealm(desired)
			if createErr != nil {
				return result, fmt.Errorf("failed to create realm: %w", createErr)
			}
			result.Action = actionCreated
			result.Resource = created
			return result, nil
		}
		return result, fmt.Errorf("failed to get realm: %w", err)
	}

	// Diff desired vs actual
	diff := DiffRealm(desired, actual)
	if !diff.HasChanges {
		result.Resource = actual
		return result, nil
	}

	// Check for breaking changes
	if isBreakingChange(diff.ChangeType) {
		return result, fmt.Errorf(
			"realm %q has breaking changes: %v. Delete the realm and recreate it with the new spec",
			desired.Metadata.Name,
			diff.BreakingChanges,
		)
	}

	// Apply compatible changes
	updated, updateErr := r.UpdateRealm(desired)
	if updateErr != nil {
		return result, fmt.Errorf("failed to update realm: %w", updateErr)
	}

	result.Action = actionUpdated
	result.Resource = updated
	result.Changes = diff.ChangedFields
	result.Details = diff.Details

	return result, nil
}

// ReconcileSpace reconciles a desired space state with the actual state.
func ReconcileSpace(r runner.Runner, desired intmodel.Space) (ReconcileResult, error) {
	result := ReconcileResult{
		Action: "unchanged",
		Kind:   "Space",
		Name:   desired.Metadata.Name,
	}

	// Ensure parent realm exists
	realm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: desired.Spec.RealmName,
		},
	}
	_, err := r.GetRealm(realm)
	if err != nil {
		if errors.Is(err, errdefs.ErrRealmNotFound) {
			// Create parent realm if it doesn't exist
			_, createErr := r.CreateRealm(realm)
			if createErr != nil {
				return result, fmt.Errorf("failed to create parent realm %q: %w", desired.Spec.RealmName, createErr)
			}
		} else {
			return result, fmt.Errorf("failed to get parent realm %q: %w", desired.Spec.RealmName, err)
		}
	}

	// Get actual state
	actual, err := r.GetSpace(desired)
	if err != nil {
		if errors.Is(err, errdefs.ErrSpaceNotFound) {
			// Space doesn't exist, create it
			created, createErr := r.CreateSpace(desired)
			if createErr != nil {
				return result, fmt.Errorf("failed to create space: %w", createErr)
			}
			result.Action = actionCreated
			result.Resource = created
			return result, nil
		}
		return result, fmt.Errorf("failed to get space: %w", err)
	}

	// Diff desired vs actual
	diff := DiffSpace(desired, actual)
	if !diff.HasChanges {
		result.Resource = actual
		return result, nil
	}

	// Check for breaking changes
	if isBreakingChange(diff.ChangeType) {
		return result, fmt.Errorf(
			"space %q has breaking changes: %v. Delete the space and recreate it with the new spec",
			desired.Metadata.Name,
			diff.BreakingChanges,
		)
	}

	// Apply compatible changes
	updated, updateErr := r.UpdateSpace(desired)
	if updateErr != nil {
		return result, fmt.Errorf("failed to update space: %w", updateErr)
	}

	result.Action = actionUpdated
	result.Resource = updated
	result.Changes = diff.ChangedFields
	result.Details = diff.Details

	return result, nil
}

// ReconcileStack reconciles a desired stack state with the actual state.
func ReconcileStack(r runner.Runner, desired intmodel.Stack) (ReconcileResult, error) {
	result := ReconcileResult{
		Action: "unchanged",
		Kind:   "Stack",
		Name:   desired.Metadata.Name,
	}

	// Ensure parent realm and space exist
	realm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: desired.Spec.RealmName,
		},
	}
	_, err := r.GetRealm(realm)
	if err != nil {
		if errors.Is(err, errdefs.ErrRealmNotFound) {
			_, createErr := r.CreateRealm(realm)
			if createErr != nil {
				return result, fmt.Errorf("failed to create parent realm %q: %w", desired.Spec.RealmName, createErr)
			}
		} else {
			return result, fmt.Errorf("failed to get parent realm %q: %w", desired.Spec.RealmName, err)
		}
	}

	space := intmodel.Space{
		Metadata: intmodel.SpaceMetadata{
			Name: desired.Spec.SpaceName,
		},
		Spec: intmodel.SpaceSpec{
			RealmName: desired.Spec.RealmName,
		},
	}
	_, err = r.GetSpace(space)
	if err != nil {
		if errors.Is(err, errdefs.ErrSpaceNotFound) {
			_, createErr := r.CreateSpace(space)
			if createErr != nil {
				return result, fmt.Errorf("failed to create parent space %q: %w", desired.Spec.SpaceName, createErr)
			}
		} else {
			return result, fmt.Errorf("failed to get parent space %q: %w", desired.Spec.SpaceName, err)
		}
	}

	// Get actual state
	actual, err := r.GetStack(desired)
	if err != nil {
		if errors.Is(err, errdefs.ErrStackNotFound) {
			// Stack doesn't exist, create it
			created, createErr := r.CreateStack(desired)
			if createErr != nil {
				return result, fmt.Errorf("failed to create stack: %w", createErr)
			}
			result.Action = actionCreated
			result.Resource = created
			return result, nil
		}
		return result, fmt.Errorf("failed to get stack: %w", err)
	}

	// Diff desired vs actual
	diff := DiffStack(desired, actual)
	if !diff.HasChanges {
		result.Resource = actual
		return result, nil
	}

	// Check for breaking changes
	if isBreakingChange(diff.ChangeType) {
		return result, fmt.Errorf(
			"stack %q has breaking changes: %v. Delete the stack and recreate it with the new spec",
			desired.Metadata.Name,
			diff.BreakingChanges,
		)
	}

	// Apply compatible changes
	updated, updateErr := r.UpdateStack(desired)
	if updateErr != nil {
		return result, fmt.Errorf("failed to update stack: %w", updateErr)
	}

	result.Action = actionUpdated
	result.Resource = updated
	result.Changes = diff.ChangedFields
	result.Details = diff.Details

	return result, nil
}

// ReconcileCell reconciles a desired cell state with the actual state.
func ReconcileCell(r runner.Runner, desired intmodel.Cell) (ReconcileResult, error) {
	result := ReconcileResult{
		Action: "unchanged",
		Kind:   "Cell",
		Name:   desired.Metadata.Name,
	}

	// Ensure parent resources exist
	realm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: desired.Spec.RealmName,
		},
	}
	_, err := r.GetRealm(realm)
	if err != nil {
		if errors.Is(err, errdefs.ErrRealmNotFound) {
			_, createErr := r.CreateRealm(realm)
			if createErr != nil {
				return result, fmt.Errorf("failed to create parent realm %q: %w", desired.Spec.RealmName, createErr)
			}
		} else {
			return result, fmt.Errorf("failed to get parent realm %q: %w", desired.Spec.RealmName, err)
		}
	}

	space := intmodel.Space{
		Metadata: intmodel.SpaceMetadata{
			Name: desired.Spec.SpaceName,
		},
		Spec: intmodel.SpaceSpec{
			RealmName: desired.Spec.RealmName,
		},
	}
	_, err = r.GetSpace(space)
	if err != nil {
		if errors.Is(err, errdefs.ErrSpaceNotFound) {
			_, createErr := r.CreateSpace(space)
			if createErr != nil {
				return result, fmt.Errorf("failed to create parent space %q: %w", desired.Spec.SpaceName, createErr)
			}
		} else {
			return result, fmt.Errorf("failed to get parent space %q: %w", desired.Spec.SpaceName, err)
		}
	}

	stack := intmodel.Stack{
		Metadata: intmodel.StackMetadata{
			Name: desired.Spec.StackName,
		},
		Spec: intmodel.StackSpec{
			RealmName: desired.Spec.RealmName,
			SpaceName: desired.Spec.SpaceName,
		},
	}
	_, err = r.GetStack(stack)
	if err != nil {
		if errors.Is(err, errdefs.ErrStackNotFound) {
			_, createErr := r.CreateStack(stack)
			if createErr != nil {
				return result, fmt.Errorf("failed to create parent stack %q: %w", desired.Spec.StackName, createErr)
			}
		} else {
			return result, fmt.Errorf("failed to get parent stack %q: %w", desired.Spec.StackName, err)
		}
	}

	// Get actual state
	actual, err := r.GetCell(desired)
	if err != nil {
		if errors.Is(err, errdefs.ErrCellNotFound) {
			// Cell doesn't exist, create it
			created, createErr := r.CreateCell(desired)
			if createErr != nil {
				return result, fmt.Errorf("failed to create cell: %w", createErr)
			}
			// Start the newly created cell (containers are created but not started)
			// This matches the behavior of controller.CreateCell which also starts cells
			if startErr := r.StartCell(created); startErr != nil {
				return result, fmt.Errorf("failed to start cell after creation: %w", startErr)
			}
			// Re-fetch the cell to get the latest state after starting
			started, getErr := r.GetCell(created)
			if getErr != nil {
				return result, fmt.Errorf("failed to get cell after starting: %w", getErr)
			}
			// Update cell state to Ready and persist metadata
			// This matches the behavior of controller.StartCell
			started.Status.State = intmodel.CellStateReady
			if updateErr := r.UpdateCellMetadata(started); updateErr != nil {
				return result, fmt.Errorf("failed to update cell metadata after start: %w", updateErr)
			}
			result.Action = actionCreated
			result.Resource = started
			return result, nil
		}
		return result, fmt.Errorf("failed to get cell: %w", err)
	}

	// Diff desired vs actual
	diff := DiffCell(desired, actual)
	if !diff.HasChanges {
		result.Resource = actual
		return result, nil
	}

	// Check for breaking changes (root container spec change)
	if diff.RootContainerChanged {
		// Recreate cell with new root container spec
		recreated, recreateErr := r.RecreateCell(desired)
		if recreateErr != nil {
			return result, fmt.Errorf("failed to recreate cell: %w", recreateErr)
		}
		result.Action = "updated"
		result.Resource = recreated
		result.Changes = []string{"root container recreated"}
		result.Details = diff.RootContainerDetails
		return result, nil
	}

	// Check for other breaking changes
	if isBreakingChange(diff.ChangeType) {
		return result, fmt.Errorf(
			"cell %q has breaking changes: %v. Delete the cell and recreate it with the new spec",
			desired.Metadata.Name,
			diff.BreakingChanges,
		)
	}

	// Apply compatible changes (container updates, metadata)
	updated, updateErr := r.UpdateCell(desired)
	if updateErr != nil {
		return result, fmt.Errorf("failed to update cell: %w", updateErr)
	}

	result.Action = "updated"
	result.Resource = updated
	result.Changes = diff.ChangedFields
	result.Details = diff.Details

	// Add container change details
	for _, containerDiff := range diff.Containers {
		switch containerDiff.Action {
		case "add":
			result.Changes = append(result.Changes, fmt.Sprintf("container %q added", containerDiff.Name))
		case "update":
			result.Changes = append(result.Changes, fmt.Sprintf("container %q updated", containerDiff.Name))
		}
	}
	for _, orphan := range diff.Orphans {
		result.Changes = append(result.Changes, fmt.Sprintf("container %q removed", orphan))
	}

	return result, nil
}

// ReconcileContainer reconciles a desired container state with the actual state.
func ReconcileContainer(r runner.Runner, desired intmodel.Container) (ReconcileResult, error) {
	result := ReconcileResult{
		Action: "unchanged",
		Kind:   "Container",
		Name:   desired.Metadata.Name,
	}

	// Ensure parent cell exists
	cell := intmodel.Cell{
		Metadata: intmodel.CellMetadata{
			Name: desired.Spec.CellName,
		},
		Spec: intmodel.CellSpec{
			RealmName: desired.Spec.RealmName,
			SpaceName: desired.Spec.SpaceName,
			StackName: desired.Spec.StackName,
		},
	}
	_, err := r.GetCell(cell)
	if err != nil {
		return result, fmt.Errorf("parent cell %q not found: %w", desired.Spec.CellName, err)
	}

	// Get actual state (need to get cell and find container in it)
	actualCell, err := r.GetCell(cell)
	if err != nil {
		return result, fmt.Errorf("failed to get cell: %w", err)
	}

	// Find container in cell
	var actualContainer *intmodel.ContainerSpec
	for i := range actualCell.Spec.Containers {
		if actualCell.Spec.Containers[i].ID == desired.Spec.ID {
			actualContainer = &actualCell.Spec.Containers[i]
			break
		}
	}

	if actualContainer == nil {
		// Container doesn't exist, create it
		updatedCell, createErr := r.CreateContainer(cell, desired.Spec)
		if createErr != nil {
			return result, fmt.Errorf("failed to create container: %w", createErr)
		}
		result.Action = actionCreated
		// Extract container from updated cell
		for i := range updatedCell.Spec.Containers {
			if updatedCell.Spec.Containers[i].ID == desired.Spec.ID {
				result.Resource = intmodel.Container{
					Metadata: desired.Metadata,
					Spec:     updatedCell.Spec.Containers[i],
				}
				break
			}
		}
		return result, nil
	}

	// Build actual container for diff
	actual := intmodel.Container{
		Metadata: desired.Metadata, // Use desired metadata for comparison
		Spec:     *actualContainer,
	}

	// Diff desired vs actual
	diff := DiffContainer(desired, actual)
	if !diff.HasChanges {
		result.Resource = actual
		return result, nil
	}

	// Check for breaking changes
	if isBreakingChange(diff.ChangeType) {
		return result, fmt.Errorf(
			"container %q has breaking changes: %v. Delete the container and recreate it with the new spec",
			desired.Metadata.Name,
			diff.BreakingChanges,
		)
	}

	// Apply compatible changes
	updatedCell, updateErr := r.UpdateContainer(cell, desired.Spec)
	if updateErr != nil {
		return result, fmt.Errorf("failed to update container: %w", updateErr)
	}

	result.Action = "updated"
	result.Changes = diff.ChangedFields
	result.Details = diff.Details

	// Extract updated container from cell
	for i := range updatedCell.Spec.Containers {
		if updatedCell.Spec.Containers[i].ID == desired.Spec.ID {
			result.Resource = intmodel.Container{
				Metadata: desired.Metadata,
				Spec:     updatedCell.Spec.Containers[i],
			}
			break
		}
	}

	return result, nil
}

// ReconcileResult represents the result of reconciling a resource.
type ReconcileResult struct {
	Action   string            // "created", "updated", "unchanged", "deleted"
	Kind     string            // Resource kind
	Name     string            // Resource name
	Resource interface{}       // The reconciled resource (internal model)
	Changes  []string          // List of changed fields
	Details  map[string]string // Detailed change descriptions
}
