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

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// TestGetBlueprint_ReportsNotFoundWithoutError pins the GetSecret-shaped
// contract: an absent blueprint yields MetadataExists=false and a nil error.
func TestGetBlueprint_ReportsNotFoundWithoutError(t *testing.T) {
	mockRunner := &fakeRunner{
		GetBlueprintFn: func(intmodel.CellBlueprint) (intmodel.CellBlueprint, error) {
			return intmodel.CellBlueprint{}, errdefs.ErrBlueprintNotFound
		},
	}
	ctrl := setupTestController(t, mockRunner)

	res, err := ctrl.GetBlueprint(intmodel.CellBlueprint{
		Metadata: intmodel.CellBlueprintMetadata{Name: "web", Realm: "default"},
	})
	if err != nil {
		t.Fatalf("GetBlueprint() error = %v, want nil", err)
	}
	if res.MetadataExists {
		t.Errorf("MetadataExists = true, want false for absent blueprint")
	}
}

// TestGetBlueprint_ReturnsFullDocument confirms a present blueprint surfaces its
// full document (unlike GetSecret, which is metadata-only).
func TestGetBlueprint_ReturnsFullDocument(t *testing.T) {
	mockRunner := &fakeRunner{
		GetBlueprintFn: func(bp intmodel.CellBlueprint) (intmodel.CellBlueprint, error) {
			return intmodel.CellBlueprint{Metadata: bp.Metadata, Document: []byte("body")}, nil
		},
	}
	ctrl := setupTestController(t, mockRunner)

	res, err := ctrl.GetBlueprint(intmodel.CellBlueprint{
		Metadata: intmodel.CellBlueprintMetadata{Name: "web", Realm: "default", Space: "team-a"},
	})
	if err != nil {
		t.Fatalf("GetBlueprint() error = %v", err)
	}
	if !res.MetadataExists {
		t.Fatal("MetadataExists = false, want true")
	}
	if string(res.Blueprint.Document) != "body" {
		t.Errorf("Document = %q, want body", res.Blueprint.Document)
	}
}

func TestGetBlueprint_ValidatesScope(t *testing.T) {
	ctrl := setupTestController(t, &fakeRunner{})

	tests := []struct {
		name string
		md   intmodel.CellBlueprintMetadata
		want error
	}{
		{"missing_name", intmodel.CellBlueprintMetadata{Realm: "default"}, errdefs.ErrBlueprintNameRequired},
		{"missing_realm", intmodel.CellBlueprintMetadata{Name: "web"}, errdefs.ErrBlueprintRealmRequired},
		{
			"stack_without_space",
			intmodel.CellBlueprintMetadata{Name: "web", Realm: "default", Stack: "st"},
			errdefs.ErrBlueprintScopeIncomplete,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ctrl.GetBlueprint(intmodel.CellBlueprint{Metadata: tt.md}); !errors.Is(err, tt.want) {
				t.Errorf("GetBlueprint() error = %v, want %v", err, tt.want)
			}
		})
	}
}
