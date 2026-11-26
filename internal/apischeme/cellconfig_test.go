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
	"strings"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/apischeme"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	ext "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func sampleConfigDoc() ext.CellConfigDoc {
	return ext.CellConfigDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindCellConfig,
		Metadata: ext.CellConfigMetadata{
			Name:  "kukeon-dev",
			Realm: "kuke-system",
			Space: "default",
		},
		Spec: ext.CellConfigSpec{
			Blueprint: ext.CellConfigBlueprintRef{Name: "dev", Realm: "kuke-system"},
			Values:    map[string]string{"PROJECT_DIR": "kukeon"},
			Repos: map[string]ext.CellConfigRepoFill{
				"project": {URL: "git@github.com:eminwux/kukeon.git", Branch: "main"},
			},
			Secrets: map[string]ext.CellConfigSecretFill{
				"anthropic-token": {SecretRef: &ext.ContainerSecretRef{Name: "anthropic", Realm: "kuke-system"}},
			},
		},
	}
}

// TestCellConfigRoundTripV1Beta1 confirms the external doc survives the
// to-internal / from-internal carrier hop verbatim: the daemon stores the body
// opaquely in Document and reconstructs the same external shape on read-back.
func TestCellConfigRoundTripV1Beta1(t *testing.T) {
	in := sampleConfigDoc()

	internal, version, err := apischeme.NormalizeCellConfig(in)
	if err != nil {
		t.Fatalf("NormalizeCellConfig() error = %v", err)
	}
	if version != ext.APIVersionV1Beta1 {
		t.Errorf("version = %q, want v1beta1", version)
	}
	if internal.Metadata.Name != "kukeon-dev" || internal.Metadata.Realm != "kuke-system" ||
		internal.Metadata.Space != "default" {
		t.Errorf("carrier metadata = %+v, want name=kukeon-dev realm=kuke-system space=default", internal.Metadata)
	}
	if len(internal.Document) == 0 {
		t.Fatal("carrier Document is empty, want serialized body")
	}

	out, err := apischeme.ConvertCellConfigToExternal(internal)
	if err != nil {
		t.Fatalf("ConvertCellConfigToExternal() error = %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("round-trip mismatch:\n in = %+v\nout = %+v", in, out)
	}
}

// TestConvertCellConfig_NormalizesKindAndVersion confirms an empty
// apiVersion/kind on the way in is canonicalized in the stored document.
func TestConvertCellConfig_NormalizesKindAndVersion(t *testing.T) {
	in := sampleConfigDoc()
	in.APIVersion = ""
	in.Kind = ""

	internal, _, err := apischeme.NormalizeCellConfig(in)
	if err != nil {
		t.Fatalf("NormalizeCellConfig() error = %v", err)
	}
	out, err := apischeme.ConvertCellConfigToExternal(internal)
	if err != nil {
		t.Fatalf("ConvertCellConfigToExternal() error = %v", err)
	}
	if out.APIVersion != ext.APIVersionV1Beta1 || out.Kind != ext.KindCellConfig {
		t.Errorf("stored doc not canonicalized: apiVersion=%q kind=%q", out.APIVersion, out.Kind)
	}
}

func TestConvertCellConfig_UnsupportedVersion(t *testing.T) {
	in := sampleConfigDoc()
	in.APIVersion = "v2"
	if _, err := apischeme.ConvertCellConfigDocToInternal(in); err == nil {
		t.Fatal("expected error for unsupported apiVersion, got nil")
	}
}

// TestConvertCellConfig_Schedule covers the schedule of a Config (issue
// #1503) across the carrier hop: spec.schedule round-trips in the document and
// is lifted onto the carrier, an applied status is dropped from the stored
// document, and the stored schedule state comes back as status.schedule.
func TestConvertCellConfig_Schedule(t *testing.T) {
	deadline := int64(300)
	next := time.Date(2026, 11, 17, 8, 30, 0, 0, time.UTC)

	in := sampleConfigDoc()
	in.Spec.Schedule = &ext.CellConfigSchedule{
		Cron:                    "*/10 * * * *",
		Timezone:                "Europe/Madrid",
		ConcurrencyPolicy:       ext.ConcurrencyPolicyForbid,
		StartingDeadlineSeconds: &deadline,
		Suspend:                 true,
	}
	// A status on the way in — `kuke get config -o yaml`, re-applied — is the
	// daemon's to write, not the operator's.
	in.Status = &ext.CellConfigStatus{Schedule: &ext.CellConfigScheduleStatus{SkippedFires: 99}}

	internal, _, err := apischeme.NormalizeCellConfig(in)
	if err != nil {
		t.Fatalf("NormalizeCellConfig() error = %v", err)
	}
	if strings.Contains(string(internal.Document), "status") {
		t.Errorf("stored document carries a status block:\n%s", internal.Document)
	}
	if got := internal.Schedule; got == nil || got.Cron != "*/10 * * * *" ||
		got.Timezone != "Europe/Madrid" || !got.Suspend {
		t.Errorf("carrier Schedule = %+v, want the lifted spec.schedule", got)
	}

	out, err := apischeme.ConvertCellConfigToExternal(internal)
	if err != nil {
		t.Fatalf("ConvertCellConfigToExternal() error = %v", err)
	}
	if !reflect.DeepEqual(out.Spec.Schedule, in.Spec.Schedule) {
		t.Errorf("spec.schedule = %+v, want %+v", out.Spec.Schedule, in.Spec.Schedule)
	}
	if out.Status != nil {
		t.Errorf("status = %+v, want none when no schedule state is stored", out.Status)
	}

	internal.ScheduleStatus = &intmodel.CellConfigScheduleStatus{
		NextScheduleTime: &next,
		LastCell:         "kukeon-dev-1794904200",
		ActiveCells:      []string{"kukeon-dev-1794904200"},
		SkippedFires:     2,
		LastError:        "boom",
	}
	out, err = apischeme.ConvertCellConfigToExternal(internal)
	if err != nil {
		t.Fatalf("ConvertCellConfigToExternal() error = %v", err)
	}
	want := &ext.CellConfigStatus{Schedule: &ext.CellConfigScheduleStatus{
		NextScheduleTime: &next,
		LastCell:         "kukeon-dev-1794904200",
		ActiveCells:      []string{"kukeon-dev-1794904200"},
		SkippedFires:     2,
		LastError:        "boom",
	}}
	if !reflect.DeepEqual(out.Status, want) {
		t.Errorf("status = %+v, want %+v", out.Status.Schedule, want.Schedule)
	}

	// The list view carries no body, but does carry the schedule.
	listed := apischeme.ConvertCellConfigMetadataToExternal(internal)
	if listed.Spec.Schedule == nil || listed.Spec.Schedule.Cron != "*/10 * * * *" ||
		listed.Spec.Schedule.Timezone != "Europe/Madrid" || !listed.Spec.Schedule.Suspend {
		t.Errorf("listed spec.schedule = %+v, want the summary", listed.Spec.Schedule)
	}
	if !reflect.DeepEqual(listed.Status, want) {
		t.Errorf("listed status = %+v, want %+v", listed.Status, want)
	}
	if listed.Spec.Blueprint.Name != "" || listed.Spec.Values != nil {
		t.Errorf("the list view leaked the config body: %+v", listed.Spec)
	}
}

// TestConvertCellConfig_UnscheduledDocumentIsUnchanged pins that a Config
// without a schedule is stored byte-for-byte as before the schedule fields
// existed, so its revision hash does not move.
func TestConvertCellConfig_UnscheduledDocumentIsUnchanged(t *testing.T) {
	internal, _, err := apischeme.NormalizeCellConfig(sampleConfigDoc())
	if err != nil {
		t.Fatalf("NormalizeCellConfig() error = %v", err)
	}
	for _, key := range []string{"schedule", "status"} {
		if strings.Contains(string(internal.Document), key) {
			t.Errorf("stored document of an unscheduled config mentions %q:\n%s", key, internal.Document)
		}
	}
	if internal.Schedule != nil {
		t.Errorf("carrier Schedule = %+v, want nil", internal.Schedule)
	}
}

// TestConvertCellConfigMetadata_CarriesAnnotations (issue #1692): the list
// view of a Config carries its annotations beside its labels, so a client
// shows an agent's display name.
func TestConvertCellConfigMetadata_CarriesAnnotations(t *testing.T) {
	doc := sampleConfigDoc()
	doc.Metadata.Annotations = map[string]string{ext.AnnotationDisplayName: "Reviewer"}
	internal, _, err := apischeme.NormalizeCellConfig(doc)
	if err != nil {
		t.Fatalf("NormalizeCellConfig() error = %v", err)
	}
	listed := apischeme.ConvertCellConfigMetadataToExternal(internal)
	if listed.Metadata.Annotations[ext.AnnotationDisplayName] != "Reviewer" {
		t.Fatalf("listed annotations = %v", listed.Metadata.Annotations)
	}
	// The listed map is a copy of the carrier's.
	listed.Metadata.Annotations[ext.AnnotationDisplayName] = "changed"
	if internal.Metadata.Annotations[ext.AnnotationDisplayName] != "Reviewer" {
		t.Fatal("the list view shares the carrier's annotations map")
	}
}
