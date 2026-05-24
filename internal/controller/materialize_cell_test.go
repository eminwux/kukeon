//go:build !integration

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

package controller_test

import (
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// TestMaterializeCell_NewCell_SkipsStart confirms the daemon-side contract
// MaterializeCell adds for #818: same create path as CreateCell, but the
// runner.StartCell step is never invoked. The CLI's `kuke create cell
// --from-blueprint` / `--from-config` modes depend on this for the
// "materialise-but-don't-start" semantics.
func TestMaterializeCell_NewCell_SkipsStart(t *testing.T) {
	var startCalled bool
	mockRunner := &fakeRunner{
		GetCellFn: func(_ intmodel.Cell) (intmodel.Cell, error) {
			return intmodel.Cell{}, errdefs.ErrCellNotFound
		},
		CreateCellFn: func(_ intmodel.Cell) (intmodel.Cell, error) {
			return buildTestCell("mz-cell", "test-realm", "test-space", "test-stack"), nil
		},
		StartCellFn: func(cell intmodel.Cell) (intmodel.Cell, error) {
			startCalled = true
			return cell, nil
		},
	}

	ctrl := setupTestController(t, mockRunner)
	cell := buildTestCell("mz-cell", "test-realm", "test-space", "test-stack")

	result, err := ctrl.MaterializeCell(cell)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if startCalled {
		t.Fatal("runner.StartCell must not be invoked by MaterializeCell")
	}
	if !result.Created {
		t.Error("expected Created to be true (cell record was created)")
	}
	if result.Started {
		t.Error("expected Started to be false (start step was skipped)")
	}
	if result.StartedPost {
		t.Error("expected StartedPost to be false (start step was skipped)")
	}
	if !result.MetadataExistsPost {
		t.Error("expected MetadataExistsPost to be true")
	}
}

// TestMaterializeCell_ExistingCell_SkipsStart confirms the same contract on
// the EnsureCell branch: when a cell record already exists, MaterializeCell
// still routes through EnsureCell (idempotent resource-existence check) but
// never reaches StartCell.
func TestMaterializeCell_ExistingCell_SkipsStart(t *testing.T) {
	existingCell := buildTestCell("mz-cell", "test-realm", "test-space", "test-stack")
	var startCalled bool
	mockRunner := &fakeRunner{
		GetCellFn: func(_ intmodel.Cell) (intmodel.Cell, error) {
			return existingCell, nil
		},
		ExistsCgroupFn: func(_ any) (bool, error) {
			return true, nil
		},
		ExistsCellRootContainerFn: func(_ intmodel.Cell) (bool, error) {
			return true, nil
		},
		EnsureCellFn: func(cell intmodel.Cell) (intmodel.Cell, error) {
			return cell, nil
		},
		StartCellFn: func(cell intmodel.Cell) (intmodel.Cell, error) {
			startCalled = true
			return cell, nil
		},
	}

	ctrl := setupTestController(t, mockRunner)

	result, err := ctrl.MaterializeCell(existingCell)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if startCalled {
		t.Fatal("runner.StartCell must not be invoked by MaterializeCell on the existing-cell branch")
	}
	if result.Created {
		t.Error("expected Created to be false (cell record already existed)")
	}
	if result.Started {
		t.Error("expected Started to be false (start step was skipped)")
	}
}
