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
