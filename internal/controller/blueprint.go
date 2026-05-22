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
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// GetBlueprintResult reports the full document view of a single
// `kind: CellBlueprint` (issue #620). Unlike a Secret, a blueprint carries no
// credential bytes — only template references — so the whole document is
// returned for `kuke run -b` to materialize.
type GetBlueprintResult struct {
	Blueprint      intmodel.CellBlueprint
	MetadataExists bool
}

// GetBlueprint reads one named, scoped CellBlueprint's document. The scope
// coordinates are validated for completeness (a deeper coordinate requires
// every shallower one; a Blueprint may not be cell-scoped); realm and name are
// mandatory. Returns MetadataExists=false (no error) when the blueprint is
// absent, mirroring GetSecret's "report, don't error on not-found" shape.
func (b *Exec) GetBlueprint(blueprint intmodel.CellBlueprint) (GetBlueprintResult, error) {
	var res GetBlueprintResult

	if err := validateBlueprintLookup(blueprint.Metadata); err != nil {
		return res, err
	}

	got, err := b.runner.GetBlueprint(blueprint)
	if err != nil {
		if errors.Is(err, errdefs.ErrBlueprintNotFound) {
			res.MetadataExists = false
			return res, nil
		}
		return res, fmt.Errorf("%w: %w", errdefs.ErrGetBlueprint, err)
	}

	res.MetadataExists = true
	res.Blueprint = got
	return res, nil
}

// DeleteBlueprintResult reports the outcome of removing a single CellBlueprint's
// daemon-stored document file.
type DeleteBlueprintResult struct {
	Blueprint intmodel.CellBlueprint
	Deleted   bool
}

// ListBlueprints lists the metadata of every CellBlueprint bound to the filter
// scope or any scope nested within it (issue #643). An empty realm lists across
// all realms; the filter coordinates must still be contiguous (no gap below a
// set level), and — a Blueprint never being cell-scoped — there is no cell
// coordinate.
func (b *Exec) ListBlueprints(realmName, spaceName, stackName string) ([]intmodel.CellBlueprint, error) {
	if err := validateBlueprintScopeFilter(realmName, spaceName, stackName); err != nil {
		return nil, err
	}
	return b.runner.ListBlueprints(
		strings.TrimSpace(realmName),
		strings.TrimSpace(spaceName),
		strings.TrimSpace(stackName),
	)
}

// DeleteBlueprint removes a single named, scoped CellBlueprint's daemon-stored
// document. Returns a "not found" error when the blueprint does not exist,
// matching the DeleteSecret contract. There is no live-reference gate: cells
// materialized from a blueprint are independent copies (#620).
func (b *Exec) DeleteBlueprint(blueprint intmodel.CellBlueprint) (DeleteBlueprintResult, error) {
	var res DeleteBlueprintResult

	if err := validateBlueprintLookup(blueprint.Metadata); err != nil {
		return res, err
	}

	if err := b.runner.DeleteBlueprint(blueprint); err != nil {
		if errors.Is(err, errdefs.ErrBlueprintNotFound) {
			return res, fmt.Errorf("blueprint %q not found", blueprint.Metadata.Name)
		}
		return res, fmt.Errorf("%w: %w", errdefs.ErrDeleteBlueprint, err)
	}

	res.Deleted = true
	res.Blueprint = blueprint
	return res, nil
}

// validateBlueprintLookup enforces the scope contract for a single-blueprint
// get: name and realm are mandatory and the scope coordinates must be
// contiguous, with the Blueprint-specific rule that a cell coordinate is never
// valid (a Blueprint is realm/space/stack-scoped only).
func validateBlueprintLookup(md intmodel.CellBlueprintMetadata) error {
	if strings.TrimSpace(md.Name) == "" {
		return errdefs.ErrBlueprintNameRequired
	}
	if strings.TrimSpace(md.Realm) == "" {
		return errdefs.ErrBlueprintRealmRequired
	}
	if strings.TrimSpace(md.Stack) != "" && strings.TrimSpace(md.Space) == "" {
		return fmt.Errorf("%w (stack set without space)", errdefs.ErrBlueprintScopeIncomplete)
	}
	return nil
}

// validateBlueprintScopeFilter enforces the scope contract for a list filter:
// realm is optional (an empty realm lists across all realms), but a set deeper
// coordinate still requires every shallower one. A Blueprint is never
// cell-scoped, so the filter bottoms out at stack.
func validateBlueprintScopeFilter(realm, space, stack string) error {
	realm = strings.TrimSpace(realm)
	space = strings.TrimSpace(space)
	stack = strings.TrimSpace(stack)

	if realm == "" && (space != "" || stack != "") {
		return fmt.Errorf("%w (scope set without realm)", errdefs.ErrBlueprintScopeIncomplete)
	}
	if stack != "" && space == "" {
		return fmt.Errorf("%w (stack set without space)", errdefs.ErrBlueprintScopeIncomplete)
	}
	return nil
}
