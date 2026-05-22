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
