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
	"fmt"

	applypkg "github.com/eminwux/kukeon/internal/controller/apply"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// ensurePerCellVolumes auto-provisions every kind: volume mount marked
// Ensure=true on the cell's containers before its containers are created or
// started, the daemon-side provisioning half of the per-cell volume claim (step
// 5, #1017). Materialization sets Ensure on per-cell (${CELL_NAME}) volume
// mounts, whose Volume cannot be pre-created for a not-yet-named cell; an
// operator may also set ensure: true directly for Docker-style "create on first
// reference". Provisioning reuses ReconcileVolume, so it is idempotent (an
// already-bound cell re-binds its existing Volume rather than minting a fresh
// one — #1017 AC4) and inherits the scope-existence check, perms, and reclaim
// handling of `kuke create volume`.
//
// A bare-source mount provisions a Volume at the cell's own realm/space/stack;
// a volumeRef mount provisions at the ref's explicit coordinates. Mounts
// without Ensure are left to step 4's resolver, which still hard-errors on a
// missing reference so a typo fails fast.
func (b *Exec) ensurePerCellVolumes(cell intmodel.Cell) error {
	for ci := range cell.Spec.Containers {
		for _, m := range cell.Spec.Containers[ci].Volumes {
			if m.Kind != intmodel.VolumeKindVolume || !m.Ensure {
				continue
			}
			vol := volumeForEnsureMount(cell, m)
			if _, err := applypkg.ReconcileVolume(b.runner, vol); err != nil {
				return fmt.Errorf(
					"%w: ensure volume %q for cell %q: %w",
					errdefs.ErrWriteVolume, vol.Metadata.Name, cell.Metadata.Name, err,
				)
			}
		}
	}
	return nil
}

// volumeForEnsureMount builds the Volume an Ensure mount provisions: a volumeRef
// mount names its Volume by the ref's explicit scope coordinates; a bare-source
// mount names it in the cell's own scope (where step 4's same-scope resolver
// finds it most-specific-first).
func volumeForEnsureMount(cell intmodel.Cell, m intmodel.VolumeMount) intmodel.Volume {
	if m.VolumeRef != nil {
		return intmodel.Volume{
			Metadata: intmodel.VolumeMetadata{
				Name:  m.VolumeRef.Name,
				Realm: m.VolumeRef.Realm,
				Space: m.VolumeRef.Space,
				Stack: m.VolumeRef.Stack,
			},
		}
	}
	return intmodel.Volume{
		Metadata: intmodel.VolumeMetadata{
			Name:  m.Source,
			Realm: cell.Spec.RealmName,
			Space: cell.Spec.SpaceName,
			Stack: cell.Spec.StackName,
		},
	}
}
