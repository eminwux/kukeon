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

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// DeleteCellResult reports what was deleted during cell deletion.
type DeleteCellResult struct {
	Cell              intmodel.Cell
	ContainersDeleted bool
	CgroupDeleted     bool
	MetadataDeleted   bool
}

// DeleteCell deletes a cell. Always deletes all containers first.
func (b *Exec) DeleteCell(cell intmodel.Cell) (DeleteCellResult, error) {
	defer b.runner.Close()
	var res DeleteCellResult

	internalCell, err := b.validateAndGetCell(cell)
	if err != nil {
		return res, err
	}

	res = DeleteCellResult{
		Cell: internalCell,
	}

	// Always delete all containers in cell first
	if len(internalCell.Spec.Containers) > 0 {
		res.ContainersDeleted = true
	}

	// Use private internal method for deletion
	if err = b.deleteCellInternal(internalCell); err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrDeleteCell, err)
	}

	res.CgroupDeleted = true
	res.MetadataDeleted = true
	return res, nil
}

// deleteCellInternal handles the core cell deletion logic using runner methods directly.
// It deletes the cell via the runner, which handles container deletion, cgroup cleanup, and metadata deletion.
// It returns an error if deletion fails, but does not return result types.
func (b *Exec) deleteCellInternal(cell intmodel.Cell) error {
	return b.runner.DeleteCell(cell)
}
