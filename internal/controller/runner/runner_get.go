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
	"github.com/eminwux/kukeon/internal/util/fs"
)

func (r *Exec) GetRealm(realm intmodel.Realm) (intmodel.Realm, error) {
	// Get realm metadata
	metadataFilePath := fs.RealmMetadataPath(r.opts.RunPath, realm.Metadata.Name)
	internalRealm, err := r.readRealmInternal(metadataFilePath)
	if err != nil {
		switch {
		case errors.Is(err, errdefs.ErrMissingMetadataFile):
			return intmodel.Realm{}, errdefs.ErrRealmNotFound
		case errors.Is(err, errdefs.ErrConversionFailed):
			return intmodel.Realm{}, err
		default:
			return intmodel.Realm{}, fmt.Errorf("%w: %w", errdefs.ErrGetRealm, err)
		}
	}

	return internalRealm, nil
}

func (r *Exec) GetSpace(space intmodel.Space) (intmodel.Space, error) {
	realmName := strings.TrimSpace(space.Spec.RealmName)
	if realmName == "" {
		return intmodel.Space{}, errdefs.ErrRealmNameRequired
	}
	spaceName := strings.TrimSpace(space.Metadata.Name)
	if spaceName == "" {
		return intmodel.Space{}, errdefs.ErrSpaceNameRequired
	}
	// Get space metadata
	metadataFilePath := fs.SpaceMetadataPath(r.opts.RunPath, realmName, spaceName)
	internalSpace, err := r.readSpaceInternal(metadataFilePath)
	if err != nil {
		switch {
		case errors.Is(err, errdefs.ErrMissingMetadataFile):
			return intmodel.Space{}, errdefs.ErrSpaceNotFound
		case errors.Is(err, errdefs.ErrConversionFailed):
			return intmodel.Space{}, err
		default:
			return intmodel.Space{}, fmt.Errorf("%w: %w", errdefs.ErrGetSpace, err)
		}
	}

	return internalSpace, nil
}

func (r *Exec) GetStack(stack intmodel.Stack) (intmodel.Stack, error) {
	realmName := strings.TrimSpace(stack.Spec.RealmName)
	if realmName == "" {
		return intmodel.Stack{}, errdefs.ErrRealmNameRequired
	}
	spaceName := strings.TrimSpace(stack.Spec.SpaceName)
	if spaceName == "" {
		return intmodel.Stack{}, errdefs.ErrSpaceNameRequired
	}
	stackName := strings.TrimSpace(stack.Metadata.Name)
	if stackName == "" {
		return intmodel.Stack{}, errdefs.ErrStackNameRequired
	}
	// Get stack metadata
	metadataFilePath := fs.StackMetadataPath(r.opts.RunPath, realmName, spaceName, stackName)
	internalStack, err := r.readStackInternal(metadataFilePath)
	if err != nil {
		switch {
		case errors.Is(err, errdefs.ErrMissingMetadataFile):
			return intmodel.Stack{}, errdefs.ErrStackNotFound
		case errors.Is(err, errdefs.ErrConversionFailed):
			return intmodel.Stack{}, err
		default:
			return intmodel.Stack{}, fmt.Errorf("%w: %w", errdefs.ErrGetStack, err)
		}
	}

	return internalStack, nil
}

func (r *Exec) GetCell(cell intmodel.Cell) (intmodel.Cell, error) {
	realmName := strings.TrimSpace(cell.Spec.RealmName)
	if realmName == "" {
		return intmodel.Cell{}, errdefs.ErrRealmNameRequired
	}
	spaceName := strings.TrimSpace(cell.Spec.SpaceName)
	if spaceName == "" {
		return intmodel.Cell{}, errdefs.ErrSpaceNameRequired
	}
	stackName := strings.TrimSpace(cell.Spec.StackName)
	if stackName == "" {
		return intmodel.Cell{}, errdefs.ErrStackNameRequired
	}
	cellName := strings.TrimSpace(cell.Metadata.Name)
	if cellName == "" {
		return intmodel.Cell{}, errdefs.ErrCellNameRequired
	}
	// Get cell metadata
	metadataFilePath := fs.CellMetadataPath(r.opts.RunPath, realmName, spaceName, stackName, cellName)
	internalCell, err := r.readCellInternal(metadataFilePath)
	if err != nil {
		switch {
		case errors.Is(err, errdefs.ErrMissingMetadataFile):
			return intmodel.Cell{}, errdefs.ErrCellNotFound
		case errors.Is(err, errdefs.ErrConversionFailed):
			return intmodel.Cell{}, err
		default:
			return intmodel.Cell{}, fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
		}
	}

	return internalCell, nil
}
