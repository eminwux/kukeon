// Copyright 2026 Emiliano Spinella (eminwux)
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
	"strconv"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/apply/parser"
	"github.com/eminwux/kukeon/internal/errdefs"
)

// TestValidateDocument_StoredKindNames is #1719's apply-time half: a
// CellBlueprint, CellConfig, Secret or Volume whose name or a scope coordinate
// fails the shared name rule is refused before reconcile can join it into a
// path, "../metadata.json" included.
func TestValidateDocument_StoredKindNames(t *testing.T) {
	bad := []string{"..", "../metadata.json", "../../../x", ".x", "A", "a_b", "a b"}
	tests := []struct {
		name string
		doc  string // %s is replaced with the bad value
		want error
	}{
		{
			"blueprint name", strings.Replace(validBlueprint, "name: web", "name: %s", 1),
			errdefs.ErrBlueprintCoordUnsafe,
		},
		{
			"blueprint realm", strings.Replace(validBlueprint, "realm: default", "realm: %s", 1),
			errdefs.ErrBlueprintCoordUnsafe,
		},
		{
			"blueprint space", strings.Replace(validBlueprint, "space: agents", "space: %s", 1),
			errdefs.ErrBlueprintCoordUnsafe,
		},
		{
			"config name", strings.Replace(validConfig, "name: kukeon-dev", "name: %s", 1),
			errdefs.ErrConfigCoordUnsafe,
		},
		{
			"config space", strings.Replace(validConfig, "space: default", "space: %s", 1),
			errdefs.ErrConfigCoordUnsafe,
		},
		{
			"config blueprint ref", strings.Replace(validConfig, "name: dev", "name: %s", 1),
			errdefs.ErrBlueprintCoordUnsafe,
		},
		{
			"secret name", "apiVersion: v1beta1\nkind: Secret\nmetadata:\n  name: %s\n  realm: default\nspec:\n  data: eA==\n",
			errdefs.ErrSecretCoordUnsafe,
		},
		{
			"volume name", "apiVersion: v1beta1\nkind: Volume\nmetadata:\n  name: %s\n  realm: default\n",
			errdefs.ErrVolumeCoordUnsafe,
		},
	}
	for _, tt := range tests {
		for _, value := range bad {
			t.Run(tt.name+"/"+value, func(t *testing.T) {
				raw := strings.Replace(tt.doc, "%s", strconv.Quote(value), 1)
				doc, err := parser.ParseDocument(0, []byte(raw))
				if err != nil {
					t.Fatalf("ParseDocument: %v\n%s", err, raw)
				}
				requireValidationErr(t, parser.ValidateDocument(doc), tt.want)
			})
		}
	}
}
