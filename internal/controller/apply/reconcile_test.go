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

	"github.com/eminwux/kukeon/internal/controller/apply"
	"github.com/eminwux/kukeon/internal/controller/runner"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// reconcileFakeRunner is a focused fake satisfying runner.Runner for
// ReconcileCell's root-container branch tests. The embedded interface is
// nil, so any method not overridden here nil-derefs — that is intentional:
// the test asserts which methods ReconcileCell calls *and* which it does
// not, and an unexpected call surfaces as a panic the test catches.
type reconcileFakeRunner struct {
	runner.Runner

	cellState intmodel.Cell

	updateCellCalled   bool
	recreateCellCalled bool
}

func (f *reconcileFakeRunner) GetRealm(realm intmodel.Realm) (intmodel.Realm, error) {
	return realm, nil
}

func (f *reconcileFakeRunner) GetSpace(space intmodel.Space) (intmodel.Space, error) {
	return space, nil
}

func (f *reconcileFakeRunner) GetStack(stack intmodel.Stack) (intmodel.Stack, error) {
	return stack, nil
}

func (f *reconcileFakeRunner) GetCell(_ intmodel.Cell) (intmodel.Cell, error) {
	return f.cellState, nil
}

func (f *reconcileFakeRunner) UpdateCell(cell intmodel.Cell) (intmodel.Cell, error) {
	f.updateCellCalled = true
	return cell, nil
}

func (f *reconcileFakeRunner) RecreateCell(cell intmodel.Cell) (intmodel.Cell, error) {
	f.recreateCellCalled = true
	return cell, nil
}

// TestReconcileCell_RootEnvCompatible_RoutesToUpdate pins the issue-#990
// reconciler-gate fix: a Compatible-on-root edit (env) sets
// `RootContainerChanged=true` so the diff readout qualifies the divergence
// under `rootContainer.env`, but the reconciler must AND that flag with
// `ChangeType == Breaking` before routing to RecreateCell. Without the AND,
// `kuke apply -f` of an env edit on the root container triggers a full
// kill-and-recreate, contradicting the PR's stated Compatible-on-root
// semantics ("re-evaluated at start or rebuilt from spec") and the
// `TestDiffCell_RootContainerEnv_Compatible` docstring ("not forced through
// RecreateCell"). The companion gate at
// `internal/controller/runner/spec_hash_test.go:252` already uses
// `diff.RootContainerChanged && diff.ChangeType == ChangeTypeBreaking`;
// the apply layer's reconciler must match.
func TestReconcileCell_RootEnvCompatible_RoutesToUpdate(t *testing.T) {
	desired := intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: "hello-world"},
		Spec: intmodel.CellSpec{
			RealmName: "default",
			SpaceName: "default",
			StackName: "default",
			Containers: []intmodel.ContainerSpec{
				{ID: "root", Root: true, Image: "busybox:latest", Env: []string{"FOO=new"}},
			},
		},
	}
	actual := intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: "hello-world"},
		Spec: intmodel.CellSpec{
			RealmName: "default",
			SpaceName: "default",
			StackName: "default",
			Containers: []intmodel.ContainerSpec{
				{ID: "root", Root: true, Image: "busybox:latest", Env: []string{"FOO=old"}},
			},
		},
	}

	r := &reconcileFakeRunner{cellState: actual}
	result, err := apply.ReconcileCell(r, desired)
	if err != nil {
		t.Fatalf("ReconcileCell returned unexpected error: %v", err)
	}

	if r.recreateCellCalled {
		t.Error("Compatible-on-root env edit must not route through RecreateCell")
	}
	if !r.updateCellCalled {
		t.Error("Compatible-on-root env edit must route through UpdateCell")
	}
	if result.Action != "updated" {
		t.Errorf("expected result.Action=updated, got %q", result.Action)
	}
}

// TestReconcileCell_RootImageBreaking_RoutesToRecreate is the positive
// counterpart: a Breaking-on-root edit (image) must still route through
// RecreateCell — the AND gate added for the Compatible case must not
// regress the existing Breaking path.
func TestReconcileCell_RootImageBreaking_RoutesToRecreate(t *testing.T) {
	desired := intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: "hello-world"},
		Spec: intmodel.CellSpec{
			RealmName: "default",
			SpaceName: "default",
			StackName: "default",
			Containers: []intmodel.ContainerSpec{
				{ID: "root", Root: true, Image: "busybox:1.36"},
			},
		},
	}
	actual := intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: "hello-world"},
		Spec: intmodel.CellSpec{
			RealmName: "default",
			SpaceName: "default",
			StackName: "default",
			Containers: []intmodel.ContainerSpec{
				{ID: "root", Root: true, Image: "busybox:1.35"},
			},
		},
	}

	r := &reconcileFakeRunner{cellState: actual}
	result, err := apply.ReconcileCell(r, desired)
	if err != nil {
		t.Fatalf("ReconcileCell returned unexpected error: %v", err)
	}

	if !r.recreateCellCalled {
		t.Error("Breaking-on-root image edit must route through RecreateCell")
	}
	if r.updateCellCalled {
		t.Error("Breaking-on-root image edit must not also fall through to UpdateCell")
	}
	if result.Action != "updated" {
		t.Errorf("expected result.Action=updated, got %q", result.Action)
	}
}
