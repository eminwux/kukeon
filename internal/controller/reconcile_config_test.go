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

	"github.com/eminwux/kukeon/internal/apischeme"
	applypkg "github.com/eminwux/kukeon/internal/controller/apply"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	ext "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// configCarrier serializes an external CellConfigDoc into the internal carrier
// ReconcileConfig consumes.
func configCarrier(t *testing.T, doc ext.CellConfigDoc) intmodel.CellConfig {
	t.Helper()
	carrier, err := apischeme.ConvertCellConfigDocToInternal(doc)
	if err != nil {
		t.Fatalf("ConvertCellConfigDocToInternal() error = %v", err)
	}
	return carrier
}

// blueprintCarrier serializes an external CellBlueprintDoc into the internal
// carrier GetBlueprint hands back.
func blueprintCarrier(t *testing.T, doc ext.CellBlueprintDoc) intmodel.CellBlueprint {
	t.Helper()
	carrier, err := apischeme.ConvertCellBlueprintDocToInternal(doc)
	if err != nil {
		t.Fatalf("ConvertCellBlueprintDocToInternal() error = %v", err)
	}
	return carrier
}

func sampleConfig() ext.CellConfigDoc {
	return ext.CellConfigDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindCellConfig,
		Metadata:   ext.CellConfigMetadata{Name: "kukeon-dev", Realm: "kuke-system"},
		Spec: ext.CellConfigSpec{
			Blueprint: ext.CellConfigBlueprintRef{Name: "dev", Realm: "kuke-system"},
			Repos: map[string]ext.CellConfigRepoFill{
				"project": {URL: "git@github.com:eminwux/kukeon.git"},
			},
		},
	}
}

func sampleReferencedBlueprint() ext.CellBlueprintDoc {
	return ext.CellBlueprintDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindCellBlueprint,
		Metadata:   ext.CellBlueprintMetadata{Name: "dev", Realm: "kuke-system"},
		Spec: ext.CellBlueprintSpec{
			Cell: ext.BlueprintCellSpec{
				Containers: []ext.BlueprintContainer{
					{ID: "main", Image: "img", Repos: []ext.ContainerRepo{
						{Name: "project", Target: "/work", Required: true},
					}},
				},
			},
		},
	}
}

// TestReconcileConfig_WritesWhenScopeAndBlueprintExist is the issue #624 happy
// path: scope reachable, referenced blueprint resolves, required slot filled →
// the document is written and the action is created.
func TestReconcileConfig_WritesWhenScopeAndBlueprintExist(t *testing.T) {
	var wrote []byte
	f := &fakeRunner{
		GetRealmFn: func(realm intmodel.Realm) (intmodel.Realm, error) { return realm, nil },
		GetBlueprintFn: func(intmodel.CellBlueprint) (intmodel.CellBlueprint, error) {
			return blueprintCarrier(t, sampleReferencedBlueprint()), nil
		},
		WriteConfigFn: func(c intmodel.CellConfig) (bool, error) {
			wrote = c.Document
			return true, nil
		},
	}

	result, err := applypkg.ReconcileConfig(f, configCarrier(t, sampleConfig()))
	if err != nil {
		t.Fatalf("ReconcileConfig() error = %v", err)
	}
	if result.Action != "created" {
		t.Errorf("action = %q, want created", result.Action)
	}
	if len(wrote) == 0 {
		t.Error("WriteConfig got an empty document")
	}
}

// TestReconcileConfig_RejectsMissingScope confirms the scope-reachability gate:
// a realm-not-found surfaces ErrConfigScopeNotFound and WriteConfig is never
// reached.
func TestReconcileConfig_RejectsMissingScope(t *testing.T) {
	var wrote bool
	f := &fakeRunner{
		GetRealmFn: func(intmodel.Realm) (intmodel.Realm, error) {
			return intmodel.Realm{}, errdefs.ErrRealmNotFound
		},
		WriteConfigFn: func(intmodel.CellConfig) (bool, error) { wrote = true; return false, nil },
	}

	_, err := applypkg.ReconcileConfig(f, configCarrier(t, sampleConfig()))
	if !errors.Is(err, errdefs.ErrConfigScopeNotFound) {
		t.Fatalf("err = %v, want ErrConfigScopeNotFound", err)
	}
	if wrote {
		t.Error("WriteConfig was called despite a missing scope")
	}
}

// TestReconcileConfig_RejectsMissingBlueprint confirms an absent referenced
// blueprint surfaces ErrConfigBlueprintNotFound and WriteConfig is never
// reached.
func TestReconcileConfig_RejectsMissingBlueprint(t *testing.T) {
	var wrote bool
	f := &fakeRunner{
		GetRealmFn: func(realm intmodel.Realm) (intmodel.Realm, error) { return realm, nil },
		GetBlueprintFn: func(intmodel.CellBlueprint) (intmodel.CellBlueprint, error) {
			return intmodel.CellBlueprint{}, errdefs.ErrBlueprintNotFound
		},
		WriteConfigFn: func(intmodel.CellConfig) (bool, error) { wrote = true; return false, nil },
	}

	_, err := applypkg.ReconcileConfig(f, configCarrier(t, sampleConfig()))
	if !errors.Is(err, errdefs.ErrConfigBlueprintNotFound) {
		t.Fatalf("err = %v, want ErrConfigBlueprintNotFound", err)
	}
	if wrote {
		t.Error("WriteConfig was called despite a missing blueprint")
	}
}

