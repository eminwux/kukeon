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

// volumeFakeRunner satisfies runner.Runner via the embedded interface and
// overrides only the methods ReconcileVolume exercises: the scope Gets and
// WriteVolume. realmExists/spaceExists/stackExists gate the scope lookups so a
// test can simulate a missing scope; writeCreated drives the create-vs-update
// branch.
type volumeFakeRunner struct {
	runner.Runner

	realmErr error
	spaceErr error
	stackErr error

	writeCreated bool
	writeErr     error
	writeCalled  bool
	wrote        intmodel.Volume
}

func (f *volumeFakeRunner) GetRealm(realm intmodel.Realm) (intmodel.Realm, error) {
	return realm, f.realmErr
}

func (f *volumeFakeRunner) GetSpace(space intmodel.Space) (intmodel.Space, error) {
	return space, f.spaceErr
}

func (f *volumeFakeRunner) GetStack(stack intmodel.Stack) (intmodel.Stack, error) {
	return stack, f.stackErr
}

func (f *volumeFakeRunner) WriteVolume(volume intmodel.Volume) (bool, error) {
	f.writeCalled = true
	f.wrote = volume
	return f.writeCreated, f.writeErr
}

// TestReconcileVolume_CreateReportsCreated confirms a first apply provisions the
// volume and reports "created".
func TestReconcileVolume_CreateReportsCreated(t *testing.T) {
	f := &volumeFakeRunner{writeCreated: true}
	desired := intmodel.Volume{Metadata: intmodel.VolumeMetadata{Name: "data", Realm: "r1", Space: "s1"}}

	res, err := apply.ReconcileVolume(f, desired)
	if err != nil {
		t.Fatalf("ReconcileVolume() error = %v", err)
	}
	if !f.writeCalled {
		t.Errorf("WriteVolume was not called")
	}
	if res.Action != "created" {
		t.Errorf("Action = %q, want created", res.Action)
	}
	if res.Kind != "Volume" {
		t.Errorf("Kind = %q, want Volume", res.Kind)
	}
}

// TestReconcileVolume_ReapplyReportsUpdated confirms a re-apply (WriteVolume
// returns created=false) reports "updated" — the idempotency contract.
func TestReconcileVolume_ReapplyReportsUpdated(t *testing.T) {
	f := &volumeFakeRunner{writeCreated: false}
	desired := intmodel.Volume{Metadata: intmodel.VolumeMetadata{Name: "data", Realm: "r1"}}

	res, err := apply.ReconcileVolume(f, desired)
	if err != nil {
		t.Fatalf("ReconcileVolume() error = %v", err)
	}
	if res.Action != "updated" {
		t.Errorf("Action = %q, want updated", res.Action)
	}
}

// TestReconcileVolume_MissingScopeRejected confirms an unreachable scope is an
// apply-time error and WriteVolume is never called — a Volume never auto-creates
// its scope.
func TestReconcileVolume_MissingScopeRejected(t *testing.T) {
	f := &volumeFakeRunner{spaceErr: errdefs.ErrSpaceNotFound}
	desired := intmodel.Volume{Metadata: intmodel.VolumeMetadata{Name: "data", Realm: "r1", Space: "missing"}}

	_, err := apply.ReconcileVolume(f, desired)
	if !errors.Is(err, errdefs.ErrVolumeScopeNotFound) {
		t.Fatalf("ReconcileVolume() error = %v, want ErrVolumeScopeNotFound", err)
	}
	if f.writeCalled {
		t.Errorf("WriteVolume must not be called when the scope is missing")
	}
}
