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
	"errors"
	"testing"

	"github.com/eminwux/kukeon/internal/controller/apply"
	"github.com/eminwux/kukeon/internal/controller/runner"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// stubRunner is a minimal runner.Runner mock for ReconcileCell tests. The
// embedded runner.Runner is left nil so calling any method that the test
// has not explicitly overridden panics — a louder signal than silently
// returning a zero value when the production code starts depending on a
// previously-unused method.
type stubRunner struct {
	runner.Runner

	getRealmFn  func(realm intmodel.Realm) (intmodel.Realm, error)
	getSpaceFn  func(space intmodel.Space) (intmodel.Space, error)
	getStackFn  func(stack intmodel.Stack) (intmodel.Stack, error)
	getCellFn   func(cell intmodel.Cell) (intmodel.Cell, error)
	startCellFn func(cell intmodel.Cell) (intmodel.Cell, error)

	startCellCalled int
}

func (s *stubRunner) GetRealm(realm intmodel.Realm) (intmodel.Realm, error) {
	return s.getRealmFn(realm)
}

func (s *stubRunner) GetSpace(space intmodel.Space) (intmodel.Space, error) {
	return s.getSpaceFn(space)
}

func (s *stubRunner) GetStack(stack intmodel.Stack) (intmodel.Stack, error) {
	return s.getStackFn(stack)
}

func (s *stubRunner) GetCell(cell intmodel.Cell) (intmodel.Cell, error) {
	return s.getCellFn(cell)
}

func (s *stubRunner) StartCell(cell intmodel.Cell) (intmodel.Cell, error) {
	s.startCellCalled++
	return s.startCellFn(cell)
}

// helloCell builds a fully-specified cell document equal in spec and
// metadata to itself, so DiffCell against a clone reports no spec changes.
// Status is left empty; tests stamp it on the actual-cell return value.
func helloCell() intmodel.Cell {
	return intmodel.Cell{
		Metadata: intmodel.CellMetadata{
			Name: "hello-world",
		},
		Spec: intmodel.CellSpec{
			ID:        "hello-world",
			RealmName: "default",
			SpaceName: "default",
			StackName: "default",
			Containers: []intmodel.ContainerSpec{
				{
					ID:    "root",
					Root:  true,
					Image: "docker.io/library/busybox:latest",
				},
			},
			RootContainerID: "root",
		},
	}
}

func parentLookupsOK(desired intmodel.Cell) (
	func(intmodel.Realm) (intmodel.Realm, error),
	func(intmodel.Space) (intmodel.Space, error),
	func(intmodel.Stack) (intmodel.Stack, error),
) {
	return func(_ intmodel.Realm) (intmodel.Realm, error) {
			return intmodel.Realm{Metadata: intmodel.RealmMetadata{Name: desired.Spec.RealmName}}, nil
		},
		func(_ intmodel.Space) (intmodel.Space, error) {
			return intmodel.Space{Metadata: intmodel.SpaceMetadata{Name: desired.Spec.SpaceName}}, nil
		},
		func(_ intmodel.Stack) (intmodel.Stack, error) {
			return intmodel.Stack{Metadata: intmodel.StackMetadata{Name: desired.Spec.StackName}}, nil
		}
}

// TestReconcileCell_StoppedCellRematerializes locks AC1 of #486: an apply
// against a spec-equal cell whose runtime has been wiped by `kuke kill
// cell` (CellStateStopped) re-materializes the containers via StartCell
// and reports "updated" with a re-materialize change line — rather than
// silently returning "unchanged" and walking away.
func TestReconcileCell_StoppedCellRematerializes(t *testing.T) {
	desired := helloCell()
	actual := helloCell()
	actual.Status.State = intmodel.CellStateStopped

	getRealm, getSpace, getStack := parentLookupsOK(desired)
	stub := &stubRunner{
		getRealmFn: getRealm,
		getSpaceFn: getSpace,
		getStackFn: getStack,
		getCellFn: func(_ intmodel.Cell) (intmodel.Cell, error) {
			return actual, nil
		},
		startCellFn: func(cell intmodel.Cell) (intmodel.Cell, error) {
			cell.Status.State = intmodel.CellStateReady
			return cell, nil
		},
	}

	result, err := apply.ReconcileCell(stub, desired)
	if err != nil {
		t.Fatalf("ReconcileCell returned error: %v", err)
	}
	if stub.startCellCalled != 1 {
		t.Errorf("expected StartCell to be called exactly once, got %d", stub.startCellCalled)
	}
	if result.Action != "updated" {
		t.Errorf("expected Action=updated for spec-equal stopped cell, got %q", result.Action)
	}
	if len(result.Changes) != 1 || result.Changes[0] != "runtime re-materialized" {
		t.Errorf("expected single change \"runtime re-materialized\", got %v", result.Changes)
	}
	cell, ok := result.Resource.(intmodel.Cell)
	if !ok {
		t.Fatalf("expected Resource to be intmodel.Cell, got %T", result.Resource)
	}
	if cell.Status.State != intmodel.CellStateReady {
		t.Errorf("expected post-rematerialize cell to be Ready, got %v", cell.Status.State)
	}
}

