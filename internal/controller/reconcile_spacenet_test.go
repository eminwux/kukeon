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
	"sort"
	"strings"
	"sync"
	"testing"

	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// TestReconcileSpaceNetworks_WalksEverySpaceAndEnsures is #1074 AC #1/#2: the
// pass walks every realm/space and re-asserts each space's network
// desired-state via the idempotent EnsureSpace helper (which fans out to the
// CNI conflist/bridge + egress-policy apply helpers).
func TestReconcileSpaceNetworks_WalksEverySpaceAndEnsures(t *testing.T) {
	realmA := buildTestRealm("realm-a", "")
	realmB := buildTestRealm("realm-b", "")
	spaceA1 := buildTestSpace("space-a1", "realm-a")
	spaceA2 := buildTestSpace("space-a2", "realm-a")
	spaceB1 := buildTestSpace("space-b1", "realm-b")

	var mu sync.Mutex
	var ensured []string
	mock := &fakeRunner{
		ListRealmsFn: func() ([]intmodel.Realm, error) {
			return []intmodel.Realm{realmA, realmB}, nil
		},
		ListSpacesFn: func(realm string) ([]intmodel.Space, error) {
			switch realm {
			case "realm-a":
				return []intmodel.Space{spaceA1, spaceA2}, nil
			case "realm-b":
				return []intmodel.Space{spaceB1}, nil
			}
			return nil, nil
		},
		EnsureSpaceFn: func(space intmodel.Space) (intmodel.Space, error) {
			mu.Lock()
			ensured = append(ensured, space.Spec.RealmName+"/"+space.Metadata.Name)
			mu.Unlock()
			return space, nil
		},
	}

	ctrl := setupTestController(t, mock)
	res, err := ctrl.ReconcileSpaceNetworks()
	if err != nil {
		t.Fatalf("ReconcileSpaceNetworks() error = %v", err)
	}
	if res.SpacesScanned != 3 {
		t.Errorf("SpacesScanned: got %d, want 3", res.SpacesScanned)
	}
	if res.SpacesErrored != 0 {
		t.Errorf("SpacesErrored: got %d, want 0", res.SpacesErrored)
	}
	if len(res.Errors) != 0 {
		t.Errorf("Errors: got %v, want empty", res.Errors)
	}

	sort.Strings(ensured)
	want := []string{"realm-a/space-a1", "realm-a/space-a2", "realm-b/space-b1"}
	if strings.Join(ensured, ",") != strings.Join(want, ",") {
		t.Errorf("EnsureSpace called for: got %v, want %v", ensured, want)
	}
}

// TestReconcileSpaceNetworks_PerSpaceErrorDoesNotAbortWalk confirms a single
// space whose EnsureSpace fails is recorded but does not silence the rest of
// the host — the loop must keep converging every other space (#1074).
func TestReconcileSpaceNetworks_PerSpaceErrorDoesNotAbortWalk(t *testing.T) {
	realm := buildTestRealm("realm-a", "")
	good := buildTestSpace("good", "realm-a")
	bad := buildTestSpace("bad", "realm-a")
	alsoGood := buildTestSpace("also-good", "realm-a")

	var ensuredCount int
	mock := &fakeRunner{
		ListRealmsFn: func() ([]intmodel.Realm, error) {
			return []intmodel.Realm{realm}, nil
		},
		ListSpacesFn: func(_ string) ([]intmodel.Space, error) {
			return []intmodel.Space{good, bad, alsoGood}, nil
		},
		EnsureSpaceFn: func(space intmodel.Space) (intmodel.Space, error) {
			ensuredCount++
			if space.Metadata.Name == "bad" {
				return intmodel.Space{}, errors.New("synthetic egress apply failure")
			}
			return space, nil
		},
	}

	ctrl := setupTestController(t, mock)
	res, err := ctrl.ReconcileSpaceNetworks()
	if err != nil {
		t.Fatalf("ReconcileSpaceNetworks() error = %v", err)
	}
	if ensuredCount != 3 {
		t.Errorf("EnsureSpace calls: got %d, want 3 (walk must continue past the bad space)", ensuredCount)
	}
	if res.SpacesScanned != 3 {
		t.Errorf("SpacesScanned: got %d, want 3", res.SpacesScanned)
	}
	if res.SpacesErrored != 1 {
		t.Errorf("SpacesErrored: got %d, want 1", res.SpacesErrored)
	}
	if len(res.Errors) != 1 || !strings.Contains(res.Errors[0], "realm-a/bad") {
		t.Errorf("Errors: got %v, want one entry naming realm-a/bad", res.Errors)
	}
}

// TestReconcileSpaceNetworks_ListRealmsErrorIsRecorded confirms a top-level
// list failure is captured in Errors and returns a nil error so the daemon
// loop keeps ticking (mirrors ReconcileCells).
func TestReconcileSpaceNetworks_ListRealmsErrorIsRecorded(t *testing.T) {
	mock := &fakeRunner{
		ListRealmsFn: func() ([]intmodel.Realm, error) {
			return nil, errors.New("containerd unavailable")
		},
	}

	ctrl := setupTestController(t, mock)
	res, err := ctrl.ReconcileSpaceNetworks()
	if err != nil {
		t.Fatalf("ReconcileSpaceNetworks() error = %v, want nil (errors surface via Errors)", err)
	}
	if res.SpacesScanned != 0 {
		t.Errorf("SpacesScanned: got %d, want 0", res.SpacesScanned)
	}
	if len(res.Errors) != 1 || !strings.Contains(res.Errors[0], "list realms") {
		t.Errorf("Errors: got %v, want one list-realms entry", res.Errors)
	}
}

// TestReconcileSpaceNetworks_ListSpacesErrorSkipsRealm confirms a per-realm
// ListSpaces failure is recorded and the walk continues into sibling realms.
func TestReconcileSpaceNetworks_ListSpacesErrorSkipsRealm(t *testing.T) {
	realmA := buildTestRealm("realm-a", "")
	realmB := buildTestRealm("realm-b", "")
	spaceB := buildTestSpace("space-b", "realm-b")

	var ensured []string
	mock := &fakeRunner{
		ListRealmsFn: func() ([]intmodel.Realm, error) {
			return []intmodel.Realm{realmA, realmB}, nil
		},
		ListSpacesFn: func(realm string) ([]intmodel.Space, error) {
			if realm == "realm-a" {
				return nil, errors.New("metadata dir unreadable")
			}
			return []intmodel.Space{spaceB}, nil
		},
		EnsureSpaceFn: func(space intmodel.Space) (intmodel.Space, error) {
			ensured = append(ensured, space.Metadata.Name)
			return space, nil
		},
	}

	ctrl := setupTestController(t, mock)
	res, err := ctrl.ReconcileSpaceNetworks()
	if err != nil {
		t.Fatalf("ReconcileSpaceNetworks() error = %v", err)
	}
	if res.SpacesScanned != 1 {
		t.Errorf("SpacesScanned: got %d, want 1 (realm-b only)", res.SpacesScanned)
	}
	if len(ensured) != 1 || ensured[0] != "space-b" {
		t.Errorf("EnsureSpace called for: got %v, want [space-b]", ensured)
	}
	if len(res.Errors) != 1 || !strings.Contains(res.Errors[0], "realm-a") {
		t.Errorf("Errors: got %v, want one entry naming realm-a", res.Errors)
	}
}
