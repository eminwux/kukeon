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
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// KillCellResult reports the outcome of killing a cell.
type KillCellResult struct {
	Cell   intmodel.Cell
	Killed bool
}

// KillCell immediately force-kills all containers in a cell and updates the cell metadata state.
func (b *Exec) KillCell(cell intmodel.Cell) (KillCellResult, error) {
	var res KillCellResult

	name := strings.TrimSpace(cell.Metadata.Name)
	if name == "" {
		return res, errdefs.ErrCellNameRequired
	}
	realmName := strings.TrimSpace(cell.Spec.RealmName)
	if realmName == "" {
		return res, errdefs.ErrRealmNameRequired
	}
	spaceName := strings.TrimSpace(cell.Spec.SpaceName)
	if spaceName == "" {
		return res, errdefs.ErrSpaceNameRequired
	}
	stackName := strings.TrimSpace(cell.Spec.StackName)
	if stackName == "" {
		return res, errdefs.ErrStackNameRequired
	}

	// Build lookup cell for GetCell
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
	getResult, err := b.GetCell(lookupCell)
	if err != nil {
		return res, err
	}
	if !getResult.MetadataExists {
		return res, fmt.Errorf(
			"cell %q not found in realm %q, space %q, stack %q",
			name,
			realmName,
			spaceName,
			stackName,
		)
	}
	internalCell := getResult.Cell

	// Kill all containers in the cell
	if err = b.runner.KillCell(internalCell); err != nil {
		return res, fmt.Errorf("failed to kill cell containers: %w", err)
	}

	// Update cell state to Pending (killed)
	internalCell.Status.State = intmodel.CellStatePending

	// Update cell metadata state to Pending (killed)
	if err = b.runner.UpdateCellMetadata(internalCell); err != nil {
		return res, fmt.Errorf("failed to update cell metadata: %w", err)
	}

	res.Cell = internalCell
	res.Killed = true
	return res, nil
}
