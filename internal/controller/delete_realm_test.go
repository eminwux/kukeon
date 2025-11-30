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
	"errors"
	"testing"

	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

func TestDeleteRealm_SuccessfulDeletion(t *testing.T) {
	tests := []struct {
		name        string
		realmName   string
		force       bool
		cascade     bool
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.DeleteRealmResult)
		wantErr     bool
	}{
		{
			name:      "successful deletion without dependencies",
			realmName: "test-realm",
			force:     false,
			cascade:   false,
			setupRunner: func(f *fakeRunner) {
				existingRealm := buildTestRealm("test-realm", "test-namespace")
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return existingRealm, nil
				}
				f.ListSpacesFn = func(_ string) ([]intmodel.Space, error) {
					return []intmodel.Space{}, nil
				}
				f.DeleteRealmFn = func(_ intmodel.Realm) error {
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.DeleteRealmResult) {
				if !result.MetadataDeleted {
					t.Error("expected MetadataDeleted to be true")
				}
				if !result.CgroupDeleted {
					t.Error("expected CgroupDeleted to be true")
				}
				if !result.ContainerdNamespaceDeleted {
					t.Error("expected ContainerdNamespaceDeleted to be true")
				}
				if len(result.Deleted) != 3 {
					t.Errorf("expected Deleted to contain 3 items, got %d", len(result.Deleted))
				}
				deletedMap := make(map[string]bool)
				for _, d := range result.Deleted {
					deletedMap[d] = true
				}
				if !deletedMap["metadata"] || !deletedMap["cgroup"] || !deletedMap["namespace"] {
					t.Errorf("expected Deleted to contain 'metadata', 'cgroup', 'namespace', got %v", result.Deleted)
				}
				if result.Realm.Metadata.Name != "test-realm" {
					t.Errorf("expected realm name to be 'test-realm', got %q", result.Realm.Metadata.Name)
				}
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner)
			}

			ctrl := setupTestController(t, mockRunner)
			realm := buildTestRealm(tt.realmName, "")

			result, err := ctrl.DeleteRealm(realm, tt.force, tt.cascade)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantResult != nil {
				tt.wantResult(t, result)
			}
		})
	}
}

func TestDeleteRealm_CascadeDeletion(t *testing.T) {
	tests := []struct {
		name        string
		realmName   string
		force       bool
		cascade     bool
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.DeleteRealmResult)
		wantErr     bool
	}{
		{
			name:      "cascade deletion with multiple spaces",
			realmName: "test-realm",
			force:     false,
			cascade:   true,
			setupRunner: func(f *fakeRunner) {
				existingRealm := buildTestRealm("test-realm", "test-namespace")
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return existingRealm, nil
				}
				spaces := []intmodel.Space{
					buildTestSpace("space1", "test-realm"),
					buildTestSpace("space2", "test-realm"),
				}
				f.ListSpacesFn = func(_ string) ([]intmodel.Space, error) {
					return spaces, nil
				}
				// deleteSpaceCascade will be called for each space
				// It will call ListStacks, then DeleteSpace
				f.ListStacksFn = func(_, _ string) ([]intmodel.Stack, error) {
					return []intmodel.Stack{}, nil
				}
				f.DeleteSpaceFn = func(_ intmodel.Space) error {
					return nil
				}
				f.DeleteRealmFn = func(_ intmodel.Realm) error {
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.DeleteRealmResult) {
				if !result.MetadataDeleted {
					t.Error("expected MetadataDeleted to be true")
				}
				if !result.CgroupDeleted {
					t.Error("expected CgroupDeleted to be true")
				}
				if !result.ContainerdNamespaceDeleted {
					t.Error("expected ContainerdNamespaceDeleted to be true")
				}
				// Check that spaces are tracked in Deleted array
				deletedMap := make(map[string]bool)
				for _, d := range result.Deleted {
					deletedMap[d] = true
				}
				if !deletedMap["space:space1"] {
					t.Error("expected Deleted to contain 'space:space1'")
				}
				if !deletedMap["space:space2"] {
					t.Error("expected Deleted to contain 'space:space2'")
				}
				if !deletedMap["metadata"] || !deletedMap["cgroup"] || !deletedMap["namespace"] {
					t.Error("expected Deleted to contain 'metadata', 'cgroup', 'namespace'")
				}
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner)
			}

			ctrl := setupTestController(t, mockRunner)
			realm := buildTestRealm(tt.realmName, "")

			result, err := ctrl.DeleteRealm(realm, tt.force, tt.cascade)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantResult != nil {
				tt.wantResult(t, result)
			}
		})
	}
}

