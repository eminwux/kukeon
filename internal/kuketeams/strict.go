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

package kuketeams

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	"gopkg.in/yaml.v3"
)

func fieldError(field, message string) error {
	return fmt.Errorf("%w: %s: %s", errdefs.ErrTeamConfigurationInvalid, field, message)
}

// decodeConfiguration checks the YAML node tree before typed decoding so unknown
// fields, duplicate keys, nulls and type mismatches carry their full field path.
// JSON is a YAML subset and goes through exactly the same structural contract.
func decodeConfiguration(raw []byte, out any) error {
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	var node yaml.Node
	if err := decoder.Decode(&node); err != nil {
		return fieldError("document", "malformed or empty input")
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fieldError("document", "expected exactly one document")
	}
	if len(node.Content) != 1 {
		return fieldError("document", "expected a mapping")
	}
	if err := checkShape(node.Content[0], reflect.TypeOf(out).Elem(), ""); err != nil {
		return err
	}
	if err := node.Decode(out); err != nil {
		return fieldError("document", "cannot decode configuration")
	}
	return nil
}

func yamlFields(t reflect.Type) map[string]reflect.Type {
	fields := make(map[string]reflect.Type)
	for i := range t.NumField() {
		f := t.Field(i)
		tag := strings.Split(f.Tag.Get("yaml"), ",")
		if len(tag) > 1 && tag[1] == "inline" {
			for key, value := range yamlFields(f.Type) {
				fields[key] = value
			}
		} else {
			fields[tag[0]] = f.Type
		}
	}
	return fields
}

func requiredYAMLFields(t reflect.Type) []string {
	var required []string
	for i := range t.NumField() {
		f := t.Field(i)
		tag := strings.Split(f.Tag.Get("yaml"), ",")
		if len(tag) > 1 && tag[1] == "inline" {
			required = append(required, requiredYAMLFields(f.Type)...)
		} else if len(tag) == 1 {
			required = append(required, tag[0])
		}
	}
	return required
}

func checkShape(n *yaml.Node, t reflect.Type, field string) error {
	if t.Kind() == reflect.Pointer {
		return checkShape(n, t.Elem(), field)
	}
	//nolint:exhaustive // Configuration schemas intentionally support only mappings, lists, strings and booleans.
	switch t.Kind() {
	case reflect.Struct, reflect.Map:
		return checkMapping(n, t, field)
	case reflect.Slice:
		if n.Kind != yaml.SequenceNode {
			return fieldError(field, "expected a list (use [] to explicitly clear)")
		}
		for i, item := range n.Content {
			if err := checkShape(item, t.Elem(), fmt.Sprintf("%s[%d]", field, i)); err != nil {
				return err
			}
		}
	case reflect.String:
		if n.Kind != yaml.ScalarNode || n.Tag != "!!str" {
			return fieldError(field, "expected a string")
		}
	case reflect.Bool:
		if n.Kind != yaml.ScalarNode || n.Tag != "!!bool" {
			return fieldError(field, "expected a boolean")
		}
	default:
		return fieldError(field, "unsupported configuration field type")
	}
	return nil
}

func checkMapping(n *yaml.Node, t reflect.Type, field string) error {
	if n.Kind != yaml.MappingNode {
		return fieldError(field, "expected a mapping")
	}
	var fields map[string]reflect.Type
	if t.Kind() == reflect.Struct {
		fields = yamlFields(t)
	}
	seen := make(map[string]bool)
	for i := 0; i < len(n.Content); i += 2 {
		key := n.Content[i]
		if key.Tag != "!!str" {
			return fieldError(field, "expected string keys; YAML merges are unsupported")
		}
		child := key.Value
		if field != "" {
			child = field + "." + child
		}
		if seen[key.Value] {
			return fieldError(child, "duplicate field")
		}
		seen[key.Value] = true
		var target reflect.Type
		if t.Kind() == reflect.Map {
			target = t.Elem()
		} else {
			target = fields[key.Value]
		}
		if target == nil {
			if key.Value == "sources" && field == "spec" {
				return fieldError(
					child,
					"aggregate project checkouts are unsupported; declare one Team per project and use spec.repo",
				)
			}
			return fieldError(child, "unknown field")
		}
		if err := checkShape(n.Content[i+1], target, child); err != nil {
			return err
		}
	}
	if t.Kind() == reflect.Struct {
		for _, key := range requiredYAMLFields(t) {
			if !seen[key] {
				child := key
				if field != "" {
					child = field + "." + key
				}
				return fieldError(child, "required")
			}
		}
	}

	return nil
}
