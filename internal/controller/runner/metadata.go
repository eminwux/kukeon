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
	"path/filepath"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/metadata"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func (r *Exec) UpdateRealmMetadata(doc *v1beta1.RealmDoc) error {
	metadataRunPath := filepath.Join(r.opts.RunPath, consts.KukeonRealmMetadataSubDir, doc.Metadata.Name)
	metadataFilePath := filepath.Join(metadataRunPath, consts.KukeonMetadataFile)
	err := metadata.WriteMetadata(r.ctx, r.logger, doc, metadataFilePath)
	if err != nil {
		r.logger.Error("failed to write metadata", "err", fmt.Sprintf("%v", err))
		return fmt.Errorf("%w: %w", errdefs.ErrWriteMetadata, err)
	}
	return nil
}

func (r *Exec) UpdateSpaceMetadata(doc *v1beta1.SpaceDoc) error {
	metadataRunPath := filepath.Join(r.opts.RunPath, consts.KukeonSpaceMetadataSubDir, doc.Metadata.Name)
	metadataFilePath := filepath.Join(metadataRunPath, consts.KukeonMetadataFile)
	err := metadata.WriteMetadata(r.ctx, r.logger, doc, metadataFilePath)
	if err != nil {
		r.logger.Error("failed to write metadata", "err", fmt.Sprintf("%v", err))
		return fmt.Errorf("%w: %w", errdefs.ErrWriteMetadata, err)
	}
	return nil
}

func (r *Exec) UpdateStackMetadata(doc *v1beta1.StackDoc) error {
	metadataRunPath := filepath.Join(r.opts.RunPath, consts.KukeonStackMetadataSubDir, doc.Metadata.Name)
	metadataFilePath := filepath.Join(metadataRunPath, consts.KukeonMetadataFile)
	err := metadata.WriteMetadata(r.ctx, r.logger, doc, metadataFilePath)
	if err != nil {
		r.logger.Error("failed to write metadata", "err", fmt.Sprintf("%v", err))
		return fmt.Errorf("%w: %w", errdefs.ErrWriteMetadata, err)
	}
	return nil
}

func (r *Exec) UpdateCellMetadata(doc *v1beta1.CellDoc) error {
	metadataRunPath := filepath.Join(r.opts.RunPath, consts.KukeonCellMetadataSubDir, doc.Metadata.Name)
	metadataFilePath := filepath.Join(metadataRunPath, consts.KukeonMetadataFile)
	err := metadata.WriteMetadata(r.ctx, r.logger, doc, metadataFilePath)
	if err != nil {
		r.logger.Error("failed to write metadata", "err", fmt.Sprintf("%v", err))
		return fmt.Errorf("%w: %w", errdefs.ErrWriteMetadata, err)
	}
	return nil
}
