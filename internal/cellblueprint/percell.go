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

package cellblueprint

import (
	"strings"

	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// CellNameVar is the reserved per-cell template variable a binding embeds in a
// kind: volume mount source (e.g. `source: mem-${CELL_NAME}`) to mint a
// distinct, private Volume per stamped cell (step 5 of the volumes epic,
// #1017). Unlike the scalar `${KEY}` parameters substituted at Resolve time
// (params.go), ${CELL_NAME} is resolved later — at MaterializeWithName, once
// the cell's name exists — because a 1:N binding's cell name is generated per
// invocation, not supplied as a parameter. CELL_NAME is therefore reserved: a
// blueprint should not declare a parameter named CELL_NAME, since Resolve
// substitutes declared `${KEY}` parameters first and the value would consume
// the token before this per-cell expansion ever runs.
const CellNameVar = "${CELL_NAME}"

// ExpandPerCellVolumes rewrites the ${CELL_NAME} template variable to cellName
// in every kind: volume mount of every container, and marks each rewritten
// mount Ensure=true so the daemon auto-provisions the now cell-specific Volume
// at create/start (a per-cell Volume cannot be pre-created for a not-yet-named
// cell). This is the naming + provisioning half of the per-cell volume claim:
// materializing N cells from one binding yields N distinct Volume names
// (`mem-<cellA>`, `mem-<cellB>`, …), so isolation comes from the name, not the
// scope (umbrella #1015).
//
// Idempotency falls out of determinism: re-materializing a cell with the same
// identity re-expands to the same Volume name, so reconcile and recreate
// re-bind the existing Volume rather than minting a fresh one (#1017 AC4). A
// mount with no ${CELL_NAME} is left untouched — Ensure stays as authored, so a
// statically-named volume keeps step 4's "missing is a hard error" default.
//
// Each container's Volumes slice is cloned before mutation and reassigned, so
// a blueprint document whose Volumes backing array is shared across
// materializations (materializeContainer copies the slice header, not its
// elements) is never aliased — without the clone, expanding ${CELL_NAME} for
// one stamped cell would overwrite the template and collapse the next cell's
// name onto the first. A VolumeRef is likewise cloned before its Name is
// rewritten.
func ExpandPerCellVolumes(cellName string, containers []v1beta1.ContainerSpec) {
	for ci := range containers {
		if len(containers[ci].Volumes) == 0 {
			continue
		}
		vols := make([]v1beta1.VolumeMount, len(containers[ci].Volumes))
		copy(vols, containers[ci].Volumes)
		containers[ci].Volumes = vols
		for vi := range vols {
			if vols[vi].Kind != v1beta1.VolumeKindVolume {
				continue
			}
			expanded := false
			if strings.Contains(vols[vi].Source, CellNameVar) {
				vols[vi].Source = strings.ReplaceAll(vols[vi].Source, CellNameVar, cellName)
				expanded = true
			}
			if ref := vols[vi].VolumeRef; ref != nil && strings.Contains(ref.Name, CellNameVar) {
				clone := *ref
				clone.Name = strings.ReplaceAll(ref.Name, CellNameVar, cellName)
				vols[vi].VolumeRef = &clone
				expanded = true
			}
			if expanded {
				vols[vi].Ensure = true
			}
		}
	}
}
