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
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// labelCellProfileLegacy is the legacy "kukeon.io/profile" cell label that the
// removed CellProfile materializer (#626) stamped on cells generated from a
// per-user profile. New cells never receive it, but cells persisted before the
// cleanup still carry it on disk; the stamping helpers below propagate that
// legacy value onto runtime container specs so KUKEON_CELL_PROFILE_NAME still
// surfaces in the workload env for long-lived legacy cells.
const labelCellProfileLegacy = "kukeon.io/profile"

// stampCellProfileNameOnContainers fills CellProfileName on every container
// in the cell from cell.Metadata.Labels[labelCellProfileLegacy] (the label
// the removed CellProfile materializer stamped when a cell was generated from
// a CellProfile). No-op when the label is absent — cells created from a plain
// CellDoc, CellBlueprint, or CellConfig carry no profile identity. Mirrors
// the runtime-only stamping shape of stampCellEtcFilePathsOnContainers /
// CellCgroupPath: BuildContainerSpec / BuildRootContainerSpec read the field,
// but it is not part of the persisted container document. Issue #351; the
// CellProfile producer was removed in #626 but the read-side stamp is kept
// for legacy cells still on disk.
func stampCellProfileNameOnContainers(cell *intmodel.Cell) {
	if cell == nil {
		return
	}
	name := cellProfileNameFromCell(cell)
	if name == "" {
		return
	}
	for i := range cell.Spec.Containers {
		if cell.Spec.Containers[i].CellProfileName == "" {
			cell.Spec.Containers[i].CellProfileName = name
		}
	}
}

// stampCellProfileNameOnContainerSpec is the per-container counterpart for
// call sites that hold a local container spec value (e.g. the root spec
// built fresh by ensureCellRootContainerSpec on the StartCell recreate path)
// and need it to carry the same CellProfileName the cell-wide stamp would
// apply. No-op when the cell carries no profile label.
func stampCellProfileNameOnContainerSpec(spec *intmodel.ContainerSpec, cell *intmodel.Cell) {
	if spec == nil || spec.CellProfileName != "" {
		return
	}
	spec.CellProfileName = cellProfileNameFromCell(cell)
}

func cellProfileNameFromCell(cell *intmodel.Cell) string {
	if cell == nil || cell.Metadata.Labels == nil {
		return ""
	}
	return cell.Metadata.Labels[labelCellProfileLegacy]
}
