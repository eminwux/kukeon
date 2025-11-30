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

// DeleteSpaceResult reports what was deleted during space deletion.
type DeleteSpaceResult struct {
	SpaceName string
	RealmName string
	Space     intmodel.Space

	MetadataDeleted   bool
	CgroupDeleted     bool
	CNINetworkDeleted bool

	Deleted []string // Resources that were deleted (metadata, cgroup, network, cascaded resources)
}

// DeleteSpace deletes a space. If cascade is true, deletes all stacks first.
// If force is true, skips validation of child resources.
func (b *Exec) DeleteSpace(space intmodel.Space, force, cascade bool) (DeleteSpaceResult, error) {
	defer b.runner.Close()
	var res DeleteSpaceResult

	name := strings.TrimSpace(space.Metadata.Name)
	if name == "" {
		return res, errdefs.ErrSpaceNameRequired
	}

	realmName := strings.TrimSpace(space.Spec.RealmName)
	if realmName == "" {
		return res, errdefs.ErrRealmNameRequired
	}

	internalSpace, err := b.runner.GetSpace(space)
	if err != nil {
		if errors.Is(err, errdefs.ErrSpaceNotFound) {
			return res, fmt.Errorf("space %q not found in realm %q", name, realmName)
		}
		return res, err
	}

	res = DeleteSpaceResult{
		SpaceName: name,
		RealmName: realmName,
		Space:     internalSpace,
		Deleted:   []string{},
	}

	// Use private cascade method for deletion
	// Track deleted stacks for result building (list before deletion to know what will be deleted)
	if cascade {
		stacks, listErr := b.runner.ListStacks(realmName, name)
		if listErr != nil {
			return res, fmt.Errorf("failed to list stacks: %w", listErr)
		}
		// Track stacks that will be deleted
		for _, stack := range stacks {
			res.Deleted = append(res.Deleted, fmt.Sprintf("stack:%s", stack.Metadata.Name))
		}
	}

	// Delete the resource itself (private method handles cascade deletion)
	if err = b.deleteSpaceCascade(internalSpace, force, cascade); err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrDeleteSpace, err)
	}

	res.MetadataDeleted = true
	res.CgroupDeleted = true
	res.CNINetworkDeleted = true
	res.Deleted = append(res.Deleted, "metadata", "cgroup", "network")
	return res, nil
}

// deleteSpaceCascade handles cascade deletion logic using runner methods directly.
// It returns an error if deletion fails, but does not return result types.
func (b *Exec) deleteSpaceCascade(space intmodel.Space, force, cascade bool) error {
	realmName := strings.TrimSpace(space.Spec.RealmName)
	spaceName := strings.TrimSpace(space.Metadata.Name)

	// If cascade is true, list and delete child resources (stacks)
	if cascade {
		stacks, listErr := b.runner.ListStacks(realmName, spaceName)
		if listErr != nil {
			return fmt.Errorf("failed to list stacks: %w", listErr)
		}
		for _, stack := range stacks {
			if delErr := b.deleteStackCascade(stack, force, cascade); delErr != nil {
				return fmt.Errorf("failed to delete stack %q: %w", stack.Metadata.Name, delErr)
			}
		}
	} else if !force {
		// Validate no child resources exist
		stacks, listErr := b.runner.ListStacks(realmName, spaceName)
		if listErr != nil {
			return fmt.Errorf("failed to list stacks: %w", listErr)
		}
		if len(stacks) > 0 {
			return fmt.Errorf("%w: space %q has %d stack(s). Use --cascade to delete them or --force to skip validation",
				errdefs.ErrResourceHasDependencies, spaceName, len(stacks))
		}
	}

	// Delete the resource itself via runner
	return b.runner.DeleteSpace(space)
}
