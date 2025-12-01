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
	"bytes"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/apply/parser"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func TestParseDocuments_SingleDocument(t *testing.T) {
	yaml := `
apiVersion: v1beta1
kind: Realm
metadata:
  name: test-realm
spec:
  namespace: test-ns
`
	docs, err := parser.ParseDocuments(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("ParseDocuments failed: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 document, got %d", len(docs))
	}
}

func TestParseDocuments_MultiDocument(t *testing.T) {
	yaml := `
apiVersion: v1beta1
kind: Realm
metadata:
  name: realm1
---
apiVersion: v1beta1
kind: Space
metadata:
  name: space1
spec:
  realmId: realm1
`
	docs, err := parser.ParseDocuments(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("ParseDocuments failed: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 documents, got %d", len(docs))
	}
}

func TestParseDocuments_EmptyDocuments(t *testing.T) {
	yaml := `
---
---
apiVersion: v1beta1
kind: Realm
metadata:
  name: test-realm
---
`
	docs, err := parser.ParseDocuments(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("ParseDocuments failed: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 document (empty ones skipped), got %d", len(docs))
	}
}

func TestParseDocuments_NoDocuments(t *testing.T) {
	yaml := `---
---
`
	_, err := parser.ParseDocuments(strings.NewReader(yaml))
	if err == nil {
		t.Fatal("expected error for no documents, got nil")
	}
}

func TestDetectKind(t *testing.T) {
	tests := []struct {
		name     string
		yaml     string
		wantKind v1beta1.Kind
		wantErr  bool
	}{
		{
			name: "realm",
			yaml: `apiVersion: v1beta1
kind: Realm
metadata:
  name: test
`,
			wantKind: v1beta1.KindRealm,
		},
		{
			name: "space",
			yaml: `apiVersion: v1beta1
kind: Space
metadata:
  name: test
`,
			wantKind: v1beta1.KindSpace,
		},
		{
			name:    "invalid yaml",
			yaml:    `invalid: yaml: [`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kind, err := parser.DetectKind([]byte(tt.yaml))
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("DetectKind failed: %v", err)
			}
			if kind != tt.wantKind {
				t.Errorf("expected kind %q, got %q", tt.wantKind, kind)
			}
		})
	}
}

func TestParseDocument_Realm(t *testing.T) {
	yaml := `apiVersion: v1beta1
kind: Realm
metadata:
  name: test-realm
spec:
  namespace: test-ns
`
	doc, err := parser.ParseDocument(0, []byte(yaml))
	if err != nil {
		t.Fatalf("ParseDocument failed: %v", err)
	}
	if doc.Kind != v1beta1.KindRealm {
		t.Errorf("expected kind Realm, got %q", doc.Kind)
	}
	if doc.RealmDoc == nil {
		t.Fatal("expected RealmDoc to be set")
	}
	if doc.RealmDoc.Metadata.Name != "test-realm" {
		t.Errorf("expected name test-realm, got %q", doc.RealmDoc.Metadata.Name)
	}
}

func TestParseDocument_Space(t *testing.T) {
	yaml := `apiVersion: v1beta1
kind: Space
metadata:
  name: test-space
spec:
  realmId: test-realm
`
	doc, err := parser.ParseDocument(0, []byte(yaml))
	if err != nil {
		t.Fatalf("ParseDocument failed: %v", err)
	}
	if doc.Kind != v1beta1.KindSpace {
		t.Errorf("expected kind Space, got %q", doc.Kind)
	}
	if doc.SpaceDoc == nil {
		t.Fatal("expected SpaceDoc to be set")
	}
	if doc.SpaceDoc.Metadata.Name != "test-space" {
		t.Errorf("expected name test-space, got %q", doc.SpaceDoc.Metadata.Name)
	}
}

func TestParseDocument_UnknownKind(t *testing.T) {
	yaml := `apiVersion: v1beta1
kind: Unknown
metadata:
  name: test
`
	_, err := parser.ParseDocument(0, []byte(yaml))
	if err == nil {
		t.Fatal("expected error for unknown kind, got nil")
	}
	if !strings.Contains(err.Error(), errdefs.ErrUnknownKind.Error()) {
		t.Errorf("expected error to contain ErrUnknownKind, got: %v", err)
	}
}

