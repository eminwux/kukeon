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

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// GetStackResult reports the current state of a stack.
type GetStackResult struct {
	Stack          intmodel.Stack
	MetadataExists bool
	CgroupExists   bool
}

// GetStack retrieves a single stack and reports its current state.
func (b *Exec) GetStack(stack intmodel.Stack) (GetStackResult, error) {
	var res GetStackResult

	name := strings.TrimSpace(stack.Metadata.Name)
	if name == "" {
		return res, errdefs.ErrStackNameRequired
	}
	realmName := strings.TrimSpace(stack.Spec.RealmName)
	if realmName == "" {
		return res, errdefs.ErrRealmNameRequired
	}
	spaceName := strings.TrimSpace(stack.Spec.SpaceName)
	if spaceName == "" {
		return res, errdefs.ErrSpaceNameRequired
	}

	// Build lookup stack for runner
	lookupStack := intmodel.Stack{
		Metadata: intmodel.StackMetadata{
			Name: name,
		},
		Spec: intmodel.StackSpec{
			RealmName: realmName,
			SpaceName: spaceName,
		},
	}

	internalStack, err := b.runner.GetStack(lookupStack)
	if err != nil {
		if errors.Is(err, errdefs.ErrStackNotFound) {
			res.MetadataExists = false
		} else {
			return res, fmt.Errorf("%w: %w", errdefs.ErrGetStack, err)
		}
	} else {
		res.MetadataExists = true
		res.CgroupExists, err = b.runner.ExistsCgroup(internalStack)
		if err != nil {
			return res, fmt.Errorf("failed to check if stack cgroup exists: %w", err)
		}
		res.Stack = internalStack
	}

	return res, nil
}

// ListStacks lists all stacks, optionally filtered by realm and/or space.
func (b *Exec) ListStacks(realmName, spaceName string) ([]intmodel.Stack, error) {
	return b.runner.ListStacks(realmName, spaceName)
}
