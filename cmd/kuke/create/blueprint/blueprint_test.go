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

package blueprint_test

import (
	"bytes"
	"strings"
	"testing"

	blueprintcmd "github.com/eminwux/kukeon/cmd/kuke/create/blueprint"
	"github.com/eminwux/kukeon/internal/apply/parser"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/viper"
)

// runScaffold executes the subcommand with the given args/flags and returns
// stdout. Centralizes the SetOut/SetErr boilerplate so individual cases stay
// focused on the assertion they care about.
func runScaffold(t *testing.T, args []string, flags map[string]string) string {
	t.Helper()
	t.Cleanup(viper.Reset)

	cmd := blueprintcmd.NewBlueprintCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	for k, v := range flags {
		if err := cmd.Flags().Set(k, v); err != nil {
			t.Fatalf("set flag %s=%s: %v", k, v, err)
		}
	}
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v\nOutput: %s", err, buf.String())
	}
	return buf.String()
}

func TestNewBlueprintCmd_RequiresName(t *testing.T) {
	t.Cleanup(viper.Reset)
	cmd := blueprintcmd.NewBlueprintCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected name-required error, got nil")
	}
}

func TestNewBlueprintCmd_DefaultsScope(t *testing.T) {
	// No flags set — scope must default per cmd/config/env.go (realm/space/stack = "default").
	out := runScaffold(t, []string{"web"}, nil)

	doc, err := parser.ParseDocument(0, []byte(out))
	if err != nil {
		t.Fatalf("ParseDocument failed on default-scope scaffold: %v\nYAML:\n%s", err, out)
	}
	if doc.Kind != v1beta1.KindCellBlueprint {
		t.Fatalf("parsed kind = %q, want %q", doc.Kind, v1beta1.KindCellBlueprint)
	}
	if doc.CellBlueprintDoc == nil {
		t.Fatalf("expected CellBlueprintDoc, got nil")
	}
	md := doc.CellBlueprintDoc.Metadata
	if md.Name != "web" {
		t.Errorf("metadata.name = %q, want %q", md.Name, "web")
	}
	if md.Realm != "default" || md.Space != "default" || md.Stack != "default" {
		t.Errorf("scope = %+v, want realm=default space=default stack=default", md)
	}
}

func TestNewBlueprintCmd_RespectsScopeFlags(t *testing.T) {
	out := runScaffold(t, []string{"web"}, map[string]string{
		"realm": "production",
		"space": "team-a",
		"stack": "frontend",
	})

	doc, err := parser.ParseDocument(0, []byte(out))
	if err != nil {
		t.Fatalf("ParseDocument failed: %v\nYAML:\n%s", err, out)
	}
	md := doc.CellBlueprintDoc.Metadata
	if md.Realm != "production" || md.Space != "team-a" || md.Stack != "frontend" {
		t.Errorf("scope = %+v, want realm=production space=team-a stack=frontend", md)
	}
}

func TestNewBlueprintCmd_EmitsRequiredHeader(t *testing.T) {
	out := runScaffold(t, []string{"web"}, nil)

	wantLines := []string{
		"apiVersion: " + string(v1beta1.APIVersionV1Beta1),
		"kind: " + string(v1beta1.KindCellBlueprint),
		"metadata:",
		"  name: web",
		"  realm: default",
		"spec:",
		"  cell:",
		"    containers:",
		"      - id: main",
	}
	for _, line := range wantLines {
		if !strings.Contains(out, line) {
			t.Errorf("output missing required line %q\nGot:\n%s", line, out)
		}
	}
}

func TestNewBlueprintCmd_EmitsImageTODO(t *testing.T) {
	out := runScaffold(t, []string{"web"}, nil)

	// The image field is the only required field per container; it must carry a
	// `# TODO (required)` marker so the operator can't miss it.
	if !strings.Contains(out, `image: "" # TODO (required)`) {
		t.Errorf("scaffold missing required image TODO marker\nGot:\n%s", out)
	}
}

func TestNewBlueprintCmd_EmitsOptionalCommentMarkers(t *testing.T) {
	out := runScaffold(t, []string{"web"}, nil)

	// AC: comment markers for parameters, ports, volumes (AC says "volumeMounts"
	// but the model field is `volumes`), repos, secrets.
	wantMarkers := []string{
		"# parameters:",
		"# ports:",
		"# volumes:",
		"# repos:",
		"# secrets:",
	}
	for _, marker := range wantMarkers {
		if !strings.Contains(out, marker) {
			t.Errorf("output missing optional-section marker %q\nGot:\n%s", marker, out)
		}
	}
}

func TestNewBlueprintCmd_ScaffoldParsesAndValidates(t *testing.T) {
	// The scaffold must parse cleanly via the same loader `kuke apply -f` uses.
	// Empty image is acceptable at validate time for a Blueprint (validation is
	// structural; the image-required check is enforced downstream at run time),
	// so the scaffold itself satisfies both ParseDocument and ValidateDocument.
	out := runScaffold(t, []string{"web"}, nil)

	doc, err := parser.ParseDocument(0, []byte(out))
	if err != nil {
		t.Fatalf("ParseDocument failed: %v\nYAML:\n%s", err, out)
	}
	if vErr := parser.ValidateDocument(doc); vErr != nil {
		t.Fatalf("ValidateDocument failed: %v\nYAML:\n%s", vErr, out)
	}
}

func TestNewBlueprintCmd_RoundTripAfterFillingImage(t *testing.T) {
	// AC round-trip: after the operator fills the required `image:` field, the
	// scaffold must apply cleanly. Simulate the edit by string-substituting the
	// TODO placeholder for a real image, then re-parse + re-validate.
	out := runScaffold(t, []string{"web"}, nil)

	filled := strings.Replace(out,
		`image: "" # TODO (required)`,
		`image: alpine:latest`,
		1,
	)
	if filled == out {
		t.Fatalf("test guard: image TODO line not found in scaffold; scaffold drift?\nGot:\n%s", out)
	}

	doc, err := parser.ParseDocument(0, []byte(filled))
	if err != nil {
		t.Fatalf("ParseDocument failed after image fill: %v\nYAML:\n%s", err, filled)
	}
	if vErr := parser.ValidateDocument(doc); vErr != nil {
		t.Fatalf("ValidateDocument failed after image fill: %v\nYAML:\n%s", vErr, filled)
	}
	if got := doc.CellBlueprintDoc.Spec.Cell.Containers[0].Image; got != "alpine:latest" {
		t.Errorf("container.image = %q, want %q", got, "alpine:latest")
	}
}

func TestNewBlueprintCmd_QuotesUnsafeName(t *testing.T) {
	// Names that aren't bare-YAML-safe must be double-quoted in the emitted
	// document so they round-trip as strings (not parsed as bool/null/numeric).
	out := runScaffold(t, []string{"name with spaces"}, nil)

	if !strings.Contains(out, `name: "name with spaces"`) {
		t.Errorf("expected quoted name in metadata\nGot:\n%s", out)
	}

	doc, err := parser.ParseDocument(0, []byte(out))
	if err != nil {
		t.Fatalf("ParseDocument failed on quoted-name scaffold: %v\nYAML:\n%s", err, out)
	}
	if doc.CellBlueprintDoc.Metadata.Name != "name with spaces" {
		t.Errorf("metadata.name = %q, want %q", doc.CellBlueprintDoc.Metadata.Name, "name with spaces")
	}
}
