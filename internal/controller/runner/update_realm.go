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
	"fmt"

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// UpdateRealm updates an existing realm with new metadata and compatible spec fields.
// It only updates fields that are backward-compatible (labels, registry credentials).
// Breaking changes (name, namespace) should be rejected before calling this method.
func (r *Exec) UpdateRealm(desired intmodel.Realm) (intmodel.Realm, error) {
	// Get existing realm
	existing, err := r.GetRealm(desired)
	if err != nil {
		return intmodel.Realm{}, fmt.Errorf("%w: %w", errdefs.ErrGetRealm, err)
	}

	// Update compatible fields
	existing.Metadata.Labels = desired.Metadata.Labels
	existing.Spec.RegistryCredentials = desired.Spec.RegistryCredentials

	// Update metadata file
	if updateErr := r.UpdateRealmMetadata(existing); updateErr != nil {
		return intmodel.Realm{}, fmt.Errorf("%w: %w", errdefs.ErrUpdateRealmMetadata, updateErr)
	}

	return existing, nil
}
