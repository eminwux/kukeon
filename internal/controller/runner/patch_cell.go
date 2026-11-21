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
	"encoding/json"
	"errors"
	"fmt"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/metadata"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// PatchCellMetadata is the locked read-modify-write for a cell's persisted
// document (issue #1569). It takes the per-cell lifecycle lock, reads the
// document straight off disk (no containerd status re-derivation), hands
// the fresh copy to mutate, and writes it back only when mutate returns true.
//
// The write is must-exist: a cell deleted before or during the patch returns
// errdefs.ErrCellNotFound instead of being recreated. It is also guarded by
// the bytes the patch read, so a writer outside this process (the in-process
// CLI path, which does not share the lock) landing between the read and the
// write surfaces as errdefs.ErrStaleResource rather than being overwritten.
//
// Because it holds the cell lock, it must not be called from code that
// already holds it.
func (r *Exec) PatchCellMetadata(cell intmodel.Cell, mutate func(fresh *intmodel.Cell) bool) error {
	defer r.lockCell(cell)()
	return r.patchCellMetadataLocked(cell, mutate)
}

// patchCellMetadataLocked is PatchCellMetadata's body. The caller must hold
// the per-cell lock.
func (r *Exec) patchCellMetadataLocked(cell intmodel.Cell, mutate func(fresh *intmodel.Cell) bool) error {
	if mutate == nil {
		return errors.New("patch cell metadata: nil mutate func")
	}
	path := fs.CellMetadataPath(
		r.opts.RunPath,
		cell.Spec.RealmName,
		cell.Spec.SpaceName,
		cell.Spec.StackName,
		cell.Metadata.Name,
	)
	raw, err := metadata.ReadRaw(r.ctx, r.logger, path)
	if errors.Is(err, errdefs.ErrMissingMetadataFile) {
		return errdefs.ErrCellNotFound
	}
	if err != nil {
		return err
	}
	// Decode twice from the same bytes: mutate may change maps and slices
	// in place, so the observed copy the write is guarded against must not
	// share them.
	fresh, err := decodeCellDoc(raw)
	if err != nil {
		return err
	}
	observed, err := decodeCellDoc(raw)
	if err != nil {
		return err
	}
	if !mutate(&fresh) {
		return nil
	}
	return r.writeCellMetadata(fresh, errdefs.ErrCellNotFound, func(prior v1beta1.CellDoc) error {
		priorInternal, convErr := apischeme.ConvertCellDocToInternal(prior)
		if convErr != nil {
			return fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, convErr)
		}
		return expectUnchanged(priorInternal, observed)
	})
}

// decodeCellDoc decodes a raw cell metadata document into the internal
// model, mirroring readCellInternal's version dispatch.
func decodeCellDoc(raw []byte) (intmodel.Cell, error) {
	version, err := fs.DetectMetadataVersion(raw)
	if err != nil {
		return intmodel.Cell{}, wrapConversionErr(err)
	}
	if version != apischeme.VersionV1Beta1 {
		return intmodel.Cell{}, wrapConversionErr(fmt.Errorf("unsupported apiVersion %q", version))
	}
	var cellDoc v1beta1.CellDoc
	if unmarshalErr := json.Unmarshal(raw, &cellDoc); unmarshalErr != nil {
		return intmodel.Cell{}, wrapConversionErr(unmarshalErr)
	}
	internalCell, convertErr := apischeme.ConvertCellDocToInternal(cellDoc)
	if convertErr != nil {
		return intmodel.Cell{}, wrapConversionErr(convertErr)
	}
	return internalCell, nil
}
