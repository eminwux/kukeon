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

// CreateStackResult reports reconciliation outcomes for a stack.
type CreateStackResult struct {
	Stack intmodel.Stack

	MetadataExistsPre  bool
	MetadataExistsPost bool
	CgroupExistsPre    bool
	CgroupExistsPost   bool
	CgroupCreated      bool
	Created            bool
}

// CreateStack creates a new stack or ensures an existing stack's resources exist.
// It returns a CreateStackResult and an error.
// The CreateStackResult reports the state of stack resources before and after the operation,
// indicating what was created vs what already existed.
// The error is returned if the stack name is required, the realm name is required,
// the space name is required, the stack cgroup does not exist, or the stack creation fails.
func (b *Exec) CreateStack(stack intmodel.Stack) (CreateStackResult, error) {
	defer b.runner.Close()
	var res CreateStackResult

	name := strings.TrimSpace(stack.Metadata.Name)
	if name == "" {
		return res, errdefs.ErrStackNameRequired
	}
	realm := strings.TrimSpace(stack.Spec.RealmName)
	if realm == "" {
		return res, errdefs.ErrRealmNameRequired
	}
	space := strings.TrimSpace(stack.Spec.SpaceName)
	if space == "" {
		return res, errdefs.ErrSpaceNameRequired
	}

	// Ensure default labels are set
	if stack.Metadata.Labels == nil {
		stack.Metadata.Labels = make(map[string]string)
	}
	if _, exists := stack.Metadata.Labels[consts.KukeonRealmLabelKey]; !exists {
		stack.Metadata.Labels[consts.KukeonRealmLabelKey] = realm
	}
	if _, exists := stack.Metadata.Labels[consts.KukeonSpaceLabelKey]; !exists {
		stack.Metadata.Labels[consts.KukeonSpaceLabelKey] = space
	}
	if _, exists := stack.Metadata.Labels[consts.KukeonStackLabelKey]; !exists {
		stack.Metadata.Labels[consts.KukeonStackLabelKey] = name
	}

	// Ensure Spec.ID is set
	if stack.Spec.ID == "" {
		stack.Spec.ID = name
	}

	// Build minimal internal stack for GetStack lookup
	lookupStack := intmodel.Stack{
		Metadata: intmodel.StackMetadata{
			Name: name,
		},
		Spec: intmodel.StackSpec{
			RealmName: realm,
			SpaceName: space,
		},
	}

	// Check if stack already exists
	internalStackPre, err := b.runner.GetStack(lookupStack)
	var resultStack intmodel.Stack
	var wasCreated bool

	if err != nil {
		// Stack not found, create new stack
		if errors.Is(err, errdefs.ErrStackNotFound) {
			res.MetadataExistsPre = false
		} else {
			return res, fmt.Errorf("%w: %w", errdefs.ErrGetStack, err)
		}

		// Create new stack
		resultStack, err = b.runner.CreateStack(stack)
		if err != nil {
			return res, fmt.Errorf("%w: %w", errdefs.ErrCreateStack, err)
		}

		wasCreated = true
	} else {
		// Stack found, check pre-state for result reporting (EnsureStack will also check internally)
		res.MetadataExistsPre = true
		// Verify space exists before checking cgroup to provide better error message
		verifySpace := intmodel.Space{
			Metadata: intmodel.SpaceMetadata{
				Name: space,
			},
			Spec: intmodel.SpaceSpec{
				RealmName: realm,
			},
		}
		_, spaceErr := b.runner.GetSpace(verifySpace)
		if spaceErr != nil {
			return res, fmt.Errorf("space %q not found at run-path %q: %w", space, b.opts.RunPath, spaceErr)
		}
		res.CgroupExistsPre, err = b.runner.ExistsCgroup(internalStackPre)
		if err != nil {
			return res, fmt.Errorf("failed to check if stack cgroup exists: %w", err)
		}

		// Ensure resources exist (EnsureStack checks/ensures internally)
		resultStack, err = b.runner.EnsureStack(internalStackPre)
		if err != nil {
			return res, fmt.Errorf("%w: %w", errdefs.ErrCreateStack, err)
		}

		wasCreated = false
	}

	// Set result fields
	res.Stack = resultStack
	res.MetadataExistsPost = true
	// After CreateStack/EnsureStack, cgroup is guaranteed to exist
	res.CgroupExistsPost = true
	res.Created = wasCreated
	if wasCreated {
		// New stack: all resources were created
		res.CgroupCreated = true
	} else {
		// Existing stack: resources were created only if they didn't exist before
		res.CgroupCreated = !res.CgroupExistsPre
	}

	return res, nil
}