// TestReconcileCell_ReadyCellUnchanged locks AC3 of #486: a fully-Ready
// spec-equal cell still returns "unchanged" — apply must not perturb a
// healthy cell with a destructive StartCell recreate when nothing diverges.
func TestReconcileCell_ReadyCellUnchanged(t *testing.T) {
	desired := helloCell()
	actual := helloCell()
	actual.Status.State = intmodel.CellStateReady

	getRealm, getSpace, getStack := parentLookupsOK(desired)
	stub := &stubRunner{
		getRealmFn: getRealm,
		getSpaceFn: getSpace,
		getStackFn: getStack,
		getCellFn: func(_ intmodel.Cell) (intmodel.Cell, error) {
			return actual, nil
		},
		startCellFn: func(_ intmodel.Cell) (intmodel.Cell, error) {
			return intmodel.Cell{}, errors.New("StartCell must not be called for spec-equal Ready cell")
		},
	}

	result, err := apply.ReconcileCell(stub, desired)
	if err != nil {
		t.Fatalf("ReconcileCell returned error: %v", err)
	}
	if stub.startCellCalled != 0 {
		t.Errorf("expected StartCell to be skipped for Ready cell, got %d calls", stub.startCellCalled)
	}
	if result.Action != "unchanged" {
		t.Errorf("expected Action=unchanged for spec-equal Ready cell, got %q", result.Action)
	}
	if len(result.Changes) != 0 {
		t.Errorf("expected no Changes for unchanged result, got %v", result.Changes)
	}
}

// TestReconcileCell_FailedCellRematerializes covers the Failed-state
// branch alongside Stopped — a cell whose root task crashed and got
// stamped Failed is still spec-equal, and apply should restore it just
// like the kill-cell case.
func TestReconcileCell_FailedCellRematerializes(t *testing.T) {
	desired := helloCell()
	actual := helloCell()
	actual.Status.State = intmodel.CellStateFailed

	getRealm, getSpace, getStack := parentLookupsOK(desired)
	stub := &stubRunner{
		getRealmFn: getRealm,
		getSpaceFn: getSpace,
		getStackFn: getStack,
		getCellFn: func(_ intmodel.Cell) (intmodel.Cell, error) {
			return actual, nil
		},
		startCellFn: func(cell intmodel.Cell) (intmodel.Cell, error) {
			cell.Status.State = intmodel.CellStateReady
			return cell, nil
		},
	}

	result, err := apply.ReconcileCell(stub, desired)
	if err != nil {
		t.Fatalf("ReconcileCell returned error: %v", err)
	}
	if stub.startCellCalled != 1 {
		t.Errorf("expected StartCell to be called once for Failed cell, got %d", stub.startCellCalled)
	}
	if result.Action != "updated" {
		t.Errorf("expected Action=updated for spec-equal Failed cell, got %q", result.Action)
	}
}

// TestReconcileCell_RematerializeStartCellErrorSurfaces verifies that a
// StartCell failure during re-materialize is reported as an apply error
// rather than swallowed as a successful "updated". Without this, a kill
// + apply against a cell whose namespace was concurrently torn down
// would print success and leave the cell broken.
func TestReconcileCell_RematerializeStartCellErrorSurfaces(t *testing.T) {
	desired := helloCell()
	actual := helloCell()
	actual.Status.State = intmodel.CellStateStopped
	startErr := errors.New("namespace gone")

	getRealm, getSpace, getStack := parentLookupsOK(desired)
	stub := &stubRunner{
		getRealmFn: getRealm,
		getSpaceFn: getSpace,
		getStackFn: getStack,
		getCellFn: func(_ intmodel.Cell) (intmodel.Cell, error) {
			return actual, nil
		},
		startCellFn: func(_ intmodel.Cell) (intmodel.Cell, error) {
			return intmodel.Cell{}, startErr
		},
	}

	_, err := apply.ReconcileCell(stub, desired)
	if err == nil {
		t.Fatal("expected error when StartCell fails during re-materialize")
	}
	if !errors.Is(err, startErr) {
		t.Errorf("expected wrapped %v, got %v", startErr, err)
	}
}
