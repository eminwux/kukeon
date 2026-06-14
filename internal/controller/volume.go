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
	"path/filepath"
	"strings"

	applypkg "github.com/eminwux/kukeon/internal/controller/apply"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// CreateVolumeResult reports the outcome of provisioning a Volume's directory.
type CreateVolumeResult struct {
	Volume  intmodel.Volume
	Created bool
}

// CreateVolume provisions a Volume's daemon-managed directory via the apply
// reconciler (issue #1236). It mirrors CreateSecret: the scope coordinates are
// validated first, then ReconcileVolume verifies the scope exists and writes
// the directory idempotently (re-creating reports created=false). A Volume is
// never cell-scoped — VolumeMetadata has no cell coordinate — so the AC's "cell
// scope rejected" holds by construction (there is no --cell flag to set).
func (b *Exec) CreateVolume(volume intmodel.Volume) (CreateVolumeResult, error) {
	var res CreateVolumeResult

	if err := validateVolumeLookup(volume.Metadata); err != nil {
		return res, err
	}

	reconcile, err := applypkg.ReconcileVolume(b.runner, volume)
	if err != nil {
		return res, err
	}
	res.Created = reconcile.Action == "created"
	res.Volume = volume
	return res, nil
}

// GetVolumeResult reports the metadata-only view of a single `kind: Volume`
// (issue #1018). A Volume carries no body, so this is scope coordinates and
// name only — the same metadata-only shape as GetSecretResult.
type GetVolumeResult struct {
	Volume         intmodel.Volume
	MetadataExists bool
}

// GetVolume reports whether one named, scoped Volume exists. The scope
// coordinates are validated for completeness (a deeper coordinate requires
// every shallower one; a Volume may not be cell-scoped); realm and name are
// mandatory. Returns MetadataExists=false (no error) when the volume is absent,
// mirroring GetBlueprint's "report, don't error on not-found" shape.
func (b *Exec) GetVolume(volume intmodel.Volume) (GetVolumeResult, error) {
	var res GetVolumeResult

	if err := validateVolumeLookup(volume.Metadata); err != nil {
		return res, err
	}

	got, err := b.runner.GetVolume(volume)
	if err != nil {
		if errors.Is(err, errdefs.ErrVolumeNotFound) {
			res.MetadataExists = false
			return res, nil
		}
		return res, fmt.Errorf("%w: %w", errdefs.ErrGetVolume, err)
	}

	res.MetadataExists = true
	res.Volume = got
	return res, nil
}

// ListVolumes lists the metadata of every Volume bound to the filter scope or
// any scope nested within it (issue #1018). An empty realm lists across all
// realms; the filter coordinates must still be contiguous (no gap below a set
// level), and — a Volume never being cell-scoped — there is no cell coordinate.
func (b *Exec) ListVolumes(realmName, spaceName, stackName string) ([]intmodel.Volume, error) {
	if err := validateVolumeScopeFilter(realmName, spaceName, stackName); err != nil {
		return nil, err
	}
	return b.runner.ListVolumes(
		strings.TrimSpace(realmName),
		strings.TrimSpace(spaceName),
		strings.TrimSpace(stackName),
	)
}

// DeleteVolumeResult reports the outcome of removing a single Volume's
// daemon-provisioned directory.
type DeleteVolumeResult struct {
	Volume  intmodel.Volume
	Deleted bool
}

