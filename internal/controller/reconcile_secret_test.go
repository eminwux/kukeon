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

// TestReconcileSecret_WritesWhenScopeExists is the issue #619 happy path: with
// the realm reachable, ReconcileSecret writes the bytes and reports created.
func TestReconcileSecret_WritesWhenScopeExists(t *testing.T) {
	var wroteData string
	f := &fakeRunner{
		GetRealmFn: func(realm intmodel.Realm) (intmodel.Realm, error) { return realm, nil },
		WriteSecretFn: func(secret intmodel.Secret) (bool, error) {
			wroteData = secret.Spec.Data
			return true, nil
		},
	}

	desired := intmodel.Secret{
		Metadata: intmodel.SecretMetadata{Name: "tok", Realm: "kuke-system"},
		Spec:     intmodel.SecretSpec{Data: "s3cr3t"},
	}
	result, err := applypkg.ReconcileSecret(f, desired)
	if err != nil {
		t.Fatalf("ReconcileSecret() error = %v", err)
	}
	if result.Action != "created" {
		t.Errorf("action = %q, want created", result.Action)
	}
	if wroteData != "s3cr3t" {
		t.Errorf("WriteSecret got data %q, want s3cr3t", wroteData)
	}
}

// TestReconcileSecret_RejectsMissingScope confirms the AC's scope-reachability
// gate: a realm-not-found surfaces ErrSecretScopeNotFound and WriteSecret is
// never reached (no auto-create of the scope, unlike the hierarchy reconcilers).
func TestReconcileSecret_RejectsMissingScope(t *testing.T) {
	var wrote bool
	f := &fakeRunner{
		GetRealmFn: func(intmodel.Realm) (intmodel.Realm, error) {
			return intmodel.Realm{}, errdefs.ErrRealmNotFound
		},
		WriteSecretFn: func(intmodel.Secret) (bool, error) {
			wrote = true
			return false, nil
		},
	}

	desired := intmodel.Secret{
		Metadata: intmodel.SecretMetadata{Name: "tok", Realm: "ghost"},
		Spec:     intmodel.SecretSpec{Data: "x"},
	}
	_, err := applypkg.ReconcileSecret(f, desired)
	if !errors.Is(err, errdefs.ErrSecretScopeNotFound) {
		t.Fatalf("err = %v, want ErrSecretScopeNotFound", err)
	}
	if wrote {
		t.Error("WriteSecret was called despite a missing scope")
	}
}

// TestReconcileSecret_ChecksDeepestScope confirms a cell-scoped secret verifies
// every coordinate down to the cell, and a missing cell rejects.
func TestReconcileSecret_ChecksDeepestScope(t *testing.T) {
	f := &fakeRunner{
		GetRealmFn: func(realm intmodel.Realm) (intmodel.Realm, error) { return realm, nil },
		GetSpaceFn: func(space intmodel.Space) (intmodel.Space, error) { return space, nil },
		GetStackFn: func(stack intmodel.Stack) (intmodel.Stack, error) { return stack, nil },
		GetCellFn: func(intmodel.Cell) (intmodel.Cell, error) {
			return intmodel.Cell{}, errdefs.ErrCellNotFound
		},
		WriteSecretFn: func(intmodel.Secret) (bool, error) { return false, nil },
	}

	desired := intmodel.Secret{
		Metadata: intmodel.SecretMetadata{
			Name: "tok", Realm: "default", Space: "team-a", Stack: "web", Cell: "api",
		},
		Spec: intmodel.SecretSpec{Data: "x"},
	}
	_, err := applypkg.ReconcileSecret(f, desired)
	if !errors.Is(err, errdefs.ErrSecretScopeNotFound) {
		t.Fatalf("err = %v, want ErrSecretScopeNotFound for missing cell", err)
	}
}
