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
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// absentParentsRunner reports every parent as missing and records any
// parent-create call. The embedded nil runner.Runner panics on any other
// method, so a rejected cell that reaches GetCell/CreateCell fails loudly.
type absentParentsRunner struct {
	runner.Runner

	createRealmCalls int
	createSpaceCalls int
	createStackCalls int
}

func (f *absentParentsRunner) GetRealm(intmodel.Realm) (intmodel.Realm, error) {
	return intmodel.Realm{}, errdefs.ErrRealmNotFound
}

func (f *absentParentsRunner) GetSpace(intmodel.Space) (intmodel.Space, error) {
	return intmodel.Space{}, errdefs.ErrSpaceNotFound
}

func (f *absentParentsRunner) GetStack(intmodel.Stack) (intmodel.Stack, error) {
	return intmodel.Stack{}, errdefs.ErrStackNotFound
}

func (f *absentParentsRunner) CreateRealm(realm intmodel.Realm) (intmodel.Realm, error) {
	f.createRealmCalls++
	return realm, nil
}

func (f *absentParentsRunner) CreateSpace(space intmodel.Space) (intmodel.Space, error) {
	f.createSpaceCalls++
	return space, nil
}

func (f *absentParentsRunner) CreateStack(stack intmodel.Stack) (intmodel.Stack, error) {
	f.createStackCalls++
	return stack, nil
}

// TestReconcileCell_InvalidIdentity_NoParentSideEffects pins #1445: an apply
// of a cell with an invalid identity must be rejected before
// ensureCellParents runs, so no realm/space/stack (and therefore no cgroup,
// bridge or KUKE-EGR-* firewall state) is materialized.
func TestReconcileCell_InvalidIdentity_NoParentSideEffects(t *testing.T) {
	valid := func() intmodel.Cell {
		return intmodel.Cell{
			Metadata: intmodel.CellMetadata{Name: "good-cell"},
			Spec: intmodel.CellSpec{
				RealmName: "default",
				SpaceName: "default",
				StackName: "default",
				Containers: []intmodel.ContainerSpec{
					{ID: "root", Root: true, Image: "busybox:latest"},
				},
			},
		}
	}
	tests := []struct {
		name    string
		mutate  func(c *intmodel.Cell)
		wantErr error
	}{
		{"cell name with underscore", func(c *intmodel.Cell) { c.Metadata.Name = "bad_cell" }, errdefs.ErrInvalidName},
		{"cell name with slash", func(c *intmodel.Cell) { c.Metadata.Name = "bad/cell" }, errdefs.ErrInvalidName},
		{"empty cell name", func(c *intmodel.Cell) { c.Metadata.Name = "  " }, errdefs.ErrCellNameRequired},
		{"empty realm", func(c *intmodel.Cell) { c.Spec.RealmName = "" }, errdefs.ErrRealmNameRequired},
		{"invalid realm", func(c *intmodel.Cell) { c.Spec.RealmName = "bad_realm" }, errdefs.ErrInvalidRealmName},
		{"empty space", func(c *intmodel.Cell) { c.Spec.SpaceName = "" }, errdefs.ErrSpaceNameRequired},
		{"invalid space", func(c *intmodel.Cell) { c.Spec.SpaceName = "bad_space" }, errdefs.ErrInvalidName},
		{"empty stack", func(c *intmodel.Cell) { c.Spec.StackName = "" }, errdefs.ErrStackNameRequired},
		{"invalid stack", func(c *intmodel.Cell) { c.Spec.StackName = "bad/stack" }, errdefs.ErrInvalidName},
		{
			"empty container id",
			func(c *intmodel.Cell) { c.Spec.Containers[0].ID = "" },
			errdefs.ErrContainerNameRequired,
		},
		{
			"invalid container id",
			func(c *intmodel.Cell) { c.Spec.Containers[0].ID = "bad_ctr" },
			errdefs.ErrInvalidName,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			desired := valid()
			tt.mutate(&desired)

			r := &absentParentsRunner{}
			_, err := apply.ReconcileCell(r, desired)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ReconcileCell error = %v, want errors.Is %v", err, tt.wantErr)
			}
			if r.createRealmCalls != 0 || r.createSpaceCalls != 0 || r.createStackCalls != 0 {
				t.Errorf("rejected cell materialized parents: CreateRealm=%d CreateSpace=%d CreateStack=%d",
					r.createRealmCalls, r.createSpaceCalls, r.createStackCalls)
			}
		})
	}
}
