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

// PurgeSpaceResult reports what was purged during space purging.
type PurgeSpaceResult struct {
	Space intmodel.Space

	MetadataDeleted   bool
	CgroupDeleted     bool
	CNINetworkDeleted bool
	PurgeSucceeded    bool
	Force             bool
	Cascade           bool

	Deleted []string // Resources that were deleted (standard cleanup)
	Purged  []string // Additional resources purged (CNI, orphaned containers, etc.)
}

// PurgeSpace purges a space with comprehensive cleanup. If cascade is true, purges all stacks first.
// If force is true, skips validation of child resources.
func (b *Exec) PurgeSpace(space intmodel.Space, force, cascade bool) (PurgeSpaceResult, error) {
	var result PurgeSpaceResult

	name := strings.TrimSpace(space.Metadata.Name)
	if name == "" {
		return result, errdefs.ErrSpaceNameRequired
	}

	realmName := strings.TrimSpace(space.Spec.RealmName)
	if realmName == "" {
		return result, errdefs.ErrRealmNameRequired
	}

	getResult, err := b.GetSpace(space)
	if err != nil {
		if errors.Is(err, errdefs.ErrSpaceNotFound) {
			return result, fmt.Errorf("space %q not found in realm %q", name, realmName)
		}
		return result, err
	}
	if !getResult.MetadataExists {
		return result, fmt.Errorf("space %q not found in realm %q", name, realmName)
	}

	internalSpace := getResult.Space

	// Initialize result with space and flags
	result = PurgeSpaceResult{
		Space:   internalSpace,
		Force:   force,
		Cascade: cascade,
		Deleted: []string{},
		Purged:  []string{},
	}

	// Track deleted stacks for result building (list before deletion to know what will be deleted)
	if cascade {
		stacks, listErr := b.runner.ListStacks(realmName, name)
		if listErr != nil {
			return result, fmt.Errorf("failed to list stacks: %w", listErr)
		}
		// Track stacks that will be deleted
		for _, stack := range stacks {
			result.Deleted = append(result.Deleted, fmt.Sprintf("stack:%s", stack.Metadata.Name))
		}
	}

	// Call private cascade method (handles cascade deletion, standard delete, and comprehensive purge)
	if err = b.purgeSpaceCascade(internalSpace, force, cascade); err != nil {
		result.Purged = append(result.Purged, fmt.Sprintf("purge-error:%v", err))
		result.PurgeSucceeded = false
	} else {
		// Since private method succeeded, assume all operations succeeded
		result.MetadataDeleted = true
		result.CgroupDeleted = true
		result.CNINetworkDeleted = true
		result.Deleted = append(result.Deleted, "metadata", "cgroup", "network")
		result.Purged = append(result.Purged, "cni-network", "cni-cache", "orphaned-containers", "all-metadata")
		result.PurgeSucceeded = true
	}

	return result, nil
}

// purgeSpaceCascade handles cascade deletion and purging logic using runner methods directly.
// It returns an error if deletion/purging fails, but does not return result types.
func (b *Exec) purgeSpaceCascade(space intmodel.Space, force, cascade bool) error {
	realmName := strings.TrimSpace(space.Spec.RealmName)
	spaceName := strings.TrimSpace(space.Metadata.Name)

	// If cascade is true, list and purge child resources (stacks) recursively
	if cascade {
		stacks, err := b.runner.ListStacks(realmName, spaceName)
		if err != nil {
			return fmt.Errorf("failed to list stacks: %w", err)
		}
		for _, stack := range stacks {
			if err = b.purgeStackCascade(stack, force, cascade); err != nil {
				return fmt.Errorf("failed to purge stack %q: %w", stack.Metadata.Name, err)
			}
		}
	} else if !force {
		// Validate no child resources exist
		stacks, err := b.runner.ListStacks(realmName, spaceName)
		if err != nil {
			return fmt.Errorf("failed to list stacks: %w", err)
		}
		if len(stacks) > 0 {
			return fmt.Errorf("%w: space %q has %d stack(s). Use --cascade to purge them or --force to skip validation",
				errdefs.ErrResourceHasDependencies, spaceName, len(stacks))
		}
	}

	// Perform standard delete first (using deleteSpaceCascade)
	if err := b.deleteSpaceCascade(space, force, cascade); err != nil {
		return fmt.Errorf("failed to delete space: %w", err)
	}

	// Perform comprehensive purge via runner
	return b.runner.PurgeSpace(space)
}
