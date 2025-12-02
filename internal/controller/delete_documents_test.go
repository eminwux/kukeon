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

	"github.com/eminwux/kukeon/internal/apply/parser"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"gopkg.in/yaml.v3"
)

func TestDeleteDocuments_ReverseDependencyOrder(t *testing.T) {
	// Create documents in forward dependency order (should be sorted to reverse)
	docs := []parser.Document{
		{
			Index: 0,
			Kind:  v1beta1.KindRealm,
			RealmDoc: &v1beta1.RealmDoc{
				APIVersion: v1beta1.APIVersionV1Beta1,
				Kind:       v1beta1.KindRealm,
				Metadata: v1beta1.RealmMetadata{
					Name: "test-realm",
				},
				Spec: v1beta1.RealmSpec{
					Namespace: "test-ns",
				},
			},
		},
		{
			Index: 1,
			Kind:  v1beta1.KindContainer,
			ContainerDoc: &v1beta1.ContainerDoc{
				APIVersion: v1beta1.APIVersionV1Beta1,
				Kind:       v1beta1.KindContainer,
				Metadata: v1beta1.ContainerMetadata{
					Name: "test-container",
				},
				Spec: v1beta1.ContainerSpec{
					ID:      "test-container",
					RealmID: "test-realm",
					SpaceID: "test-space",
					StackID: "test-stack",
					CellID:  "test-cell",
					Image:   "test-image",
					Root:    false,
					Command: "echo",
					Args:    []string{"test"},
				},
			},
		},
	}

	fake := &fakeRunner{
		GetRealmFn: func(realm intmodel.Realm) (intmodel.Realm, error) {
			return realm, nil
		},
		DeleteRealmFn: func(_ intmodel.Realm) error {
			return nil
		},
		GetCellFn: func(_ intmodel.Cell) (intmodel.Cell, error) {
			return intmodel.Cell{
				Metadata: intmodel.CellMetadata{Name: "test-cell"},
				Spec: intmodel.CellSpec{
					RealmName: "test-realm",
					SpaceName: "test-space",
					StackName: "test-stack",
					Containers: []intmodel.ContainerSpec{
						{ID: "test-container"},
					},
				},
			}, nil
		},
		DeleteContainerFn: func(_ intmodel.Cell, _ string) error {
			return nil
		},
		UpdateCellMetadataFn: func(_ intmodel.Cell) error {
			return nil
		},
	}

	exec := setupTestController(t, fake)
	result, err := exec.DeleteDocuments(docs, false, false)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if len(result.Resources) != 2 {
		t.Fatalf("expected 2 resources, got %d", len(result.Resources))
	}

	// Container should be deleted first (index 1, but processed first)
	if result.Resources[0].Kind != "Container" {
		t.Errorf("expected first resource to be Container, got %s", result.Resources[0].Kind)
	}
	if result.Resources[0].Index != 1 {
		t.Errorf("expected first resource index to be 1, got %d", result.Resources[0].Index)
	}

	// Realm should be deleted second (index 0, but processed second)
	if result.Resources[1].Kind != "Realm" {
		t.Errorf("expected second resource to be Realm, got %s", result.Resources[1].Kind)
	}
	if result.Resources[1].Index != 0 {
		t.Errorf("expected second resource index to be 0, got %d", result.Resources[1].Index)
	}
}

func TestDeleteDocuments_NotFound_Idempotent(t *testing.T) {
	docs := []parser.Document{
		{
			Index: 0,
			Kind:  v1beta1.KindRealm,
			RealmDoc: &v1beta1.RealmDoc{
				APIVersion: v1beta1.APIVersionV1Beta1,
				Kind:       v1beta1.KindRealm,
				Metadata: v1beta1.RealmMetadata{
					Name: "nonexistent-realm",
				},
				Spec: v1beta1.RealmSpec{
					Namespace: "test-ns",
				},
			},
		},
	}

	fake := &fakeRunner{
		GetRealmFn: func(_ intmodel.Realm) (intmodel.Realm, error) {
			return intmodel.Realm{}, errdefs.ErrRealmNotFound
		},
	}

	exec := setupTestController(t, fake)
	result, err := exec.DeleteDocuments(docs, false, false)
	if err != nil {
		t.Fatalf("expected no error (idempotent), got: %v", err)
	}

	if len(result.Resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(result.Resources))
	}

	if result.Resources[0].Action != "not found" {
		t.Errorf("expected action to be 'not found', got %q", result.Resources[0].Action)
	}

	if result.Resources[0].Error != nil {
		t.Errorf("expected no error for not found, got: %v", result.Resources[0].Error)
	}
}

