// Copyright 2026 Emiliano Spinella (eminwux)
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

package controller_test

import (
	"errors"
	"testing"

	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

func execTarget() intmodel.Container {
	return intmodel.Container{
		Metadata: intmodel.ContainerMetadata{Name: "work"},
		Spec: intmodel.ContainerSpec{
			ID: "work", RealmName: "r", SpaceName: "s", StackName: "st", CellName: "c",
		},
	}
}

func TestExecContainer_ForwardsToRunner(t *testing.T) {
	mockRunner := &fakeRunner{}
	mockRunner.GetCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
		if cell.Metadata.Name != "c" || cell.Spec.RealmName != "r" || cell.Spec.SpaceName != "s" ||
			cell.Spec.StackName != "st" {
			t.Errorf("GetCell lookup = %+v", cell)
		}
		cell.Spec.Containers = []intmodel.ContainerSpec{{ID: "work", ContainerdID: "s_st_c_work"}}
		return cell, nil
	}
	var gotCell intmodel.Cell
	var gotName string
	var gotSpec ctr.ExecSpec
	mockRunner.ExecContainerFn = func(
		cell intmodel.Cell, name string, spec ctr.ExecSpec, _ ctr.ExecStdio,
	) (ctr.ExecProcess, error) {
		gotCell, gotName, gotSpec = cell, name, spec
		return nil, nil //nolint:nilnil // the test only inspects the call
	}
	ctrl := setupTestController(t, mockRunner)

	spec := ctr.ExecSpec{ID: "kuke-exec-1", Command: []string{"id", "-u"}, Root: true}
	if _, err := ctrl.ExecContainer(execTarget(), spec, ctr.ExecStdio{}); err != nil {
		t.Fatalf("ExecContainer: %v", err)
	}
	if gotName != "work" || len(gotCell.Spec.Containers) != 1 || gotSpec.ID != "kuke-exec-1" || !gotSpec.Root {
		t.Fatalf("runner got cell %+v, name %q, spec %+v", gotCell.Spec, gotName, gotSpec)
	}
}

func TestExecContainer_CellNotFound(t *testing.T) {
	mockRunner := &fakeRunner{}
	mockRunner.GetCellFn = func(intmodel.Cell) (intmodel.Cell, error) {
		return intmodel.Cell{}, errdefs.ErrCellNotFound
	}
	ctrl := setupTestController(t, mockRunner)

	_, err := ctrl.ExecContainer(execTarget(), ctr.ExecSpec{ID: "x", Command: []string{"id"}}, ctr.ExecStdio{})
	if !errors.Is(err, errdefs.ErrCellNotFound) {
		t.Fatalf("ExecContainer = %v, want ErrCellNotFound", err)
	}
}

func TestExecContainer_RequiresNames(t *testing.T) {
	ctrl := setupTestController(t, &fakeRunner{})

	noName := execTarget()
	noName.Metadata.Name = " "
	if _, err := ctrl.ExecContainer(noName, ctr.ExecSpec{}, ctr.ExecStdio{}); !errors.Is(
		err, errdefs.ErrContainerNameRequired,
	) {
		t.Errorf("no container name = %v, want ErrContainerNameRequired", err)
	}
	noCell := execTarget()
	noCell.Spec.CellName = ""
	if _, err := ctrl.ExecContainer(noCell, ctr.ExecSpec{}, ctr.ExecStdio{}); !errors.Is(
		err, errdefs.ErrCellNameRequired,
	) {
		t.Errorf("no cell name = %v, want ErrCellNameRequired", err)
	}
}
