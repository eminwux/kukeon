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

package cellblueprint_test

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/cellblueprint"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func strptr(s string) *string { return &s }

// baseDoc returns a minimal runnable blueprint with a single container and one
// parameter substituted into the image tag.
func baseDoc() v1beta1.CellBlueprintDoc {
	return v1beta1.CellBlueprintDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellBlueprint,
		Metadata: v1beta1.CellBlueprintMetadata{
			Name:   "web",
			Realm:  "default",
			Space:  "agents",
			Stack:  "claude",
			Labels: map[string]string{"team": "infra"},
		},
		Spec: v1beta1.CellBlueprintSpec{
			Prefix: "web",
			Parameters: []v1beta1.CellBlueprintParameter{
				{Name: "TAG", Default: strptr("latest")},
			},
			Cell: v1beta1.BlueprintCellSpec{
				Containers: []v1beta1.BlueprintContainer{
					{
						ID:         "main",
						Image:      "registry.example.com/web:${TAG}",
						Attachable: true,
					},
				},
			},
		},
	}
}

func TestResolve_DefaultParameter(t *testing.T) {
	out, err := cellblueprint.Resolve(baseDoc(), nil, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := out.Spec.Cell.Containers[0].Image; got != "registry.example.com/web:latest" {
		t.Fatalf("image = %q, want default-substituted", got)
	}
}

func TestResolve_CliParamWins(t *testing.T) {
	out, err := cellblueprint.Resolve(baseDoc(), map[string]string{"TAG": "v2"}, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := out.Spec.Cell.Containers[0].Image; got != "registry.example.com/web:v2" {
		t.Fatalf("image = %q, want cli-param override", got)
	}
}

func TestResolve_EnvFallback(t *testing.T) {
	doc := baseDoc()
	doc.Spec.Parameters = []v1beta1.CellBlueprintParameter{{Name: "TAG"}} // no default
	lookup := func(k string) (string, bool) {
		if k == "TAG" {
			return "from-env", true
		}
		return "", false
	}
	out, err := cellblueprint.Resolve(doc, nil, lookup)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := out.Spec.Cell.Containers[0].Image; got != "registry.example.com/web:from-env" {
		t.Fatalf("image = %q, want env fallback", got)
	}
}

func TestResolve_UndeclaredParamErrors(t *testing.T) {
	_, err := cellblueprint.Resolve(baseDoc(), map[string]string{"NOPE": "x"}, nil)
	if !errors.Is(err, errdefs.ErrBlueprintInvalid) {
		t.Fatalf("err = %v, want ErrBlueprintInvalid for undeclared param", err)
	}
}

func TestResolve_RequiredUnsetErrors(t *testing.T) {
	doc := baseDoc()
	doc.Spec.Parameters = []v1beta1.CellBlueprintParameter{{Name: "TAG", Required: true}}
	_, err := cellblueprint.Resolve(doc, nil, nil)
	if !errors.Is(err, errdefs.ErrBlueprintInvalid) {
		t.Fatalf("err = %v, want ErrBlueprintInvalid for required-unset param", err)
	}
}

var hexSuffixRE = regexp.MustCompile(`^web-[0-9a-f]{6}$`)

func TestMaterialize_GeneratedNameAndScope(t *testing.T) {
	resolved, err := cellblueprint.Resolve(baseDoc(), nil, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	cell, err := cellblueprint.Materialize(resolved)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if !hexSuffixRE.MatchString(cell.Metadata.Name) {
		t.Fatalf("name = %q, want <prefix>-<6hex>", cell.Metadata.Name)
	}
	if cell.Spec.ID != cell.Metadata.Name {
		t.Fatalf("spec.id = %q, want = metadata.name %q", cell.Spec.ID, cell.Metadata.Name)
	}
	if cell.Spec.RealmID != "default" || cell.Spec.SpaceID != "agents" || cell.Spec.StackID != "claude" {
		t.Fatalf("scope = %q/%q/%q, want default/agents/claude", cell.Spec.RealmID, cell.Spec.SpaceID, cell.Spec.StackID)
	}
	if cell.Metadata.Labels[cellblueprint.LabelBlueprint] != "web" {
		t.Fatalf("missing blueprint back-reference label: %v", cell.Metadata.Labels)
	}
	if cell.Metadata.Labels["team"] != "infra" {
		t.Fatalf("metadata labels not carried through: %v", cell.Metadata.Labels)
	}
}

func TestMaterialize_FreshNameEachCall(t *testing.T) {
	resolved, _ := cellblueprint.Resolve(baseDoc(), nil, nil)
	a, _ := cellblueprint.Materialize(resolved)
	b, _ := cellblueprint.Materialize(resolved)
	if a.Metadata.Name == b.Metadata.Name {
		t.Fatalf("two materializations produced the same name %q", a.Metadata.Name)
	}
}

func TestMaterialize_NameOverride(t *testing.T) {
	resolved, _ := cellblueprint.Resolve(baseDoc(), nil, nil)
	cell, err := cellblueprint.MaterializeWithName(resolved, "pinned")
	if err != nil {
		t.Fatalf("MaterializeWithName: %v", err)
	}
	if cell.Metadata.Name != "pinned" || cell.Spec.ID != "pinned" {
		t.Fatalf("override not honored: name=%q id=%q", cell.Metadata.Name, cell.Spec.ID)
	}
}

func TestMaterialize_FilledRepoCarried(t *testing.T) {
	doc := baseDoc()
	doc.Spec.Cell.Containers[0].Repos = []v1beta1.ContainerRepo{
		{Name: "app", Target: "/src", URL: "https://example.com/app.git"},
	}
	resolved, _ := cellblueprint.Resolve(doc, nil, nil)
	cell, err := cellblueprint.Materialize(resolved)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if len(cell.Spec.Containers[0].Repos) != 1 {
		t.Fatalf("filled repo dropped: %+v", cell.Spec.Containers[0].Repos)
	}
}

func TestMaterialize_OptionalEmptyRepoDropped(t *testing.T) {
	doc := baseDoc()
	doc.Spec.Cell.Containers[0].Repos = []v1beta1.ContainerRepo{
		{Name: "app", Target: "/src"}, // no url, not required → structural slot, dropped
	}
	resolved, _ := cellblueprint.Resolve(doc, nil, nil)
	cell, err := cellblueprint.Materialize(resolved)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if len(cell.Spec.Containers[0].Repos) != 0 {
		t.Fatalf("optional empty repo slot not dropped: %+v", cell.Spec.Containers[0].Repos)
	}
}

func TestMaterialize_RequiredEmptyRepoBlocks(t *testing.T) {
	doc := baseDoc()
	doc.Spec.Cell.Containers[0].Repos = []v1beta1.ContainerRepo{
		{Name: "app", Target: "/src", Required: true}, // no url, required → blocks inline -b
	}
	resolved, _ := cellblueprint.Resolve(doc, nil, nil)
	_, err := cellblueprint.Materialize(resolved)
	if !errors.Is(err, errdefs.ErrBlueprintStructuralSlots) {
		t.Fatalf("err = %v, want ErrBlueprintStructuralSlots", err)
	}
}

func TestMaterialize_RequiredSecretSlotBlocks(t *testing.T) {
	doc := baseDoc()
	doc.Spec.Cell.Containers[0].Secrets = []v1beta1.BlueprintSecretSlot{
		{Name: "token", Mode: v1beta1.BlueprintSecretModeEnv, EnvName: "TOKEN", Required: true},
	}
	resolved, _ := cellblueprint.Resolve(doc, nil, nil)
	_, err := cellblueprint.Materialize(resolved)
	if !errors.Is(err, errdefs.ErrBlueprintStructuralSlots) {
		t.Fatalf("err = %v, want ErrBlueprintStructuralSlots for required secret slot", err)
	}
	if !strings.Contains(err.Error(), "token") {
		t.Fatalf("error should name the offending slot: %v", err)
	}
}

func TestMaterialize_OptionalSecretSlotDropped(t *testing.T) {
	doc := baseDoc()
	doc.Spec.Cell.Containers[0].Secrets = []v1beta1.BlueprintSecretSlot{
		{Name: "token", Mode: v1beta1.BlueprintSecretModeEnv, EnvName: "TOKEN"}, // optional
	}
	resolved, _ := cellblueprint.Resolve(doc, nil, nil)
	cell, err := cellblueprint.Materialize(resolved)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	// Secret slots never land on the runtime ContainerSpec (no secret field on
	// it yet — Config-side fill lands in #624); the optional slot simply drops.
	if len(cell.Spec.Containers) != 1 {
		t.Fatalf("container count changed: %d", len(cell.Spec.Containers))
	}
}

func TestMaterialize_PrefixFallsBackToName(t *testing.T) {
	doc := baseDoc()
	doc.Spec.Prefix = ""
	doc.Metadata.Name = "fallback"
	resolved, _ := cellblueprint.Resolve(doc, nil, nil)
	cell, err := cellblueprint.Materialize(resolved)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if !strings.HasPrefix(cell.Metadata.Name, "fallback-") {
		t.Fatalf("name = %q, want prefix from metadata.name", cell.Metadata.Name)
	}
}
