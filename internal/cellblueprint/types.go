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

package cellblueprint

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// componentNameRE bounds component and mode names to lowercase identifiers so
// they are safe as labels, map keys and CLI values.
var componentNameRE = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)

// ParameterType returns a parameter's type with the empty default resolved to
// string — the type every parameter had before types existed.
func ParameterType(p v1beta1.CellBlueprintParameter) string {
	if t := strings.TrimSpace(p.Type); t != "" {
		return t
	}
	return v1beta1.ParameterTypeString
}

// ValidateParameters checks parameter declarations (issue #1472, step 8): a
// valid, unique name; a known type; an enum with values and no values on any
// other type; and a default that fits the type.
func ValidateParameters(field string, params []v1beta1.CellBlueprintParameter) error {
	seen := map[string]struct{}{}
	for i, p := range params {
		at := fmt.Sprintf("%s[%d]", field, i)
		if !paramNameRE.MatchString(p.Name) {
			return fmt.Errorf("%w: %s.name %q must match [A-Za-z_][A-Za-z0-9_]*",
				errdefs.ErrBlueprintParameterType, at, p.Name)
		}
		if _, dup := seen[p.Name]; dup {
			return fmt.Errorf("%w: %s.name %q is declared twice", errdefs.ErrBlueprintParameterType, at, p.Name)
		}
		seen[p.Name] = struct{}{}
		switch ParameterType(p) {
		case v1beta1.ParameterTypeEnum:
			if len(p.Enum) == 0 {
				return fmt.Errorf("%w: %s (%s): an enum parameter lists its values in enum",
					errdefs.ErrBlueprintParameterType, at, p.Name)
			}
		case v1beta1.ParameterTypeString, v1beta1.ParameterTypePath,
			v1beta1.ParameterTypeImage, v1beta1.ParameterTypeBool:
			if len(p.Enum) > 0 {
				return fmt.Errorf("%w: %s (%s): enum values need type: enum",
					errdefs.ErrBlueprintParameterType, at, p.Name)
			}
		default:
			return fmt.Errorf("%w: %s (%s): unknown type %q (want string, enum, path, image or bool)",
				errdefs.ErrBlueprintParameterType, at, p.Name, p.Type)
		}
		// A default may itself reference another parameter; only a literal
		// default can be checked before resolution.
		if p.Default != nil && !strings.Contains(*p.Default, "${") {
			if err := ValidateValue(p, *p.Default); err != nil {
				return fmt.Errorf("%s.default: %w", at, err)
			}
		}
	}
	return nil
}

// ValidateValue checks a resolved value against its parameter's type. An empty
// value is always accepted here: whether a parameter may be left unset is the
// Required rule's business, not the type's.
func ValidateValue(p v1beta1.CellBlueprintParameter, value string) error {
	if value == "" {
		return nil
	}
	switch ParameterType(p) {
	case v1beta1.ParameterTypeEnum:
		if !slices.Contains(p.Enum, value) {
			return fmt.Errorf("%w: parameter %q = %q, want one of: %s",
				errdefs.ErrBlueprintParameterType, p.Name, value, strings.Join(p.Enum, ", "))
		}
	case v1beta1.ParameterTypeBool:
		if value != "true" && value != "false" {
			return fmt.Errorf("%w: parameter %q = %q, want true or false",
				errdefs.ErrBlueprintParameterType, p.Name, value)
		}
	case v1beta1.ParameterTypePath:
		if strings.ContainsAny(value, "\x00\n\r") {
			return fmt.Errorf("%w: parameter %q: a path must be a single line",
				errdefs.ErrBlueprintParameterType, p.Name)
		}
	case v1beta1.ParameterTypeImage:
		if strings.ContainsAny(value, " \t\n\r\x00") {
			return fmt.Errorf("%w: parameter %q = %q: an image reference has no whitespace",
				errdefs.ErrBlueprintParameterType, p.Name, value)
		}
	}
	return nil
}
