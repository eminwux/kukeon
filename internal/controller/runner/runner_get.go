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

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/metadata"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func (r *Exec) GetRealm(realm intmodel.Realm) (intmodel.Realm, error) {
	// Get realm metadata
	metadataFilePath := fs.RealmMetadataPath(r.opts.RunPath, realm.Metadata.Name)
	realmDoc, err := metadata.ReadMetadata[v1beta1.RealmDoc](r.ctx, r.logger, metadataFilePath)
	if err != nil {
		if errors.Is(err, errdefs.ErrMissingMetadataFile) {
			return intmodel.Realm{}, errdefs.ErrRealmNotFound
		}
		return intmodel.Realm{}, fmt.Errorf("%w: %w", errdefs.ErrGetRealm, err)
	}

	// Convert external doc from metadata to internal model
	internalRealm, err := apischeme.ConvertRealmDocToInternal(realmDoc)
	if err != nil {
		return intmodel.Realm{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
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
	spaceDoc, err := metadata.ReadMetadata[v1beta1.SpaceDoc](r.ctx, r.logger, metadataFilePath)
	if err != nil {
		if errors.Is(err, errdefs.ErrMissingMetadataFile) {
			return intmodel.Space{}, errdefs.ErrSpaceNotFound
		}
		return intmodel.Space{}, fmt.Errorf("%w: %w", errdefs.ErrGetSpace, err)
	}

	// Convert external doc from metadata to internal model
	internalSpace, err := apischeme.ConvertSpaceDocToInternal(spaceDoc)
	if err != nil {
		return intmodel.Space{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
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
	stackDoc, err := metadata.ReadMetadata[v1beta1.StackDoc](r.ctx, r.logger, metadataFilePath)
	if err != nil {
		if errors.Is(err, errdefs.ErrMissingMetadataFile) {
			return intmodel.Stack{}, errdefs.ErrStackNotFound
		}
		return intmodel.Stack{}, fmt.Errorf("%w: %w", errdefs.ErrGetStack, err)
	}

	// Convert external doc from metadata to internal model
	internalStack, err := apischeme.ConvertStackDocToInternal(stackDoc)
	if err != nil {
		return intmodel.Stack{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
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
	cellDoc, err := metadata.ReadMetadata[v1beta1.CellDoc](r.ctx, r.logger, metadataFilePath)
	if err != nil {
		if errors.Is(err, errdefs.ErrMissingMetadataFile) {
			return intmodel.Cell{}, errdefs.ErrCellNotFound
		}
		return intmodel.Cell{}, fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
	}

	// Convert external doc from metadata to internal model
	internalCell, err := apischeme.ConvertCellDocToInternal(cellDoc)
	if err != nil {
		return intmodel.Cell{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	return internalCell, nil
}
