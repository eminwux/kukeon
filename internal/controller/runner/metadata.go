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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/metadata"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// firstGeneration is the Metadata.Generation stamped on the initial write
// of a resource — there is no prior spec on disk, so the spec is "new" and
// the monotonic counter starts at 1 (matching the Kubernetes convention a
// freshly-created object has generation 1).
const firstGeneration int64 = 1

// writeDocWithGeneration performs the phase-3 read-modify-write the four
// Update*Metadata writers share. Under the sidecar flock it reads the prior
// on-disk document, treats the caller's incoming Generation as an optimistic
// token, bumps Metadata.Generation only when the incoming spec differs from
// the persisted spec (a status-only update preserves it), and CAS-writes the
// result against the bytes it observed.
//
// Lost-update protection has two layers:
//
//   - Optimistic token: a versioned caller (incoming Generation > 0) that
//     read generation G is rejected with errdefs.ErrStaleResource when the
//     on-disk generation has advanced past G. This catches a writer whose
//     in-memory snapshot was made stale by an intervening write — the very
//     gap the reconciler-vs-user race opens.
//   - CAS: the WriteMetadataCAS against the observed bytes catches a write
//     that lands in the narrow window between this read and the write.
//
// An incoming Generation of 0 is the "unversioned" escape hatch for callers
// that constructed the document from scratch rather than reading it back
// (create/provision flows): the spec-vs-disk bump still applies, but the
// token check is skipped so the create's own follow-up writes do not trip a
// false stale. A freshly-read document always carries a non-zero generation,
// so 0 reliably distinguishes "built it" from "read then modified it".
func writeDocWithGeneration[D any](
	ctx context.Context,
	logger *slog.Logger,
	file string,
	doc D,
	getGeneration func(D) int64,
	setGeneration func(*D, int64),
	specOf func(D) any,
) error {
	prior, readErr := metadata.ReadRaw(ctx, logger, file)
	if errors.Is(readErr, errdefs.ErrMissingMetadataFile) {
		// No prior document: this is the initial spec write. CAS with a
		// nil prior asserts the file does not yet exist, so a concurrent
		// creator racing us surfaces as ErrStaleResource.
		setGeneration(&doc, firstGeneration)
		return metadata.WriteMetadataCAS(ctx, logger, nil, doc, file)
	}
	if readErr != nil {
		return readErr
	}

	var priorDoc D
	if err := json.Unmarshal(prior, &priorDoc); err != nil {
		return fmt.Errorf("decode prior metadata %s: %w", file, err)
	}

	onDiskGeneration := getGeneration(priorDoc)
	if incoming := getGeneration(doc); incoming > 0 && onDiskGeneration > incoming {
		return fmt.Errorf(
			"write %s: caller observed generation %d, on-disk is %d: %w",
			file, incoming, onDiskGeneration, errdefs.ErrStaleResource,
		)
	}

	newGeneration := onDiskGeneration
	specUnchanged, err := jsonEqual(specOf(priorDoc), specOf(doc))
	if err != nil {
		return fmt.Errorf("compare spec for %s: %w", file, err)
	}
	if !specUnchanged {
		newGeneration++
	}
	setGeneration(&doc, newGeneration)

	return metadata.WriteMetadataCAS(ctx, logger, prior, doc, file)
}

// refreshCellGeneration syncs cell.Metadata.Generation to the value
// currently persisted on disk. Chained writers call it after a
// spec-bumping write so a follow-up status persist on the same in-memory
// object carries the current optimistic token instead of a stale one that
// the writer would (correctly) reject as ErrStaleResource. A read failure
// leaves the in-memory token untouched — best-effort, the follow-up write
// then falls back to surfacing the stale error rather than masking it.
func (r *Exec) refreshCellGeneration(cell *intmodel.Cell) {
	path := fs.CellMetadataPath(
		r.opts.RunPath,
		cell.Spec.RealmName,
		cell.Spec.SpaceName,
		cell.Spec.StackName,
		cell.Metadata.Name,
	)
	if onDisk, err := r.readCellInternal(path); err == nil {
		cell.Metadata.Generation = onDisk.Metadata.Generation
	}
}

// jsonEqual reports whether two values serialise to identical JSON. The
// writers compare spec sub-documents through it rather than reflect.DeepEqual
// so the comparison matches the persisted form exactly — the prior doc is
// decoded from disk and the candidate doc is produced by the deterministic
// apischeme conversion, so an unchanged spec round-trips to identical bytes.
func jsonEqual(a, b any) (bool, error) {
	ab, err := json.Marshal(a)
	if err != nil {
		return false, err
	}
	bb, err := json.Marshal(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(ab, bb), nil
}

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
	err = writeDocWithGeneration(r.ctx, r.logger, metadataFilePath, realmDoc,
		func(d v1beta1.RealmDoc) int64 { return d.Metadata.Generation },
		func(d *v1beta1.RealmDoc, g int64) { d.Metadata.Generation = g },
		func(d v1beta1.RealmDoc) any { return d.Spec },
	)
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
	err = writeDocWithGeneration(r.ctx, r.logger, metadataFilePath, spaceDoc,
		func(d v1beta1.SpaceDoc) int64 { return d.Metadata.Generation },
		func(d *v1beta1.SpaceDoc, g int64) { d.Metadata.Generation = g },
		func(d v1beta1.SpaceDoc) any { return d.Spec },
	)
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
	err = writeDocWithGeneration(r.ctx, r.logger, metadataFilePath, stackDoc,
		func(d v1beta1.StackDoc) int64 { return d.Metadata.Generation },
		func(d *v1beta1.StackDoc, g int64) { d.Metadata.Generation = g },
		func(d v1beta1.StackDoc) any { return d.Spec },
	)
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
	err = writeDocWithGeneration(r.ctx, r.logger, metadataFilePath, cellDoc,
		func(d v1beta1.CellDoc) int64 { return d.Metadata.Generation },
		func(d *v1beta1.CellDoc, g int64) { d.Metadata.Generation = g },
		func(d v1beta1.CellDoc) any { return d.Spec },
	)
	if err != nil {
		r.logger.Error("failed to write metadata", "err", fmt.Sprintf("%v", err))
		return fmt.Errorf("%w: %w", errdefs.ErrWriteMetadata, err)
	}
	return nil
}
