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
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/apply/parser"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// requireValidationErr asserts the validation error is non-nil and wraps (by
// message — ValidationError carries the sentinel in its rendered string) the
// expected sentinel, matching the convention in parser_test.go.
func requireValidationErr(t *testing.T, err error, want error) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected validation error %v, got nil", want)
	}
	if !strings.Contains(err.Error(), want.Error()) {
		t.Fatalf("err = %v, want it to contain %v", err, want)
	}
}

func parseBlueprint(t *testing.T, yaml string) *parser.Document {
	t.Helper()
	doc, err := parser.ParseDocument(0, []byte(yaml))
	if err != nil {
		t.Fatalf("ParseDocument failed: %v", err)
	}
	if doc.Kind != v1beta1.KindCellBlueprint {
		t.Fatalf("kind = %q, want CellBlueprint", doc.Kind)
	}
	return doc
}

const validBlueprint = `apiVersion: v1beta1
kind: CellBlueprint
metadata:
  name: web
  realm: default
  space: agents
spec:
  prefix: web
  parameters:
    - name: TAG
      default: latest
  cell:
    containers:
      - id: main
        image: registry.example.com/web:${TAG}
        attachable: true
`

func TestValidateDocument_Blueprint_Valid(t *testing.T) {
	doc := parseBlueprint(t, validBlueprint)
	if err := parser.ValidateDocument(doc); err != nil {
		t.Fatalf("expected valid blueprint, got: %v", err)
	}
}

func TestValidateDocument_Blueprint_MissingRealm(t *testing.T) {
	yaml := `apiVersion: v1beta1
kind: CellBlueprint
metadata:
  name: web
spec:
  cell:
    containers:
      - id: main
        image: alpine:latest
`
	doc := parseBlueprint(t, yaml)
	requireValidationErr(t, parser.ValidateDocument(doc), errdefs.ErrBlueprintRealmRequired)
}

func TestValidateDocument_Blueprint_StackWithoutSpace(t *testing.T) {
	yaml := `apiVersion: v1beta1
kind: CellBlueprint
metadata:
  name: web
  realm: default
  stack: claude
spec:
  cell:
    containers:
      - id: main
        image: alpine:latest
`
	doc := parseBlueprint(t, yaml)
	requireValidationErr(t, parser.ValidateDocument(doc), errdefs.ErrBlueprintScopeIncomplete)
}

func TestValidateDocument_Blueprint_RepoSlotNoURLValid(t *testing.T) {
	// Unlike the Cell/Container apply path, a repo with no url is a valid
	// structural slot in a blueprint (CellConfig fills it, #624).
	yaml := `apiVersion: v1beta1
kind: CellBlueprint
metadata:
  name: web
  realm: default
spec:
  cell:
    containers:
      - id: main
        image: alpine:latest
        repos:
          - name: app
            target: /src
`
	doc := parseBlueprint(t, yaml)
	if err := parser.ValidateDocument(doc); err != nil {
		t.Fatalf("repo slot without url should validate in a blueprint, got: %v", err)
	}
}

func TestValidateDocument_Blueprint_RepoSlotRelativeTarget(t *testing.T) {
	yaml := `apiVersion: v1beta1
kind: CellBlueprint
metadata:
  name: web
  realm: default
spec:
  cell:
    containers:
      - id: main
        image: alpine:latest
        repos:
          - name: app
            target: relative/path
`
	doc := parseBlueprint(t, yaml)
	requireValidationErr(t, parser.ValidateDocument(doc), errdefs.ErrRepoTargetNotAbsolute)
}

func TestValidateDocument_Blueprint_SecretSlotEnvMissingEnvName(t *testing.T) {
	yaml := `apiVersion: v1beta1
kind: CellBlueprint
metadata:
  name: web
  realm: default
spec:
  cell:
    containers:
      - id: main
        image: alpine:latest
        secrets:
          - name: token
            mode: env
`
	doc := parseBlueprint(t, yaml)
	requireValidationErr(t, parser.ValidateDocument(doc), errdefs.ErrBlueprintSecretSlotEnvName)
}

func TestValidateDocument_Blueprint_SecretSlotFileRelativeMount(t *testing.T) {
	yaml := `apiVersion: v1beta1
kind: CellBlueprint
metadata:
  name: web
  realm: default
spec:
  cell:
    containers:
      - id: main
        image: alpine:latest
        secrets:
          - name: token
            mode: file
            mountPath: rel/path
`
	doc := parseBlueprint(t, yaml)
	requireValidationErr(t, parser.ValidateDocument(doc), errdefs.ErrBlueprintSecretSlotMountPath)
}

func TestValidateDocument_Blueprint_SecretSlotBadMode(t *testing.T) {
	yaml := `apiVersion: v1beta1
kind: CellBlueprint
metadata:
  name: web
  realm: default
spec:
  cell:
    containers:
      - id: main
        image: alpine:latest
        secrets:
          - name: token
            mode: bogus
`
	doc := parseBlueprint(t, yaml)
	requireValidationErr(t, parser.ValidateDocument(doc), errdefs.ErrBlueprintSecretSlotMode)
}

func TestValidateDocument_Blueprint_NoContainers(t *testing.T) {
	yaml := `apiVersion: v1beta1
kind: CellBlueprint
metadata:
  name: web
  realm: default
spec:
  cell: {}
`
	doc := parseBlueprint(t, yaml)
	requireValidationErr(t, parser.ValidateDocument(doc), errdefs.ErrBlueprintCellRequired)
}

func TestParseDocument_Blueprint_RoundTripsSlots(t *testing.T) {
	doc := parseBlueprint(t, validBlueprint)
	if got := doc.CellBlueprintDoc.Spec.Cell.Containers[0].Image; !strings.Contains(got, "${TAG}") {
		t.Fatalf("image = %q, want raw ${TAG} preserved before substitution", got)
	}
}