func TestDeleteDocuments_Cascade(t *testing.T) {
	docs := []parser.Document{
		{
			Index: 0,
			Kind:  v1beta1.KindRealm,
			RealmDoc: &v1beta1.RealmDoc{
				APIVersion: v1beta1.APIVersionV1Beta1,
				Kind:       v1beta1.KindRealm,
				Metadata: v1beta1.RealmMetadata{
					Name: "test-realm",
				},
				Spec: v1beta1.RealmSpec{
					Namespace: "test-ns",
				},
			},
		},
	}

	fake := &fakeRunner{
		GetRealmFn: func(realm intmodel.Realm) (intmodel.Realm, error) {
			return realm, nil
		},
		ListSpacesFn: func(_ string) ([]intmodel.Space, error) {
			return []intmodel.Space{
				{Metadata: intmodel.SpaceMetadata{Name: "test-space"}},
			}, nil
		},
		GetSpaceFn: func(space intmodel.Space) (intmodel.Space, error) {
			return space, nil
		},
		ListStacksFn: func(_, _ string) ([]intmodel.Stack, error) {
			return nil, nil
		},
		DeleteSpaceFn: func(_ intmodel.Space) error {
			return nil
		},
		DeleteRealmFn: func(_ intmodel.Realm) error {
			return nil
		},
	}

	exec := setupTestController(t, fake)
	result, err := exec.DeleteDocuments(docs, true, false) // cascade=true
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if len(result.Resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(result.Resources))
	}

	if result.Resources[0].Action != "deleted" {
		t.Errorf("expected action to be 'deleted', got %q", result.Resources[0].Action)
	}

	if len(result.Resources[0].Cascaded) != 1 {
		t.Errorf("expected 1 cascaded resource, got %d", len(result.Resources[0].Cascaded))
	}

	if result.Resources[0].Cascaded[0] != "test-space" {
		t.Errorf("expected cascaded resource to be 'test-space', got %q", result.Resources[0].Cascaded[0])
	}
}

func TestDeleteDocuments_Force(t *testing.T) {
	docs := []parser.Document{
		{
			Index: 0,
			Kind:  v1beta1.KindRealm,
			RealmDoc: &v1beta1.RealmDoc{
				APIVersion: v1beta1.APIVersionV1Beta1,
				Kind:       v1beta1.KindRealm,
				Metadata: v1beta1.RealmMetadata{
					Name: "test-realm",
				},
				Spec: v1beta1.RealmSpec{
					Namespace: "test-ns",
				},
			},
		},
	}

	fake := &fakeRunner{
		GetRealmFn: func(realm intmodel.Realm) (intmodel.Realm, error) {
			return realm, nil
		},
		ListSpacesFn: func(_ string) ([]intmodel.Space, error) {
			return []intmodel.Space{
				{Metadata: intmodel.SpaceMetadata{Name: "test-space"}},
			}, nil
		},
		DeleteRealmFn: func(_ intmodel.Realm) error {
			return nil
		},
	}

	exec := setupTestController(t, fake)
	result, err := exec.DeleteDocuments(docs, false, true) // force=true
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if len(result.Resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(result.Resources))
	}

	if result.Resources[0].Action != "deleted" {
		t.Errorf("expected action to be 'deleted', got %q", result.Resources[0].Action)
	}
}

