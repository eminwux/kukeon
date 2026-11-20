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
// It only updates fields that are backward-compatible (labels, registry credentials, CellConfig defaults, images).
// Breaking changes (name, namespace) should be rejected before calling this method.
func (r *Exec) UpdateRealm(desired intmodel.Realm) (intmodel.Realm, error) {
	// Get existing realm
	existing, err := r.GetRealm(desired)
	if err != nil {
		return intmodel.Realm{}, fmt.Errorf("%w: %w", errdefs.ErrGetRealm, err)
	}

	// Update compatible fields. Preserve controller-managed `*.kukeon.io`
	// canonical labels when the user's desired doc omits them (issue #455).
	existing.Metadata.Labels = mergeManagedLabels(existing.Metadata.Labels, desired.Metadata.Labels)
	existing.Spec.RegistryCredentials = keepInlinePasswords(
		desired.Spec.RegistryCredentials, existing.Spec.RegistryCredentials)
	existing.Spec.Defaults = desired.Spec.Defaults
	existing.Spec.Images = desired.Spec.Images

	// Update metadata file
	if updateErr := r.UpdateRealmMetadata(existing); updateErr != nil {
		return intmodel.Realm{}, fmt.Errorf("%w: %w", errdefs.ErrUpdateRealmMetadata, updateErr)
	}

	return existing, nil
}

// keepInlinePasswords carries the stored password of an inline registry entry
// over to a desired entry that names the same server and username without one.
// A Realm read through `kuke get realm` or the web UI never carries a registry
// password (issue #1548), so a document edited and applied back would
// otherwise blank every inline credential it did not mean to touch.
func keepInlinePasswords(desired, existing []intmodel.RegistryCredentials) []intmodel.RegistryCredentials {
	if len(desired) == 0 {
		return desired
	}
	out := make([]intmodel.RegistryCredentials, len(desired))
	copy(out, desired)
	for i := range out {
		if out[i].SecretRef != nil || out[i].Password != "" {
			continue
		}
		for _, stored := range existing {
			if stored.SecretRef == nil && stored.Password != "" &&
				stored.ServerAddress == out[i].ServerAddress && stored.Username == out[i].Username {
				out[i].Password = stored.Password
				break
			}
		}
	}
	return out
}
