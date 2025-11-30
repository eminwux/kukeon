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

// GetCellResult reports the current state of a cell.
type GetCellResult struct {
	Cell                intmodel.Cell
	MetadataExists      bool
	CgroupExists        bool
	RootContainerExists bool
}

// GetCell retrieves a single cell and reports its current state.
func (b *Exec) GetCell(cell intmodel.Cell) (GetCellResult, error) {
	var res GetCellResult

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

	// Build lookup cell for runner
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

	internalCell, err := b.runner.GetCell(lookupCell)
	if err != nil {
		if errors.Is(err, errdefs.ErrCellNotFound) {
			res.MetadataExists = false
		} else {
			return res, fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
		}
	} else {
		res.MetadataExists = true
		// Verify realm, space, and stack match
		if internalCell.Spec.RealmName != realmName {
			return res, fmt.Errorf("cell %q not found in realm %q (found in realm %q) at run-path %q",
				name, realmName, internalCell.Spec.RealmName, b.opts.RunPath)
		}
		if internalCell.Spec.SpaceName != spaceName {
			return res, fmt.Errorf("cell %q not found in space %q (found in space %q) at run-path %q",
				name, spaceName, internalCell.Spec.SpaceName, b.opts.RunPath)
		}
		if internalCell.Spec.StackName != stackName {
			return res, fmt.Errorf("cell %q not found in stack %q (found in stack %q) at run-path %q",
				name, stackName, internalCell.Spec.StackName, b.opts.RunPath)
		}
		res.CgroupExists, err = b.runner.ExistsCgroup(internalCell)
		if err != nil {
			return res, fmt.Errorf("failed to check if cell cgroup exists: %w", err)
		}
		res.RootContainerExists, err = b.runner.ExistsCellRootContainer(internalCell)
		if err != nil {
			return res, fmt.Errorf("failed to check root container: %w", err)
		}
		res.Cell = internalCell
	}

	return res, nil
}

// ListCells lists all cells, optionally filtered by realm, space, and/or stack.
func (b *Exec) ListCells(realmName, spaceName, stackName string) ([]intmodel.Cell, error) {
	return b.runner.ListCells(realmName, spaceName, stackName)
}

// validateAndGetCell validates cell input parameters and retrieves the cell.
// It performs validation of name, realmName, spaceName, and stackName,
// builds a lookup cell, calls GetCell, and handles errors appropriately.
func (b *Exec) validateAndGetCell(cell intmodel.Cell) (intmodel.Cell, error) {
	name := strings.TrimSpace(cell.Metadata.Name)
	if name == "" {
		return intmodel.Cell{}, errdefs.ErrCellNameRequired
	}

	realmName := strings.TrimSpace(cell.Spec.RealmName)
	if realmName == "" {
		return intmodel.Cell{}, errdefs.ErrRealmNameRequired
	}

	spaceName := strings.TrimSpace(cell.Spec.SpaceName)
	if spaceName == "" {
		return intmodel.Cell{}, errdefs.ErrSpaceNameRequired
	}

	stackName := strings.TrimSpace(cell.Spec.StackName)
	if stackName == "" {
		return intmodel.Cell{}, errdefs.ErrStackNameRequired
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
		if errors.Is(err, errdefs.ErrCellNotFound) {
			return intmodel.Cell{}, fmt.Errorf(
				"cell %q not found in realm %q, space %q, stack %q",
				name,
				realmName,
				spaceName,
				stackName,
			)
		}
		return intmodel.Cell{}, err
	}
	if !getResult.MetadataExists {
		return intmodel.Cell{}, fmt.Errorf("cell %q not found", name)
	}

	return getResult.Cell, nil
}
