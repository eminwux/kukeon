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
	"testing"

	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// TestSetCellCgroupPath asserts the helper copies cell.Status.CgroupPath
// into spec.CellCgroupPath when the spec field is empty.
func TestSetCellCgroupPath(t *testing.T) {
	spec := &intmodel.ContainerSpec{}
	cell := &intmodel.Cell{Status: intmodel.CellStatus{CgroupPath: "/kukeon/default/space/stack/cell"}}

	setCellCgroupPath(spec, cell)

	if got := spec.CellCgroupPath; got != "/kukeon/default/space/stack/cell" {
		t.Errorf("spec.CellCgroupPath = %q, want %q", got, "/kukeon/default/space/stack/cell")
	}
}

// TestSetCellCgroupPath_DoesNotOverwrite verifies the helper leaves a
// pre-populated CellCgroupPath alone so a caller that already injected a
// value (e.g., a test fixture) is not stomped.
func TestSetCellCgroupPath_DoesNotOverwrite(t *testing.T) {
	spec := &intmodel.ContainerSpec{CellCgroupPath: "/preset/path"}
	cell := &intmodel.Cell{Status: intmodel.CellStatus{CgroupPath: "/kukeon/default/space/stack/cell"}}

	setCellCgroupPath(spec, cell)

	if got := spec.CellCgroupPath; got != "/preset/path" {
		t.Errorf("spec.CellCgroupPath = %q, want %q (preset value must not be overwritten)", got, "/preset/path")
	}
}

// TestSetCellCgroupPath_NilSafe locks in the nil-spec and nil-cell guards so
// the helper is safe to drop in across the six runner sites without each
// caller having to repeat a defensive nil check.
func TestSetCellCgroupPath_NilSafe(t *testing.T) {
	setCellCgroupPath(nil, &intmodel.Cell{Status: intmodel.CellStatus{CgroupPath: "/x"}})

	spec := &intmodel.ContainerSpec{}
	setCellCgroupPath(spec, nil)
	if got := spec.CellCgroupPath; got != "" {
		t.Errorf("spec.CellCgroupPath = %q, want empty (nil cell should be a no-op)", got)
	}
}

// TestSetCellCgroupPath_EmptyStatusPath asserts that a cell that has not yet
// populated Status.CgroupPath (e.g., a not-yet-provisioned cell) leaves the
// spec field empty — matching the pre-refactor inline behavior, which also
// only ever wrote whatever cell.Status.CgroupPath held at call time.
func TestSetCellCgroupPath_EmptyStatusPath(t *testing.T) {
	spec := &intmodel.ContainerSpec{}
	cell := &intmodel.Cell{}

	setCellCgroupPath(spec, cell)

	if got := spec.CellCgroupPath; got != "" {
		t.Errorf("spec.CellCgroupPath = %q, want empty (cell has no status.cgroupPath)", got)
	}
}
