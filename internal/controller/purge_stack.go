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

// PurgeStackResult reports what was purged during stack purging.
type PurgeStackResult struct {
	Stack   intmodel.Stack
	Deleted []string // Resources that were deleted (standard cleanup)
	Purged  []string // Additional resources purged (CNI, orphaned containers, etc.)
}

// PurgeStack purges a stack with comprehensive cleanup. If cascade is true, purges all cells first.
// If force is true, skips validation of child resources.
func (b *Exec) PurgeStack(stack intmodel.Stack, force, cascade bool) (PurgeStackResult, error) {
	var result PurgeStackResult

	name := strings.TrimSpace(stack.Metadata.Name)
	if name == "" {
		return result, errdefs.ErrStackNameRequired
	}

	realmName := strings.TrimSpace(stack.Spec.RealmName)
	if realmName == "" {
		return result, errdefs.ErrRealmNameRequired
	}

	spaceName := strings.TrimSpace(stack.Spec.SpaceName)
	if spaceName == "" {
		return result, errdefs.ErrSpaceNameRequired
	}

	internalStack, err := b.runner.GetStack(stack)
	if err != nil {
		if errors.Is(err, errdefs.ErrStackNotFound) {
			return result, fmt.Errorf("stack %q not found in realm %q, space %q", name, realmName, spaceName)
		}
		return result, err
	}

	// Initialize result with stack and flags
	result = PurgeStackResult{
		Stack:   internalStack,
		Deleted: []string{},
		Purged:  []string{},
	}

	// Track deleted cells for result building (list before deletion to know what will be deleted)
	if cascade {
		cells, listErr := b.runner.ListCells(realmName, spaceName, name)
		if listErr != nil {
			return result, fmt.Errorf("failed to list cells: %w", listErr)
		}
		// Track cells that will be deleted
		for _, cell := range cells {
			result.Deleted = append(result.Deleted, fmt.Sprintf("cell:%s", cell.Metadata.Name))
		}
	}

	// Call private cascade method (handles cascade deletion, standard delete, and comprehensive purge)
	if err = b.purgeStackCascade(internalStack, force, cascade); err != nil {
		result.Purged = append(result.Purged, fmt.Sprintf("purge-error:%v", err))
	} else {
		// Since private method succeeded, assume all operations succeeded
		result.Deleted = append(result.Deleted, "metadata", "cgroup")
		result.Purged = append(result.Purged, "cni-resources", "orphaned-containers", "all-metadata")
	}

	return result, nil
}

// purgeStackCascade handles cascade deletion and purging logic using runner methods directly.
// It returns an error if deletion/purging fails, but does not return result types.
func (b *Exec) purgeStackCascade(stack intmodel.Stack, force, cascade bool) error {
	realmName := strings.TrimSpace(stack.Spec.RealmName)
	spaceName := strings.TrimSpace(stack.Spec.SpaceName)
	stackName := strings.TrimSpace(stack.Metadata.Name)

	// If cascade is true, list and purge child resources (cells) recursively
	if cascade {
		cells, err := b.runner.ListCells(realmName, spaceName, stackName)
		if err != nil {
			return fmt.Errorf("failed to list cells: %w", err)
		}
		for _, cell := range cells {
			// Cells don't cascade, so pass cascade=false
			if err = b.purgeCellCascade(cell, force, false); err != nil {
				return fmt.Errorf("failed to purge cell %q: %w", cell.Metadata.Name, err)
			}
		}
	} else if !force {
		// Validate no child resources exist
		cells, err := b.runner.ListCells(realmName, spaceName, stackName)
		if err != nil {
			return fmt.Errorf("failed to list cells: %w", err)
		}
		if len(cells) > 0 {
			return fmt.Errorf("%w: stack %q has %d cell(s). Use --cascade to purge them or --force to skip validation",
				errdefs.ErrResourceHasDependencies, stackName, len(cells))
		}
	}

	// Perform standard delete first (using deleteStackCascade)
	if err := b.deleteStackCascade(stack, force, cascade); err != nil {
		return fmt.Errorf("failed to delete stack: %w", err)
	}

	// Perform comprehensive purge via runner
	return b.runner.PurgeStack(stack)
}
