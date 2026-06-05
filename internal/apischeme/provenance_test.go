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
	"encoding/json"
	"reflect"
	"testing"

	"github.com/eminwux/kukeon/internal/apischeme"
	ext "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"gopkg.in/yaml.v3"
)

// provenanceFixture is a fully-populated provenance block exercising every
// field (issue #1021).
func provenanceFixture() *ext.CellProvenance {
	return &ext.CellProvenance{
		BindingKind:  ext.BindingKindConfig,
		BindingRef:   ext.CellBindingRef{Name: "web", Realm: "default", Space: "team-a", Stack: "api"},
		Params:       map[string]string{"TAG": "v2", "REPLICAS": "3"},
		EnvOverrides: []string{"DEBUG=1", "LOG_LEVEL=info"},
	}
}

// TestCellProvenance_ConversionRoundTrip pins issue #1021 AC1: the provenance
// block survives the external → internal → external conversion (both directions
// copy it, unlike the transport-only RuntimeEnv which the outbound builder
// drops).
func TestCellProvenance_ConversionRoundTrip(t *testing.T) {
	input := ext.CellDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindCell,
		Metadata:   ext.CellMetadata{Name: "web"},
		Spec: ext.CellSpec{
			ID:         "web",
			RealmID:    "default",
			Containers: []ext.ContainerSpec{},
			Provenance: provenanceFixture(),
		},
	}

	internal, err := apischeme.ConvertCellDocToInternal(input)
	if err != nil {
		t.Fatalf("ConvertCellDocToInternal: %v", err)
	}
	if internal.Spec.Provenance == nil {
		t.Fatalf("internal provenance dropped on inbound conversion")
	}
	if internal.Spec.Provenance.BindingKind != ext.BindingKindConfig {
		t.Errorf("internal bindingKind=%q want %q",
			internal.Spec.Provenance.BindingKind, ext.BindingKindConfig)
	}

	output, err := apischeme.BuildCellExternalFromInternal(internal, apischeme.VersionV1Beta1)
	if err != nil {
		t.Fatalf("BuildCellExternalFromInternal: %v", err)
	}
	if !reflect.DeepEqual(output.Spec.Provenance, input.Spec.Provenance) {
		t.Errorf("provenance not preserved across conversion round-trip\n got: %+v\nwant: %+v",
			output.Spec.Provenance, input.Spec.Provenance)
	}
}

// TestCellProvenance_PersistenceRoundTrip pins issue #1021 AC1 across the
// on-disk persistence path: the daemon writes metadata.json via JSON marshal
// and re-reads it via JSON unmarshal, and `kuke get cell -o yaml` renders via
// YAML. The provenance block must survive both encodings (it carries no
// yaml:"-", unlike RuntimeEnv).
func TestCellProvenance_PersistenceRoundTrip(t *testing.T) {
	doc := ext.CellDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindCell,
		Metadata:   ext.CellMetadata{Name: "web"},
		Spec: ext.CellSpec{
			ID:         "web",
			RealmID:    "default",
			Containers: []ext.ContainerSpec{},
			Provenance: provenanceFixture(),
		},
	}

	t.Run("json", func(t *testing.T) {
		raw, err := json.Marshal(doc)
		if err != nil {
			t.Fatalf("json.Marshal: %v", err)
		}
		var got ext.CellDoc
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("json.Unmarshal: %v", err)
		}
		if !reflect.DeepEqual(got.Spec.Provenance, doc.Spec.Provenance) {
			t.Errorf("provenance not preserved across JSON round-trip\n got: %+v\nwant: %+v",
				got.Spec.Provenance, doc.Spec.Provenance)
		}
	})

	t.Run("yaml", func(t *testing.T) {
		raw, err := yaml.Marshal(doc)
		if err != nil {
			t.Fatalf("yaml.Marshal: %v", err)
		}
		var got ext.CellDoc
		if err := yaml.Unmarshal(raw, &got); err != nil {
			t.Fatalf("yaml.Unmarshal: %v", err)
		}
		if !reflect.DeepEqual(got.Spec.Provenance, doc.Spec.Provenance) {
			t.Errorf("provenance not preserved across YAML round-trip\n got: %+v\nwant: %+v",
				got.Spec.Provenance, doc.Spec.Provenance)
		}
	})
}

// TestCellProvenance_NilOmitted confirms a hand-built cell with no provenance
// round-trips as nil (the omitempty pointer keeps it out of the encoded
// document entirely).
func TestCellProvenance_NilOmitted(t *testing.T) {
	doc := ext.CellDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindCell,
		Metadata:   ext.CellMetadata{Name: "hand-built"},
		Spec:       ext.CellSpec{ID: "hand-built", RealmID: "default", Containers: []ext.ContainerSpec{}},
	}

	internal, err := apischeme.ConvertCellDocToInternal(doc)
	if err != nil {
		t.Fatalf("ConvertCellDocToInternal: %v", err)
	}
	if internal.Spec.Provenance != nil {
		t.Errorf("nil provenance materialized into %+v", internal.Spec.Provenance)
	}
	output, err := apischeme.BuildCellExternalFromInternal(internal, apischeme.VersionV1Beta1)
	if err != nil {
		t.Fatalf("BuildCellExternalFromInternal: %v", err)
	}
	if output.Spec.Provenance != nil {
		t.Errorf("nil provenance round-tripped to %+v", output.Spec.Provenance)
	}
}
