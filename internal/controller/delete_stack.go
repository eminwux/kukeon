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

// DeleteStackResult reports what was deleted during stack deletion.
type DeleteStackResult struct {
	StackName string
	RealmName string
	SpaceName string
	Stack     intmodel.Stack

	MetadataDeleted bool
	CgroupDeleted   bool

	Deleted []string // Resources that were deleted (metadata, cgroup, cascaded resources)
}

// DeleteStack deletes a stack. If cascade is true, deletes all cells first.
// If force is true, skips validation of child resources.
func (b *Exec) DeleteStack(stack intmodel.Stack, force, cascade bool) (DeleteStackResult, error) {
	var res DeleteStackResult

	name := strings.TrimSpace(stack.Metadata.Name)
	if name == "" {
		return res, errdefs.ErrStackNameRequired
	}

	realmName := strings.TrimSpace(stack.Spec.RealmName)
	if realmName == "" {
		return res, errdefs.ErrRealmNameRequired
	}

	spaceName := strings.TrimSpace(stack.Spec.SpaceName)
	if spaceName == "" {
		return res, errdefs.ErrSpaceNameRequired
	}

	getResult, err := b.GetStack(stack)
	if err != nil {
		if errors.Is(err, errdefs.ErrStackNotFound) {
			return res, fmt.Errorf("stack %q not found in realm %q, space %q", name, realmName, spaceName)
		}
		return res, err
	}
	if !getResult.MetadataExists {
		return res, fmt.Errorf("stack %q not found in realm %q, space %q", name, realmName, spaceName)
	}

	res = DeleteStackResult{
		StackName: name,
		RealmName: realmName,
		SpaceName: spaceName,
		Stack:     getResult.Stack,
		Deleted:   []string{},
	}

	// Use private cascade method for deletion
	// Track deleted cells for result building (list before deletion to know what will be deleted)
	if cascade {
		cells, listErr := b.runner.ListCells(realmName, spaceName, name)
		if listErr != nil {
			return res, fmt.Errorf("failed to list cells: %w", listErr)
		}
		// Track cells that will be deleted
		for _, cell := range cells {
			res.Deleted = append(res.Deleted, fmt.Sprintf("cell:%s", cell.Metadata.Name))
		}
	}

	// Delete the resource itself (private method handles cascade deletion)
	if err = b.deleteStackCascade(getResult.Stack, force, cascade); err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrDeleteStack, err)
	}

	res.MetadataDeleted = true
	res.CgroupDeleted = true
	res.Deleted = append(res.Deleted, "metadata", "cgroup")
	return res, nil
}

// deleteStackCascade handles cascade deletion logic using runner methods directly.
// It returns an error if deletion fails, but does not return result types.
func (b *Exec) deleteStackCascade(stack intmodel.Stack, force, cascade bool) error {
	realmName := strings.TrimSpace(stack.Spec.RealmName)
	spaceName := strings.TrimSpace(stack.Spec.SpaceName)
	stackName := strings.TrimSpace(stack.Metadata.Name)

	// If cascade is true, list and delete child resources (cells)
	if cascade {
		cells, listErr := b.runner.ListCells(realmName, spaceName, stackName)
		if listErr != nil {
			return fmt.Errorf("failed to list cells: %w", listErr)
		}
		for _, cell := range cells {
			if delErr := b.deleteCellInternal(cell); delErr != nil {
				return fmt.Errorf("failed to delete cell %q: %w", cell.Metadata.Name, delErr)
			}
		}
	} else if !force {
		// Validate no child resources exist
		cells, listErr := b.runner.ListCells(realmName, spaceName, stackName)
		if listErr != nil {
			return fmt.Errorf("failed to list cells: %w", listErr)
		}
		if len(cells) > 0 {
			return fmt.Errorf("%w: stack %q has %d cell(s). Use --cascade to delete them or --force to skip validation",
				errdefs.ErrResourceHasDependencies, stackName, len(cells))
		}
	}

	// Delete the resource itself via runner
	return b.runner.DeleteStack(stack)
}
