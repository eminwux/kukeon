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
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

func TestReconcileCells_WalksEveryRealmSpaceStackCell(t *testing.T) {
	realmA := buildTestRealm("realm-a", "")
	realmB := buildTestRealm("realm-b", "")
	spaceA := buildTestSpace("space-a", "realm-a")
	spaceB := buildTestSpace("space-b", "realm-b")
	stackA := buildTestStack("stack-a", "realm-a", "space-a")
	stackB := buildTestStack("stack-b", "realm-b", "space-b")
	cellA := buildTestCell("cell-a", "realm-a", "space-a", "stack-a")
	cellB := buildTestCell("cell-b", "realm-b", "space-b", "stack-b")

	var reconcileCalls atomic.Int32
	mock := &fakeRunner{
		ListRealmsFn: func() ([]intmodel.Realm, error) {
			return []intmodel.Realm{realmA, realmB}, nil
		},
		ListSpacesFn: func(realm string) ([]intmodel.Space, error) {
			switch realm {
			case "realm-a":
				return []intmodel.Space{spaceA}, nil
			case "realm-b":
				return []intmodel.Space{spaceB}, nil
			}
			return nil, nil
		},
		ListStacksFn: func(realm, space string) ([]intmodel.Stack, error) {
			switch {
			case realm == "realm-a" && space == "space-a":
				return []intmodel.Stack{stackA}, nil
			case realm == "realm-b" && space == "space-b":
				return []intmodel.Stack{stackB}, nil
			}
			return nil, nil
		},
		ListCellsFn: func(realm, space, stack string) ([]intmodel.Cell, error) {
			switch {
			case realm == "realm-a" && space == "space-a" && stack == "stack-a":
				return []intmodel.Cell{cellA}, nil
			case realm == "realm-b" && space == "space-b" && stack == "stack-b":
				return []intmodel.Cell{cellB}, nil
			}
			return nil, nil
		},
		ReconcileCellFn: func(cell intmodel.Cell) (intmodel.Cell, bool, error) {
			reconcileCalls.Add(1)
			return cell, true, nil
		},
	}

	ctrl := setupTestController(t, mock)
	res, err := ctrl.ReconcileCells()
	if err != nil {
		t.Fatalf("ReconcileCells() error = %v", err)
	}
	if got := reconcileCalls.Load(); got != 2 {
		t.Errorf("ReconcileCell calls: got %d, want 2", got)
	}
	if res.CellsScanned != 2 {
		t.Errorf("CellsScanned: got %d, want 2", res.CellsScanned)
	}
	if res.CellsUpdated != 2 {
		t.Errorf("CellsUpdated: got %d, want 2", res.CellsUpdated)
	}
	if res.CellsErrored != 0 {
		t.Errorf("CellsErrored: got %d, want 0", res.CellsErrored)
	}
	if len(res.Errors) != 0 {
		t.Errorf("Errors: got %v, want empty", res.Errors)
	}
}

func TestReconcileCells_PerCellErrorDoesNotAbortWalk(t *testing.T) {
	realm := buildTestRealm("realm-a", "")
	space := buildTestSpace("space-a", "realm-a")
	stack := buildTestStack("stack-a", "realm-a", "space-a")
	good := buildTestCell("good", "realm-a", "space-a", "stack-a")
	bad := buildTestCell("bad", "realm-a", "space-a", "stack-a")

	var reconcileCalls atomic.Int32
	mock := &fakeRunner{
		ListRealmsFn: func() ([]intmodel.Realm, error) { return []intmodel.Realm{realm}, nil },
		ListSpacesFn: func(string) ([]intmodel.Space, error) { return []intmodel.Space{space}, nil },
		ListStacksFn: func(string, string) ([]intmodel.Stack, error) {
			return []intmodel.Stack{stack}, nil
		},
		ListCellsFn: func(string, string, string) ([]intmodel.Cell, error) {
			return []intmodel.Cell{bad, good}, nil
		},
		ReconcileCellFn: func(cell intmodel.Cell) (intmodel.Cell, bool, error) {
			reconcileCalls.Add(1)
			if cell.Metadata.Name == "bad" {
				return cell, false, errors.New("synthetic reconcile failure")
			}
			return cell, true, nil
		},
	}

	ctrl := setupTestController(t, mock)
	res, err := ctrl.ReconcileCells()
	if err != nil {
		t.Fatalf("ReconcileCells() error = %v", err)
	}
	if got := reconcileCalls.Load(); got != 2 {
		t.Errorf("ReconcileCell calls: got %d, want 2 (the failing cell must not abort the walk)", got)
	}
	if res.CellsScanned != 2 {
		t.Errorf("CellsScanned: got %d, want 2", res.CellsScanned)
	}
	if res.CellsUpdated != 1 {
		t.Errorf("CellsUpdated: got %d, want 1", res.CellsUpdated)
	}
	if res.CellsErrored != 1 {
		t.Errorf("CellsErrored: got %d, want 1", res.CellsErrored)
	}
	if len(res.Errors) != 1 {
		t.Errorf("Errors: got %v, want 1 entry", res.Errors)
	}
	if len(res.Errors) == 1 && !strings.Contains(res.Errors[0], "bad") {
		t.Errorf("error string should name the failing cell, got %q", res.Errors[0])
	}
}

func TestReconcileCells_ListRealmsErrorIsRecorded(t *testing.T) {
	mock := &fakeRunner{
		ListRealmsFn: func() ([]intmodel.Realm, error) {
			return nil, errors.New("containerd unreachable")
		},
	}
	ctrl := setupTestController(t, mock)
	res, err := ctrl.ReconcileCells()
	if err != nil {
		t.Fatalf("ReconcileCells() must not return a hard error on a list-realms failure (loop must keep ticking next pass): %v", err)
	}
	if len(res.Errors) != 1 {
		t.Errorf("Errors: got %v, want 1 entry", res.Errors)
	}
	if res.CellsScanned != 0 {
		t.Errorf("CellsScanned: got %d, want 0", res.CellsScanned)
	}
}

func TestReconcileCells_EmptyHostNoOp(t *testing.T) {
	mock := &fakeRunner{
		ListRealmsFn: func() ([]intmodel.Realm, error) { return nil, nil },
	}
	ctrl := setupTestController(t, mock)
	res, err := ctrl.ReconcileCells()
	if err != nil {
		t.Fatalf("ReconcileCells() error = %v", err)
	}
	if res.CellsScanned != 0 || res.CellsUpdated != 0 || res.CellsErrored != 0 {
		t.Errorf("expected zero counters, got %+v", res)
	}
}