// DeleteVolume removes a single named, scoped Volume's daemon-provisioned
// directory. Returns a "not found" error when the volume does not exist,
// matching the DeleteBlueprint contract. A running cell that mounts the Volume
// via a kind: volume mount gates the delete (issue #1016): the request is
// refused with ErrVolumeInUse naming the mounting cell so the directory is
// never pulled out from under a live mount. A Volume mounted only by stopped
// cells, or by none, deletes cleanly.
func (b *Exec) DeleteVolume(volume intmodel.Volume) (DeleteVolumeResult, error) {
	var res DeleteVolumeResult

	if err := validateVolumeLookup(volume.Metadata); err != nil {
		return res, err
	}

	if cellRef, mounted, err := b.runner.VolumeMountedByLiveCell(volume); err != nil {
		return res, err
	} else if mounted {
		return res, fmt.Errorf("%w: mounted by cell %q", errdefs.ErrVolumeInUse, cellRef)
	}

	if err := b.runner.DeleteVolume(volume); err != nil {
		if errors.Is(err, errdefs.ErrVolumeNotFound) {
			return res, fmt.Errorf("volume %q not found", volume.Metadata.Name)
		}
		return res, fmt.Errorf("%w: %w", errdefs.ErrDeleteVolume, err)
	}

	res.Deleted = true
	res.Volume = volume
	return res, nil
}

// validateVolumeLookup enforces the scope contract for a single-volume get or
// delete: name and realm are mandatory and the scope coordinates must be
// contiguous, with the Volume-specific rule that a cell coordinate is never
// valid (a Volume is realm/space/stack-scoped only).
func validateVolumeLookup(md intmodel.VolumeMetadata) error {
	if strings.TrimSpace(md.Name) == "" {
		return errdefs.ErrVolumeNameRequired
	}
	name := strings.TrimSpace(md.Name)
	realm := strings.TrimSpace(md.Realm)
	space := strings.TrimSpace(md.Space)
	stack := strings.TrimSpace(md.Stack)

	if realm == "" {
		return errdefs.ErrVolumeRealmRequired
	}
	if stack != "" && space == "" {
		return fmt.Errorf("%w (stack set without space)", errdefs.ErrVolumeScopeIncomplete)
	}
	for _, seg := range []struct{ field, value string }{
		{"metadata.name", name},
		{"metadata.realm", realm},
		{"metadata.space", space},
		{"metadata.stack", stack},
	} {
		if err := validateVolumeSegment(seg.value); err != nil {
			return fmt.Errorf("%w (%s)", err, seg.field)
		}
	}
	return nil
}

func validateVolumeSegment(value string) error {
	if value == "" {
		return nil
	}
	if value == "." || value == ".." {
		return errdefs.ErrVolumeCoordUnsafe
	}
	if strings.ContainsRune(value, 0) ||
		strings.ContainsRune(value, '/') ||
		strings.ContainsRune(value, '\\') ||
		strings.ContainsRune(value, filepath.Separator) {
		return errdefs.ErrVolumeCoordUnsafe
	}
	return nil
}

// validateVolumeScopeFilter enforces the scope contract for a list filter:
// realm is optional (an empty realm lists across all realms), but a set deeper
// coordinate still requires every shallower one. A Volume is never cell-scoped,
// so the filter bottoms out at stack. Each set coordinate is also screened for
// unsafe path segments (issue #1293): ListVolumes passes the raw segments to
// the runner's childScopeNames, which filepath.Joins them onto the scope dir,
// so an unscreened "space=.." would stat/enumerate a parent scope — the same
// `..`/separator traversal class validateVolumeSegment closes for single-volume
// lookups. An empty segment is allowed (it means "unset", not unsafe).
func validateVolumeScopeFilter(realm, space, stack string) error {
	realm = strings.TrimSpace(realm)
	space = strings.TrimSpace(space)
	stack = strings.TrimSpace(stack)

	if realm == "" && (space != "" || stack != "") {
		return fmt.Errorf("%w (scope set without realm)", errdefs.ErrVolumeScopeIncomplete)
	}
	if stack != "" && space == "" {
		return fmt.Errorf("%w (stack set without space)", errdefs.ErrVolumeScopeIncomplete)
	}
	for _, seg := range []struct{ field, value string }{
		{"realm", realm},
		{"space", space},
		{"stack", stack},
	} {
		if err := validateVolumeSegment(seg.value); err != nil {
			return fmt.Errorf("%w (%s)", err, seg.field)
		}
	}
	return nil
}
