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

	"github.com/eminwux/kukeon/internal/cellprofile"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// TestStampCellProfileNameOnContainers asserts the helper copies the
// cell's kukeon.io/profile label into every container's CellProfileName
// while leaving already-populated entries alone (the stamp is idempotent
// across repeat runs from the same cell).
func TestStampCellProfileNameOnContainers(t *testing.T) {
	cell := &intmodel.Cell{
		Metadata: intmodel.CellMetadata{
			Name:   "kukeon-pr-a4aaab",
			Labels: map[string]string{cellprofile.LabelProfile: "kukeon-pr"},
		},
		Spec: intmodel.CellSpec{
			Containers: []intmodel.ContainerSpec{
				{ID: "root"},
				{ID: "work"},
				{ID: "preset", CellProfileName: "preset-value"},
			},
		},
	}

	stampCellProfileNameOnContainers(cell)

	if got := cell.Spec.Containers[0].CellProfileName; got != "kukeon-pr" {
		t.Errorf("Containers[root].CellProfileName = %q, want %q", got, "kukeon-pr")
	}
	if got := cell.Spec.Containers[1].CellProfileName; got != "kukeon-pr" {
		t.Errorf("Containers[work].CellProfileName = %q, want %q", got, "kukeon-pr")
	}
	if got := cell.Spec.Containers[2].CellProfileName; got != "preset-value" {
		t.Errorf(
			"Containers[preset].CellProfileName = %q, want %q (preset value should not be overwritten)",
			got,
			"preset-value",
		)
	}
}

// TestStampCellProfileNameOnContainers_NoLabel verifies that cells created
// from a plain CellDoc (no profile label) leave CellProfileName empty so
// kukeonDefaultEnv emits no KUKEON_CELL_PROFILE_NAME entry.
func TestStampCellProfileNameOnContainers_NoLabel(t *testing.T) {
	cell := &intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: "plain-cell"},
		Spec: intmodel.CellSpec{
			Containers: []intmodel.ContainerSpec{{ID: "work"}},
		},
	}

	stampCellProfileNameOnContainers(cell)

	if got := cell.Spec.Containers[0].CellProfileName; got != "" {
		t.Errorf("Containers[work].CellProfileName = %q, want empty (no profile label on cell)", got)
	}
}

// TestStampCellProfileNameOnContainers_NilSafe locks in the nil-cell
// guard — every runtime-injection helper in the runner is called on
// possibly-nil cells in error paths.
func TestStampCellProfileNameOnContainers_NilSafe(_ *testing.T) {
	stampCellProfileNameOnContainers(nil) // must not panic
}

// TestStampCellProfileNameOnContainerSpec covers the per-container
// counterpart used by the StartCell-recreate paths that hold a local root
// container spec.
func TestStampCellProfileNameOnContainerSpec(t *testing.T) {
	cell := &intmodel.Cell{
		Metadata: intmodel.CellMetadata{
			Labels: map[string]string{cellprofile.LabelProfile: "kukeon-pr"},
		},
	}
	root := &intmodel.ContainerSpec{ID: "root"}
	stampCellProfileNameOnContainerSpec(root, cell)
	if root.CellProfileName != "kukeon-pr" {
		t.Errorf("rootSpec.CellProfileName = %q, want %q", root.CellProfileName, "kukeon-pr")
	}

	preset := &intmodel.ContainerSpec{ID: "root", CellProfileName: "preset"}
	stampCellProfileNameOnContainerSpec(preset, cell)
	if preset.CellProfileName != "preset" {
		t.Errorf("preset spec was overwritten: %q, want %q", preset.CellProfileName, "preset")
	}

	// Nil cell or nil spec: must be a no-op.
	stampCellProfileNameOnContainerSpec(nil, cell)
	stampCellProfileNameOnContainerSpec(&intmodel.ContainerSpec{}, nil)
}
