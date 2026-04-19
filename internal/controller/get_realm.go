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

// GetRealmResult reports the current state of a realm.
type GetRealmResult struct {
	Realm                     intmodel.Realm
	MetadataExists            bool
	CgroupExists              bool
	ContainerdNamespaceExists bool
}

// GetRealm retrieves a single realm and reports its current state.
func (b *Exec) GetRealm(realm intmodel.Realm) (GetRealmResult, error) {
	var res GetRealmResult

	name := strings.TrimSpace(realm.Metadata.Name)
	if name == "" {
		return res, errdefs.ErrRealmNameRequired
	}

	namespace := strings.TrimSpace(realm.Spec.Namespace)
	if namespace == "" {
		namespace = name
	}

	// Build lookup realm for runner
	lookupRealm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: name,
		},
	}

	// Call runner with internal type
	internalRealm, err := b.runner.GetRealm(lookupRealm)
	if err != nil {
		if errors.Is(err, errdefs.ErrRealmNotFound) {
			res.MetadataExists = false
		} else {
			return res, fmt.Errorf("%w: %w", errdefs.ErrGetRealm, err)
		}
	} else {
		res.MetadataExists = true

		res.CgroupExists, err = b.runner.ExistsCgroup(internalRealm)
		if err != nil {
			return res, fmt.Errorf("failed to check if realm cgroup exists: %w", err)
		}
		res.Realm = internalRealm
	}

	res.ContainerdNamespaceExists, err = b.runner.ExistsRealmContainerdNamespace(namespace)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrCheckNamespaceExists, err)
	}

	return res, nil
}

// ListRealms lists all realms.
func (b *Exec) ListRealms() ([]intmodel.Realm, error) {
	return b.runner.ListRealms()
}
