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
	"errors"
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

// TestParseDocument_CellProfile_MigrationPointer asserts the parser surfaces
// the #626 migration pointer when a YAML carries the removed `kind:
// CellProfile`, rather than the generic unknown-kind error. The kind is a
// common stumble during the cutover, so the explicit pointer is worth the
// extra branch.
func TestParseDocument_CellProfile_MigrationPointer(t *testing.T) {
	yaml := `apiVersion: v1beta1
kind: CellProfile
metadata:
  name: test
`
	_, err := parser.ParseDocument(0, []byte(yaml))
	if err == nil {
		t.Fatal("expected error for removed CellProfile kind, got nil")
	}
	if !errors.Is(err, errdefs.ErrUnknownKind) {
		t.Errorf("expected error to wrap ErrUnknownKind, got: %v", err)
	}
	if !strings.Contains(err.Error(), "CellBlueprint") {
		t.Errorf("expected error to point at CellBlueprint, got: %v", err)
	}
	if !strings.Contains(err.Error(), "#626") {
		t.Errorf("expected error to cite the removal issue, got: %v", err)
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

func TestValidateDocument_Container_SecretMissingSource(t *testing.T) {
	yaml := `apiVersion: v1beta1
kind: Container
metadata:
  name: test-container
spec:
  id: test-container
  realmId: r
  spaceId: s
  stackId: k
  cellId: c
  image: alpine:latest
  secrets:
    - name: BROKEN
`
	doc, err := parser.ParseDocument(0, []byte(yaml))
	if err != nil {
		t.Fatalf("ParseDocument failed: %v", err)
	}

	validationErr := parser.ValidateDocument(doc)
	if validationErr == nil {
		t.Fatal("expected validation error for missing secret source, got nil")
	}
	if !strings.Contains(validationErr.Error(), errdefs.ErrSecretSourceRequired.Error()) {
		t.Errorf("expected ErrSecretSourceRequired, got: %v", validationErr)
	}
}

func TestValidateDocument_Container_SecretMultipleSources(t *testing.T) {
	yaml := `apiVersion: v1beta1
kind: Container
metadata:
  name: test-container
spec:
  id: test-container
  realmId: r
  spaceId: s
  stackId: k
  cellId: c
  image: alpine:latest
  secrets:
    - name: BOTH
      fromFile: /etc/foo
      fromEnv: FOO
`
	doc, err := parser.ParseDocument(0, []byte(yaml))
	if err != nil {
		t.Fatalf("ParseDocument failed: %v", err)
	}

	validationErr := parser.ValidateDocument(doc)
	if validationErr == nil {
		t.Fatal("expected validation error for multiple secret sources, got nil")
	}
	if !strings.Contains(validationErr.Error(), errdefs.ErrSecretMultipleSources.Error()) {
		t.Errorf("expected ErrSecretMultipleSources, got: %v", validationErr)
	}
}

func TestValidateDocument_Container_SecretMissingName(t *testing.T) {
	yaml := `apiVersion: v1beta1
kind: Container
metadata:
  name: test-container
spec:
  id: test-container
  realmId: r
  spaceId: s
  stackId: k
  cellId: c
  image: alpine:latest
  secrets:
    - fromEnv: FOO
`
	doc, err := parser.ParseDocument(0, []byte(yaml))
	if err != nil {
		t.Fatalf("ParseDocument failed: %v", err)
	}

	validationErr := parser.ValidateDocument(doc)
	if validationErr == nil {
		t.Fatal("expected validation error for missing secret name, got nil")
	}
	if !strings.Contains(validationErr.Error(), errdefs.ErrSecretNameRequired.Error()) {
		t.Errorf("expected ErrSecretNameRequired, got: %v", validationErr)
	}
}

func TestValidateDocument_Container_SecretMountPathNotAbsolute(t *testing.T) {
	yaml := `apiVersion: v1beta1
kind: Container
metadata:
  name: test-container
spec:
  id: test-container
  realmId: r
  spaceId: s
  stackId: k
  cellId: c
  image: alpine:latest
  secrets:
    - name: TLS
      fromFile: /etc/kukeon/secrets/tls.crt
      mountPath: relative/tls.crt
`
	doc, err := parser.ParseDocument(0, []byte(yaml))
	if err != nil {
		t.Fatalf("ParseDocument failed: %v", err)
	}

	validationErr := parser.ValidateDocument(doc)
	if validationErr == nil {
		t.Fatal("expected validation error for relative mountPath, got nil")
	}
	if !strings.Contains(validationErr.Error(), errdefs.ErrSecretMountPathNotAbsolute.Error()) {
		t.Errorf("expected ErrSecretMountPathNotAbsolute, got: %v", validationErr)
	}
}

func TestValidateDocument_Container_SecretValidFormsAccepted(t *testing.T) {
	yaml := `apiVersion: v1beta1
kind: Container
metadata:
  name: test-container
spec:
  id: test-container
  realmId: r
  spaceId: s
  stackId: k
  cellId: c
  image: alpine:latest
  secrets:
    - name: ANTHROPIC_API_KEY
      fromFile: /etc/kukeon/secrets/anthropic.key
    - name: GITHUB_TOKEN
      fromEnv: GITHUB_TOKEN_SCOPED
    - name: tls.crt
      fromFile: /etc/kukeon/secrets/tls.crt
      mountPath: /run/secrets/tls.crt
`
	doc, err := parser.ParseDocument(0, []byte(yaml))
	if err != nil {
		t.Fatalf("ParseDocument failed: %v", err)
	}

	if validationErr := parser.ValidateDocument(doc); validationErr != nil {
		t.Fatalf("expected valid secrets to pass, got: %v", validationErr)
	}
}

func TestValidateDocument_Container_SecretRefValidFormsAccepted(t *testing.T) {
	yaml := `apiVersion: v1beta1
kind: Container
metadata:
  name: test-container
spec:
  id: test-container
  realmId: r
  spaceId: s
  stackId: k
  cellId: c
  image: alpine:latest
  secrets:
    - name: ANTHROPIC_AUTH_TOKEN
      secretRef:
        name: anthropic-token
        realm: kuke-system
    - name: tls.crt
      secretRef:
        name: tls-cert
        realm: default
        space: ai
        stack: agents
        cell: claude
      mountPath: /run/secrets/tls.crt
`
	doc, err := parser.ParseDocument(0, []byte(yaml))
	if err != nil {
		t.Fatalf("ParseDocument failed: %v", err)
	}

	if validationErr := parser.ValidateDocument(doc); validationErr != nil {
		t.Fatalf("expected valid secretRef forms to pass, got: %v", validationErr)
	}
}

func TestValidateDocument_Container_SecretRefValidation(t *testing.T) {
	base := `apiVersion: v1beta1
kind: Container
metadata:
  name: test-container
spec:
  id: test-container
  realmId: r
  spaceId: s
  stackId: k
  cellId: c
  image: alpine:latest
  secrets:
    - name: TOKEN
      secretRef:
`
	cases := []struct {
		name    string
		refYAML string
		wantErr error
	}{
		{
			name:    "missing name",
			refYAML: "        realm: kuke-system\n",
			wantErr: errdefs.ErrSecretRefNameRequired,
		},
		{
			name:    "missing realm",
			refYAML: "        name: anthropic-token\n",
			wantErr: errdefs.ErrSecretRefRealmRequired,
		},
		{
			name:    "cell without stack",
			refYAML: "        name: anthropic-token\n        realm: default\n        space: ai\n        cell: claude\n",
			wantErr: errdefs.ErrSecretRefScopeIncomplete,
		},
		{
			name:    "stack without space",
			refYAML: "        name: anthropic-token\n        realm: default\n        stack: agents\n",
			wantErr: errdefs.ErrSecretRefScopeIncomplete,
		},
		{
			name:    "name traversal",
			refYAML: "        name: ../../../etc/shadow\n        realm: kuke-system\n",
			wantErr: errdefs.ErrSecretCoordUnsafe,
		},
		{
			name:    "realm traversal",
			refYAML: "        name: anthropic-token\n        realm: ..\n",
			wantErr: errdefs.ErrSecretCoordUnsafe,
		},
		{
			name:    "cell separator",
			refYAML: "        name: anthropic-token\n        realm: default\n        space: ai\n        stack: agents\n        cell: claude/../../root\n",
			wantErr: errdefs.ErrSecretCoordUnsafe,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := parser.ParseDocument(0, []byte(base+tc.refYAML))
			if err != nil {
				t.Fatalf("ParseDocument failed: %v", err)
			}
			validationErr := parser.ValidateDocument(doc)
			if validationErr == nil {
				t.Fatalf("expected validation error for %s, got nil", tc.name)
			}
			if !strings.Contains(validationErr.Error(), tc.wantErr.Error()) {
				t.Errorf("expected %v, got: %v", tc.wantErr, validationErr)
			}
		})
	}
}

func TestValidateDocument_Container_SecretRefRejectsExtraSource(t *testing.T) {
	yaml := `apiVersion: v1beta1
kind: Container
metadata:
  name: test-container
spec:
  id: test-container
  realmId: r
  spaceId: s
  stackId: k
  cellId: c
  image: alpine:latest
  secrets:
    - name: TOKEN
      fromEnv: SOME_ENV
      secretRef:
        name: anthropic-token
        realm: kuke-system
`
	doc, err := parser.ParseDocument(0, []byte(yaml))
	if err != nil {
		t.Fatalf("ParseDocument failed: %v", err)
	}
	validationErr := parser.ValidateDocument(doc)
	if validationErr == nil {
		t.Fatal("expected validation error for fromEnv + secretRef, got nil")
	}
	if !strings.Contains(validationErr.Error(), errdefs.ErrSecretMultipleSources.Error()) {
		t.Errorf("expected ErrSecretMultipleSources, got: %v", validationErr)
	}
}

func TestValidateDocument_Container_RepoValidation(t *testing.T) {
	base := `apiVersion: v1beta1
kind: Container
metadata:
  name: test-container
spec:
  id: test-container
  realmId: r
  spaceId: s
  stackId: k
  cellId: c
  image: alpine:latest
  attachable: true
  repos:
`
	cases := []struct {
		name     string
		repoYAML string
		wantErr  error // nil = expect pass
	}{
		{
			name:     "missing name",
			repoYAML: "    - target: /home/claude/p\n      url: https://example.com/p.git\n",
			wantErr:  errdefs.ErrRepoNameRequired,
		},
		{
			name:     "missing target",
			repoYAML: "    - name: p\n      url: https://example.com/p.git\n",
			wantErr:  errdefs.ErrRepoTargetRequired,
		},
		{
			name:     "relative target",
			repoYAML: "    - name: p\n      target: rel/p\n      url: https://example.com/p.git\n",
			wantErr:  errdefs.ErrRepoTargetNotAbsolute,
		},
		{
			name:     "missing url",
			repoYAML: "    - name: p\n      target: /home/claude/p\n",
			wantErr:  errdefs.ErrRepoURLRequired,
		},
		{
			name:     "valid",
			repoYAML: "    - name: project\n      target: /home/claude/project\n      branch: main\n      url: https://example.com/p.git\n      required: true\n",
			wantErr:  nil,
		},
		{
			name:     "valid ref pin",
			repoYAML: "    - name: pinned\n      target: /home/claude/pinned\n      ref: v0.1.0\n      url: https://example.com/p.git\n      required: true\n",
			wantErr:  nil,
		},
		{
			name:     "branch and ref both set",
			repoYAML: "    - name: pinned\n      target: /home/claude/pinned\n      branch: main\n      ref: v0.1.0\n      url: https://example.com/p.git\n",
			wantErr:  errdefs.ErrRepoBranchRefMutex,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := parser.ParseDocument(0, []byte(base+tc.repoYAML))
			if err != nil {
				t.Fatalf("ParseDocument failed: %v", err)
			}
			validationErr := parser.ValidateDocument(doc)
			if tc.wantErr == nil {
				if validationErr != nil {
					t.Fatalf("expected valid repos to pass, got: %v", validationErr)
				}
				return
			}
			if validationErr == nil {
				t.Fatalf("expected validation error %v, got nil", tc.wantErr)
			}
			if !strings.Contains(validationErr.Error(), tc.wantErr.Error()) {
				t.Errorf("expected %v, got: %v", tc.wantErr, validationErr)
			}
		})
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

// TestParseDocument_Secret confirms a Secret document parses into the typed
// SecretDoc with its scope coordinates and material (issue #619).
func TestParseDocument_Secret(t *testing.T) {
	yaml := `
apiVersion: v1beta1
kind: Secret
metadata:
  name: anthropic-token
  realm: kuke-system
spec:
  data: s3cr3t-bytes`

	doc, err := parser.ParseDocument(0, []byte(yaml))
	if err != nil {
		t.Fatalf("ParseDocument failed: %v", err)
	}
	if doc.Kind != v1beta1.KindSecret {
		t.Errorf("Kind = %q, want %q", doc.Kind, v1beta1.KindSecret)
	}
	if doc.SecretDoc == nil {
		t.Fatal("SecretDoc is nil")
	}
	if doc.SecretDoc.Metadata.Name != "anthropic-token" {
		t.Errorf("name = %q, want anthropic-token", doc.SecretDoc.Metadata.Name)
	}
	if doc.SecretDoc.Metadata.Realm != "kuke-system" {
		t.Errorf("realm = %q, want kuke-system", doc.SecretDoc.Metadata.Realm)
	}
	if doc.SecretDoc.Spec.Data != "s3cr3t-bytes" {
		t.Errorf("data = %q, want s3cr3t-bytes", doc.SecretDoc.Spec.Data)
	}
}

// TestValidateDocument_Secret exercises the issue #619 apply-time gates that
// don't require the runner: name required, realm required, scope-coordinate
// completeness, and non-empty data. The scope-reachability gate is enforced
// at reconcile time and is not covered here.
func TestValidateDocument_Secret(t *testing.T) {
	base := func() *v1beta1.SecretDoc {
		return &v1beta1.SecretDoc{
			APIVersion: v1beta1.APIVersionV1Beta1,
			Kind:       v1beta1.KindSecret,
			Metadata:   v1beta1.SecretMetadata{Name: "tok", Realm: "default"},
			Spec:       v1beta1.SecretSpec{Data: "x"},
		}
	}

	tests := []struct {
		name    string
		mutate  func(*v1beta1.SecretDoc)
		wantErr error // nil means valid
	}{
		{name: "valid realm-scoped", mutate: func(*v1beta1.SecretDoc) {}},
		{
			name:   "valid cell-scoped",
			mutate: func(d *v1beta1.SecretDoc) { d.Metadata.Space, d.Metadata.Stack, d.Metadata.Cell = "s", "st", "c" },
		},
		{
			name:    "missing name",
			mutate:  func(d *v1beta1.SecretDoc) { d.Metadata.Name = "" },
			wantErr: nil, // name error is a plain errors.New, asserted by message below
		},
		{
			name:    "missing realm",
			mutate:  func(d *v1beta1.SecretDoc) { d.Metadata.Realm = "" },
			wantErr: errdefs.ErrSecretRealmRequired,
		},
		{
			name:    "stack without space",
			mutate:  func(d *v1beta1.SecretDoc) { d.Metadata.Stack = "st" },
			wantErr: errdefs.ErrSecretScopeIncomplete,
		},
		{
			name:    "cell without stack",
			mutate:  func(d *v1beta1.SecretDoc) { d.Metadata.Space, d.Metadata.Cell = "s", "c" },
			wantErr: errdefs.ErrSecretScopeIncomplete,
		},
		{
			name:    "empty data",
			mutate:  func(d *v1beta1.SecretDoc) { d.Spec.Data = "   " },
			wantErr: errdefs.ErrSecretDataRequired,
		},
		{
			name:    "name traversal dotdot",
			mutate:  func(d *v1beta1.SecretDoc) { d.Metadata.Name = "../../../etc/shadow" },
			wantErr: errdefs.ErrSecretCoordUnsafe,
		},
		{
			name:    "name is dotdot",
			mutate:  func(d *v1beta1.SecretDoc) { d.Metadata.Name = ".." },
			wantErr: errdefs.ErrSecretCoordUnsafe,
		},
		{
			name:    "realm separator",
			mutate:  func(d *v1beta1.SecretDoc) { d.Metadata.Realm = "default/../kuke-system" },
			wantErr: errdefs.ErrSecretCoordUnsafe,
		},
		{
			name: "cell traversal",
			mutate: func(d *v1beta1.SecretDoc) {
				d.Metadata.Space, d.Metadata.Stack, d.Metadata.Cell = "s", "st", ".."
			},
			wantErr: errdefs.ErrSecretCoordUnsafe,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secretDoc := base()
			tt.mutate(secretDoc)
			doc := &parser.Document{
				Index:      0,
				APIVersion: secretDoc.APIVersion,
				Kind:       v1beta1.KindSecret,
				SecretDoc:  secretDoc,
			}
			err := parser.ValidateDocument(doc)

			// "missing name" surfaces a plain errors.New, not a sentinel, so
			// it is asserted by message rather than via errors.Is.
			if tt.name == "missing name" {
				if err == nil || !strings.Contains(err.Error(), "metadata.name is required") {
					t.Fatalf("err = %v, want metadata.name required", err)
				}
				return
			}

			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("ValidateDocument() = %v, want nil", err)
				}
				return
			}
			if err == nil || !errors.Is(err.Err, tt.wantErr) {
				t.Fatalf("ValidateDocument() = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
