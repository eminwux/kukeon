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

// DeleteRealmResult reports what was deleted during realm deletion.
type DeleteRealmResult struct {
	Realm                      intmodel.Realm
	Deleted                    []string // Resources that were deleted (metadata, cgroup, namespace, cascaded resources)
	MetadataDeleted            bool
	CgroupDeleted              bool
	ContainerdNamespaceDeleted bool
}

// deleteRealmCascade handles cascade deletion logic using runner methods directly.
// It returns an error if deletion fails, but does not return result types.
func (b *Exec) deleteRealmCascade(realm intmodel.Realm, force, cascade bool) error {
	realmName := strings.TrimSpace(realm.Metadata.Name)

	// If cascade is true, list and delete child resources (spaces)
	if cascade {
		spaces, listErr := b.runner.ListSpaces(realmName)
		if listErr != nil {
			return fmt.Errorf("failed to list spaces: %w", listErr)
		}
		for _, space := range spaces {
			if delErr := b.deleteSpaceCascade(space, force, cascade); delErr != nil {
				return fmt.Errorf("failed to delete space %q: %w", space.Metadata.Name, delErr)
			}
		}
	} else if !force {
		// Validate no child resources exist
		spaces, listErr := b.runner.ListSpaces(realmName)
		if listErr != nil {
			return fmt.Errorf("failed to list spaces: %w", listErr)
		}
		if len(spaces) > 0 {
			return fmt.Errorf("%w: realm %q has %d space(s). Use --cascade to delete them or --force to skip validation",
				errdefs.ErrResourceHasDependencies, realmName, len(spaces))
		}
	}

	// Delete the resource itself via runner (ignore outcome, return error only)
	_, err := b.runner.DeleteRealm(realm)
	return err
}

// DeleteRealm deletes a realm. If cascade is true, deletes all spaces first.
// If force is true, skips validation of child resources.
func (b *Exec) DeleteRealm(realm intmodel.Realm, force, cascade bool) (DeleteRealmResult, error) {
	var res DeleteRealmResult

	name := strings.TrimSpace(realm.Metadata.Name)
	if name == "" {
		return res, errdefs.ErrRealmNameRequired
	}

	getResult, err := b.runner.GetRealm(realm)
	if err != nil {
		if errors.Is(err, errdefs.ErrRealmNotFound) {
			// Realm not found, return error
			return res, fmt.Errorf("realm %q not found", name)
		}
		// Other error, return error
		return res, err
	}

	res = DeleteRealmResult{
		Realm:   getResult,
		Deleted: []string{},
	}

	// Use private cascade method for deletion
	// Track deleted spaces for result building (list before deletion to know what will be deleted)
	if cascade {
		spaces, listErr := b.runner.ListSpaces(name)
		if listErr != nil {
			return res, fmt.Errorf("failed to list spaces: %w", listErr)
		}
		// Track spaces that will be deleted
		for _, space := range spaces {
			res.Deleted = append(res.Deleted, fmt.Sprintf("space:%s", space.Metadata.Name))
		}
	}

	// Delete the resource itself (private method handles cascade deletion)
	// Note: private method deletes realm and returns error only, so we can't get outcome
	// Since private method succeeded, we assume all deletions succeeded
	if err = b.deleteRealmCascade(getResult, force, cascade); err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrDeleteRealm, err)
	}

	// Since private method succeeded, assume all deletions succeeded
	// (We can't get the actual outcome since realm is already deleted)
	res.MetadataDeleted = true
	res.CgroupDeleted = true
	res.ContainerdNamespaceDeleted = true

	res.Deleted = append(res.Deleted, "metadata", "cgroup", "namespace")

	return res, nil
}
