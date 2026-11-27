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

// UpdateSpace updates an existing space with the backward-compatible fields of
// desired: labels, spec.defaults and spec.network. spec.defaults and
// spec.network are replaced, so a desired document without spec.network
// clears the space's egress policy (the space becomes unconstrained). The
// egress policy is installed on the host firewall before the space is
// persisted (#1703), the order CreateSpace uses: an invalid rule fails the
// update without being stored, so the reconcile loop never re-asserts a policy
// it cannot install.
// Breaking changes (name, realm association, CNI config path) should be rejected before calling this method.
func (r *Exec) UpdateSpace(desired intmodel.Space) (intmodel.Space, error) {
	// Get existing space
	existing, err := r.GetSpace(desired)
	if err != nil {
		return intmodel.Space{}, fmt.Errorf("%w: %w", errdefs.ErrGetSpace, err)
	}

	// Update compatible fields. Preserve controller-managed `*.kukeon.io`
	// canonical labels when the user's desired doc omits them (issue #455).
	existing.Metadata.Labels = mergeManagedLabels(existing.Metadata.Labels, desired.Metadata.Labels)
	existing.Spec.Defaults = desired.Spec.Defaults
	existing.Spec.Network = desired.Spec.Network
	// Note: CNIConfigPath is not updated as it's a breaking change

	// Rebuild the space's egress chain from the new policy. The enforcer
	// flushes and refills the chain, so removed rules and an allow→deny flip
	// take effect here, not on a later reconcile tick.
	if policyErr := r.applySpaceEgressPolicy(existing); policyErr != nil {
		return intmodel.Space{}, policyErr
	}

	// Update metadata file
	if updateErr := r.UpdateSpaceMetadata(existing); updateErr != nil {
		return intmodel.Space{}, fmt.Errorf("%w: %w", errdefs.ErrUpdateSpaceMetadata, updateErr)
	}

	return existing, nil
}
