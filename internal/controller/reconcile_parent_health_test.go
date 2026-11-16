//go:build !integration

// Copyright 2025 Emiliano Spinella (eminwux)
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package controller_test

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/controller/runner"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// parentHealthHost is a one-realm/two-space/one-stack-per-space host whose
// parent health hooks record the order they ran in (#1381).
func parentHealthHost(
	calls *[]string,
	realmHealth func() (bool, error),
	spaceHealth func(name string) (bool, error),
	stackHealth func(name string) (bool, error),
) *fakeRunner {
	return &fakeRunner{
		ListRealmsFn: func() ([]intmodel.Realm, error) {
			return []intmodel.Realm{buildTestRealm("r", "")}, nil
		},
		ListSpacesFn: func(string) ([]intmodel.Space, error) {
			return []intmodel.Space{buildTestSpace("s1", "r"), buildTestSpace("s2", "r")}, nil
		},
		ListStacksFn: func(_, space string) ([]intmodel.Stack, error) {
			*calls = append(*calls, "list-stacks "+space)
			return []intmodel.Stack{buildTestStack("st-"+space, "r", space)}, nil
		},
		ListCellsFn: func(_, space, stack string) ([]intmodel.Cell, error) {
			*calls = append(*calls, "list-cells "+stack)
			return []intmodel.Cell{buildTestCell("c", "r", space, stack)}, nil
		},
		ReconcileCellFn: func(cell intmodel.Cell) (intmodel.Cell, runner.ReconcileOutcome, error) {
			return cell, runner.ReconcileOutcome{}, nil
		},
		ReconcileRealmHealthFn: func(realm intmodel.Realm) (bool, error) {
			*calls = append(*calls, "realm "+realm.Metadata.Name)
			return realmHealth()
		},
		ReconcileSpaceHealthFn: func(space intmodel.Space) (bool, error) {
			*calls = append(*calls, "space "+space.Metadata.Name)
			return spaceHealth(space.Metadata.Name)
		},
		ReconcileStackHealthFn: func(stack intmodel.Stack) (bool, error) {
			*calls = append(*calls, "stack "+stack.Metadata.Name)
			return stackHealth(stack.Metadata.Name)
		},
	}
}

func TestReconcileCells_ReconcilesParentHealthOnEveryTick(t *testing.T) {
	var calls []string
	mock := parentHealthHost(&calls,
		func() (bool, error) { return true, nil },
		func(name string) (bool, error) { return name == "s1", nil },
		func(string) (bool, error) { return false, nil },
	)

	res, err := setupTestController(t, mock).ReconcileCells()
	if err != nil {
		t.Fatalf("ReconcileCells() error = %v", err)
	}
	want := []string{
		"realm r",
		"space s1", "list-stacks s1", "stack st-s1", "list-cells st-s1",
		"space s2", "list-stacks s2", "stack st-s2", "list-cells st-s2",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("walk order:\n got %v\nwant %v", calls, want)
	}
	if res.ParentsUpdated != 2 {
		t.Errorf("ParentsUpdated = %d, want 2 (realm + space s1)", res.ParentsUpdated)
	}
	if res.ParentsErrored != 0 || len(res.Errors) != 0 {
		t.Errorf("ParentsErrored = %d, Errors = %v, want none", res.ParentsErrored, res.Errors)
	}
	if res.CellsScanned != 2 {
		t.Errorf("CellsScanned = %d, want 2", res.CellsScanned)
	}
}

// TestReconcileCells_ParentHealthErrorStillDescends pins that a failed parent
// health reconcile is recorded but does not hide the cells beneath it.
func TestReconcileCells_ParentHealthErrorStillDescends(t *testing.T) {
	var calls []string
	mock := parentHealthHost(&calls,
		func() (bool, error) { return false, errors.New("realm write failed") },
		func(string) (bool, error) { return false, nil },
		func(name string) (bool, error) {
			if name == "st-s1" {
				return false, errors.New("stack write failed")
			}
			return false, nil
		},
	)

	res, err := setupTestController(t, mock).ReconcileCells()
	if err != nil {
		t.Fatalf("ReconcileCells() error = %v", err)
	}
	if res.CellsScanned != 2 {
		t.Errorf("CellsScanned = %d, want 2 (health errors must not skip cells)", res.CellsScanned)
	}
	if res.ParentsErrored != 2 {
		t.Errorf("ParentsErrored = %d, want 2", res.ParentsErrored)
	}
	joined := strings.Join(res.Errors, "\n")
	for _, want := range []string{"reconcile realm r health", "reconcile stack r/s1/st-s1 health"} {
		if !strings.Contains(joined, want) {
			t.Errorf("Errors %v missing %q", res.Errors, want)
		}
	}
}

// TestReconcileCells_VanishedParentSkipsSubtree pins that a parent deleted
// since the listing is skipped silently, subtree included.
func TestReconcileCells_VanishedParentSkipsSubtree(t *testing.T) {
	var calls []string
	mock := parentHealthHost(&calls,
		func() (bool, error) { return false, nil },
		func(name string) (bool, error) {
			if name == "s1" {
				return false, fmt.Errorf("get space: %w", errdefs.ErrSpaceNotFound)
			}
			return false, nil
		},
		func(string) (bool, error) { return false, nil },
	)

	res, err := setupTestController(t, mock).ReconcileCells()
	if err != nil {
		t.Fatalf("ReconcileCells() error = %v", err)
	}
	for _, call := range calls {
		if call == "list-stacks s1" {
			t.Errorf("walk descended into vanished space s1: %v", calls)
		}
	}
	if res.CellsScanned != 1 {
		t.Errorf("CellsScanned = %d, want 1 (only s2's cell)", res.CellsScanned)
	}
	if res.ParentsErrored != 0 || len(res.Errors) != 0 {
		t.Errorf("ParentsErrored = %d, Errors = %v, want none for a vanished parent",
			res.ParentsErrored, res.Errors)
	}
}