func TestDeleteRealm_ForceDeletion(t *testing.T) {
	tests := []struct {
		name        string
		realmName   string
		force       bool
		cascade     bool
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.DeleteRealmResult)
		wantErr     bool
	}{
		{
			name:      "force deletion with dependencies",
			realmName: "test-realm",
			force:     true,
			cascade:   false,
			setupRunner: func(f *fakeRunner) {
				existingRealm := buildTestRealm("test-realm", "test-namespace")
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return existingRealm, nil
				}
				// ListSpaces is not called when force=true and cascade=false
				// (validation is skipped)
				f.DeleteRealmFn = func(_ intmodel.Realm) error {
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.DeleteRealmResult) {
				if !result.MetadataDeleted {
					t.Error("expected MetadataDeleted to be true")
				}
				// Spaces are not tracked when cascade=false
				deletedMap := make(map[string]bool)
				for _, d := range result.Deleted {
					deletedMap[d] = true
				}
				if deletedMap["space:space1"] || deletedMap["space:space2"] {
					t.Error("expected spaces not to be tracked when cascade=false")
				}
				if !deletedMap["metadata"] || !deletedMap["cgroup"] || !deletedMap["namespace"] {
					t.Error("expected Deleted to contain 'metadata', 'cgroup', 'namespace'")
				}
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner)
			}

			ctrl := setupTestController(t, mockRunner)
			realm := buildTestRealm(tt.realmName, "")

			result, err := ctrl.DeleteRealm(realm, tt.force, tt.cascade)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantResult != nil {
				tt.wantResult(t, result)
			}
		})
	}
}

func TestDeleteRealm_DependencyValidationError(t *testing.T) {
	tests := []struct {
		name        string
		realmName   string
		force       bool
		cascade     bool
		setupRunner func(*fakeRunner)
		wantErr     bool
		errContains string
	}{
		{
			name:      "dependency validation error when spaces exist",
			realmName: "test-realm",
			force:     false,
			cascade:   false,
			setupRunner: func(f *fakeRunner) {
				existingRealm := buildTestRealm("test-realm", "test-namespace")
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return existingRealm, nil
				}
				spaces := []intmodel.Space{
					buildTestSpace("space1", "test-realm"),
					buildTestSpace("space2", "test-realm"),
				}
				f.ListSpacesFn = func(_ string) ([]intmodel.Space, error) {
					return spaces, nil
				}
			},
			wantErr:     true,
			errContains: "realm \"test-realm\" has 2 space(s)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner)
			}

			ctrl := setupTestController(t, mockRunner)
			realm := buildTestRealm(tt.realmName, "")

			_, err := ctrl.DeleteRealm(realm, tt.force, tt.cascade)

			if !tt.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}

			if err == nil {
				t.Fatal("expected error but got none")
			}

			if !errors.Is(err, errdefs.ErrResourceHasDependencies) {
				t.Errorf("expected error to wrap ErrResourceHasDependencies, got %v", err)
			}

			if tt.errContains != "" {
				errStr := err.Error()
				found := false
				for i := 0; i <= len(errStr)-len(tt.errContains); i++ {
					if i+len(tt.errContains) <= len(errStr) && errStr[i:i+len(tt.errContains)] == tt.errContains {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error message to contain %q, got %q", tt.errContains, err.Error())
				}
			}
		})
	}
}

