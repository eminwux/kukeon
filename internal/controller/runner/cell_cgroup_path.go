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

import intmodel "github.com/eminwux/kukeon/internal/modelhub"

// setCellCgroupPath fills CellCgroupPath on a single container spec from
// cell.Status.CgroupPath. CellCgroupPath is a runtime-only injection (not
// persisted with the cell document), so every BuildContainerSpec /
// BuildRootContainerSpec call site must populate it from the live cell right
// before the build — otherwise the OCI Linux.CgroupsPath falls back to the
// runc-shim default and the cell-rooted placement from issue #312 silently
// regresses. The runtime-only nature is what made the six prior copies easy
// to add but hard to keep in sync (issue #317): missing a site doesn't break
// compile, only the actual cgroup placement at runtime.
//
// No-op when spec is nil, cell is nil, or spec.CellCgroupPath is already
// populated (idempotent across repeat runs from the same cell).
func setCellCgroupPath(spec *intmodel.ContainerSpec, cell *intmodel.Cell) {
	if spec == nil || cell == nil || spec.CellCgroupPath != "" {
		return
	}
	spec.CellCgroupPath = cell.Status.CgroupPath
}
