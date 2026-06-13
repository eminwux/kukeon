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

	"github.com/eminwux/kukeon/internal/apply/parser"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// TestGetVolume_ReportsNotFoundWithoutError pins the GetBlueprint-shaped
// contract: an absent volume yields MetadataExists=false and a nil error.
func TestGetVolume_ReportsNotFoundWithoutError(t *testing.T) {
	mockRunner := &fakeRunner{
		GetVolumeFn: func(intmodel.Volume) (intmodel.Volume, error) {
			return intmodel.Volume{}, errdefs.ErrVolumeNotFound
		},
	}
	ctrl := setupTestController(t, mockRunner)

	res, err := ctrl.GetVolume(intmodel.Volume{
		Metadata: intmodel.VolumeMetadata{Name: "data", Realm: "default"},
	})
	if err != nil {
		t.Fatalf("GetVolume() error = %v, want nil", err)
	}
	if res.MetadataExists {
		t.Errorf("MetadataExists = true, want false for absent volume")
	}
}

// TestGetVolume_ReturnsMetadata confirms a present volume surfaces its
// metadata-only view.
func TestGetVolume_ReturnsMetadata(t *testing.T) {
	mockRunner := &fakeRunner{
		GetVolumeFn: func(v intmodel.Volume) (intmodel.Volume, error) {
			return intmodel.Volume{Metadata: v.Metadata}, nil
		},
	}
	ctrl := setupTestController(t, mockRunner)

	res, err := ctrl.GetVolume(intmodel.Volume{
		Metadata: intmodel.VolumeMetadata{Name: "data", Realm: "default"},
	})
	if err != nil {
		t.Fatalf("GetVolume() error = %v", err)
	}
	if !res.MetadataExists {
		t.Fatalf("MetadataExists = false, want true")
	}
	if res.Volume.Metadata.Name != "data" {
		t.Errorf("name = %q, want data", res.Volume.Metadata.Name)
	}
}

// TestGetVolume_NameRequired confirms the lookup scope guard fires before the
// runner is touched.
func TestGetVolume_NameRequired(t *testing.T) {
	ctrl := setupTestController(t, &fakeRunner{})
	_, err := ctrl.GetVolume(intmodel.Volume{Metadata: intmodel.VolumeMetadata{Realm: "default"}})
	if !errors.Is(err, errdefs.ErrVolumeNameRequired) {
		t.Errorf("GetVolume(no name) = %v, want ErrVolumeNameRequired", err)
	}
}

// TestDeleteVolume_NotFound confirms an absent volume surfaces a clear "not
// found" error rather than a silent success.
func TestDeleteVolume_NotFound(t *testing.T) {
	mockRunner := &fakeRunner{
		DeleteVolumeFn: func(intmodel.Volume) error { return errdefs.ErrVolumeNotFound },
	}
	ctrl := setupTestController(t, mockRunner)

	res, err := ctrl.DeleteVolume(intmodel.Volume{
		Metadata: intmodel.VolumeMetadata{Name: "data", Realm: "default"},
	})
	if err == nil {
		t.Fatalf("DeleteVolume(absent) error = nil, want not-found")
	}
	if res.Deleted {
		t.Errorf("Deleted = true for absent volume")
	}
}

// TestDeleteVolume_Success confirms a present volume is removed and reported.
func TestDeleteVolume_Success(t *testing.T) {
	mockRunner := &fakeRunner{
		DeleteVolumeFn: func(intmodel.Volume) error { return nil },
	}
	ctrl := setupTestController(t, mockRunner)

	res, err := ctrl.DeleteVolume(intmodel.Volume{
		Metadata: intmodel.VolumeMetadata{Name: "data", Realm: "default"},
	})
	if err != nil {
		t.Fatalf("DeleteVolume() error = %v", err)
	}
	if !res.Deleted {
		t.Errorf("Deleted = false, want true")
	}
}

// TestListVolumes_FilterGuard confirms a gap in the scope filter (space set
// without realm) is rejected before the runner is touched.
func TestListVolumes_FilterGuard(t *testing.T) {
	ctrl := setupTestController(t, &fakeRunner{})
	_, err := ctrl.ListVolumes("", "agents", "")
	if !errors.Is(err, errdefs.ErrVolumeScopeIncomplete) {
		t.Errorf("ListVolumes(space without realm) = %v, want ErrVolumeScopeIncomplete", err)
	}
}

// TestListVolumes_PassesThrough confirms a valid filter reaches the runner and
// returns its result.
func TestListVolumes_PassesThrough(t *testing.T) {
	mockRunner := &fakeRunner{
		ListVolumesFn: func(realm, _, _ string) ([]intmodel.Volume, error) {
			return []intmodel.Volume{{Metadata: intmodel.VolumeMetadata{Name: "data", Realm: realm}}}, nil
		},
	}
	ctrl := setupTestController(t, mockRunner)

	got, err := ctrl.ListVolumes("default", "", "")
	if err != nil {
		t.Fatalf("ListVolumes() error = %v", err)
	}
	if len(got) != 1 || got[0].Metadata.Name != "data" {
		t.Errorf("ListVolumes() = %+v, want one volume named data", got)
	}
}

// TestApplyDocuments_VolumeRoutesToReconcile pins the AC end-to-end: a parsed
// `kind: Volume` document routes through ApplyDocuments → ReconcileVolume →
// WriteVolume and surfaces a "created" resource result.
func TestApplyDocuments_VolumeRoutesToReconcile(t *testing.T) {
	var wrote []intmodel.Volume
	mock := &fakeRunner{
		GetRealmFn: func(r intmodel.Realm) (intmodel.Realm, error) { return r, nil },
		GetSpaceFn: func(s intmodel.Space) (intmodel.Space, error) { return s, nil },
		GetStackFn: func(s intmodel.Stack) (intmodel.Stack, error) { return s, nil },
		WriteVolumeFn: func(v intmodel.Volume) (bool, error) {
			wrote = append(wrote, v)
			return true, nil
		},
	}
	exec := setupTestController(t, mock)

	docs := []parser.Document{
		{
			Kind: v1beta1.KindVolume,
			VolumeDoc: &v1beta1.VolumeDoc{
				APIVersion: v1beta1.APIVersionV1Beta1,
				Kind:       v1beta1.KindVolume,
				Metadata:   v1beta1.VolumeMetadata{Name: "data", Realm: "default", Space: "agents"},
			},
		},
	}

	result, err := exec.ApplyDocuments(docs, "")
	if err != nil {
		t.Fatalf("ApplyDocuments() error = %v", err)
	}
	if len(wrote) != 1 || wrote[0].Metadata.Name != "data" {
		t.Fatalf("WriteVolume calls = %+v, want one volume named data", wrote)
	}
	if len(result.Resources) != 1 || result.Resources[0].Action != "created" {
		t.Errorf("apply result = %+v, want one created Volume", result.Resources)
	}
}
