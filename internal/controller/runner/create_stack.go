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
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/naming"
)

func (r *Exec) CreateStack(stack intmodel.Stack) (intmodel.Stack, error) {
	if err := r.ensureClientConnected(); err != nil {
		return intmodel.Stack{}, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}

	trimmedName := strings.TrimSpace(stack.Metadata.Name)
	if err := naming.ValidateHierarchyName("stack", trimmedName); err != nil {
		return intmodel.Stack{}, err
	}
	stack.Metadata.Name = trimmedName

	// Get existing stack (returns internal model)
	existingStack, err := r.GetStack(stack)
	if err != nil && !errors.Is(err, errdefs.ErrStackNotFound) {
		return intmodel.Stack{}, fmt.Errorf("%w: %w", errdefs.ErrGetStack, err)
	}

	// Stack found, ensure resources exist
	if err == nil {
		ensuredStack, ensureErr := r.EnsureStack(existingStack)
		if ensureErr != nil {
			return intmodel.Stack{}, ensureErr
		}

		return ensuredStack, nil
	}

	// Stack not found: the parent realm and space must exist before any
	// side effect. provisionNewStack persists the stack document and its
	// cgroup first, so without this preflight a missing parent leaves a
	// Ready orphan behind (#1373).
	if parentErr := r.requireParentSpace(stack.Spec.RealmName, stack.Spec.SpaceName); parentErr != nil {
		return intmodel.Stack{}, parentErr
	}

	// Stack not found, create new stack
	resultStack, err := r.provisionNewStack(stack)
	if err != nil {
		return intmodel.Stack{}, err
	}

	return resultStack, nil
}

// EnsureStack ensures that all required resources for a stack exist.
// It ensures the cgroup exists.
func (r *Exec) EnsureStack(stack intmodel.Stack) (intmodel.Stack, error) {
	// Ensure cgroup exists
	ensuredStack, ensureErr := r.ensureStackCgroup(stack)
	if ensureErr != nil {
		return intmodel.Stack{}, ensureErr
	}

	return ensuredStack, nil
}

// requireParentSpace returns a wrapped ErrRealmNotFound / ErrSpaceNotFound
// when the stack's parent realm or space has no metadata, so first-time stack
// provisioning fails before persisting anything beneath them.
func (r *Exec) requireParentSpace(realmName, spaceName string) error {
	realmName = strings.TrimSpace(realmName)
	spaceName = strings.TrimSpace(spaceName)
	if realmName == "" {
		return errdefs.ErrRealmNameRequired
	}
	if spaceName == "" {
		return errdefs.ErrSpaceNameRequired
	}
	if err := r.requireParentRealm(realmName); err != nil {
		return err
	}
	_, err := r.GetSpace(intmodel.Space{
		Metadata: intmodel.SpaceMetadata{Name: spaceName},
		Spec:     intmodel.SpaceSpec{RealmName: realmName},
	})
	if err != nil {
		if errors.Is(err, errdefs.ErrSpaceNotFound) {
			return fmt.Errorf("parent space %q in realm %q: %w", spaceName, realmName, err)
		}
		return fmt.Errorf("%w: %w", errdefs.ErrGetSpace, err)
	}
	return nil
}
