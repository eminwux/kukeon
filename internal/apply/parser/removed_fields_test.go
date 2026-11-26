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
	"encoding/json"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/apply/parser"
)

// TestRemovedContainerFieldsAreIgnored pins that a manifest written before
// ports, networks and networksAliases left the container spec still parses
// and validates: the keys are unknown, so they are dropped like any other.
func TestRemovedContainerFieldsAreIgnored(t *testing.T) {
	const removed = `ports: ["8080:80"]
networks: ["data-plane"]
networksAliases: ["web"]
`
	indent := func(prefix string) string {
		return prefix + strings.ReplaceAll(strings.TrimSuffix(removed, "\n"), "\n", "\n"+prefix) + "\n"
	}
	tests := []struct {
		name     string
		manifest string
		document func(*parser.Document) any
	}{
		{
			name: "Cell",
			manifest: `apiVersion: v1beta1
kind: Cell
metadata:
  name: web
spec:
  realmId: r
  spaceId: s
  stackId: k
  containers:
    - id: main
      image: alpine:latest
` + indent("      "),
			document: func(doc *parser.Document) any { return doc.CellDoc },
		},
		{
			name: "Container",
			manifest: `apiVersion: v1beta1
kind: Container
metadata:
  name: main
spec:
  id: main
  realmId: r
  spaceId: s
  stackId: k
  cellId: c
  image: alpine:latest
` + indent("  "),
			document: func(doc *parser.Document) any { return doc.ContainerDoc },
		},
		{
			name: "CellBlueprint",
			manifest: `apiVersion: v1beta1
kind: CellBlueprint
metadata:
  name: web
  realm: default
  space: agents
spec:
  cell:
    containers:
      - id: main
        image: alpine:latest
` + indent("        "),
			document: func(doc *parser.Document) any { return doc.CellBlueprintDoc },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := parser.ParseDocument(0, []byte(tt.manifest))
			if err != nil {
				t.Fatalf("ParseDocument: %v", err)
			}
			if validationErr := parser.ValidateDocument(doc); validationErr != nil {
				t.Fatalf("ValidateDocument: %v", validationErr)
			}
			out, err := json.Marshal(tt.document(doc))
			if err != nil {
				t.Fatalf("marshal parsed document: %v", err)
			}
			if !strings.Contains(string(out), `"image":"alpine:latest"`) {
				t.Fatalf("parsed document lost the container: %s", out)
			}
			for _, key := range []string{`"ports"`, `"networks"`, `"networksAliases"`} {
				if strings.Contains(string(out), key) {
					t.Errorf("parsed document still carries %s: %s", key, out)
				}
			}
		})
	}
}
