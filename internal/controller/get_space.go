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

// GetSpaceResult reports the current state of a space.
type GetSpaceResult struct {
	Space            intmodel.Space
	MetadataExists   bool
	CgroupExists     bool
	CNINetworkExists bool
}

// GetSpace retrieves a single space and reports its current state.
func (b *Exec) GetSpace(space intmodel.Space) (GetSpaceResult, error) {
	defer b.runner.Close()
	var res GetSpaceResult

	name := strings.TrimSpace(space.Metadata.Name)
	if name == "" {
		return res, errdefs.ErrSpaceNameRequired
	}
	realmName := strings.TrimSpace(space.Spec.RealmName)
	if realmName == "" {
		return res, errdefs.ErrRealmNameRequired
	}

	// Build lookup space for runner
	lookupSpace := intmodel.Space{
		Metadata: intmodel.SpaceMetadata{
			Name: name,
		},
		Spec: intmodel.SpaceSpec{
			RealmName: realmName,
		},
	}

	// Call runner with internal type
	internalSpace, err := b.runner.GetSpace(lookupSpace)
	if err != nil {
		if errors.Is(err, errdefs.ErrSpaceNotFound) {
			res.MetadataExists = false
		} else {
			return res, fmt.Errorf("%w: %w", errdefs.ErrGetSpace, err)
		}
	} else {
		res.MetadataExists = true

		res.CgroupExists, err = b.runner.ExistsCgroup(internalSpace)
		if err != nil {
			return res, fmt.Errorf("failed to check if space cgroup exists: %w", err)
		}
		res.Space = internalSpace
	}

	res.CNINetworkExists, err = b.runner.ExistsSpaceCNIConfig(lookupSpace)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, err)
	}

	return res, nil
}

// ListSpaces lists all spaces, optionally filtered by realm.
func (b *Exec) ListSpaces(realmName string) ([]intmodel.Space, error) {
	defer b.runner.Close()
	return b.runner.ListSpaces(realmName)
}
