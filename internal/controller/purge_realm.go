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

// PurgeRealmResult reports what was purged during realm purging.
type PurgeRealmResult struct {
	Realm          intmodel.Realm
	RealmDeleted   bool     // Whether realm deletion succeeded
	PurgeSucceeded bool     // Whether comprehensive purge succeeded
	Force          bool     // Force flag that was used
	Cascade        bool     // Cascade flag that was used
	Deleted        []string // Resources that were deleted (standard cleanup)
	Purged         []string // Additional resources purged (CNI, orphaned containers, etc.)
}

// PurgeRealm purges a realm with comprehensive cleanup. If cascade is true, purges all spaces first.
// If force is true, skips validation of child resources.
func (b *Exec) PurgeRealm(realm intmodel.Realm, force, cascade bool) (PurgeRealmResult, error) {
	var result PurgeRealmResult

	name := strings.TrimSpace(realm.Metadata.Name)
	if name == "" {
		return result, errdefs.ErrRealmNameRequired
	}

	// Default namespace to realm name if not set (matching CreateRealm behavior)
	namespace := strings.TrimSpace(realm.Spec.Namespace)
	if namespace == "" {
		namespace = name
		realm.Spec.Namespace = namespace
	}

	getResult, err := b.GetRealm(realm)
	if err != nil && !errors.Is(err, errdefs.ErrRealmNotFound) {
		return result, err
	}

	// Determine which realm to use: from metadata if available, otherwise use provided realm
	var internalRealm intmodel.Realm
	metadataExists := err == nil && getResult.MetadataExists
	if metadataExists {
		internalRealm = getResult.Realm
		// Ensure namespace is set (use from metadata if available, otherwise use default)
		if internalRealm.Spec.Namespace == "" {
			internalRealm.Spec.Namespace = namespace
		}
	} else {
		// Metadata doesn't exist - construct realm from input with default namespace
		internalRealm = intmodel.Realm{
			Metadata: intmodel.RealmMetadata{
				Name:   name,
				Labels: realm.Metadata.Labels,
			},
			Spec: intmodel.RealmSpec{
				Namespace: namespace,
			},
			Status: intmodel.RealmStatus{
				State: intmodel.RealmStateUnknown,
			},
		}
	}

	// Initialize result with realm and flags
	result = PurgeRealmResult{
		Realm:   internalRealm,
		Force:   force,
		Cascade: cascade,
		Deleted: []string{},
		Purged:  []string{},
	}

	// Track deleted spaces for result building (list before deletion to know what will be deleted)
	if cascade && metadataExists {
		spaces, listErr := b.runner.ListSpaces(name)
		if listErr != nil {
			return result, fmt.Errorf("failed to list spaces: %w", listErr)
		}
		// Track spaces that will be deleted
		for _, space := range spaces {
			result.Deleted = append(result.Deleted, fmt.Sprintf("space:%s", space.Metadata.Name))
		}
	}

	// Call private cascade method (handles cascade deletion, standard delete, and comprehensive purge)
	if err = b.purgeRealmCascade(internalRealm, force, cascade, metadataExists); err != nil {
		result.Purged = append(result.Purged, fmt.Sprintf("purge-error:%v", err))
		result.PurgeSucceeded = false
		// If metadata exists and delete failed, mark as not deleted
		if metadataExists {
			result.RealmDeleted = false
		}
		return result, err
	}

	// Since private method succeeded, assume all operations succeeded
	if metadataExists {
		result.RealmDeleted = true
		result.Deleted = append(result.Deleted, "metadata", "cgroup", "namespace")
	} else {
		result.RealmDeleted = false
	}
	result.Purged = append(result.Purged, "orphaned-containers", "cni-resources", "all-metadata")
	result.PurgeSucceeded = true

	return result, nil
}

// purgeRealmCascade handles cascade deletion and purging logic using runner methods directly.
// It returns an error if deletion/purging fails, but does not return result types.
// metadataExists indicates whether realm metadata exists (affects cascade and delete operations).
func (b *Exec) purgeRealmCascade(realm intmodel.Realm, force, cascade, metadataExists bool) error {
	realmName := strings.TrimSpace(realm.Metadata.Name)

	// If cascade is true, list and purge child resources (spaces) recursively (only if metadata exists)
	if cascade && metadataExists {
		spaces, err := b.runner.ListSpaces(realmName)
		if err != nil {
			return fmt.Errorf("failed to list spaces: %w", err)
		}
		for _, space := range spaces {
			if err = b.purgeSpaceCascade(space, force, cascade); err != nil {
				return fmt.Errorf("failed to purge space %q: %w", space.Metadata.Name, err)
			}
		}
	} else if !force && metadataExists {
		// Validate no child resources exist (only if metadata exists)
		spaces, err := b.runner.ListSpaces(realmName)
		if err != nil {
			return fmt.Errorf("failed to list spaces: %w", err)
		}
		if len(spaces) > 0 {
			return fmt.Errorf("%w: realm %q has %d space(s). Use --cascade to purge them or --force to skip validation",
				errdefs.ErrResourceHasDependencies, realmName, len(spaces))
		}
	}

	// Perform standard delete first (only if metadata exists, as deleteRealmCascade requires metadata)
	if metadataExists {
		if err := b.deleteRealmCascade(realm, force, cascade); err != nil {
			return fmt.Errorf("failed to delete realm: %w", err)
		}
	}

	// Perform comprehensive purge via runner (works even without metadata)
	return b.runner.PurgeRealm(realm)
}
