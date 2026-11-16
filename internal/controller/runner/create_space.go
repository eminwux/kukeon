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

package runner

import (
	"errors"
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/naming"
)

func (r *Exec) CreateSpace(space intmodel.Space) (intmodel.Space, error) {
	if err := r.ensureClientConnected(); err != nil {
		return intmodel.Space{}, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}

	trimmedName := strings.TrimSpace(space.Metadata.Name)
	if err := naming.ValidateHierarchyName("space", trimmedName); err != nil {
		return intmodel.Space{}, err
	}
	space.Metadata.Name = trimmedName

	// Get existing space (returns internal model)
	existingSpace, err := r.GetSpace(space)
	if err != nil && !errors.Is(err, errdefs.ErrSpaceNotFound) {
		return intmodel.Space{}, fmt.Errorf("%w: %w", errdefs.ErrGetSpace, err)
	}

	realmName := space.Spec.RealmName
	if err == nil {
		if existingSpace.Spec.RealmName != "" {
			realmName = existingSpace.Spec.RealmName
		}
	}
	if realmName == "" {
		return intmodel.Space{}, errdefs.ErrRealmNameRequired
	}

	// Space found, ensure resources exist
	if err == nil {
		ensuredSpace, ensureErr := r.EnsureSpace(existingSpace)
		if ensureErr != nil {
			return intmodel.Space{}, ensureErr
		}

		return ensuredSpace, nil
	}

	// Space not found: the parent realm must exist before any side effect.
	// provisionNewSpace persists the space document first, so without this
	// preflight a missing realm leaves a discoverable orphan behind (#1374).
	if parentErr := r.requireParentRealm(realmName); parentErr != nil {
		return intmodel.Space{}, parentErr
	}

	// Space not found, create new space
	space.Status.State = intmodel.SpaceStateCreating
	resultSpace, err := r.provisionNewSpace(space)
	if err != nil {
		return intmodel.Space{}, err
	}

	return resultSpace, nil
}

// EnsureSpace ensures that all required resources for a space exist.
// It ensures the CNI config and cgroup exist.
func (r *Exec) EnsureSpace(space intmodel.Space) (intmodel.Space, error) {
	// Re-read under the space lock: a concurrent DeleteSpace may have removed
	// the space since the caller looked it up, and ensuring it now would
	// re-create its egress chain with nothing left to remove it. A space
	// found gone here drops its lock file, as DeleteSpace does, so waking
	// behind a delete does not re-create and leak it.
	release := r.lockSpaceRemovable(space.Spec.RealmName, space.Metadata.Name)
	spaceGone := false
	defer func() { release(spaceGone) }()
	if _, err := r.GetSpace(space); err != nil {
		spaceGone = errors.Is(err, errdefs.ErrSpaceNotFound)
		return intmodel.Space{}, err
	}

	// Ensure CNI config exists
	ensuredSpace, ensureErr := r.ensureSpaceCNIConfig(space)
	if ensureErr != nil {
		return intmodel.Space{}, ensureErr
	}

	// Ensure cgroup exists
	ensuredSpace, ensureErr = r.ensureSpaceCgroup(ensuredSpace)
	if ensureErr != nil {
		return intmodel.Space{}, ensureErr
	}

	return ensuredSpace, nil
}

// requireParentRealm returns a wrapped ErrRealmNotFound when the named realm
// has no metadata, so first-time child provisioning fails before persisting
// anything beneath it.
func (r *Exec) requireParentRealm(realmName string) error {
	if _, err := r.GetRealm(intmodel.Realm{Metadata: intmodel.RealmMetadata{Name: realmName}}); err != nil {
		if errors.Is(err, errdefs.ErrRealmNotFound) {
			return fmt.Errorf("parent realm %q: %w", realmName, err)
		}
		return fmt.Errorf("%w: %w", errdefs.ErrGetRealm, err)
	}
	return nil
}
