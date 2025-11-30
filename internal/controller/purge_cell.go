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

// PurgeCellResult reports what was purged during cell purging.
type PurgeCellResult struct {
	Cell              intmodel.Cell
	ContainersDeleted bool
	CgroupDeleted     bool
	MetadataDeleted   bool
	PurgeSucceeded    bool
	Force             bool
	Cascade           bool
	Deleted           []string // Resources that were deleted (standard cleanup)
	Purged            []string // Additional resources purged (CNI, orphaned containers, etc.)
}

// PurgeCell purges a cell with comprehensive cleanup. Always purges all containers first.
// If force is true, skips validation (currently unused but recorded for auditing).
func (b *Exec) PurgeCell(cell intmodel.Cell, force, cascade bool) (PurgeCellResult, error) {
	var result PurgeCellResult

	name := strings.TrimSpace(cell.Metadata.Name)
	if name == "" {
		return result, errdefs.ErrCellNameRequired
	}

	realmName := strings.TrimSpace(cell.Spec.RealmName)
	if realmName == "" {
		return result, errdefs.ErrRealmNameRequired
	}

	spaceName := strings.TrimSpace(cell.Spec.SpaceName)
	if spaceName == "" {
		return result, errdefs.ErrSpaceNameRequired
	}

	stackName := strings.TrimSpace(cell.Spec.StackName)
	if stackName == "" {
		return result, errdefs.ErrStackNameRequired
	}

	internalCell, err := b.runner.GetCell(cell)
	if err != nil {
		if errors.Is(err, errdefs.ErrCellNotFound) {
			return result, fmt.Errorf(
				"cell %q not found in realm %q, space %q, stack %q",
				name,
				realmName,
				spaceName,
				stackName,
			)
		}
		return result, err
	}

	// Initialize result with cell and flags
	result = PurgeCellResult{
		Cell:    internalCell,
		Force:   force,
		Cascade: cascade,
		Deleted: []string{},
		Purged:  []string{},
	}

	// Track what will be deleted before calling private method
	if len(internalCell.Spec.Containers) > 0 {
		result.Deleted = append(result.Deleted, "containers")
	}
	result.Deleted = append(result.Deleted, "cgroup", "metadata")

	// Call private cascade method for deletion and purging
	if err = b.purgeCellCascade(internalCell, force, cascade); err != nil {
		result.Purged = append(result.Purged, fmt.Sprintf("purge-error:%v", err))
		result.PurgeSucceeded = false
	} else {
		// Since private method succeeded, assume all deletions succeeded
		result.ContainersDeleted = len(internalCell.Spec.Containers) > 0
		result.CgroupDeleted = true
		result.MetadataDeleted = true
		result.Purged = append(result.Purged, "cni-resources", "orphaned-containers", "all-metadata")
		result.PurgeSucceeded = true
	}

	return result, nil
}

// purgeCellCascade handles cell deletion and purging logic using runner methods directly.
// It returns an error if deletion/purging fails, but does not return result types.
// Note: cascade parameter is accepted for consistency but not used (cells are leaf resources).
func (b *Exec) purgeCellCascade(cell intmodel.Cell, _, _ bool) error {
	// Perform standard delete first (using deleteCellInternal)
	if err := b.deleteCellInternal(cell); err != nil {
		return fmt.Errorf("failed to delete cell: %w", err)
	}

	// Perform comprehensive purge via runner
	return b.runner.PurgeCell(cell)
}
