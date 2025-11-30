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

// CreateRealmResult reports the reconciliation outcomes for a realm.
type CreateRealmResult struct {
	Realm intmodel.Realm

	MetadataExistsPre             bool
	MetadataExistsPost            bool
	CgroupExistsPre               bool
	CgroupExistsPost              bool
	CgroupCreated                 bool
	ContainerdNamespaceExistsPre  bool
	ContainerdNamespaceExistsPost bool
	ContainerdNamespaceCreated    bool
	Created                       bool
}

// CreateRealm creates a new realm or ensures an existing realm's resources exist.
// It returns a CreateRealmResult and an error.
// The CreateRealmResult reports the state of realm resources before and after the operation,
// indicating what was created vs what already existed.
// The error is returned if the realm name is required, the namespace is required,
// the realm cgroup does not exist, the containerd namespace does not exist, or the realm creation fails.
func (b *Exec) CreateRealm(realm intmodel.Realm) (CreateRealmResult, error) {
	defer b.runner.Close()
	var res CreateRealmResult

	name := strings.TrimSpace(realm.Metadata.Name)
	if name == "" {
		return res, errdefs.ErrRealmNameRequired
	}
	namespace := strings.TrimSpace(realm.Spec.Namespace)
	if namespace == "" {
		namespace = name
		// Update realm with default namespace
		realm.Spec.Namespace = namespace
	}

	// Ensure default labels are set
	if realm.Metadata.Labels == nil {
		realm.Metadata.Labels = make(map[string]string)
	}
	if _, exists := realm.Metadata.Labels[consts.KukeonRealmLabelKey]; !exists {
		realm.Metadata.Labels[consts.KukeonRealmLabelKey] = namespace
	}

	// Check if realm already exists
	internalRealmPre, err := b.runner.GetRealm(realm)
	var resultRealm intmodel.Realm
	var wasCreated bool

	if err != nil {
		// Realm not found, create new realm
		if errors.Is(err, errdefs.ErrRealmNotFound) {
			res.MetadataExistsPre = false
		} else {
			return res, fmt.Errorf("%w: %w", errdefs.ErrGetRealm, err)
		}

		// Create new realm
		resultRealm, err = b.runner.CreateRealm(realm)
		if err != nil && !errors.Is(err, errdefs.ErrNamespaceAlreadyExists) {
			return res, fmt.Errorf("%w: %w", errdefs.ErrCreateRealm, err)
		}

		wasCreated = true
	} else {
		// Realm found, check pre-state for result reporting (EnsureRealm will also check internally)
		res.MetadataExistsPre = true
		res.CgroupExistsPre, err = b.runner.ExistsCgroup(internalRealmPre)
		if err != nil {
			return res, fmt.Errorf("failed to check if realm cgroup exists: %w", err)
		}
		res.ContainerdNamespaceExistsPre, err = b.runner.ExistsRealmContainerdNamespace(namespace)
		if err != nil {
			return res, fmt.Errorf("%w: %w", errdefs.ErrCheckNamespaceExists, err)
		}

		// Ensure resources exist and reconcile state (EnsureRealm checks/ensures internally)
		resultRealm, err = b.runner.EnsureRealm(internalRealmPre)
		if err != nil {
			return res, fmt.Errorf("%w: %w", errdefs.ErrCreateRealm, err)
		}

		wasCreated = false
	}

	// Set result fields
	res.Realm = resultRealm
	res.MetadataExistsPost = true
	// After CreateRealm/EnsureRealm, both namespace and cgroup are guaranteed to exist
	res.CgroupExistsPost = true
	res.ContainerdNamespaceExistsPost = true
	res.Created = wasCreated
	if wasCreated {
		// New realm: all resources were created
		res.CgroupCreated = true
		res.ContainerdNamespaceCreated = true
	} else {
		// Existing realm: resources were created only if they didn't exist before
		res.CgroupCreated = !res.CgroupExistsPre
		res.ContainerdNamespaceCreated = !res.ContainerdNamespaceExistsPre
	}

	return res, nil
}