func TestDeleteDocuments_ContinueOnFailure(t *testing.T) {
	docs := []parser.Document{
		{
			Index: 0,
			Kind:  v1beta1.KindRealm,
			RealmDoc: &v1beta1.RealmDoc{
				APIVersion: v1beta1.APIVersionV1Beta1,
				Kind:       v1beta1.KindRealm,
				Metadata: v1beta1.RealmMetadata{
					Name: "test-realm",
				},
				Spec: v1beta1.RealmSpec{
					Namespace: "test-ns",
				},
			},
		},
		{
			Index: 1,
			Kind:  v1beta1.KindSpace,
			SpaceDoc: &v1beta1.SpaceDoc{
				APIVersion: v1beta1.APIVersionV1Beta1,
				Kind:       v1beta1.KindSpace,
				Metadata: v1beta1.SpaceMetadata{
					Name: "test-space",
				},
				Spec: v1beta1.SpaceSpec{
					RealmID: "test-realm",
				},
			},
		},
	}

	fake := &fakeRunner{
		GetRealmFn: func(_ intmodel.Realm) (intmodel.Realm, error) {
			return intmodel.Realm{}, errors.New("delete failed")
		},
		GetSpaceFn: func(space intmodel.Space) (intmodel.Space, error) {
			return space, nil
		},
		ListStacksFn: func(_, _ string) ([]intmodel.Stack, error) {
			return nil, nil
		},
		DeleteSpaceFn: func(_ intmodel.Space) error {
			return nil
		},
	}

	exec := setupTestController(t, fake)
	result, err := exec.DeleteDocuments(docs, false, false)
	if err != nil {
		t.Fatalf("expected no error (continue on failure), got: %v", err)
	}

	if len(result.Resources) != 2 {
		t.Fatalf("expected 2 resources, got %d", len(result.Resources))
	}

	// Space should be deleted first (reverse dependency order)
	// First resource should be Space (succeeds)
	if result.Resources[0].Kind != "Space" {
		t.Errorf("expected first resource to be Space, got %q", result.Resources[0].Kind)
	}
	if result.Resources[0].Action != "deleted" {
		t.Errorf("expected first resource action to be 'deleted', got %q", result.Resources[0].Action)
	}

	// Second resource should be Realm (fails)
	if result.Resources[1].Kind != "Realm" {
		t.Errorf("expected second resource to be Realm, got %q", result.Resources[1].Kind)
	}
	if result.Resources[1].Action != "failed" {
		t.Errorf("expected second resource action to be 'failed', got %q", result.Resources[1].Action)
	}
}

func TestDeleteDocuments_Cell_ContainersDeleted(t *testing.T) {
	docs := []parser.Document{
		{
			Index: 0,
			Kind:  v1beta1.KindCell,
			CellDoc: &v1beta1.CellDoc{
				APIVersion: v1beta1.APIVersionV1Beta1,
				Kind:       v1beta1.KindCell,
				Metadata: v1beta1.CellMetadata{
					Name: "test-cell",
				},
				Spec: v1beta1.CellSpec{
					RealmID: "test-realm",
					SpaceID: "test-space",
					StackID: "test-stack",
					Containers: []v1beta1.ContainerSpec{
						{ID: "container1"},
						{ID: "container2"},
					},
				},
			},
		},
	}

	existingCell := intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: "test-cell"},
		Spec: intmodel.CellSpec{
			RealmName: "test-realm",
			SpaceName: "test-space",
			StackName: "test-stack",
			Containers: []intmodel.ContainerSpec{
				{ID: "container1"},
				{ID: "container2"},
			},
		},
	}

	fake := &fakeRunner{
		GetCellFn: func(_ intmodel.Cell) (intmodel.Cell, error) {
			return existingCell, nil
		},
		ExistsCgroupFn: func(_ any) (bool, error) {
			return true, nil
		},
		ExistsCellRootContainerFn: func(_ intmodel.Cell) (bool, error) {
			return true, nil
		},
		DeleteCellFn: func(_ intmodel.Cell) error {
			return nil
		},
	}

	exec := setupTestController(t, fake)
	result, err := exec.DeleteDocuments(docs, false, false)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if len(result.Resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(result.Resources))
	}

	if result.Resources[0].Action != "deleted" {
		t.Errorf("expected action to be 'deleted', got %q", result.Resources[0].Action)
	}

	if result.Resources[0].Details["containers"] != "2 deleted" {
		t.Errorf("expected 2 containers deleted, got %q", result.Resources[0].Details["containers"])
	}
}

