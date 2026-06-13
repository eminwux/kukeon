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

package parser_test

import (
	"testing"

	"github.com/eminwux/kukeon/internal/apply/parser"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func parseVolume(t *testing.T, yaml string) *parser.Document {
	t.Helper()
	doc, err := parser.ParseDocument(0, []byte(yaml))
	if err != nil {
		t.Fatalf("ParseDocument failed: %v", err)
	}
	if doc.Kind != v1beta1.KindVolume {
		t.Fatalf("kind = %q, want Volume", doc.Kind)
	}
	return doc
}

const validVolume = `apiVersion: v1beta1
kind: Volume
metadata:
  name: data
  realm: default
  space: agents
  stack: web
`

func TestValidateDocument_Volume_Valid(t *testing.T) {
	doc := parseVolume(t, validVolume)
	if err := parser.ValidateDocument(doc); err != nil {
		t.Fatalf("expected valid volume, got: %v", err)
	}
	if doc.VolumeDoc == nil {
		t.Fatalf("VolumeDoc is nil after parse")
	}
	if got := doc.VolumeDoc.Metadata.Stack; got != "web" {
		t.Errorf("parsed stack = %q, want web", got)
	}
}

func TestValidateDocument_Volume_RealmOnlyValid(t *testing.T) {
	doc := parseVolume(t, "apiVersion: v1beta1\nkind: Volume\nmetadata:\n  name: data\n  realm: default\n")
	if err := parser.ValidateDocument(doc); err != nil {
		t.Fatalf("expected valid realm-scoped volume, got: %v", err)
	}
}

func TestValidateDocument_Volume_NameRequired(t *testing.T) {
	doc := parseVolume(t, "apiVersion: v1beta1\nkind: Volume\nmetadata:\n  realm: default\n")
	requireValidationErr(t, parser.ValidateDocument(doc), errdefs.ErrVolumeNameRequired)
}

func TestValidateDocument_Volume_RealmRequired(t *testing.T) {
	doc := parseVolume(t, "apiVersion: v1beta1\nkind: Volume\nmetadata:\n  name: data\n")
	requireValidationErr(t, parser.ValidateDocument(doc), errdefs.ErrVolumeRealmRequired)
}

func TestValidateDocument_Volume_StackWithoutSpaceRejected(t *testing.T) {
	doc := parseVolume(t, "apiVersion: v1beta1\nkind: Volume\nmetadata:\n  name: data\n  realm: default\n  stack: web\n")
	requireValidationErr(t, parser.ValidateDocument(doc), errdefs.ErrVolumeScopeIncomplete)
}

// TestValidateDocument_Volume_CellScopeStructurallyRejected confirms a Volume
// can never be cell-scoped: VolumeMetadata has no Cell field, so a `cell:`
// coordinate in the YAML is dropped at unmarshal time and the volume is treated
// as (at most) stack-scoped — the structural equivalent of validateBlueprint's
// cell rejection. The document still validates (the realm/space/stack chain is
// complete); the point is that the cell coordinate has no effect.
func TestValidateDocument_Volume_CellScopeStructurallyRejected(t *testing.T) {
	yaml := "apiVersion: v1beta1\nkind: Volume\nmetadata:\n  name: data\n  realm: default\n  space: agents\n  stack: web\n  cell: ignored\n"
	doc := parseVolume(t, yaml)
	if err := parser.ValidateDocument(doc); err != nil {
		t.Fatalf("expected valid volume (cell coordinate ignored), got: %v", err)
	}
	// VolumeMetadata has no Cell field; the struct cannot carry a cell scope.
	// Re-marshal-safe assertion: the deepest coordinate is the stack.
	if doc.VolumeDoc.Metadata.Stack != "web" {
		t.Errorf("stack = %q, want web", doc.VolumeDoc.Metadata.Stack)
	}
}

// TestValidateDocument_Volume_UnsafeNameRejected confirms a path-traversal name
// is rejected before it can escape the volumes tree (mirrors the Secret
// coordinate guard, #673).
func TestValidateDocument_Volume_UnsafeNameRejected(t *testing.T) {
	doc := parseVolume(t, "apiVersion: v1beta1\nkind: Volume\nmetadata:\n  name: ../escape\n  realm: default\n")
	requireValidationErr(t, parser.ValidateDocument(doc), errdefs.ErrVolumeCoordUnsafe)
}
