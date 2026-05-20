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
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/apischeme"
	ext "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"gopkg.in/yaml.v3"
)

// The tests below cover issue #596 phase 2: Metadata.Generation and
// Status.ObservedGeneration are mechanical type-layer fields. They must
// (a) survive the external→internal→external round-trip through all 8
// apischeme converters, (b) read back as zero from pre-existing metadata
// that predates the fields, and (c) stay omitted from the serialized form
// when zero so existing manifests keep their on-disk shape.

// TestRealmGenerationRoundTrip pins Generation/ObservedGeneration through the
// realm converter pair and the YAML form.
func TestRealmGenerationRoundTrip(t *testing.T) {
	input := ext.RealmDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindRealm,
		Metadata:   ext.RealmMetadata{Name: "realm0", Generation: 7},
		Spec:       ext.RealmSpec{Namespace: "realm0"},
		Status:     ext.RealmStatus{State: ext.RealmStateReady, ObservedGeneration: 6},
	}

	internal, version, err := apischeme.NormalizeRealm(input)
	if err != nil {
		t.Fatalf("NormalizeRealm: %v", err)
	}
	if internal.Metadata.Generation != 7 {
		t.Errorf("internal Generation = %d, want 7", internal.Metadata.Generation)
	}
	if internal.Status.ObservedGeneration != 6 {
		t.Errorf("internal ObservedGeneration = %d, want 6", internal.Status.ObservedGeneration)
	}

	output, err := apischeme.BuildRealmExternalFromInternal(internal, version)
	if err != nil {
		t.Fatalf("BuildRealmExternalFromInternal: %v", err)
	}
	if output.Metadata.Generation != 7 {
		t.Errorf("external Generation = %d, want 7", output.Metadata.Generation)
	}
	if output.Status.ObservedGeneration != 6 {
		t.Errorf("external ObservedGeneration = %d, want 6", output.Status.ObservedGeneration)
	}

	rendered, err := yaml.Marshal(output)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	if !strings.Contains(string(rendered), "generation: 7") {
		t.Errorf("rendered YAML missing generation; got:\n%s", string(rendered))
	}
	if !strings.Contains(string(rendered), "observedGeneration: 6") {
		t.Errorf("rendered YAML missing observedGeneration; got:\n%s", string(rendered))
	}
}

// TestRealmGenerationZeroValue covers reading a pre-existing realm metadata
// document that predates the fields: they unmarshal to zero, survive the
// round-trip, and stay omitted from the re-serialized form.
func TestRealmGenerationZeroValue(t *testing.T) {
	const legacy = `apiVersion: v1beta1
kind: Realm
metadata:
  name: realm0
spec:
  namespace: realm0
status:
  state: 2
`
	var doc ext.RealmDoc
	if err := yaml.Unmarshal([]byte(legacy), &doc); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	if doc.Metadata.Generation != 0 || doc.Status.ObservedGeneration != 0 {
		t.Fatalf("legacy doc did not zero-default: gen=%d obs=%d",
			doc.Metadata.Generation, doc.Status.ObservedGeneration)
	}

	internal, version, err := apischeme.NormalizeRealm(doc)
	if err != nil {
		t.Fatalf("NormalizeRealm: %v", err)
	}
	output, err := apischeme.BuildRealmExternalFromInternal(internal, version)
	if err != nil {
		t.Fatalf("BuildRealmExternalFromInternal: %v", err)
	}
	if output.Metadata.Generation != 0 || output.Status.ObservedGeneration != 0 {
		t.Errorf("zero values not preserved: gen=%d obs=%d",
			output.Metadata.Generation, output.Status.ObservedGeneration)
	}

	rendered, err := yaml.Marshal(output)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	if strings.Contains(string(rendered), "generation:") {
		t.Errorf("generation must be omitted when zero; got:\n%s", string(rendered))
	}
	if strings.Contains(string(rendered), "observedGeneration:") {
		t.Errorf("observedGeneration must be omitted when zero; got:\n%s", string(rendered))
	}
}

