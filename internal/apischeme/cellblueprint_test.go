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

package apischeme_test

import (
	"reflect"
	"testing"

	"github.com/eminwux/kukeon/internal/apischeme"
	ext "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func sampleBlueprintDoc() ext.CellBlueprintDoc {
	return ext.CellBlueprintDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindCellBlueprint,
		Metadata: ext.CellBlueprintMetadata{
			Name:  "web",
			Realm: "default",
			Space: "agents",
		},
		Spec: ext.CellBlueprintSpec{
			Prefix: "web",
			Cell: ext.BlueprintCellSpec{
				Containers: []ext.BlueprintContainer{
					{ID: "main", Image: "alpine:latest", Attachable: true},
				},
			},
		},
	}
}

// TestCellBlueprintRoundTripV1Beta1 confirms the external doc survives the
// to-internal / from-internal carrier hop verbatim: the daemon stores the body
// opaquely in Document and reconstructs the same external shape on read-back.
func TestCellBlueprintRoundTripV1Beta1(t *testing.T) {
	in := sampleBlueprintDoc()

	internal, version, err := apischeme.NormalizeCellBlueprint(in)
	if err != nil {
		t.Fatalf("NormalizeCellBlueprint() error = %v", err)
	}
	if version != ext.APIVersionV1Beta1 {
		t.Errorf("version = %q, want v1beta1", version)
	}
	if internal.Metadata.Name != "web" || internal.Metadata.Realm != "default" || internal.Metadata.Space != "agents" {
		t.Errorf("carrier metadata = %+v, want name=web realm=default space=agents", internal.Metadata)
	}
	if len(internal.Document) == 0 {
		t.Fatal("carrier Document is empty, want serialized body")
	}

	out, err := apischeme.ConvertCellBlueprintToExternal(internal)
	if err != nil {
		t.Fatalf("ConvertCellBlueprintToExternal() error = %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("round-trip mismatch:\n in = %+v\nout = %+v", in, out)
	}
}

// TestConvertCellBlueprint_NormalizesKindAndVersion confirms an empty
// apiVersion/kind on the way in is canonicalized in the stored document.
func TestConvertCellBlueprint_NormalizesKindAndVersion(t *testing.T) {
	in := sampleBlueprintDoc()
	in.APIVersion = ""
	in.Kind = ""

	internal, _, err := apischeme.NormalizeCellBlueprint(in)
	if err != nil {
		t.Fatalf("NormalizeCellBlueprint() error = %v", err)
	}
	out, err := apischeme.ConvertCellBlueprintToExternal(internal)
	if err != nil {
		t.Fatalf("ConvertCellBlueprintToExternal() error = %v", err)
	}
	if out.APIVersion != ext.APIVersionV1Beta1 || out.Kind != ext.KindCellBlueprint {
		t.Errorf("stored doc not canonicalized: apiVersion=%q kind=%q", out.APIVersion, out.Kind)
	}
}

func TestConvertCellBlueprint_UnsupportedVersion(t *testing.T) {
	in := sampleBlueprintDoc()
	in.APIVersion = "v2"
	if _, err := apischeme.ConvertCellBlueprintDocToInternal(in); err == nil {
		t.Fatal("expected error for unsupported apiVersion, got nil")
	}
}
