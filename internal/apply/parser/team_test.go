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
	"errors"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/apply/parser"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// oldTeamHeader is the team apiVersion before v1beta1 (issue #1618, AC20),
// built here so the retired spelling appears nowhere in the tree.
const oldTeamHeader = "kuketeams" + ".io/v1"

const validTeam = `apiVersion: v1beta1
kind: Team
metadata:
  name: kukeon
  realm: default
spec:
  projectSource:
    name: project
    url: https://github.com/eminwux/kukeon
    branch: main
    clonePath: /home/user/kukeon
  roles:
    - {name: dev, source: project, path: team/roles/dev}
  harnesses: []
`

const validTeamDefaults = `apiVersion: v1beta1
kind: TeamDefaults
metadata:
  name: default
  realm: default
spec:
  defaults:
    sources:
      - {name: kukeon, url: https://github.com/eminwux/kukeon, branch: main}
    harnesses:
      - {name: claude, source: kukeon, path: team/harnesses/claude}
`

// parseAndValidate returns the first error of parsing then validating raw.
func parseAndValidate(raw string) (*parser.Document, error) {
	doc, err := parser.ParseDocument(0, []byte(raw))
	if err != nil {
		return nil, err
	}
	if vErr := parser.ValidateDocument(doc); vErr != nil {
		return doc, vErr
	}
	return doc, nil
}

func TestTeamKindsAccepted(t *testing.T) {
	doc, err := parseAndValidate(validTeam)
	if err != nil {
		t.Fatalf("Team rejected: %v", err)
	}
	if doc.Kind != v1beta1.KindTeam || doc.TeamDoc == nil {
		t.Fatalf("kind = %q, TeamDoc = %v", doc.Kind, doc.TeamDoc)
	}
	team := parser.TeamConfig(doc.TeamDoc)
	if team.Metadata.Name != "kukeon" || team.Spec.ProjectSource == nil ||
		team.Spec.ProjectSource.URL != "https://github.com/eminwux/kukeon" {
		t.Fatalf("converted team = %+v", team)
	}
	// An authored [] stays distinct from an omitted list.
	if team.Spec.Harnesses == nil || len(team.Spec.Harnesses) != 0 || team.Spec.Sources != nil {
		t.Fatalf("lists: harnesses=%#v sources=%#v", team.Spec.Harnesses, team.Spec.Sources)
	}

	doc, err = parseAndValidate(validTeamDefaults)
	if err != nil {
		t.Fatalf("TeamDefaults rejected: %v", err)
	}
	if doc.Kind != v1beta1.KindTeamDefaults || doc.TeamDefaultsDoc == nil {
		t.Fatalf("kind = %q, TeamDefaultsDoc = %v", doc.Kind, doc.TeamDefaultsDoc)
	}
	defaults := parser.TeamDefaultsConfig(doc.TeamDefaultsDoc)
	if len(defaults.Spec.Defaults.Sources) != 1 || defaults.Spec.Defaults.Roles != nil {
		t.Fatalf("converted defaults = %+v", defaults.Spec.Defaults)
	}
}

func TestTeamKindsRejected(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "unknown spec field",
			raw:  strings.Replace(validTeam, "  harnesses: []\n", "  harnesess: []\n", 1),
			want: "harnesess",
		},
		{
			name: "unknown top-level field",
			raw:  validTeam + "status: {}\n",
			want: "status",
		},
		{
			name: "old apiVersion",
			raw:  strings.Replace(validTeam, "apiVersion: v1beta1", "apiVersion: "+oldTeamHeader, 1),
			want: oldTeamHeader,
		},
		{
			name: "invalid name",
			raw:  strings.Replace(validTeam, "name: kukeon\n", "name: Not_Valid\n", 1),
			want: "metadata.name",
		},
		{
			name: "missing name",
			raw:  strings.Replace(validTeam, "  name: kukeon\n", "", 1),
			want: "metadata.name",
		},
		{
			name: "missing realm",
			raw:  strings.Replace(validTeam, "  realm: default\n", "", 1),
			want: "metadata.realm",
		},
		{
			name: "projectSource renamed",
			raw:  strings.Replace(validTeam, "    name: project\n", "    name: other\n", 1),
			want: "projectSource",
		},
		{
			name: "defaults not named default",
			raw:  strings.Replace(validTeamDefaults, "name: default\n", "name: other\n", 1),
			want: "metadata.name",
		},
		{
			name: "defaults unknown field",
			raw:  strings.Replace(validTeamDefaults, "    harnesses:", "    harnessez:", 1),
			want: "harnessez",
		},
		{
			name: "defaults old apiVersion",
			raw:  strings.Replace(validTeamDefaults, "apiVersion: v1beta1", "apiVersion: "+oldTeamHeader, 1),
			want: oldTeamHeader,
		},
		{
			name: "defaults missing realm",
			raw:  strings.Replace(validTeamDefaults, "  realm: default\n", "", 1),
			want: "metadata.realm",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseAndValidate(tt.raw)
			if err == nil {
				t.Fatalf("accepted:\n%s", tt.raw)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestTeamValidationErrorsAreInvalidTeam(t *testing.T) {
	for _, raw := range []string{
		strings.Replace(validTeam, "name: kukeon\n", "name: Not_Valid\n", 1),
		strings.Replace(validTeamDefaults, "name: default\n", "name: other\n", 1),
	} {
		doc, err := parser.ParseDocument(0, []byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		vErr := parser.ValidateDocument(doc)
		if vErr == nil || !errors.Is(vErr.Err, errdefs.ErrInvalidTeam) {
			t.Fatalf("err = %v, want ErrInvalidTeam", vErr)
		}
	}
}

// The source documents kuke reads from a fetched source are not server-side
// resources: kuke apply keeps rejecting them.
func TestSourceDocumentKindsRejected(t *testing.T) {
	for _, kind := range []v1beta1.Kind{
		v1beta1.KindRole, v1beta1.KindHarness, v1beta1.KindHarnessImageCatalog,
		v1beta1.KindComponent, v1beta1.KindConnector,
	} {
		t.Run(string(kind), func(t *testing.T) {
			raw := "apiVersion: v1beta1\nkind: " + string(kind) + "\nmetadata:\n  name: x\n"
			_, err := parseAndValidate(raw)
			if !errors.Is(err, errdefs.ErrUnknownKind) {
				t.Fatalf("err = %v, want ErrUnknownKind", err)
			}
		})
	}
}