// TestSpaceGenerationRoundTrip pins the fields through the space converter pair.
func TestSpaceGenerationRoundTrip(t *testing.T) {
	input := ext.SpaceDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindSpace,
		Metadata:   ext.SpaceMetadata{Name: "space0", Generation: 3},
		Spec:       ext.SpaceSpec{RealmID: "realm0"},
		Status:     ext.SpaceStatus{State: ext.SpaceStateReady, ObservedGeneration: 2},
	}

	internal, version, err := apischeme.NormalizeSpace(input)
	if err != nil {
		t.Fatalf("NormalizeSpace: %v", err)
	}
	if internal.Metadata.Generation != 3 || internal.Status.ObservedGeneration != 2 {
		t.Errorf("internal gen=%d obs=%d, want 3/2",
			internal.Metadata.Generation, internal.Status.ObservedGeneration)
	}

	output, err := apischeme.BuildSpaceExternalFromInternal(internal, version)
	if err != nil {
		t.Fatalf("BuildSpaceExternalFromInternal: %v", err)
	}
	if output.Metadata.Generation != 3 || output.Status.ObservedGeneration != 2 {
		t.Errorf("external gen=%d obs=%d, want 3/2",
			output.Metadata.Generation, output.Status.ObservedGeneration)
	}

	rendered, err := yaml.Marshal(output)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	if !strings.Contains(string(rendered), "generation: 3") ||
		!strings.Contains(string(rendered), "observedGeneration: 2") {
		t.Errorf("rendered YAML missing generation fields; got:\n%s", string(rendered))
	}
}

// TestSpaceGenerationZeroValue covers the pre-existing-metadata read for spaces.
func TestSpaceGenerationZeroValue(t *testing.T) {
	input := ext.SpaceDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindSpace,
		Metadata:   ext.SpaceMetadata{Name: "space0"},
		Spec:       ext.SpaceSpec{RealmID: "realm0"},
		Status:     ext.SpaceStatus{State: ext.SpaceStatePending},
	}

	internal, version, err := apischeme.NormalizeSpace(input)
	if err != nil {
		t.Fatalf("NormalizeSpace: %v", err)
	}
	output, err := apischeme.BuildSpaceExternalFromInternal(internal, version)
	if err != nil {
		t.Fatalf("BuildSpaceExternalFromInternal: %v", err)
	}
	if output.Metadata.Generation != 0 || output.Status.ObservedGeneration != 0 {
		t.Errorf("zero values not preserved: gen=%d obs=%d",
			output.Metadata.Generation, output.Status.ObservedGeneration)
	}

	rendered, err := yaml.Marshal(output)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	if strings.Contains(string(rendered), "generation:") ||
		strings.Contains(string(rendered), "observedGeneration:") {
		t.Errorf("generation fields must be omitted when zero; got:\n%s", string(rendered))
	}
}

// TestStackGenerationRoundTrip pins the fields through the stack converter pair.
func TestStackGenerationRoundTrip(t *testing.T) {
	input := ext.StackDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindStack,
		Metadata:   ext.StackMetadata{Name: "stack0", Generation: 11},
		Spec:       ext.StackSpec{ID: "stack0", RealmID: "realm0", SpaceID: "space0"},
		Status:     ext.StackStatus{State: ext.StackStateReady, ObservedGeneration: 10},
	}

	internal, version, err := apischeme.NormalizeStack(input)
	if err != nil {
		t.Fatalf("NormalizeStack: %v", err)
	}
	if internal.Metadata.Generation != 11 || internal.Status.ObservedGeneration != 10 {
		t.Errorf("internal gen=%d obs=%d, want 11/10",
			internal.Metadata.Generation, internal.Status.ObservedGeneration)
	}

	output, err := apischeme.BuildStackExternalFromInternal(internal, version)
	if err != nil {
		t.Fatalf("BuildStackExternalFromInternal: %v", err)
	}
	if output.Metadata.Generation != 11 || output.Status.ObservedGeneration != 10 {
		t.Errorf("external gen=%d obs=%d, want 11/10",
			output.Metadata.Generation, output.Status.ObservedGeneration)
	}

	rendered, err := yaml.Marshal(output)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	if !strings.Contains(string(rendered), "generation: 11") ||
		!strings.Contains(string(rendered), "observedGeneration: 10") {
		t.Errorf("rendered YAML missing generation fields; got:\n%s", string(rendered))
	}
}

