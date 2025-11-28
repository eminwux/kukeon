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

	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

func (r *Exec) CreateSpace(space intmodel.Space) (intmodel.Space, error) {
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}

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

	// Space found, ensure CNI config exists
	if err == nil {
		ensuredSpace, ensureErr := r.ensureSpaceCNIConfig(existingSpace)
		if ensureErr != nil {
			return intmodel.Space{}, ensureErr
		}

		ensuredSpace, ensureErr = r.ensureSpaceCgroup(ensuredSpace)
		if ensureErr != nil {
			return intmodel.Space{}, ensureErr
		}

		return ensuredSpace, nil
	}

	// Space not found, create new space
	space.Status.State = intmodel.SpaceStateCreating
	resultSpace, err := r.provisionNewSpace(space)
	if err != nil {
		return intmodel.Space{}, err
	}

	return resultSpace, nil
}
