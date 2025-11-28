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

// DeleteRealm deletes a realm. If cascade is true, deletes all spaces first.
// If force is true, skips validation of child resources.
func (b *Exec) DeleteRealm(realm intmodel.Realm, force, cascade bool) (DeleteRealmResult, error) {
	var res DeleteRealmResult

	name := strings.TrimSpace(realm.Metadata.Name)
	if name == "" {
		return res, errdefs.ErrRealmNameRequired
	}

	// Ensure realm exists and capture its latest state
	lookupRealm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: name,
		},
	}
	getResult, err := b.GetRealm(lookupRealm)
	if err != nil {
		if errors.Is(err, errdefs.ErrRealmNotFound) {
			return res, fmt.Errorf("realm %q not found", name)
		}
		return res, err
	}
	if !getResult.MetadataExists {
		return res, fmt.Errorf("realm %q not found", name)
	}

	res = DeleteRealmResult{
		Realm:   getResult.Realm,
		Deleted: []string{},
	}

	// If cascade, delete all spaces first
	var spaces []intmodel.Space
	if cascade {
		spaces, err = b.ListSpaces(name)
		if err != nil {
			return res, fmt.Errorf("failed to list spaces: %w", err)
		}
		for _, spaceInternal := range spaces {
			_, err = b.DeleteSpace(spaceInternal, force, cascade)
			if err != nil {
				return res, fmt.Errorf("failed to delete space %q: %w", spaceInternal.Metadata.Name, err)
			}
			res.Deleted = append(res.Deleted, fmt.Sprintf("space:%s", spaceInternal.Metadata.Name))
		}
	} else if !force {
		// Validate no spaces exist
		spaces, err = b.ListSpaces(name)
		if err != nil {
			return res, fmt.Errorf("failed to list spaces: %w", err)
		}
		if len(spaces) > 0 {
			return res, fmt.Errorf("%w: realm %q has %d space(s). Use --cascade to delete them or --force to skip validation", errdefs.ErrResourceHasDependencies, name, len(spaces))
		}
	}

	// Delete realm via runner and capture detailed outcome
	outcome, err := b.runner.DeleteRealm(getResult.Realm)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrDeleteRealm, err)
	}

	res.MetadataDeleted = outcome.MetadataDeleted
	res.CgroupDeleted = outcome.CgroupDeleted
	res.ContainerdNamespaceDeleted = outcome.ContainerdNamespaceDeleted

	if outcome.MetadataDeleted {
		res.Deleted = append(res.Deleted, "metadata")
	}
	if outcome.CgroupDeleted {
		res.Deleted = append(res.Deleted, "cgroup")
	}
	if outcome.ContainerdNamespaceDeleted {
		res.Deleted = append(res.Deleted, "namespace")
	}

	return res, nil
}
