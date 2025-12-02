//go:build !integration

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

package controller_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/controller"
	"gopkg.in/yaml.v3"
)

func TestResourceResult_MarshalJSON_WithError(t *testing.T) {
	result := controller.ResourceResult{
		Index:  0,
		Kind:   "Realm",
		Name:   "test-realm",
		Action: "failed",
		Error:  errors.New("conversion failed: invalid namespace"),
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("failed to marshal JSON: %v", err)
	}

	var unmarshaled map[string]interface{}
	if err = json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}

	// Verify error field is a string
	errorVal, ok := unmarshaled["error"]
	if !ok {
		t.Fatal("expected 'error' field to be present")
	}

	errorStr, ok := errorVal.(string)
	if !ok {
		t.Fatalf("expected error to be a string, got %T: %v", errorVal, errorVal)
	}

	if errorStr != "conversion failed: invalid namespace" {
		t.Errorf("expected error message 'conversion failed: invalid namespace', got %q", errorStr)
	}
}

func TestResourceResult_MarshalJSON_WithoutError(t *testing.T) {
	result := controller.ResourceResult{
		Index:  0,
		Kind:   "Realm",
		Name:   "test-realm",
		Action: "created",
		Error:  nil,
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("failed to marshal JSON: %v", err)
	}

	var unmarshaled map[string]interface{}
	if err = json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}

	// Verify error field is omitted (due to omitempty)
	if _, ok := unmarshaled["error"]; ok {
		t.Error("expected 'error' field to be omitted when nil")
	}
}

func TestResourceResult_MarshalJSON_WithChangesAndDetails(t *testing.T) {
	result := controller.ResourceResult{
		Index:   1,
		Kind:    "Space",
		Name:    "test-space",
		Action:  "updated",
		Error:   nil,
		Changes: []string{"spec.realmID changed", "spec.network changed"},
		Details: map[string]string{
			"network": "test-network",
		},
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("failed to marshal JSON: %v", err)
	}

	var unmarshaled map[string]interface{}
	if err = json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}

	if unmarshaled["index"].(float64) != 1 {
		t.Errorf("expected index 1, got %v", unmarshaled["index"])
	}

	if unmarshaled["kind"].(string) != "Space" {
		t.Errorf("expected kind 'Space', got %q", unmarshaled["kind"])
	}

	changes, ok := unmarshaled["changes"].([]interface{})
	if !ok {
		t.Fatalf("expected changes to be an array, got %T", unmarshaled["changes"])
	}

	if len(changes) != 2 {
		t.Errorf("expected 2 changes, got %d", len(changes))
	}
}

func TestResourceResult_MarshalYAML_WithError(t *testing.T) {
	result := controller.ResourceResult{
		Index:  0,
		Kind:   "Realm",
		Name:   "test-realm",
		Action: "failed",
		Error:  errors.New("conversion failed: invalid namespace"),
	}

	data, err := yaml.Marshal(result)
	if err != nil {
		t.Fatalf("failed to marshal YAML: %v", err)
	}

	var unmarshaled map[string]interface{}
	if err = yaml.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal YAML: %v", err)
	}

	// Verify error field is a string
	errorVal, ok := unmarshaled["error"]
	if !ok {
		t.Fatal("expected 'error' field to be present")
	}

	errorStr, ok := errorVal.(string)
	if !ok {
		t.Fatalf("expected error to be a string, got %T: %v", errorVal, errorVal)
	}

	if errorStr != "conversion failed: invalid namespace" {
		t.Errorf("expected error message 'conversion failed: invalid namespace', got %q", errorStr)
	}

	// Verify YAML contains the error as a string (may be quoted)
	yamlStr := string(data)
	if !strings.Contains(yamlStr, "conversion failed: invalid namespace") {
		t.Errorf("expected YAML to contain error message, got:\n%s", yamlStr)
	}
	if !strings.Contains(yamlStr, "error:") {
		t.Errorf("expected YAML to contain error field, got:\n%s", yamlStr)
	}
}

func TestResourceResult_MarshalYAML_WithoutError(t *testing.T) {
	result := controller.ResourceResult{
		Index:  0,
		Kind:   "Realm",
		Name:   "test-realm",
		Action: "created",
		Error:  nil,
	}

	data, err := yaml.Marshal(result)
	if err != nil {
		t.Fatalf("failed to marshal YAML: %v", err)
	}

	var unmarshaled map[string]interface{}
	if err = yaml.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal YAML: %v", err)
	}

	// Verify error field is omitted (due to omitempty)
	if _, ok := unmarshaled["error"]; ok {
		t.Error("expected 'error' field to be omitted when nil")
	}

	// Verify YAML doesn't contain error field
	yamlStr := string(data)
	if strings.Contains(yamlStr, "error:") {
		t.Errorf("expected YAML to not contain error field, got:\n%s", yamlStr)
	}
}
