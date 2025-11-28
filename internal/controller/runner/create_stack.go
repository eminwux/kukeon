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

func (r *Exec) CreateStack(stack intmodel.Stack) (intmodel.Stack, error) {
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}

	// Get existing stack (returns internal model)
	existingStack, err := r.GetStack(stack)
	if err != nil && !errors.Is(err, errdefs.ErrStackNotFound) {
		return intmodel.Stack{}, fmt.Errorf("%w: %w", errdefs.ErrGetStack, err)
	}

	// Stack found, ensure cgroup exists
	if err == nil {
		ensuredStack, ensureErr := r.ensureStackCgroup(existingStack)
		if ensureErr != nil {
			return intmodel.Stack{}, ensureErr
		}

		return ensuredStack, nil
	}

	// Stack not found, create new stack
	resultStack, err := r.provisionNewStack(stack)
	if err != nil {
		return intmodel.Stack{}, err
	}

	return resultStack, nil
}