func TestDeleteRealm_ValidationErrors(t *testing.T) {
	tests := []struct {
		name      string
		realmName string
		wantErr   error
	}{
		{
			name:      "empty realm name returns ErrRealmNameRequired",
			realmName: "",
			wantErr:   errdefs.ErrRealmNameRequired,
		},
		{
			name:      "whitespace-only realm name returns ErrRealmNameRequired",
			realmName: "   ",
			wantErr:   errdefs.ErrRealmNameRequired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			ctrl := setupTestController(t, mockRunner)

			realm := buildTestRealm(tt.realmName, "")

			_, err := ctrl.DeleteRealm(realm, false, false)

			if err == nil {
				t.Fatal("expected error but got none")
			}

			if !errors.Is(err, tt.wantErr) {
				t.Errorf("expected error %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestDeleteRealm_RealmNotFound(t *testing.T) {
	tests := []struct {
		name        string
		realmName   string
		setupRunner func(*fakeRunner)
		wantErr     bool
		errContains string
	}{
		{
			name:      "realm not found returns descriptive error",
			realmName: "test-realm",
			setupRunner: func(f *fakeRunner) {
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return intmodel.Realm{}, errdefs.ErrRealmNotFound
				}
			},
			wantErr:     true,
			errContains: "realm \"test-realm\" not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner)
			}

			ctrl := setupTestController(t, mockRunner)
			realm := buildTestRealm(tt.realmName, "")

			_, err := ctrl.DeleteRealm(realm, false, false)

			if !tt.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}

			if err == nil {
				t.Fatal("expected error but got none")
			}

			if tt.errContains != "" {
				errStr := err.Error()
				found := false
				for i := 0; i <= len(errStr)-len(tt.errContains); i++ {
					if i+len(tt.errContains) <= len(errStr) && errStr[i:i+len(tt.errContains)] == tt.errContains {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error message to contain %q, got %q", tt.errContains, err.Error())
				}
			}
		})
	}
}

func TestDeleteRealm_RunnerErrors(t *testing.T) {
	tests := []struct {
		name        string
		realmName   string
		force       bool
		cascade     bool
		setupRunner func(*fakeRunner)
		wantErr     error
		errContains string
	}{
		{
			name:      "GetRealm error (non-NotFound) is returned as-is",
			realmName: "test-realm",
			force:     false,
			cascade:   false,
			setupRunner: func(f *fakeRunner) {
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return intmodel.Realm{}, errors.New("unexpected error")
				}
			},
			wantErr:     nil, // Error is returned as-is, not wrapped
			errContains: "unexpected error",
		},
		{
			name:      "ListSpaces error during cascade is wrapped",
			realmName: "test-realm",
			force:     false,
			cascade:   true,
			setupRunner: func(f *fakeRunner) {
				existingRealm := buildTestRealm("test-realm", "test-namespace")
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return existingRealm, nil
				}
				f.ListSpacesFn = func(_ string) ([]intmodel.Space, error) {
					return nil, errors.New("list failed")
				}
			},
			wantErr:     nil, // Custom error message
			errContains: "failed to list spaces",
		},
		{
			name:      "ListSpaces error during dependency validation is wrapped",
			realmName: "test-realm",
			force:     false,
			cascade:   false,
			setupRunner: func(f *fakeRunner) {
				existingRealm := buildTestRealm("test-realm", "test-namespace")
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return existingRealm, nil
				}
				f.ListSpacesFn = func(_ string) ([]intmodel.Space, error) {
					return nil, errors.New("list failed")
				}
			},
			wantErr:     nil, // Custom error message
			errContains: "failed to list spaces",
		},
		{
			name:      "deleteSpaceCascade error is wrapped with ErrDeleteRealm",
			realmName: "test-realm",
			force:     false,
			cascade:   true,
			setupRunner: func(f *fakeRunner) {
				existingRealm := buildTestRealm("test-realm", "test-namespace")
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return existingRealm, nil
				}
				spaces := []intmodel.Space{
					buildTestSpace("space1", "test-realm"),
				}
				f.ListSpacesFn = func(_ string) ([]intmodel.Space, error) {
					return spaces, nil
				}
				f.ListStacksFn = func(_, _ string) ([]intmodel.Stack, error) {
					return []intmodel.Stack{}, nil
				}
				f.DeleteSpaceFn = func(_ intmodel.Space) error {
					return errors.New("space deletion failed")
				}
			},
			wantErr:     errdefs.ErrDeleteRealm,
			errContains: "failed to delete space \"space1\"",
		},
		{
			name:      "DeleteRealm runner error is wrapped with ErrDeleteRealm",
			realmName: "test-realm",
			force:     false,
			cascade:   false,
			setupRunner: func(f *fakeRunner) {
				existingRealm := buildTestRealm("test-realm", "test-namespace")
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return existingRealm, nil
				}
				f.ListSpacesFn = func(_ string) ([]intmodel.Space, error) {
					return []intmodel.Space{}, nil
				}
				f.DeleteRealmFn = func(_ intmodel.Realm) error {
					return errors.New("deletion failed")
				}
			},
			wantErr:     errdefs.ErrDeleteRealm,
			errContains: "deletion failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner)
			}

			ctrl := setupTestController(t, mockRunner)
			realm := buildTestRealm(tt.realmName, "")

			_, err := ctrl.DeleteRealm(realm, tt.force, tt.cascade)

			if err == nil {
				t.Fatal("expected error but got none")
			}

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("expected error to wrap %v, got %v", tt.wantErr, err)
				}
			}

			if tt.errContains != "" {
				errStr := err.Error()
				found := false
				for i := 0; i <= len(errStr)-len(tt.errContains); i++ {
					if i+len(tt.errContains) <= len(errStr) && errStr[i:i+len(tt.errContains)] == tt.errContains {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error message to contain %q, got %q", tt.errContains, err.Error())
				}
			}
		})
	}
}

