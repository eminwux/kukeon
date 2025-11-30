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

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// CreateSpaceResult reports reconciliation outcomes for a space.
type CreateSpaceResult struct {
	Space intmodel.Space

	MetadataExistsPre    bool
	MetadataExistsPost   bool
	CgroupExistsPre      bool
	CgroupExistsPost     bool
	CgroupCreated        bool
	CNINetworkExistsPre  bool
	CNINetworkExistsPost bool
	CNINetworkCreated    bool
	Created              bool
}

// CreateSpace creates a new space or ensures an existing space's resources exist.
// It returns a CreateSpaceResult and an error.
// The CreateSpaceResult reports the state of space resources before and after the operation,
// indicating what was created vs what already existed.
// The error is returned if the space name is required, the realm name is required,
// the space cgroup does not exist, the cni network does not exist, or the space creation fails.
func (b *Exec) CreateSpace(space intmodel.Space) (CreateSpaceResult, error) {
	defer b.runner.Close()
	var res CreateSpaceResult

	name := strings.TrimSpace(space.Metadata.Name)
	if name == "" {
		return res, errdefs.ErrSpaceNameRequired
	}
	realm := strings.TrimSpace(space.Spec.RealmName)
	if realm == "" {
		return res, errdefs.ErrRealmNameRequired
	}

	// Ensure default labels are set
	if space.Metadata.Labels == nil {
		space.Metadata.Labels = make(map[string]string)
	}
	if _, exists := space.Metadata.Labels[consts.KukeonRealmLabelKey]; !exists {
		space.Metadata.Labels[consts.KukeonRealmLabelKey] = realm
	}
	if _, exists := space.Metadata.Labels[consts.KukeonSpaceLabelKey]; !exists {
		space.Metadata.Labels[consts.KukeonSpaceLabelKey] = name
	}

	// Build minimal internal space for GetSpace lookup
	lookupSpace := intmodel.Space{
		Metadata: intmodel.SpaceMetadata{
			Name: name,
		},
		Spec: intmodel.SpaceSpec{
			RealmName: realm,
		},
	}

	// Check if space already exists
	internalSpacePre, err := b.runner.GetSpace(lookupSpace)
	var resultSpace intmodel.Space
	var wasCreated bool

	if err != nil {
		// Space not found, create new space
		if errors.Is(err, errdefs.ErrSpaceNotFound) {
			res.MetadataExistsPre = false
		} else {
			return res, fmt.Errorf("%w: %w", errdefs.ErrGetSpace, err)
		}

		// Create new space
		resultSpace, err = b.runner.CreateSpace(space)
		if err != nil {
			return res, fmt.Errorf("%w: %w", errdefs.ErrCreateSpace, err)
		}

		wasCreated = true
	} else {
		// Space found, check pre-state for result reporting (EnsureSpace will also check internally)
		res.MetadataExistsPre = true
		res.CgroupExistsPre, err = b.runner.ExistsCgroup(internalSpacePre)
		if err != nil {
			return res, fmt.Errorf("failed to check if space cgroup exists: %w", err)
		}
		res.CNINetworkExistsPre, err = b.runner.ExistsSpaceCNIConfig(lookupSpace)
		if err != nil {
			return res, fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, err)
		}

		// Ensure resources exist (EnsureSpace checks/ensures internally)
		resultSpace, err = b.runner.EnsureSpace(internalSpacePre)
		if err != nil {
			return res, fmt.Errorf("%w: %w", errdefs.ErrCreateSpace, err)
		}

		wasCreated = false
	}

	// Set result fields
	res.Space = resultSpace
	res.MetadataExistsPost = true
	// After CreateSpace/EnsureSpace, both CNI network and cgroup are guaranteed to exist
	res.CNINetworkExistsPost = true
	res.CgroupExistsPost = true
	res.Created = wasCreated
	if wasCreated {
		// New space: all resources were created
		res.CNINetworkCreated = true
		res.CgroupCreated = true
	} else {
		// Existing space: resources were created only if they didn't exist before
		res.CNINetworkCreated = !res.CNINetworkExistsPre
		res.CgroupCreated = !res.CgroupExistsPre
	}

	return res, nil
}