func TestValidateDocument_Realm_Valid(t *testing.T) {
	yaml := `apiVersion: v1beta1
kind: Realm
metadata:
  name: test-realm
spec:
  namespace: test-ns
`
	doc, err := parser.ParseDocument(0, []byte(yaml))
	if err != nil {
		t.Fatalf("ParseDocument failed: %v", err)
	}

	validationErr := parser.ValidateDocument(doc)
	if validationErr != nil {
		t.Fatalf("expected no validation error, got: %v", validationErr)
	}
}

func TestValidateDocument_Realm_MissingName(t *testing.T) {
	yaml := `apiVersion: v1beta1
kind: Realm
metadata:
  name: ""
spec:
  namespace: test-ns
`
	doc, err := parser.ParseDocument(0, []byte(yaml))
	if err != nil {
		t.Fatalf("ParseDocument failed: %v", err)
	}

	validationErr := parser.ValidateDocument(doc)
	if validationErr == nil {
		t.Fatal("expected validation error for missing name, got nil")
	}
	if !strings.Contains(validationErr.Error(), "metadata.name is required") {
		t.Errorf("expected error about metadata.name, got: %v", validationErr)
	}
}

func TestValidateDocument_Space_MissingRealmID(t *testing.T) {
	yaml := `apiVersion: v1beta1
kind: Space
metadata:
  name: test-space
spec:
  realmId: ""
`
	doc, err := parser.ParseDocument(0, []byte(yaml))
	if err != nil {
		t.Fatalf("ParseDocument failed: %v", err)
	}

	validationErr := parser.ValidateDocument(doc)
	if validationErr == nil {
		t.Fatal("expected validation error for missing realmId, got nil")
	}
	if !strings.Contains(validationErr.Error(), "spec.realmId is required") {
		t.Errorf("expected error about spec.realmId, got: %v", validationErr)
	}
}

func TestValidateDocument_Cell_MissingContainers(t *testing.T) {
	yaml := `apiVersion: v1beta1
kind: Cell
metadata:
  name: test-cell
spec:
  realmId: test-realm
  spaceId: test-space
  stackId: test-stack
  containers: []
`
	doc, err := parser.ParseDocument(0, []byte(yaml))
	if err != nil {
		t.Fatalf("ParseDocument failed: %v", err)
	}

	validationErr := parser.ValidateDocument(doc)
	if validationErr == nil {
		t.Fatal("expected validation error for empty containers, got nil")
	}
	if !strings.Contains(validationErr.Error(), "spec.containers is required") {
		t.Errorf("expected error about spec.containers, got: %v", validationErr)
	}
}

func TestValidateDocument_UnsupportedAPIVersion(t *testing.T) {
	yaml := `apiVersion: v1alpha1
kind: Realm
metadata:
  name: test-realm
`
	doc, err := parser.ParseDocument(0, []byte(yaml))
	if err != nil {
		t.Fatalf("ParseDocument failed: %v", err)
	}

	validationErr := parser.ValidateDocument(doc)
	if validationErr == nil {
		t.Fatal("expected validation error for unsupported apiVersion, got nil")
	}
	if !strings.Contains(validationErr.Error(), errdefs.ErrUnsupportedAPIVersion.Error()) {
		t.Errorf("expected error about unsupported apiVersion, got: %v", validationErr)
	}
}

func TestValidateDocument_DefaultAPIVersion(t *testing.T) {
	yaml := `kind: Realm
metadata:
  name: test-realm
`
	doc, err := parser.ParseDocument(0, []byte(yaml))
	if err != nil {
		t.Fatalf("ParseDocument failed: %v", err)
	}

	validationErr := parser.ValidateDocument(doc)
	if validationErr != nil {
		t.Fatalf("expected no validation error (empty apiVersion should default), got: %v", validationErr)
	}
}

func TestParseDocuments_FromStdin(t *testing.T) {
	yaml := `apiVersion: v1beta1
kind: Realm
metadata:
  name: test-realm
`
	docs, err := parser.ParseDocuments(bytes.NewReader([]byte(yaml)))
	if err != nil {
		t.Fatalf("ParseDocuments failed: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 document, got %d", len(docs))
	}
}

