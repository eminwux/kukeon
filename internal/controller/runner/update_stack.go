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

// UpdateStack updates an existing stack with new metadata and compatible spec fields.
// It only updates fields that are backward-compatible (labels, ID).
// Breaking changes (name, realm/space association) should be rejected before calling this method.
func (r *Exec) UpdateStack(desired intmodel.Stack) (intmodel.Stack, error) {
	// Get existing stack
	existing, err := r.GetStack(desired)
	if err != nil {
		return intmodel.Stack{}, fmt.Errorf("%w: %w", errdefs.ErrGetStack, err)
	}

	// Update compatible fields
	existing.Metadata.Labels = desired.Metadata.Labels
	if desired.Spec.ID != "" {
		existing.Spec.ID = desired.Spec.ID
	}

	// Update metadata file
	if updateErr := r.UpdateStackMetadata(existing); updateErr != nil {
		return intmodel.Stack{}, fmt.Errorf("%w: %w", errdefs.ErrUpdateStackMetadata, updateErr)
	}

	return existing, nil
}
