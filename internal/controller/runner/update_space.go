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

// UpdateSpace updates an existing space with new metadata and compatible spec fields.
// It only updates fields that are backward-compatible (labels).
// Breaking changes (name, realm association, CNI config path) should be rejected before calling this method.
func (r *Exec) UpdateSpace(desired intmodel.Space) (intmodel.Space, error) {
	// Get existing space
	existing, err := r.GetSpace(desired)
	if err != nil {
		return intmodel.Space{}, fmt.Errorf("%w: %w", errdefs.ErrGetSpace, err)
	}

	// Update compatible fields
	existing.Metadata.Labels = desired.Metadata.Labels
	// Note: CNIConfigPath is not updated as it's a breaking change

	// Update metadata file
	if updateErr := r.UpdateSpaceMetadata(existing); updateErr != nil {
		return intmodel.Space{}, fmt.Errorf("%w: %w", errdefs.ErrUpdateSpaceMetadata, updateErr)
	}

	return existing, nil
}