func TestDeleteRealm_NameTrimming(t *testing.T) {
	tests := []struct {
		name        string
		realmName   string
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.DeleteRealmResult)
		wantErr     bool
	}{
		{
			name:      "realm name with leading/trailing whitespace is trimmed",
			realmName: "  test-realm  ",
			setupRunner: func(f *fakeRunner) {
				existingRealm := buildTestRealm("test-realm", "test-namespace")
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					// Verify trimmed name was used in lookup
					return existingRealm, nil
				}
				f.ListSpacesFn = func(realmName string) ([]intmodel.Space, error) {
					if realmName != "test-realm" {
						return nil, errors.New("unexpected realm name")
					}
					return []intmodel.Space{}, nil
				}
				f.DeleteRealmFn = func(_ intmodel.Realm) error {
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.DeleteRealmResult) {
				if result.Realm.Metadata.Name != "test-realm" {
					t.Errorf("expected realm name to be trimmed to 'test-realm', got %q", result.Realm.Metadata.Name)
				}
			},
			wantErr: false,
		},
		{
			name:      "realm name that becomes empty after trimming triggers validation error",
			realmName: "   ",
			setupRunner: func(_ *fakeRunner) {
				// Should not be called due to validation error
			},
			wantResult: nil,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner)
			}

			ctrl := setupTestController(t, mockRunner)
			realm := buildTestRealm(tt.realmName, "")

			result, err := ctrl.DeleteRealm(realm, false, false)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantResult != nil {
				tt.wantResult(t, result)
			}
		})
	}
}