// TestStackGenerationZeroValue covers the pre-existing-metadata read for stacks.
func TestStackGenerationZeroValue(t *testing.T) {
	input := ext.StackDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindStack,
		Metadata:   ext.StackMetadata{Name: "stack0"},
		Spec:       ext.StackSpec{ID: "stack0", RealmID: "realm0", SpaceID: "space0"},
		Status:     ext.StackStatus{State: ext.StackStatePending},
	}

	internal, version, err := apischeme.NormalizeStack(input)
	if err != nil {
		t.Fatalf("NormalizeStack: %v", err)
	}
	output, err := apischeme.BuildStackExternalFromInternal(internal, version)
	if err != nil {
		t.Fatalf("BuildStackExternalFromInternal: %v", err)
	}
	if output.Metadata.Generation != 0 || output.Status.ObservedGeneration != 0 {
		t.Errorf("zero values not preserved: gen=%d obs=%d",
			output.Metadata.Generation, output.Status.ObservedGeneration)
	}

	rendered, err := yaml.Marshal(output)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	if strings.Contains(string(rendered), "generation:") ||
		strings.Contains(string(rendered), "observedGeneration:") {
		t.Errorf("generation fields must be omitted when zero; got:\n%s", string(rendered))
	}
}

// TestCellGenerationRoundTrip pins the fields through the cell converter pair.
func TestCellGenerationRoundTrip(t *testing.T) {
	input := ext.CellDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindCell,
		Metadata:   ext.CellMetadata{Name: "cell0", Generation: 5},
		Spec: ext.CellSpec{
			ID:         "cell0",
			RealmID:    "realm0",
			SpaceID:    "space0",
			StackID:    "stack0",
			Containers: []ext.ContainerSpec{},
		},
		Status: ext.CellStatus{State: ext.CellStateReady, ObservedGeneration: 4},
	}

	internal, version, err := apischeme.NormalizeCell(input)
	if err != nil {
		t.Fatalf("NormalizeCell: %v", err)
	}
	if internal.Metadata.Generation != 5 || internal.Status.ObservedGeneration != 4 {
		t.Errorf("internal gen=%d obs=%d, want 5/4",
			internal.Metadata.Generation, internal.Status.ObservedGeneration)
	}

	output, err := apischeme.BuildCellExternalFromInternal(internal, version)
	if err != nil {
		t.Fatalf("BuildCellExternalFromInternal: %v", err)
	}
	if output.Metadata.Generation != 5 || output.Status.ObservedGeneration != 4 {
		t.Errorf("external gen=%d obs=%d, want 5/4",
			output.Metadata.Generation, output.Status.ObservedGeneration)
	}

	rendered, err := yaml.Marshal(output)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	if !strings.Contains(string(rendered), "generation: 5") ||
		!strings.Contains(string(rendered), "observedGeneration: 4") {
		t.Errorf("rendered YAML missing generation fields; got:\n%s", string(rendered))
	}
}

// TestCellGenerationZeroValue covers the pre-existing-metadata read for cells.
func TestCellGenerationZeroValue(t *testing.T) {
	input := ext.CellDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindCell,
		Metadata:   ext.CellMetadata{Name: "cell0"},
		Spec: ext.CellSpec{
			ID:         "cell0",
			RealmID:    "realm0",
			SpaceID:    "space0",
			StackID:    "stack0",
			Containers: []ext.ContainerSpec{},
		},
		Status: ext.CellStatus{State: ext.CellStatePending},
	}

	internal, version, err := apischeme.NormalizeCell(input)
	if err != nil {
		t.Fatalf("NormalizeCell: %v", err)
	}
	output, err := apischeme.BuildCellExternalFromInternal(internal, version)
	if err != nil {
		t.Fatalf("BuildCellExternalFromInternal: %v", err)
	}
	if output.Metadata.Generation != 0 || output.Status.ObservedGeneration != 0 {
		t.Errorf("zero values not preserved: gen=%d obs=%d",
			output.Metadata.Generation, output.Status.ObservedGeneration)
	}

	rendered, err := yaml.Marshal(output)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	if strings.Contains(string(rendered), "generation:") ||
		strings.Contains(string(rendered), "observedGeneration:") {
		t.Errorf("generation fields must be omitted when zero; got:\n%s", string(rendered))
	}
}
