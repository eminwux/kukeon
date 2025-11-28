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

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

func (r *Exec) CreateRealm(realm intmodel.Realm) (intmodel.Realm, error) {
	r.logger.Debug("run-path", "run-path", r.opts.RunPath)

	// Get existing realm (returns internal model)
	existingRealm, err := r.GetRealm(realm)
	if err != nil && !errors.Is(err, errdefs.ErrRealmNotFound) {
		return intmodel.Realm{}, fmt.Errorf("%w: %w", errdefs.ErrGetRealm, err)
	}

	if err != nil {
		// Realm not found, create new realm
		realm.Status.State = intmodel.RealmStateCreating
		var resultRealm intmodel.Realm
		resultRealm, err = r.provisionNewRealm(realm)
		if err != nil {
			return intmodel.Realm{}, err
		}

		return resultRealm, nil
	}

	// Realm found, ensure resources exist and reconcile state
	ensuredRealm, ensureErr := r.EnsureRealm(existingRealm)
	if ensureErr != nil {
		return intmodel.Realm{}, ensureErr
	}

	return ensuredRealm, nil
}

// EnsureRealm ensures that all required resources for a realm exist and reconciles its state.
// It ensures the containerd namespace and cgroup exist, and transitions the realm from
// "Creating" to "Ready" state if all resources are present.
func (r *Exec) EnsureRealm(realm intmodel.Realm) (intmodel.Realm, error) {
	// Ensure containerd namespace exists
	ensuredRealm, ensureErr := r.ensureRealmContainerdNamespace(realm)
	if ensureErr != nil {
		return intmodel.Realm{}, ensureErr
	}

	// Ensure cgroup exists
	ensuredRealm, ensureErr = r.ensureRealmCgroup(ensuredRealm)
	if ensureErr != nil {
		return intmodel.Realm{}, ensureErr
	}

	// Reconcile state: if realm is in "Creating" but resources exist, transition to "Ready"
	if ensuredRealm.Status.State == intmodel.RealmStateCreating {
		// Verify both namespace and cgroup exist
		namespaceExists, nsErr := r.ExistsRealmContainerdNamespace(ensuredRealm.Spec.Namespace)
		if nsErr != nil {
			return intmodel.Realm{}, fmt.Errorf("failed to check namespace existence: %w", nsErr)
		}
		cgroupExists, cgErr := r.ExistsCgroup(ensuredRealm)
		if cgErr != nil {
			return intmodel.Realm{}, fmt.Errorf("failed to check cgroup existence: %w", cgErr)
		}

		if namespaceExists && cgroupExists {
			// Resources exist, transition to Ready
			ensuredRealm.Status.State = intmodel.RealmStateReady
			if updateErr := r.UpdateRealmMetadata(ensuredRealm); updateErr != nil {
				return intmodel.Realm{}, fmt.Errorf("%w: %w", errdefs.ErrUpdateRealmMetadata, updateErr)
			}
			r.logger.InfoContext(
				r.ctx,
				"reconciled realm state from Creating to Ready",
				"realm", ensuredRealm.Metadata.Name,
			)
		}
	}

	return ensuredRealm, nil
}
