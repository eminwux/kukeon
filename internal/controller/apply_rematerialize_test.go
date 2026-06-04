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
	"strings"
	"testing"

	applypkg "github.com/eminwux/kukeon/internal/controller/apply"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// TestReconcileCell_RematerializesStoppedCell is AC #1 in issue #486:
// `kuke apply -f` against a cell that was previously killed (spec equal,
// runtime missing — Status.State == Stopped) must call StartCell, report
// `updated`, and surface a per-component summary naming the recreated
// containers. Without the runtime-state branch in apply.ReconcileCell,
// the diff returns no changes and apply walks away with `unchanged` while
// the cell stays Stopped forever — the divergent-state regression the
// docs/cli-use-cases.md "apply (declarative, multi-document)" section
// names by example (`kuke kill` then `kuke apply`).
func TestReconcileCell_RematerializesStoppedCell(t *testing.T) {
	desired := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
	desired.Spec.Containers = []intmodel.ContainerSpec{
		{ID: "root", Root: true, Image: "alpine:latest"},
		{ID: "web", Image: "nginx:latest"},
		{ID: "sidecar", Image: "busybox:latest"},
	}

	actual := desired
	actual.Status.State = intmodel.CellStateStopped

	var startCellCalled bool
	f := &fakeRunner{
		GetRealmFn: func(realm intmodel.Realm) (intmodel.Realm, error) { return realm, nil },
		GetSpaceFn: func(space intmodel.Space) (intmodel.Space, error) { return space, nil },
		GetStackFn: func(stack intmodel.Stack) (intmodel.Stack, error) { return stack, nil },
		GetCellFn:  func(_ intmodel.Cell) (intmodel.Cell, error) { return actual, nil },
		StartCellFn: func(cell intmodel.Cell) (intmodel.Cell, error) {
			startCellCalled = true
			cell.Status.State = intmodel.CellStateReady
			return cell, nil
		},
	}

	result, err := applypkg.ReconcileCell(f, desired)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !startCellCalled {
		t.Error("expected StartCell to be called on a spec-equal Stopped cell")
	}
	if result.Action != "updated" {
		t.Errorf("expected action %q, got %q", "updated", result.Action)
	}

	// AC: per-component summary naming the recreated containers. Root is
	// excluded — see rematerializeChanges' contract.
	joined := strings.Join(result.Changes, "\n")
	for _, want := range []string{`container "web" re-materialized`, `container "sidecar" re-materialized`} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected changes to mention %q; got:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, `container "root"`) {
		t.Errorf("expected root container to be excluded from per-component summary; got:\n%s", joined)
	}

	resCell, ok := result.Resource.(intmodel.Cell)
	if !ok {
		t.Fatalf("expected result.Resource to be a Cell, got %T", result.Resource)
	}
	if resCell.Status.State != intmodel.CellStateReady {
		t.Errorf("expected cell state to be Ready after re-materialize, got %v", resCell.Status.State)
	}
}

// TestReconcileCell_NoRegressionForReadyCell is AC #3 in issue #486: a
// spec-equal apply against a fully-Ready cell must still report
// `unchanged`. StartCell must not be invoked — the re-materialize trigger
// is conditioned on Status.State == Stopped, not on "spec matches."
// Without this guard, every apply on a healthy cell would tear down and
// recreate its containers, which is exactly the destructive churn the
// idempotency contract forbids.
func TestReconcileCell_NoRegressionForReadyCell(t *testing.T) {
	desired := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
	desired.Spec.Containers = []intmodel.ContainerSpec{
		{ID: "root", Root: true, Image: "alpine:latest"},
		{ID: "web", Image: "nginx:latest"},
	}

	actual := desired
	actual.Status.State = intmodel.CellStateReady

	f := &fakeRunner{
		GetRealmFn: func(realm intmodel.Realm) (intmodel.Realm, error) { return realm, nil },
		GetSpaceFn: func(space intmodel.Space) (intmodel.Space, error) { return space, nil },
		GetStackFn: func(stack intmodel.Stack) (intmodel.Stack, error) { return stack, nil },
		GetCellFn:  func(_ intmodel.Cell) (intmodel.Cell, error) { return actual, nil },
		StartCellFn: func(_ intmodel.Cell) (intmodel.Cell, error) {
			t.Fatal("StartCell must not be called when cell is Ready and spec matches")
			return intmodel.Cell{}, nil
		},
	}

	result, err := applypkg.ReconcileCell(f, desired)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != "unchanged" {
		t.Errorf("expected action %q, got %q", "unchanged", result.Action)
	}
}

// TestReconcileCell_FailedCellDoesNotRematerialize defends the conservative
// scope chosen for issue #486: only CellStateStopped triggers
// re-materialize. CellStateFailed is sticky and signals a startup problem
// the user (not apply) should investigate — a silent recreate would mask
// the original failure cause that markCellFailedAfterStartupFailure went
// out of its way to record in Status.Reason/Message.
func TestReconcileCell_FailedCellDoesNotRematerialize(t *testing.T) {
	desired := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
	actual := desired
	actual.Status.State = intmodel.CellStateFailed

	f := &fakeRunner{
		GetRealmFn: func(realm intmodel.Realm) (intmodel.Realm, error) { return realm, nil },
		GetSpaceFn: func(space intmodel.Space) (intmodel.Space, error) { return space, nil },
		GetStackFn: func(stack intmodel.Stack) (intmodel.Stack, error) { return stack, nil },
		GetCellFn:  func(_ intmodel.Cell) (intmodel.Cell, error) { return actual, nil },
		StartCellFn: func(_ intmodel.Cell) (intmodel.Cell, error) {
			t.Fatal("StartCell must not be called for a Failed cell")
			return intmodel.Cell{}, nil
		},
	}

	result, err := applypkg.ReconcileCell(f, desired)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != "unchanged" {
		t.Errorf("expected action %q for Failed cell, got %q", "unchanged", result.Action)
	}
}