func TestDeleteDocuments_Container_Deleted(t *testing.T) {
	docs := []parser.Document{
		{
			Index: 0,
			Kind:  v1beta1.KindContainer,
			ContainerDoc: &v1beta1.ContainerDoc{
				APIVersion: v1beta1.APIVersionV1Beta1,
				Kind:       v1beta1.KindContainer,
				Metadata: v1beta1.ContainerMetadata{
					Name: "test-container",
				},
				Spec: v1beta1.ContainerSpec{
					ID:      "test-container",
					RealmID: "test-realm",
					SpaceID: "test-space",
					StackID: "test-stack",
					CellID:  "test-cell",
					Image:   "test-image",
					Root:    false,
					Command: "echo",
					Args:    []string{"test"},
				},
			},
		},
	}

	fake := &fakeRunner{
		GetCellFn: func(_ intmodel.Cell) (intmodel.Cell, error) {
			return intmodel.Cell{
				Metadata: intmodel.CellMetadata{Name: "test-cell"},
				Spec: intmodel.CellSpec{
					RealmName: "test-realm",
					SpaceName: "test-space",
					StackName: "test-stack",
					Containers: []intmodel.ContainerSpec{
						{ID: "test-container"},
					},
				},
			}, nil
		},
		DeleteContainerFn: func(_ intmodel.Cell, _ string) error {
			return nil
		},
		UpdateCellMetadataFn: func(_ intmodel.Cell) error {
			return nil
		},
	}

	exec := setupTestController(t, fake)
	result, err := exec.DeleteDocuments(docs, false, false)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if len(result.Resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(result.Resources))
	}

	if result.Resources[0].Action != "deleted" {
		t.Errorf("expected action to be 'deleted', got %q", result.Resources[0].Action)
	}

	if result.Resources[0].Details["containers"] != "1 deleted" {
		t.Errorf("expected 1 container deleted, got %q", result.Resources[0].Details["containers"])
	}
}

func TestResourceDeleteResult_MarshalJSON_WithError(t *testing.T) {
	result := controller.ResourceDeleteResult{
		Index:  0,
		Kind:   "Realm",
		Name:   "test-realm",
		Action: "failed",
		Error:  errors.New("delete failed: resource in use"),
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

	if errorStr != "delete failed: resource in use" {
		t.Errorf("expected error message 'delete failed: resource in use', got %q", errorStr)
	}
}

func TestResourceDeleteResult_MarshalJSON_WithoutError(t *testing.T) {
	result := controller.ResourceDeleteResult{
		Index:  0,
		Kind:   "Realm",
		Name:   "test-realm",
		Action: "deleted",
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

func TestResourceDeleteResult_MarshalJSON_WithCascaded(t *testing.T) {
	result := controller.ResourceDeleteResult{
		Index:    1,
		Kind:     "Realm",
		Name:     "test-realm",
		Action:   "deleted",
		Error:    nil,
		Cascaded: []string{"test-space", "another-space"},
		Details: map[string]string{
			"spaces": "2 deleted",
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

	cascaded, ok := unmarshaled["cascaded"].([]interface{})
	if !ok {
		t.Fatalf("expected cascaded to be an array, got %T", unmarshaled["cascaded"])
	}

	if len(cascaded) != 2 {
		t.Errorf("expected 2 cascaded resources, got %d", len(cascaded))
	}
}

func TestResourceDeleteResult_MarshalYAML_WithError(t *testing.T) {
	result := controller.ResourceDeleteResult{
		Index:  0,
		Kind:   "Realm",
		Name:   "test-realm",
		Action: "failed",
		Error:  errors.New("delete failed: resource in use"),
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

	if errorStr != "delete failed: resource in use" {
		t.Errorf("expected error message 'delete failed: resource in use', got %q", errorStr)
	}

	// Verify YAML contains the error as a string (may be quoted)
	yamlStr := string(data)
	if !strings.Contains(yamlStr, "delete failed: resource in use") {
		t.Errorf("expected YAML to contain error message, got:\n%s", yamlStr)
	}
	if !strings.Contains(yamlStr, "error:") {
		t.Errorf("expected YAML to contain error field, got:\n%s", yamlStr)
	}
}

func TestResourceDeleteResult_MarshalYAML_WithoutError(t *testing.T) {
	result := controller.ResourceDeleteResult{
		Index:  0,
		Kind:   "Realm",
		Name:   "test-realm",
		Action: "deleted",
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
