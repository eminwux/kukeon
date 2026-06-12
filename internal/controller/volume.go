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
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

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
// matching the DeleteBlueprint contract. There is no live-reference gate in
// step 1: the container-side mount kind that would reference a Volume lands in
// step 4 (#1016), where the delete gate against a live mount is exercised.
func (b *Exec) DeleteVolume(volume intmodel.Volume) (DeleteVolumeResult, error) {
	var res DeleteVolumeResult

	if err := validateVolumeLookup(volume.Metadata); err != nil {
		return res, err
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
	if strings.TrimSpace(md.Realm) == "" {
		return errdefs.ErrVolumeRealmRequired
	}
	if strings.TrimSpace(md.Stack) != "" && strings.TrimSpace(md.Space) == "" {
		return fmt.Errorf("%w (stack set without space)", errdefs.ErrVolumeScopeIncomplete)
	}
	return nil
}

// validateVolumeScopeFilter enforces the scope contract for a list filter:
// realm is optional (an empty realm lists across all realms), but a set deeper
// coordinate still requires every shallower one. A Volume is never cell-scoped,
// so the filter bottoms out at stack.
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
	return nil
}
