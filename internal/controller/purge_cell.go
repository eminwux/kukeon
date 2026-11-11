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
	"slices"
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

	result = PurgeCellResult{
		Cell:    internalCell,
		Force:   force,
		Cascade: cascade,
		Deleted: []string{},
		Purged:  []string{},
	}

	// The runner owns deletion and the comprehensive purge so it can report the
	// final state of each cleanup step instead of inferring success from nil.
	outcome, purgeErr := b.runner.PurgeCell(internalCell)
	result.ContainersDeleted = outcome.ContainersDeleted
	result.CgroupDeleted = outcome.CgroupDeleted
	result.MetadataDeleted = outcome.MetadataDeleted
	result.PurgeSucceeded = purgeErr == nil && outcome.CgroupDeleted && outcome.MetadataDeleted &&
		(len(internalCell.Spec.Containers) == 0 || outcome.ContainersDeleted) &&
		slices.Contains(outcome.Purged, "cni-resources") &&
		slices.Contains(outcome.Purged, "orphaned-containers")
	if len(internalCell.Spec.Containers) > 0 && outcome.ContainersDeleted {
		result.Deleted = append(result.Deleted, "containers")
	}
	if outcome.CgroupDeleted {
		result.Deleted = append(result.Deleted, "cgroup")
	}
	if outcome.MetadataDeleted {
		result.Deleted = append(result.Deleted, "metadata")
	}
	result.Purged = append(result.Purged, outcome.Purged...)
	if purgeErr != nil {
		result.Purged = append(result.Purged, fmt.Sprintf("purge-error:%v", purgeErr))
	}

	return result, nil
}

// purgeCellCascade is used by parent-resource purges, which need an error-only
// result to stop the cascade when a child cell could not be fully cleaned up.
func (b *Exec) purgeCellCascade(cell intmodel.Cell, _, _ bool) error {
	if err := b.deleteCellInternal(cell); err != nil {
		return fmt.Errorf("failed to delete cell: %w", err)
	}
	_, err := b.runner.PurgeCell(cell)
	return err
}