// TestCreateConfig_HappyPathWritesAtomicCreateOnly is the issue #839 happy
// path: scope reachable, blueprint resolves, slots valid → the runner's
// WriteConfigIfAbsent is called (not WriteConfig) and the action reports
// "created". Pinning WriteConfigIfAbsent specifically guards against a
// regression where CreateConfig falls back to the write-through path used by
// `kuke apply -f` — which would silently overwrite a colliding clone and
// defeat the concurrent-allocation AC.
func TestCreateConfig_HappyPathWritesAtomicCreateOnly(t *testing.T) {
	var atomicWrite, plainWrite int
	f := &fakeRunner{
		GetRealmFn: func(realm intmodel.Realm) (intmodel.Realm, error) { return realm, nil },
		GetBlueprintFn: func(intmodel.CellBlueprint) (intmodel.CellBlueprint, error) {
			return blueprintCarrier(t, sampleReferencedBlueprint()), nil
		},
		WriteConfigIfAbsentFn: func(intmodel.CellConfig) error {
			atomicWrite++
			return nil
		},
		WriteConfigFn: func(intmodel.CellConfig) (bool, error) {
			plainWrite++
			return true, nil
		},
	}

	result, err := applypkg.CreateConfig(f, configCarrier(t, sampleConfig()))
	if err != nil {
		t.Fatalf("CreateConfig() error = %v", err)
	}
	if result.Action != "created" {
		t.Errorf("action = %q, want created", result.Action)
	}
	if atomicWrite != 1 {
		t.Errorf("WriteConfigIfAbsent calls = %d, want 1", atomicWrite)
	}
	if plainWrite != 0 {
		t.Errorf("WriteConfig calls = %d, want 0 (CreateConfig must use the atomic path)", plainWrite)
	}
}

// TestCreateConfig_CollisionPropagatesErrConfigExists confirms the runner's
// EEXIST sentinel travels through CreateConfig unmodified. The CLI's
// gap-fill counter loop reads errdefs.ErrConfigExists to know it lost the
// race and should retry; wrapping or re-mapping the error would break that.
func TestCreateConfig_CollisionPropagatesErrConfigExists(t *testing.T) {
	f := &fakeRunner{
		GetRealmFn: func(realm intmodel.Realm) (intmodel.Realm, error) { return realm, nil },
		GetBlueprintFn: func(intmodel.CellBlueprint) (intmodel.CellBlueprint, error) {
			return blueprintCarrier(t, sampleReferencedBlueprint()), nil
		},
		WriteConfigIfAbsentFn: func(intmodel.CellConfig) error {
			return errdefs.ErrConfigExists
		},
	}

	_, err := applypkg.CreateConfig(f, configCarrier(t, sampleConfig()))
	if !errors.Is(err, errdefs.ErrConfigExists) {
		t.Fatalf("err = %v, want ErrConfigExists", err)
	}
}

// TestCreateConfig_RejectsMissingScope confirms scope-reachability gates
// CreateConfig the same way it gates ReconcileConfig — no clone can be
// allocated into a realm/space/stack that doesn't exist.
func TestCreateConfig_RejectsMissingScope(t *testing.T) {
	var wrote bool
	f := &fakeRunner{
		GetRealmFn: func(intmodel.Realm) (intmodel.Realm, error) {
			return intmodel.Realm{}, errdefs.ErrRealmNotFound
		},
		WriteConfigIfAbsentFn: func(intmodel.CellConfig) error {
			wrote = true
			return nil
		},
	}

	_, err := applypkg.CreateConfig(f, configCarrier(t, sampleConfig()))
	if !errors.Is(err, errdefs.ErrConfigScopeNotFound) {
		t.Fatalf("err = %v, want ErrConfigScopeNotFound", err)
	}
	if wrote {
		t.Error("WriteConfigIfAbsent was called despite a missing scope")
	}
}

// TestReconcileConfig_RejectsUnfilledRequiredSlot confirms slot-fill validation
// runs against the referenced blueprint: a required slot the config leaves
// unfilled surfaces ErrConfigRequiredSlotUnfilled, before any write.
func TestReconcileConfig_RejectsUnfilledRequiredSlot(t *testing.T) {
	var wrote bool
	cfg := sampleConfig()
	cfg.Spec.Repos = nil // leave the required "project" repo slot unfilled
	f := &fakeRunner{
		GetRealmFn: func(realm intmodel.Realm) (intmodel.Realm, error) { return realm, nil },
		GetBlueprintFn: func(intmodel.CellBlueprint) (intmodel.CellBlueprint, error) {
			return blueprintCarrier(t, sampleReferencedBlueprint()), nil
		},
		WriteConfigFn: func(intmodel.CellConfig) (bool, error) { wrote = true; return false, nil },
	}

	_, err := applypkg.ReconcileConfig(f, configCarrier(t, cfg))
	if !errors.Is(err, errdefs.ErrConfigRequiredSlotUnfilled) {
		t.Fatalf("err = %v, want ErrConfigRequiredSlotUnfilled", err)
	}
	if wrote {
		t.Error("WriteConfig was called despite an unfilled required slot")
	}
}
