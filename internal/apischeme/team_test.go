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

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/apply/parser"
)

// TestNormalizeTeamRoundTrip: the stored form of a Team is canonical YAML
// that decodes back to the parsed document, keeping each list's presence
// and the projectSource and secrets in spec.
func TestNormalizeTeamRoundTrip(t *testing.T) {
	for _, spec := range []string{
		"",
		"spec:\n  roles: []\n",
		"spec:\n  projectSource: {url: https://example.test/a, branch: main}\n  sources: []\n" +
			"  secrets:\n    - {name: gh-token, secretRef: {name: gh-token, realm: default}, envName: GH_TOKEN}\n",
	} {
		raw := "kind: Team\nmetadata:\n  name: alpha\n  realm: default\n" + spec
		doc, err := parser.ParseDocument(0, []byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		stored, err := apischeme.NormalizeTeam(*doc.TeamDoc)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(string(stored.Document), "apiVersion: v1beta1\n") {
			t.Fatalf("apiVersion not defaulted:\n%s", stored.Document)
		}
		back, err := apischeme.ConvertTeamToExternal(stored)
		if err != nil {
			t.Fatal(err)
		}
		want := *doc.TeamDoc
		want.APIVersion = back.APIVersion
		if !reflect.DeepEqual(back, want) {
			t.Fatalf("round trip of %q:\nstored:\n%s\ngot %+v\nwant %+v", spec, stored.Document, back, want)
		}
		if strings.Contains(spec, "roles: []") != strings.Contains(string(stored.Document), "roles: []") ||
			strings.Contains(string(stored.Document), "harnesses") {
			t.Fatalf("list presence changed:\n%s", stored.Document)
		}
	}
}

func TestNormalizeTeamDefaultsRoundTrip(t *testing.T) {
	raw := "kind: TeamDefaults\nmetadata:\n  name: default\n  realm: default\nspec:\n  defaults:\n    roles: []\n"
	doc, err := parser.ParseDocument(0, []byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	stored, err := apischeme.NormalizeTeamDefaults(*doc.TeamDefaultsDoc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stored.Document), "roles: []") || strings.Contains(string(stored.Document), "sources") {
		t.Fatalf("stored:\n%s", stored.Document)
	}
	back, err := apischeme.ConvertTeamDefaultsToExternal(stored)
	if err != nil || back.Spec.Defaults.Roles == nil || back.Spec.Defaults.Sources != nil {
		t.Fatalf("back = %+v, %v", back, err)
	}
}

func TestConvertTeamRejectsUnknownStoredField(t *testing.T) {
	doc, err := parser.ParseDocument(0, []byte("kind: Team\nmetadata:\n  name: alpha\n  realm: default\n"))
	if err != nil {
		t.Fatal(err)
	}
	stored, err := apischeme.NormalizeTeam(*doc.TeamDoc)
	if err != nil {
		t.Fatal(err)
	}
	stored.Document = append(stored.Document, []byte("bogus: 1\n")...)
	if _, err = apischeme.ConvertTeamToExternal(stored); err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("err = %v", err)
	}
}
