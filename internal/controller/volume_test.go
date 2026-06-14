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
	"strings"
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

// TestGetVolume_RejectsUnsafeLookupSegments confirms imperative lookups apply
// the same path-segment guard as the apply parser before reaching the runner.
func TestGetVolume_RejectsUnsafeLookupSegments(t *testing.T) {
	ctrl := setupTestController(t, &fakeRunner{})

	tests := []struct {
		name string
		md   intmodel.VolumeMetadata
	}{
		{
			name: "name slash",
			md:   intmodel.VolumeMetadata{Name: "data/escape", Realm: "default"},
		},
		{
			name: "realm dotdot",
			md:   intmodel.VolumeMetadata{Name: "data", Realm: ".."},
		},
		{
			name: "space backslash",
			md:   intmodel.VolumeMetadata{Name: "data", Realm: "default", Space: `bad\space`},
		},
		{
			name: "stack nul",
			md:   intmodel.VolumeMetadata{Name: "data", Realm: "default", Space: "agents", Stack: "stack\x00bad"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ctrl.GetVolume(intmodel.Volume{Metadata: tc.md})
			if !errors.Is(err, errdefs.ErrVolumeCoordUnsafe) {
				t.Fatalf("GetVolume() error = %v, want ErrVolumeCoordUnsafe", err)
			}
		})
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

// TestDeleteVolume_RejectsUnsafeLookupSegment confirms delete uses the shared
// lookup guard before a raw name can reach fs.VolumePath via the runner.
func TestDeleteVolume_RejectsUnsafeLookupSegment(t *testing.T) {
	ctrl := setupTestController(t, &fakeRunner{})
	_, err := ctrl.DeleteVolume(intmodel.Volume{
		Metadata: intmodel.VolumeMetadata{Name: "..", Realm: "default"},
	})
	if !errors.Is(err, errdefs.ErrVolumeCoordUnsafe) {
		t.Errorf("DeleteVolume(unsafe name) = %v, want ErrVolumeCoordUnsafe", err)
	}
}

// TestDeleteVolume_InUseRefused pins AC #7 (the delete gate): a Volume mounted
// by a running cell cannot be deleted. The controller refuses with
// ErrVolumeInUse naming the mounting cell, and the runner's DeleteVolume is
// never reached — the directory is left intact under the live mount.
func TestDeleteVolume_InUseRefused(t *testing.T) {
	deleteCalled := false
	mockRunner := &fakeRunner{
		VolumeMountedByLiveCellFn: func(intmodel.Volume) (string, bool, error) {
			return "default/app/web/db", true, nil
		},
		DeleteVolumeFn: func(intmodel.Volume) error {
			deleteCalled = true
			return nil
		},
	}
	ctrl := setupTestController(t, mockRunner)

	res, err := ctrl.DeleteVolume(intmodel.Volume{
		Metadata: intmodel.VolumeMetadata{Name: "data", Realm: "default"},
	})
	if !errors.Is(err, errdefs.ErrVolumeInUse) {
		t.Fatalf("DeleteVolume(in-use) error = %v, want ErrVolumeInUse", err)
	}
	if !strings.Contains(err.Error(), "default/app/web/db") {
		t.Errorf("error %q does not name the mounting cell", err)
	}
	if res.Deleted {
		t.Errorf("Deleted = true for an in-use volume")
	}
	if deleteCalled {
		t.Errorf("runner.DeleteVolume was called despite the live-mount gate")
	}
}

// TestDeleteVolume_NotMountedDeletes confirms the gate is a no-op when no
// running cell mounts the volume — the delete proceeds to the runner.
func TestDeleteVolume_NotMountedDeletes(t *testing.T) {
	mockRunner := &fakeRunner{
		VolumeMountedByLiveCellFn: func(intmodel.Volume) (string, bool, error) {
			return "", false, nil
		},
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

// TestListVolumes_RejectsUnsafeFilterSegment confirms a `..` filter coordinate
// is rejected before the runner is touched (issue #1289), bringing the list
// path to parity with the single-volume lookup path.
func TestListVolumes_RejectsUnsafeFilterSegment(t *testing.T) {
	ctrl := setupTestController(t, &fakeRunner{})
	_, err := ctrl.ListVolumes("default", "..", "")
	if !errors.Is(err, errdefs.ErrVolumeCoordUnsafe) {
		t.Errorf("ListVolumes(unsafe space) = %v, want ErrVolumeCoordUnsafe", err)
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

// TestCreateVolume_Provisions confirms the imperative create path routes
// through ReconcileVolume → WriteVolume and reports created=true.
func TestCreateVolume_Provisions(t *testing.T) {
	var wrote []intmodel.Volume
	mock := &fakeRunner{
		GetRealmFn: func(r intmodel.Realm) (intmodel.Realm, error) { return r, nil },
		GetSpaceFn: func(s intmodel.Space) (intmodel.Space, error) { return s, nil },
		WriteVolumeFn: func(v intmodel.Volume) (bool, error) {
			wrote = append(wrote, v)
			return true, nil
		},
	}
	ctrl := setupTestController(t, mock)

	res, err := ctrl.CreateVolume(intmodel.Volume{
		Metadata: intmodel.VolumeMetadata{Name: "data", Realm: "default", Space: "agents"},
	})
	if err != nil {
		t.Fatalf("CreateVolume() error = %v", err)
	}
	if !res.Created {
		t.Errorf("Created = false, want true")
	}
	if len(wrote) != 1 || wrote[0].Metadata.Name != "data" {
		t.Errorf("WriteVolume calls = %+v, want one volume named data", wrote)
	}
}

// TestCreateVolume_ReReportsUpdated confirms re-creating an existing volume
// reports created=false (WriteVolume is idempotent).
func TestCreateVolume_ReReportsUpdated(t *testing.T) {
	mock := &fakeRunner{
		GetRealmFn:    func(r intmodel.Realm) (intmodel.Realm, error) { return r, nil },
		WriteVolumeFn: func(intmodel.Volume) (bool, error) { return false, nil },
	}
	ctrl := setupTestController(t, mock)

	res, err := ctrl.CreateVolume(intmodel.Volume{
		Metadata: intmodel.VolumeMetadata{Name: "data", Realm: "default"},
	})
	if err != nil {
		t.Fatalf("CreateVolume() error = %v", err)
	}
	if res.Created {
		t.Errorf("Created = true, want false for re-create")
	}
}

// TestCreateVolume_NameRequired confirms the lookup scope guard fires before
// the runner is touched.
func TestCreateVolume_NameRequired(t *testing.T) {
	ctrl := setupTestController(t, &fakeRunner{})
	_, err := ctrl.CreateVolume(intmodel.Volume{Metadata: intmodel.VolumeMetadata{Realm: "default"}})
	if !errors.Is(err, errdefs.ErrVolumeNameRequired) {
		t.Errorf("CreateVolume(no name) = %v, want ErrVolumeNameRequired", err)
	}
}

// TestCreateVolume_RejectsUnsafeLookupSegment confirms create also shares the
// daemon-side segment guard before ReconcileVolume reaches WriteVolume.
func TestCreateVolume_RejectsUnsafeLookupSegment(t *testing.T) {
	ctrl := setupTestController(t, &fakeRunner{})
	_, err := ctrl.CreateVolume(intmodel.Volume{
		Metadata: intmodel.VolumeMetadata{Name: "data", Realm: "default", Space: "."},
	})
	if !errors.Is(err, errdefs.ErrVolumeCoordUnsafe) {
		t.Errorf("CreateVolume(unsafe space) = %v, want ErrVolumeCoordUnsafe", err)
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
