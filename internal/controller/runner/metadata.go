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
	"time"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/metadata"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
)

// nowUTC returns the current wall clock in UTC. Wrapped through the Exec
// so tests that need to freeze time can override the function.
func (r *Exec) nowUTC() time.Time {
	if r.nowFn != nil {
		return r.nowFn().UTC()
	}
	return time.Now().UTC()
}

// stampRealmLifecycle applies the lifecycle-timestamp invariants from
// issue #166 to a realm immediately before persistence: CreatedAt is
// stamped only when zero, UpdatedAt is bumped on every call, and
// ReadyAt is set once on the first State==Ready persist and never
// overwritten. Extracted as a pure function so the timestamp logic is
// unit-testable independent of the metadata-write path.
func stampRealmLifecycle(realm *intmodel.Realm, now time.Time) {
	if realm.Status.CreatedAt.IsZero() {
		realm.Status.CreatedAt = now
	}
	realm.Status.UpdatedAt = now
	if realm.Status.ReadyAt.IsZero() && realm.Status.State == intmodel.RealmStateReady {
		realm.Status.ReadyAt = now
	}
}

// stampSpaceLifecycle is the Space counterpart of stampRealmLifecycle.
func stampSpaceLifecycle(space *intmodel.Space, now time.Time) {
	if space.Status.CreatedAt.IsZero() {
		space.Status.CreatedAt = now
	}
	space.Status.UpdatedAt = now
	if space.Status.ReadyAt.IsZero() && space.Status.State == intmodel.SpaceStateReady {
		space.Status.ReadyAt = now
	}
}

// stampStackLifecycle is the Stack counterpart of stampRealmLifecycle.
func stampStackLifecycle(stack *intmodel.Stack, now time.Time) {
	if stack.Status.CreatedAt.IsZero() {
		stack.Status.CreatedAt = now
	}
	stack.Status.UpdatedAt = now
	if stack.Status.ReadyAt.IsZero() && stack.Status.State == intmodel.StackStateReady {
		stack.Status.ReadyAt = now
	}
}

// stampCellLifecycle is the Cell counterpart of stampRealmLifecycle.
func stampCellLifecycle(cell *intmodel.Cell, now time.Time) {
	if cell.Status.CreatedAt.IsZero() {
		cell.Status.CreatedAt = now
	}
	cell.Status.UpdatedAt = now
	if cell.Status.ReadyAt.IsZero() && cell.Status.State == intmodel.CellStateReady {
		cell.Status.ReadyAt = now
	}
}

func (r *Exec) UpdateRealmMetadata(realm intmodel.Realm) error {
	stampRealmLifecycle(&realm, r.nowUTC())

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
	stampSpaceLifecycle(&space, r.nowUTC())

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
	stampStackLifecycle(&stack, r.nowUTC())

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
	stampCellLifecycle(&cell, r.nowUTC())

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
