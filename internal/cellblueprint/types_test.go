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
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/cellblueprint"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func TestParameterType_EmptyIsString(t *testing.T) {
	if got := cellblueprint.ParameterType(v1beta1.CellBlueprintParameter{Name: "A"}); got != v1beta1.ParameterTypeString {
		t.Errorf("empty type = %q, want %q", got, v1beta1.ParameterTypeString)
	}
	p := v1beta1.CellBlueprintParameter{Name: "A", Type: " bool "}
	if got := cellblueprint.ParameterType(p); got != v1beta1.ParameterTypeBool {
		t.Errorf("padded type = %q, want %q", got, v1beta1.ParameterTypeBool)
	}
}

func TestValidateParameters_AcceptsEveryType(t *testing.T) {
	params := []v1beta1.CellBlueprintParameter{
		{Name: "UNTYPED"},
		{Name: "S", Type: v1beta1.ParameterTypeString, Default: strptr("anything goes")},
		{Name: "E", Type: v1beta1.ParameterTypeEnum, Enum: []string{"opus", "sonnet"}, Default: strptr("opus")},
		{Name: "P", Type: v1beta1.ParameterTypePath, Default: strptr("/work/space")},
		{Name: "I", Type: v1beta1.ParameterTypeImage, Default: strptr("registry.example.com/img:v1")},
		{Name: "B", Type: v1beta1.ParameterTypeBool, Default: strptr("true")},
		// A default referencing another parameter is checked only after
		// resolution, so a non-literal default never fails the declaration.
		{Name: "E2", Type: v1beta1.ParameterTypeEnum, Enum: []string{"a"}, Default: strptr("${E}")},
	}
	if err := cellblueprint.ValidateParameters("spec.parameters", params); err != nil {
		t.Fatalf("ValidateParameters: %v", err)
	}
}

func TestValidateParameters_Rejections(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params []v1beta1.CellBlueprintParameter
		want   string
	}{
		{
			name:   "invalid name",
			params: []v1beta1.CellBlueprintParameter{{Name: "1BAD"}},
			want:   "must match",
		},
		{
			name:   "duplicate name",
			params: []v1beta1.CellBlueprintParameter{{Name: "A"}, {Name: "A"}},
			want:   "declared twice",
		},
		{
			name:   "enum without values",
			params: []v1beta1.CellBlueprintParameter{{Name: "E", Type: v1beta1.ParameterTypeEnum}},
			want:   "lists its values in enum",
		},
		{
			name:   "enum values on a string",
			params: []v1beta1.CellBlueprintParameter{{Name: "S", Enum: []string{"a"}}},
			want:   "enum values need type: enum",
		},
		{
			name:   "unknown type",
			params: []v1beta1.CellBlueprintParameter{{Name: "X", Type: "integer"}},
			want:   `unknown type "integer"`,
		},
		{
			name: "default outside enum",
			params: []v1beta1.CellBlueprintParameter{
				{Name: "E", Type: v1beta1.ParameterTypeEnum, Enum: []string{"a", "b"}, Default: strptr("c")},
			},
			want: "want one of: a, b",
		},
		{
			name:   "non-bool default",
			params: []v1beta1.CellBlueprintParameter{{Name: "B", Type: v1beta1.ParameterTypeBool, Default: strptr("yes")}},
			want:   "want true or false",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := cellblueprint.ValidateParameters("spec.parameters", tc.params)
			if !errors.Is(err, errdefs.ErrBlueprintParameterType) {
				t.Fatalf("err = %v, want ErrBlueprintParameterType", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want substring %q", err, tc.want)
			}
			if !strings.Contains(err.Error(), "spec.parameters[") {
				t.Errorf("err = %v, want the offending field path", err)
			}
		})
	}
}

func TestValidateValue(t *testing.T) {
	enum := v1beta1.CellBlueprintParameter{Name: "E", Type: v1beta1.ParameterTypeEnum, Enum: []string{"a", "b"}}
	boolean := v1beta1.CellBlueprintParameter{Name: "B", Type: v1beta1.ParameterTypeBool}
	path := v1beta1.CellBlueprintParameter{Name: "P", Type: v1beta1.ParameterTypePath}
	image := v1beta1.CellBlueprintParameter{Name: "I", Type: v1beta1.ParameterTypeImage}
	str := v1beta1.CellBlueprintParameter{Name: "S"}

	for _, tc := range []struct {
		name  string
		param v1beta1.CellBlueprintParameter
		value string
		ok    bool
	}{
		{"enum member", enum, "b", true},
		{"enum non-member", enum, "c", false},
		{"enum empty is the Required rule's business", enum, "", true},
		{"bool true", boolean, "true", true},
		{"bool false", boolean, "false", true},
		{"bool other", boolean, "1", false},
		{"path single line", path, "/a/b c", true},
		{"path multi line", path, "/a\n/b", false},
		{"image ref", image, "ghcr.io/o/i@sha256:abc", true},
		{"image with whitespace", image, "ghcr.io/o/i latest", false},
		{"string anything", str, "multi\nline value", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := cellblueprint.ValidateValue(tc.param, tc.value)
			if tc.ok && err != nil {
				t.Fatalf("ValidateValue(%q) = %v, want nil", tc.value, err)
			}
			if !tc.ok && !errors.Is(err, errdefs.ErrBlueprintParameterType) {
				t.Fatalf("ValidateValue(%q) = %v, want ErrBlueprintParameterType", tc.value, err)
			}
		})
	}
}

// TestResolve_TypedValueChecked asserts the resolver enforces the declared
// type on whatever level supplied the value (#1472 step 8): a --param outside
// the enum fails rather than being substituted into the cell template.
func TestResolve_TypedValueChecked(t *testing.T) {
	doc := baseDoc()
	doc.Spec.Parameters = []v1beta1.CellBlueprintParameter{
		{Name: "TAG", Type: v1beta1.ParameterTypeEnum, Enum: []string{"latest", "stable"}, Default: strptr("latest")},
	}
	out, err := cellblueprint.Resolve(doc, map[string]string{"TAG": "stable"}, nil)
	if err != nil {
		t.Fatalf("Resolve with an enum member: %v", err)
	}
	if got := out.Spec.Cell.Containers[0].Image; got != "registry.example.com/web:stable" {
		t.Errorf("image = %q, want the enum value substituted", got)
	}

	_, err = cellblueprint.Resolve(doc, map[string]string{"TAG": "nightly"}, nil)
	if !errors.Is(err, errdefs.ErrBlueprintParameterType) {
		t.Fatalf("err = %v, want ErrBlueprintParameterType for a value outside the enum", err)
	}

	// The env fallback is typed too.
	doc.Spec.Parameters[0].Default = nil
	_, err = cellblueprint.Resolve(doc, nil, func(string) (string, bool) { return "nightly", true })
	if !errors.Is(err, errdefs.ErrBlueprintParameterType) {
		t.Fatalf("err = %v, want ErrBlueprintParameterType for an env value outside the enum", err)
	}
}