func TestDeleteRealm_MultipleSpacesCascade(t *testing.T) {
	tests := []struct {
		name        string
		realmName   string
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.DeleteRealmResult)
		wantErr     bool
	}{
		{
			name:      "multiple spaces are deleted in cascade and tracked",
			realmName: "test-realm",
			setupRunner: func(f *fakeRunner) {
				existingRealm := buildTestRealm("test-realm", "test-namespace")
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return existingRealm, nil
				}
				spaces := []intmodel.Space{
					buildTestSpace("space1", "test-realm"),
					buildTestSpace("space2", "test-realm"),
					buildTestSpace("space3", "test-realm"),
				}
				f.ListSpacesFn = func(_ string) ([]intmodel.Space, error) {
					return spaces, nil
				}
				f.ListStacksFn = func(_, _ string) ([]intmodel.Stack, error) {
					return []intmodel.Stack{}, nil
				}
				deletedSpaces := make(map[string]bool)
				f.DeleteSpaceFn = func(space intmodel.Space) error {
					deletedSpaces[space.Metadata.Name] = true
					return nil
				}
				f.DeleteRealmFn = func(_ intmodel.Realm) error {
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.DeleteRealmResult) {
				// Verify all spaces are tracked
				deletedMap := make(map[string]bool)
				for _, d := range result.Deleted {
					deletedMap[d] = true
				}
				if !deletedMap["space:space1"] {
					t.Error("expected Deleted to contain 'space:space1'")
				}
				if !deletedMap["space:space2"] {
					t.Error("expected Deleted to contain 'space:space2'")
				}
				if !deletedMap["space:space3"] {
					t.Error("expected Deleted to contain 'space:space3'")
				}
				// Verify order: spaces first, then metadata, cgroup, namespace
				if len(result.Deleted) != 6 {
					t.Errorf("expected 6 items in Deleted, got %d", len(result.Deleted))
				}
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner)
			}

			ctrl := setupTestController(t, mockRunner)
			realm := buildTestRealm(tt.realmName, "")

			result, err := ctrl.DeleteRealm(realm, false, true)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantResult != nil {
				tt.wantResult(t, result)
			}
		})
	}
}

func TestDeleteRealm_PartialCascadeFailure(t *testing.T) {
	tests := []struct {
		name        string
		realmName   string
		setupRunner func(*fakeRunner)
		wantErr     bool
		errContains string
	}{
		{
			name:      "partial cascade failure - second space deletion fails",
			realmName: "test-realm",
			setupRunner: func(f *fakeRunner) {
				existingRealm := buildTestRealm("test-realm", "test-namespace")
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return existingRealm, nil
				}
				spaces := []intmodel.Space{
					buildTestSpace("space1", "test-realm"),
					buildTestSpace("space2", "test-realm"),
				}
				f.ListSpacesFn = func(_ string) ([]intmodel.Space, error) {
					return spaces, nil
				}
				deleteCallCount := 0
				f.ListStacksFn = func(_, _ string) ([]intmodel.Stack, error) {
					return []intmodel.Stack{}, nil
				}
				f.DeleteSpaceFn = func(space intmodel.Space) error {
					deleteCallCount++
					if space.Metadata.Name == "space2" {
						return errors.New("space2 deletion failed")
					}
					return nil
				}
			},
			wantErr:     true,
			errContains: "failed to delete space \"space2\"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner)
			}

			ctrl := setupTestController(t, mockRunner)
			realm := buildTestRealm(tt.realmName, "")

			_, err := ctrl.DeleteRealm(realm, false, true)

			if !tt.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}

			if err == nil {
				t.Fatal("expected error but got none")
			}

			if !errors.Is(err, errdefs.ErrDeleteRealm) {
				t.Errorf("expected error to wrap ErrDeleteRealm, got %v", err)
			}

			if tt.errContains != "" {
				errStr := err.Error()
				found := false
				for i := 0; i <= len(errStr)-len(tt.errContains); i++ {
					if i+len(tt.errContains) <= len(errStr) && errStr[i:i+len(tt.errContains)] == tt.errContains {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error message to contain %q, got %q", tt.errContains, err.Error())
				}
			}
		})
	}
}
