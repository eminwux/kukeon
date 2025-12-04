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

// StopCellResult reports the outcome of stopping a cell.
type StopCellResult struct {
	Cell    intmodel.Cell
	Stopped bool
}

// StopCell stops all containers in a cell and updates the cell metadata state.
func (b *Exec) StopCell(cell intmodel.Cell) (StopCellResult, error) {
	defer b.runner.Close()
	var result StopCellResult

	internalCell, err := b.validateAndGetCell(cell)
	if err != nil {
		return result, err
	}

	// Stop all containers in the cell
	internalCell, err = b.runner.StopCell(internalCell)
	if err != nil {
		return result, fmt.Errorf("failed to stop cell containers: %w", err)
	}

	// Update cell metadata state to Stopped
	if err = b.runner.UpdateCellMetadata(internalCell); err != nil {
		return result, fmt.Errorf("failed to update cell metadata: %w", err)
	}

	result.Cell = internalCell
	result.Stopped = true
	return result, nil
}
