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
	"testing"

	applypkg "github.com/eminwux/kukeon/internal/controller/apply"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// TestReconcileBlueprint_WritesWhenScopeExists is the issue #620 happy path:
// with the realm reachable, ReconcileBlueprint writes the document and reports
// created.
func TestReconcileBlueprint_WritesWhenScopeExists(t *testing.T) {
	var wrote []byte
	f := &fakeRunner{
		GetRealmFn: func(realm intmodel.Realm) (intmodel.Realm, error) { return realm, nil },
		WriteBlueprintFn: func(bp intmodel.CellBlueprint) (bool, error) {
			wrote = bp.Document
			return true, nil
		},
	}

	desired := intmodel.CellBlueprint{
		Metadata: intmodel.CellBlueprintMetadata{Name: "web", Realm: "default"},
		Document: []byte("body"),
	}
	result, err := applypkg.ReconcileBlueprint(f, desired)
	if err != nil {
		t.Fatalf("ReconcileBlueprint() error = %v", err)
	}
	if result.Action != "created" {
		t.Errorf("action = %q, want created", result.Action)
	}
	if string(wrote) != "body" {
		t.Errorf("WriteBlueprint got document %q, want body", wrote)
	}
}

// TestReconcileBlueprint_RejectsMissingScope confirms the scope-reachability
// gate: a realm-not-found surfaces ErrBlueprintScopeNotFound and WriteBlueprint
// is never reached.
func TestReconcileBlueprint_RejectsMissingScope(t *testing.T) {
	var wrote bool
	f := &fakeRunner{
		GetRealmFn: func(intmodel.Realm) (intmodel.Realm, error) {
			return intmodel.Realm{}, errdefs.ErrRealmNotFound
		},
		WriteBlueprintFn: func(intmodel.CellBlueprint) (bool, error) {
			wrote = true
			return false, nil
		},
	}

	desired := intmodel.CellBlueprint{
		Metadata: intmodel.CellBlueprintMetadata{Name: "web", Realm: "ghost"},
		Document: []byte("x"),
	}
	_, err := applypkg.ReconcileBlueprint(f, desired)
	if !errors.Is(err, errdefs.ErrBlueprintScopeNotFound) {
		t.Fatalf("err = %v, want ErrBlueprintScopeNotFound", err)
	}
	if wrote {
		t.Error("WriteBlueprint was called despite a missing scope")
	}
}

// TestReconcileBlueprint_ChecksSpaceScope confirms a space-scoped blueprint
// verifies the space coordinate and rejects when it is missing. (Blueprints
// are never cell-scoped, so the walk stops at the stack.)
func TestReconcileBlueprint_ChecksSpaceScope(t *testing.T) {
	f := &fakeRunner{
		GetRealmFn: func(realm intmodel.Realm) (intmodel.Realm, error) { return realm, nil },
		GetSpaceFn: func(intmodel.Space) (intmodel.Space, error) {
			return intmodel.Space{}, errdefs.ErrSpaceNotFound
		},
		WriteBlueprintFn: func(intmodel.CellBlueprint) (bool, error) { return false, nil },
	}

	desired := intmodel.CellBlueprint{
		Metadata: intmodel.CellBlueprintMetadata{Name: "web", Realm: "default", Space: "ghost"},
		Document: []byte("x"),
	}
	_, err := applypkg.ReconcileBlueprint(f, desired)
	if !errors.Is(err, errdefs.ErrBlueprintScopeNotFound) {
		t.Fatalf("err = %v, want ErrBlueprintScopeNotFound for missing space", err)
	}
}
