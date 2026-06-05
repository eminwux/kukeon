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
	"strings"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/cellconfig"
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
			started, startErr := r.StartCell(created)
			if startErr != nil {
				return result, fmt.Errorf("failed to start cell after creation: %w", startErr)
			}
			// Update cell metadata (status already set by runner)
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
		// Spec is in sync, but the cell's runtime may have been torn down
		// out of band — typically by `kuke kill`, which leaves the
		// cell document intact and flips Status.State to Stopped. Without
		// this branch, apply walks away reporting "unchanged" and the cell
		// stays Stopped forever (issue #486 — docs/cli-use-cases.md's
		// "Apply (declarative, multi-document)" names the kill-then-apply
		// flow as a divergence apply must reconcile).
		if cellNeedsRematerialize(actual) {
			rematerialized, startErr := r.StartCell(actual)
			if startErr != nil {
				return result, fmt.Errorf("failed to re-materialize cell: %w", startErr)
			}
			result.Action = actionUpdated
			result.Resource = rematerialized
			result.Changes = rematerializeChanges(rematerialized)
			return result, nil
		}
		result.Resource = actual
		return result, nil
	}

	// Check for breaking changes (root container spec change). Only
	// Breaking-on-root edits route through RecreateCell — Compatible-on-root
	// fields (env, ports, volumes, securityOpts, secrets, repos) toggle
	// `RootContainerChanged` so the diff surfaces them under
	// `rootContainer.<field>`, but they can be re-evaluated at start or
	// rebuilt from spec by UpdateCell and must not force a kill-and-recreate.
	// The companion gate at internal/controller/runner/spec_hash_test.go's
	// `TestSpecHashDomainPinsToDiffCellBreakingFields` keeps this AND in
	// lockstep with the spec-hash domain so the apply layer and StartCell
	// agree on which root edits are destructive.
	if diff.RootContainerChanged && diff.ChangeType == ChangeTypeBreaking {
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

	// Add container change details. The update branch surfaces the changed
	// fields so `kuke apply -f` of an image bump reports
	// `container "web" updated: image` instead of an opaque
	// `container "web" updated` — the issue-#485 per-component summary the
	// docs/cli-use-cases.md "apply updates a divergent existing cell" claim
	// promises.
	for _, containerDiff := range diff.Containers {
		switch containerDiff.Action {
		case "add":
			result.Changes = append(result.Changes, fmt.Sprintf("container %q added", containerDiff.Name))
		case "update":
			fields := append([]string{}, containerDiff.ChangedFields...)
			fields = append(fields, containerDiff.BreakingChanges...)
			if len(fields) > 0 {
				result.Changes = append(
					result.Changes,
					fmt.Sprintf("container %q updated: %s", containerDiff.Name, strings.Join(fields, ", ")),
				)
			} else {
				result.Changes = append(result.Changes, fmt.Sprintf("container %q updated", containerDiff.Name))
			}
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

// ReconcileSecret reconciles a desired `kind: Secret` by verifying its scope
// exists and persisting its bytes to the daemon-managed file (issue #619).
//
// Unlike the hierarchy reconcilers, ReconcileSecret never auto-creates a
// missing parent: a Secret targets an existing scope, so an unreachable
// realm/space/stack/cell is an apply-time error, not an implicit create. The
// scope coordinates are validated for completeness at parse time
// (validateSecretScope); this function adds the reachability gate the AC
// requires (scope must exist). The secret material itself is written
// write-through — re-applying overwrites and reports "updated"; the daemon
// never reads the bytes back to diff them, keeping the never-round-tripped
// contract crisp.
func ReconcileSecret(r runner.Runner, desired intmodel.Secret) (ReconcileResult, error) {
	result := ReconcileResult{
		Action: "unchanged",
		Kind:   "Secret",
		Name:   desired.Metadata.Name,
	}

	if err := ensureSecretScopeExists(r, desired.Metadata); err != nil {
		return result, err
	}

	created, writeErr := r.WriteSecret(desired)
	if writeErr != nil {
		return result, writeErr
	}
	if created {
		result.Action = actionCreated
	} else {
		result.Action = actionUpdated
	}
	return result, nil
}

// ensureSecretScopeExists verifies every scope coordinate the secret names is
// reachable, deepest-first short-circuiting on the first miss. A NotFound is
// translated to errdefs.ErrSecretScopeNotFound so apply surfaces a single,
// stable "scope does not exist" error regardless of which level is missing.
func ensureSecretScopeExists(r runner.Runner, md intmodel.SecretMetadata) error {
	if _, err := r.GetRealm(intmodel.Realm{
		Metadata: intmodel.RealmMetadata{Name: md.Realm},
	}); err != nil {
		return scopeLookupError(err, "realm", md.Realm)
	}

	if md.Space != "" {
		if _, err := r.GetSpace(intmodel.Space{
			Metadata: intmodel.SpaceMetadata{Name: md.Space},
			Spec:     intmodel.SpaceSpec{RealmName: md.Realm},
		}); err != nil {
			return scopeLookupError(err, "space", md.Space)
		}
	}

	if md.Stack != "" {
		if _, err := r.GetStack(intmodel.Stack{
			Metadata: intmodel.StackMetadata{Name: md.Stack},
			Spec:     intmodel.StackSpec{RealmName: md.Realm, SpaceName: md.Space},
		}); err != nil {
			return scopeLookupError(err, "stack", md.Stack)
		}
	}

	if md.Cell != "" {
		if _, err := r.GetCell(intmodel.Cell{
			Metadata: intmodel.CellMetadata{Name: md.Cell},
			Spec: intmodel.CellSpec{
				RealmName: md.Realm,
				SpaceName: md.Space,
				StackName: md.Stack,
			},
		}); err != nil {
			return scopeLookupError(err, "cell", md.Cell)
		}
	}

	return nil
}

// ReconcileBlueprint reconciles a desired `kind: CellBlueprint` by verifying
// its scope exists and persisting its document to the daemon-managed file
// (issue #620). Like ReconcileSecret it never auto-creates a missing scope: a
// Blueprint targets an existing realm/space/stack, so an unreachable scope is
// an apply-time error. The document is written write-through — re-applying
// overwrites and reports "updated".
func ReconcileBlueprint(r runner.Runner, desired intmodel.CellBlueprint) (ReconcileResult, error) {
	result := ReconcileResult{
		Action: "unchanged",
		Kind:   "CellBlueprint",
		Name:   desired.Metadata.Name,
	}

	if err := ensureBlueprintScopeExists(r, desired.Metadata); err != nil {
		return result, err
	}

	created, writeErr := r.WriteBlueprint(desired)
	if writeErr != nil {
		return result, writeErr
	}
	if created {
		result.Action = actionCreated
	} else {
		result.Action = actionUpdated
	}
	return result, nil
}

// ensureBlueprintScopeExists verifies every scope coordinate the blueprint
// names is reachable, deepest-first. A Blueprint is scopable at realm/space/
// stack only (never cell), so the walk stops at the stack. A NotFound is
// translated to errdefs.ErrBlueprintScopeNotFound.
func ensureBlueprintScopeExists(r runner.Runner, md intmodel.CellBlueprintMetadata) error {
	if _, err := r.GetRealm(intmodel.Realm{
		Metadata: intmodel.RealmMetadata{Name: md.Realm},
	}); err != nil {
		return blueprintScopeLookupError(err, "realm", md.Realm)
	}

	if md.Space != "" {
		if _, err := r.GetSpace(intmodel.Space{
			Metadata: intmodel.SpaceMetadata{Name: md.Space},
			Spec:     intmodel.SpaceSpec{RealmName: md.Realm},
		}); err != nil {
			return blueprintScopeLookupError(err, "space", md.Space)
		}
	}

	if md.Stack != "" {
		if _, err := r.GetStack(intmodel.Stack{
			Metadata: intmodel.StackMetadata{Name: md.Stack},
			Spec:     intmodel.StackSpec{RealmName: md.Realm, SpaceName: md.Space},
		}); err != nil {
			return blueprintScopeLookupError(err, "stack", md.Stack)
		}
	}

	return nil
}

// blueprintScopeLookupError maps a scope Get failure to a stable error. A
// NotFound at any level becomes ErrBlueprintScopeNotFound; any other error is
// propagated with context so it is not masked as a missing scope.
func blueprintScopeLookupError(err error, level, name string) error {
	switch {
	case errors.Is(err, errdefs.ErrRealmNotFound),
		errors.Is(err, errdefs.ErrSpaceNotFound),
		errors.Is(err, errdefs.ErrStackNotFound):
		return fmt.Errorf("%w: %s %q", errdefs.ErrBlueprintScopeNotFound, level, name)
	default:
		return fmt.Errorf("failed to verify blueprint scope %s %q: %w", level, name, err)
	}
}

// ReconcileConfig reconciles a desired `kind: CellConfig` by verifying its own
// scope exists, resolving the referenced CellBlueprint from daemon storage,
// validating the config's structural slot fills against that blueprint's
// declared slots, and persisting the config document (issue #624). Like
// ReconcileSecret/ReconcileBlueprint it never auto-creates a missing scope, and
// the document is written write-through — re-applying overwrites and reports
// "updated". The slot-fill match runs here (not at parse time) because only the
// daemon can read the stored blueprint the config references.
func ReconcileConfig(r runner.Runner, desired intmodel.CellConfig) (ReconcileResult, error) {
	result := ReconcileResult{
		Action: "unchanged",
		Kind:   "CellConfig",
		Name:   desired.Metadata.Name,
	}

	if err := ensureConfigScopeExists(r, desired.Metadata); err != nil {
		return result, err
	}

	cfgDoc, err := apischeme.ConvertCellConfigToExternal(desired)
	if err != nil {
		return result, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	ref := cfgDoc.Spec.Blueprint
	bpCarrier, getErr := r.GetBlueprint(intmodel.CellBlueprint{
		Metadata: intmodel.CellBlueprintMetadata{
			Name:  ref.Name,
			Realm: ref.Realm,
			Space: ref.Space,
			Stack: ref.Stack,
		},
	})
	if getErr != nil {
		if errors.Is(getErr, errdefs.ErrBlueprintNotFound) {
			return result, fmt.Errorf("%w: %q (realm %q)", errdefs.ErrConfigBlueprintNotFound, ref.Name, ref.Realm)
		}
		return result, fmt.Errorf("failed to read referenced blueprint %q: %w", ref.Name, getErr)
	}
	bpDoc, bpErr := apischeme.ConvertCellBlueprintToExternal(bpCarrier)
	if bpErr != nil {
		return result, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, bpErr)
	}

	if slotErr := cellconfig.ValidateSlotFill(cfgDoc, bpDoc); slotErr != nil {
		return result, slotErr
	}

	created, writeErr := r.WriteConfig(desired)
	if writeErr != nil {
		return result, writeErr
	}
	if created {
		result.Action = actionCreated
	} else {
		result.Action = actionUpdated
	}
	return result, nil
}

// CreateConfig is the atomic create-only sibling of ReconcileConfig used by
// `kuke create config` (issue #839). It runs the same scope-existence,
// blueprint-resolution, and slot-fill validation as ReconcileConfig, but
// persists via runner.WriteConfigIfAbsent — so two concurrent create
// attempts on the same name can never silently overwrite each other.
// Returns errdefs.ErrConfigExists when a Config with the same name already
// lives in scope (the caller surfaces it as a hard collision).
func CreateConfig(r runner.Runner, desired intmodel.CellConfig) (ReconcileResult, error) {
	result := ReconcileResult{
		Action: "unchanged",
		Kind:   "CellConfig",
		Name:   desired.Metadata.Name,
	}

	if err := ensureConfigScopeExists(r, desired.Metadata); err != nil {
		return result, err
	}

	cfgDoc, err := apischeme.ConvertCellConfigToExternal(desired)
	if err != nil {
		return result, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	ref := cfgDoc.Spec.Blueprint
	bpCarrier, getErr := r.GetBlueprint(intmodel.CellBlueprint{
		Metadata: intmodel.CellBlueprintMetadata{
			Name:  ref.Name,
			Realm: ref.Realm,
			Space: ref.Space,
			Stack: ref.Stack,
		},
	})
	if getErr != nil {
		if errors.Is(getErr, errdefs.ErrBlueprintNotFound) {
			return result, fmt.Errorf("%w: %q (realm %q)", errdefs.ErrConfigBlueprintNotFound, ref.Name, ref.Realm)
		}
		return result, fmt.Errorf("failed to read referenced blueprint %q: %w", ref.Name, getErr)
	}
	bpDoc, bpErr := apischeme.ConvertCellBlueprintToExternal(bpCarrier)
	if bpErr != nil {
		return result, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, bpErr)
	}

	if slotErr := cellconfig.ValidateSlotFill(cfgDoc, bpDoc); slotErr != nil {
		return result, slotErr
	}

	if writeErr := r.WriteConfigIfAbsent(desired); writeErr != nil {
		return result, writeErr
	}
	result.Action = actionCreated
	return result, nil
}

// ensureConfigScopeExists verifies every scope coordinate the config names is
// reachable, deepest-first. A Config is scopable at realm/space/stack only
// (never cell), so the walk stops at the stack. A NotFound is translated to
// errdefs.ErrConfigScopeNotFound.
func ensureConfigScopeExists(r runner.Runner, md intmodel.CellConfigMetadata) error {
	if _, err := r.GetRealm(intmodel.Realm{
		Metadata: intmodel.RealmMetadata{Name: md.Realm},
	}); err != nil {
		return configScopeLookupError(err, "realm", md.Realm)
	}

	if md.Space != "" {
		if _, err := r.GetSpace(intmodel.Space{
			Metadata: intmodel.SpaceMetadata{Name: md.Space},
			Spec:     intmodel.SpaceSpec{RealmName: md.Realm},
		}); err != nil {
			return configScopeLookupError(err, "space", md.Space)
		}
	}

	if md.Stack != "" {
		if _, err := r.GetStack(intmodel.Stack{
			Metadata: intmodel.StackMetadata{Name: md.Stack},
			Spec:     intmodel.StackSpec{RealmName: md.Realm, SpaceName: md.Space},
		}); err != nil {
			return configScopeLookupError(err, "stack", md.Stack)
		}
	}

	return nil
}

// configScopeLookupError maps a scope Get failure to a stable error. A NotFound
// at any level becomes ErrConfigScopeNotFound; any other error is propagated
// with context so it is not masked as a missing scope.
func configScopeLookupError(err error, level, name string) error {
	switch {
	case errors.Is(err, errdefs.ErrRealmNotFound),
		errors.Is(err, errdefs.ErrSpaceNotFound),
		errors.Is(err, errdefs.ErrStackNotFound):
		return fmt.Errorf("%w: %s %q", errdefs.ErrConfigScopeNotFound, level, name)
	default:
		return fmt.Errorf("failed to verify config scope %s %q: %w", level, name, err)
	}
}

// scopeLookupError maps a scope Get failure to a stable error. A NotFound at
// any level becomes ErrSecretScopeNotFound (the AC's "scope must exist"
// gate); any other error (e.g. a corrupt metadata read) is propagated with
// context so it is not masked as a missing scope.
func scopeLookupError(err error, level, name string) error {
	switch {
	case errors.Is(err, errdefs.ErrRealmNotFound),
		errors.Is(err, errdefs.ErrSpaceNotFound),
		errors.Is(err, errdefs.ErrStackNotFound),
		errors.Is(err, errdefs.ErrCellNotFound):
		return fmt.Errorf("%w: %s %q", errdefs.ErrSecretScopeNotFound, level, name)
	default:
		return fmt.Errorf("failed to verify secret scope %s %q: %w", level, name, err)
	}
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

// cellNeedsRematerialize reports whether a spec-equal cell still needs
// runtime work because its containers were torn down out of band. Today
// this fires for CellStateStopped — the state runner.KillCell persists
// after `kuke kill` removes the cell's containers. Failed, Pending,
// and Unknown intentionally do not trigger re-materialize: Failed is
// sticky and signals a startup problem the user should investigate;
// Pending and Unknown are mid-lifecycle states the daemon reconciler
// loop normalizes on its own.
func cellNeedsRematerialize(cell intmodel.Cell) bool {
	return cell.Status.State == intmodel.CellStateStopped
}

// rematerializeChanges builds the per-component summary printed under
// `Cell <name>: updated` after StartCell brings the cell back from
// Stopped. Root containers are excluded to match the existing
// container-diff branch — users authored the workload containers, the
// auto-default root is an implementation detail.
func rematerializeChanges(cell intmodel.Cell) []string {
	changes := []string{"runtime stopped: containers re-materialized"}
	for _, c := range cell.Spec.Containers {
		if c.Root {
			continue
		}
		changes = append(changes, fmt.Sprintf("container %q re-materialized", c.ID))
	}
	return changes
}