func TestParseDocuments_SeparatorInString(t *testing.T) {
	// Test that --- in a string value should not split documents
	yaml := `apiVersion: v1beta1
kind: Realm
metadata:
  name: test-realm
  description: "This contains --- three dashes"
spec:
  namespace: test-ns
---
apiVersion: v1beta1
kind: Space
metadata:
  name: test-space
spec:
  realmId: test-realm
`
	docs, err := parser.ParseDocuments(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("ParseDocuments failed: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 documents, got %d", len(docs))
	}
	// Verify the first document contains the --- in the string
	if !strings.Contains(string(docs[0]), "This contains --- three dashes") {
		t.Errorf("expected first document to contain '---' in string, got: %s", string(docs[0]))
	}
}

func TestParseDocuments_SeparatorInComment(t *testing.T) {
	// Test that --- in a comment should not split documents
	yaml := `apiVersion: v1beta1
kind: Realm
metadata:
  name: test-realm
  # This is a comment with --- three dashes
spec:
  namespace: test-ns
---
apiVersion: v1beta1
kind: Space
metadata:
  name: test-space
spec:
  realmId: test-realm
`
	docs, err := parser.ParseDocuments(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("ParseDocuments failed: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 documents, got %d", len(docs))
	}
	// Verify the first document contains the comment
	if !strings.Contains(string(docs[0]), "# This is a comment with --- three dashes") {
		t.Errorf("expected first document to contain comment with '---', got: %s", string(docs[0]))
	}
}

func TestParseDocuments_SeparatorInMultilineString(t *testing.T) {
	// Test that --- in a multiline string should not split documents
	yaml := `apiVersion: v1beta1
kind: Realm
metadata:
  name: test-realm
spec:
  namespace: test-ns
  description: |
    This is a multiline string
    that contains --- three dashes
    in the middle of the content
---
apiVersion: v1beta1
kind: Space
metadata:
  name: test-space
spec:
  realmId: test-realm
`
	docs, err := parser.ParseDocuments(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("ParseDocuments failed: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 documents, got %d", len(docs))
	}
	// Verify the first document contains the multiline string with ---
	if !strings.Contains(string(docs[0]), "that contains --- three dashes") {
		t.Errorf("expected first document to contain multiline string with '---', got: %s", string(docs[0]))
	}
}

func TestParseDocuments_LeadingWhitespace(t *testing.T) {
	// Test that --- with leading whitespace should still split documents
	yaml := `apiVersion: v1beta1
kind: Realm
metadata:
  name: realm1
    ---
apiVersion: v1beta1
kind: Space
metadata:
  name: space1
spec:
  realmId: realm1
`
	docs, err := parser.ParseDocuments(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("ParseDocuments failed: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 documents, got %d", len(docs))
	}
}

func TestParseDocuments_FirstDocumentNoSeparator(t *testing.T) {
	// Test that first document without --- should work
	yaml := `apiVersion: v1beta1
kind: Realm
metadata:
  name: realm1
spec:
  namespace: test-ns
---
apiVersion: v1beta1
kind: Space
metadata:
  name: space1
spec:
  realmId: realm1
`
	docs, err := parser.ParseDocuments(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("ParseDocuments failed: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 documents, got %d", len(docs))
	}
}

func TestParseDocuments_ComplexContent(t *testing.T) {
	// Test complex YAML with --- in various places
	yaml := `apiVersion: v1beta1
kind: Realm
metadata:
  name: test-realm
  labels:
    key1: "value with --- dashes"
    key2: |
      Multiline
      with --- dashes
  # Comment with --- dashes
spec:
  namespace: test-ns
  description: "String with --- dashes"
---
apiVersion: v1beta1
kind: Space
metadata:
  name: test-space
  # Another comment --- here
spec:
  realmId: test-realm
  config: |
    Some config
    with --- dashes
    in it
---
apiVersion: v1beta1
kind: Stack
metadata:
  name: test-stack
spec:
  realmId: test-realm
  spaceId: test-space
`
	docs, err := parser.ParseDocuments(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("ParseDocuments failed: %v", err)
	}
	if len(docs) != 3 {
		t.Fatalf("expected 3 documents, got %d", len(docs))
	}
	// Verify each document contains the expected content
	if !strings.Contains(string(docs[0]), "value with --- dashes") {
		t.Errorf("expected first document to contain '---' in label value")
	}
	if !strings.Contains(string(docs[1]), "with --- dashes") {
		t.Errorf("expected second document to contain '---' in multiline config")
	}
	if !strings.Contains(string(docs[2]), "kind: Stack") {
		t.Errorf("expected third document to be a Stack")
	}
}
