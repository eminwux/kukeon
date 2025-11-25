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

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/metadata"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
)

func (r *Exec) UpdateRealmMetadata(realm intmodel.Realm) error {
	// Convert to external model for filesystem boundary
	realmDoc, err := apischeme.BuildRealmExternalFromInternal(realm, apischeme.VersionV1Beta1)
	if err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	metadataFilePath := fs.RealmMetadataPath(r.opts.RunPath, realmDoc.Metadata.Name)
	err = metadata.WriteMetadata(r.ctx, r.logger, realmDoc, metadataFilePath)
	if err != nil {
		r.logger.Error("failed to write metadata", "err", fmt.Sprintf("%v", err))
		return fmt.Errorf("%w: %w", errdefs.ErrWriteMetadata, err)
	}
	return nil
}

func (r *Exec) UpdateSpaceMetadata(space intmodel.Space) error {
	// Convert to external model for filesystem boundary
	spaceDoc, err := apischeme.BuildSpaceExternalFromInternal(space, apischeme.VersionV1Beta1)
	if err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	metadataFilePath := fs.SpaceMetadataPath(
		r.opts.RunPath,
		spaceDoc.Spec.RealmID,
		spaceDoc.Metadata.Name,
	)
	err = metadata.WriteMetadata(r.ctx, r.logger, spaceDoc, metadataFilePath)
	if err != nil {
		r.logger.Error("failed to write metadata", "err", fmt.Sprintf("%v", err))
		return fmt.Errorf("%w: %w", errdefs.ErrWriteMetadata, err)
	}
	return nil
}

func (r *Exec) UpdateStackMetadata(stack intmodel.Stack) error {
	// Convert to external model for filesystem boundary
	stackDoc, err := apischeme.BuildStackExternalFromInternal(stack, apischeme.VersionV1Beta1)
	if err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	metadataFilePath := fs.StackMetadataPath(
		r.opts.RunPath,
		stackDoc.Spec.RealmID,
		stackDoc.Spec.SpaceID,
		stackDoc.Metadata.Name,
	)
	err = metadata.WriteMetadata(r.ctx, r.logger, stackDoc, metadataFilePath)
	if err != nil {
		r.logger.Error("failed to write metadata", "err", fmt.Sprintf("%v", err))
		return fmt.Errorf("%w: %w", errdefs.ErrWriteMetadata, err)
	}
	return nil
}

func (r *Exec) UpdateCellMetadata(cell intmodel.Cell) error {
	// Convert to external model for filesystem boundary
	cellDoc, err := apischeme.BuildCellExternalFromInternal(cell, apischeme.VersionV1Beta1)
	if err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	metadataFilePath := fs.CellMetadataPath(
		r.opts.RunPath,
		cellDoc.Spec.RealmID,
		cellDoc.Spec.SpaceID,
		cellDoc.Spec.StackID,
		cellDoc.Metadata.Name,
	)
	err = metadata.WriteMetadata(r.ctx, r.logger, cellDoc, metadataFilePath)
	if err != nil {
		r.logger.Error("failed to write metadata", "err", fmt.Sprintf("%v", err))
		return fmt.Errorf("%w: %w", errdefs.ErrWriteMetadata, err)
	}
	return nil
}
