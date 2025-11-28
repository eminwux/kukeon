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

	// Build lookup realm for GetRealm
	lookupRealm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: name,
		},
	}
	getResult, err := b.GetRealm(lookupRealm)
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

	// If cascade, purge all spaces first (only if metadata exists, as ListSpaces requires metadata)
	if cascade && metadataExists {
		var spaces []intmodel.Space
		spaces, err = b.ListSpaces(name)
		if err != nil {
			return result, fmt.Errorf("failed to list spaces: %w", err)
		}
		for _, spaceInternal := range spaces {
			_, err = b.PurgeSpace(spaceInternal, force, cascade)
			if err != nil {
				return result, fmt.Errorf("failed to purge space %q: %w", spaceInternal.Metadata.Name, err)
			}
			result.Deleted = append(result.Deleted, fmt.Sprintf("space:%s", spaceInternal.Metadata.Name))
		}
	} else if !force && metadataExists {
		// Validate no spaces exist (only if metadata exists)
		var spaces []intmodel.Space
		spaces, err = b.ListSpaces(name)
		if err != nil {
			return result, fmt.Errorf("failed to list spaces: %w", err)
		}
		if len(spaces) > 0 {
			return result, fmt.Errorf("%w: realm %q has %d space(s). Use --cascade to purge them or --force to skip validation", errdefs.ErrResourceHasDependencies, name, len(spaces))
		}
	}

	// Perform standard delete first (only if metadata exists, as DeleteRealm requires metadata)
	if metadataExists {
		var deleteResult DeleteRealmResult
		deleteResult, err = b.DeleteRealm(internalRealm, force, cascade)
		if err != nil {
			// Log but continue with purge
			result.Purged = append(result.Purged, fmt.Sprintf("delete-warning:%v", err))
			result.RealmDeleted = false
		} else {
			result.Deleted = append(result.Deleted, deleteResult.Deleted...)
			result.RealmDeleted = true
			// Update result realm with deleted realm
			result.Realm = deleteResult.Realm
		}
	} else {
		// Metadata doesn't exist - skip DeleteRealm and mark as not deleted via standard delete
		result.RealmDeleted = false
	}

	// Perform comprehensive purge
	if err = b.runner.PurgeRealm(internalRealm); err != nil {
		result.Purged = append(result.Purged, fmt.Sprintf("purge-error:%v", err))
		result.PurgeSucceeded = false
	} else {
		result.Purged = append(result.Purged, "orphaned-containers", "cni-resources", "all-metadata")
		result.PurgeSucceeded = true
	}

	return result, nil
}
