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
	"github.com/eminwux/kukeon/internal/metadata"
	"github.com/eminwux/kukeon/internal/util/fs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func (r *Exec) GetRealm(doc *v1beta1.RealmDoc) (*v1beta1.RealmDoc, error) {
	// Get realm metadata
	metadataFilePath := fs.RealmMetadataPath(r.opts.RunPath, doc.Metadata.Name)
	realmDoc, err := metadata.ReadMetadata[v1beta1.RealmDoc](r.ctx, r.logger, metadataFilePath)
	if err != nil {
		if errors.Is(err, errdefs.ErrMissingMetadataFile) {
			return nil, errdefs.ErrRealmNotFound
		}
		return nil, fmt.Errorf("%w: %w", errdefs.ErrGetRealm, err)
	}
	return &realmDoc, nil
}

func (r *Exec) GetSpace(doc *v1beta1.SpaceDoc) (*v1beta1.SpaceDoc, error) {
	realmName := strings.TrimSpace(doc.Spec.RealmID)
	if realmName == "" {
		return nil, errdefs.ErrRealmNameRequired
	}
	// Get space metadata
	metadataFilePath := fs.SpaceMetadataPath(r.opts.RunPath, realmName, doc.Metadata.Name)
	spaceDoc, err := metadata.ReadMetadata[v1beta1.SpaceDoc](r.ctx, r.logger, metadataFilePath)
	if err != nil {
		if errors.Is(err, errdefs.ErrMissingMetadataFile) {
			return nil, errdefs.ErrSpaceNotFound
		}
		return nil, fmt.Errorf("%w: %w", errdefs.ErrGetSpace, err)
	}
	return &spaceDoc, nil
}

func (r *Exec) GetStack(doc *v1beta1.StackDoc) (*v1beta1.StackDoc, error) {
	realmName := strings.TrimSpace(doc.Spec.RealmID)
	if realmName == "" {
		return nil, errdefs.ErrRealmNameRequired
	}
	spaceName := strings.TrimSpace(doc.Spec.SpaceID)
	if spaceName == "" {
		return nil, errdefs.ErrSpaceNameRequired
	}
	// Get stack metadata
	metadataFilePath := fs.StackMetadataPath(r.opts.RunPath, realmName, spaceName, doc.Metadata.Name)
	stackDoc, err := metadata.ReadMetadata[v1beta1.StackDoc](r.ctx, r.logger, metadataFilePath)
	if err != nil {
		if errors.Is(err, errdefs.ErrMissingMetadataFile) {
			return nil, errdefs.ErrStackNotFound
		}
		return nil, fmt.Errorf("%w: %w", errdefs.ErrGetStack, err)
	}
	return &stackDoc, nil
}

func (r *Exec) GetCell(doc *v1beta1.CellDoc) (*v1beta1.CellDoc, error) {
	realmName := strings.TrimSpace(doc.Spec.RealmID)
	if realmName == "" {
		return nil, errdefs.ErrRealmNameRequired
	}
	spaceName := strings.TrimSpace(doc.Spec.SpaceID)
	if spaceName == "" {
		return nil, errdefs.ErrSpaceNameRequired
	}
	stackName := strings.TrimSpace(doc.Spec.StackID)
	if stackName == "" {
		return nil, errdefs.ErrStackNameRequired
	}
	// Get cell metadata
	metadataFilePath := fs.CellMetadataPath(r.opts.RunPath, realmName, spaceName, stackName, doc.Metadata.Name)
	cellDoc, err := metadata.ReadMetadata[v1beta1.CellDoc](r.ctx, r.logger, metadataFilePath)
	if err != nil {
		if errors.Is(err, errdefs.ErrMissingMetadataFile) {
			return nil, errdefs.ErrCellNotFound
		}
		return nil, fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
	}
	return &cellDoc, nil
}
