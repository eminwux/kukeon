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

package apply_test

import (
	"testing"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/controller/apply"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// createLabelsFakeRunner drives ReconcileCell down its not-found → create
// branch and captures the cell handed to runner.CreateCell.
type createLabelsFakeRunner struct {
	reconcileFakeRunner

	created *intmodel.Cell
}

func (f *createLabelsFakeRunner) GetCell(_ intmodel.Cell) (intmodel.Cell, error) {
	return intmodel.Cell{}, errdefs.ErrCellNotFound
}

func (f *createLabelsFakeRunner) CreateCell(cell intmodel.Cell) (intmodel.Cell, error) {
	f.created = &cell
	return cell, nil
}

func (f *createLabelsFakeRunner) StartCell(cell intmodel.Cell) (intmodel.Cell, error) {
	return cell, nil
}

func (f *createLabelsFakeRunner) UpdateCellMetadata(_ intmodel.Cell) error {
	return nil
}

func applyCreateCell(labels map[string]string) intmodel.Cell {
	return intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: "web", Labels: labels},
		Spec: intmodel.CellSpec{
			RealmName: "r1",
			SpaceName: "s1",
			StackName: "st1",
			Containers: []intmodel.ContainerSpec{
				{ID: "root", Root: true, Image: "busybox:latest"},
			},
		},
	}
}

// TestReconcileCell_Create_StampsManagedScopeLabels pins issue #1461: a cell
// first created by `kuke apply` must carry the same four managed scope labels
// controller.CreateCell stamps, alongside the user-authored ones, and the
// caller's manifest label map must not be mutated.
func TestReconcileCell_Create_StampsManagedScopeLabels(t *testing.T) {
	userLabels := map[string]string{"audit": "metadata"}
	r := &createLabelsFakeRunner{}

	result, err := apply.ReconcileCell(r, applyCreateCell(userLabels))
	if err != nil {
		t.Fatalf("ReconcileCell returned unexpected error: %v", err)
	}
	if result.Action != "created" {
		t.Fatalf("expected result.Action=created, got %q", result.Action)
	}
	if r.created == nil {
		t.Fatal("ReconcileCell did not call runner.CreateCell")
	}

	want := map[string]string{
		consts.KukeonRealmLabelKey: "r1",
		consts.KukeonSpaceLabelKey: "s1",
		consts.KukeonStackLabelKey: "st1",
		consts.KukeonCellLabelKey:  "web",
		"audit":                    "metadata",
	}
	for k, v := range want {
		if got := r.created.Metadata.Labels[k]; got != v {
			t.Errorf("CreateCell label %s = %q, want %q", k, got, v)
		}
	}
	if len(userLabels) != 1 {
		t.Errorf("caller's label map was mutated: %v", userLabels)
	}
}

// TestReconcileCell_Create_UserAuthoredManagedLabelWins keeps the #455 AC #3
// "if not exists" semantics on the apply create path: an explicit
// user-authored managed label is not overwritten.
func TestReconcileCell_Create_UserAuthoredManagedLabelWins(t *testing.T) {
	r := &createLabelsFakeRunner{}

	_, err := apply.ReconcileCell(r, applyCreateCell(map[string]string{consts.KukeonCellLabelKey: "custom"}))
	if err != nil {
		t.Fatalf("ReconcileCell returned unexpected error: %v", err)
	}
	if got := r.created.Metadata.Labels[consts.KukeonCellLabelKey]; got != "custom" {
		t.Errorf("user-authored %s = %q, want %q", consts.KukeonCellLabelKey, got, "custom")
	}
	if got := r.created.Metadata.Labels[consts.KukeonRealmLabelKey]; got != "r1" {
		t.Errorf("%s = %q, want %q", consts.KukeonRealmLabelKey, got, "r1")
	}
}
