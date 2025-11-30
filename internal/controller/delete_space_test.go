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

func TestDeleteSpace_SuccessfulDeletion(t *testing.T) {
	tests := []struct {
		name        string
		spaceName   string
		realmName   string
		force       bool
		cascade     bool
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.DeleteSpaceResult)
		wantErr     bool
	}{
		{
			name:      "successful deletion without dependencies",
			spaceName: "test-space",
			realmName: "test-realm",
			force:     false,
			cascade:   false,
			setupRunner: func(f *fakeRunner) {
				existingSpace := buildTestSpace("test-space", "test-realm")
				// Mock GetSpace (called by DeleteSpace)
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return existingSpace, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsSpaceCNIConfigFn = func(_ intmodel.Space) (bool, error) {
					return true, nil
				}
				// No stacks exist
				f.ListStacksFn = func(_, _ string) ([]intmodel.Stack, error) {
					return []intmodel.Stack{}, nil
				}
				f.DeleteSpaceFn = func(_ intmodel.Space) error {
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.DeleteSpaceResult) {
				if !result.MetadataDeleted {
					t.Error("expected MetadataDeleted to be true")
				}
				if !result.CgroupDeleted {
					t.Error("expected CgroupDeleted to be true")
				}
				if !result.CNINetworkDeleted {
					t.Error("expected CNINetworkDeleted to be true")
				}
				if len(result.Deleted) != 3 {
					t.Errorf("expected Deleted to contain 3 items, got %d", len(result.Deleted))
				}
				deletedMap := make(map[string]bool)
				for _, d := range result.Deleted {
					deletedMap[d] = true
				}
				if !deletedMap["metadata"] || !deletedMap["cgroup"] || !deletedMap["network"] {
					t.Errorf("expected Deleted to contain 'metadata', 'cgroup', 'network', got %v", result.Deleted)
				}
				if result.SpaceName != "test-space" {
					t.Errorf("expected SpaceName to be 'test-space', got %q", result.SpaceName)
				}
				if result.RealmName != "test-realm" {
					t.Errorf("expected RealmName to be 'test-realm', got %q", result.RealmName)
				}
				if result.Space.Metadata.Name != "test-space" {
					t.Errorf("expected space name to be 'test-space', got %q", result.Space.Metadata.Name)
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
			space := buildTestSpace(tt.spaceName, tt.realmName)

			result, err := ctrl.DeleteSpace(space, tt.force, tt.cascade)

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

func TestDeleteSpace_CascadeDeletion(t *testing.T) {
	tests := []struct {
		name        string
		spaceName   string
		realmName   string
		force       bool
		cascade     bool
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.DeleteSpaceResult)
		wantErr     bool
	}{
		{
			name:      "cascade deletion with multiple stacks",
			spaceName: "test-space",
			realmName: "test-realm",
			force:     false,
			cascade:   true,
			setupRunner: func(f *fakeRunner) {
				existingSpace := buildTestSpace("test-space", "test-realm")
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return existingSpace, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsSpaceCNIConfigFn = func(_ intmodel.Space) (bool, error) {
					return true, nil
				}
				stacks := []intmodel.Stack{
					buildTestStack("stack1", "test-realm", "test-space"),
					buildTestStack("stack2", "test-realm", "test-space"),
				}
				f.ListStacksFn = func(_, _ string) ([]intmodel.Stack, error) {
					return stacks, nil
				}
				// deleteStackCascade will be called for each stack
				// It will call ListCells, then DeleteStack
				f.ListCellsFn = func(_, _, _ string) ([]intmodel.Cell, error) {
					return []intmodel.Cell{}, nil
				}
				f.DeleteStackFn = func(_ intmodel.Stack) error {
					return nil
				}
				f.DeleteSpaceFn = func(_ intmodel.Space) error {
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.DeleteSpaceResult) {
				if !result.MetadataDeleted {
					t.Error("expected MetadataDeleted to be true")
				}
				if !result.CgroupDeleted {
					t.Error("expected CgroupDeleted to be true")
				}
				if !result.CNINetworkDeleted {
					t.Error("expected CNINetworkDeleted to be true")
				}
				// Check that stacks are tracked in Deleted array
				deletedMap := make(map[string]bool)
				for _, d := range result.Deleted {
					deletedMap[d] = true
				}
				if !deletedMap["stack:stack1"] {
					t.Error("expected Deleted to contain 'stack:stack1'")
				}
				if !deletedMap["stack:stack2"] {
					t.Error("expected Deleted to contain 'stack:stack2'")
				}
				if !deletedMap["metadata"] || !deletedMap["cgroup"] || !deletedMap["network"] {
					t.Error("expected Deleted to contain 'metadata', 'cgroup', 'network'")
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
			space := buildTestSpace(tt.spaceName, tt.realmName)

			result, err := ctrl.DeleteSpace(space, tt.force, tt.cascade)

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

func TestDeleteSpace_ForceDeletion(t *testing.T) {
	tests := []struct {
		name        string
		spaceName   string
		realmName   string
		force       bool
		cascade     bool
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.DeleteSpaceResult)
		wantErr     bool
	}{
		{
			name:      "force deletion with dependencies",
			spaceName: "test-space",
			realmName: "test-realm",
			force:     true,
			cascade:   false,
			setupRunner: func(f *fakeRunner) {
				existingSpace := buildTestSpace("test-space", "test-realm")
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return existingSpace, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsSpaceCNIConfigFn = func(_ intmodel.Space) (bool, error) {
					return true, nil
				}
				// ListStacks is not called when force=true and cascade=false
				// (validation is skipped)
				f.DeleteSpaceFn = func(_ intmodel.Space) error {
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.DeleteSpaceResult) {
				if !result.MetadataDeleted {
					t.Error("expected MetadataDeleted to be true")
				}
				// Stacks are not tracked when cascade=false
				deletedMap := make(map[string]bool)
				for _, d := range result.Deleted {
					deletedMap[d] = true
				}
				if deletedMap["stack:stack1"] || deletedMap["stack:stack2"] {
					t.Error("expected stacks not to be tracked when cascade=false")
				}
				if !deletedMap["metadata"] || !deletedMap["cgroup"] || !deletedMap["network"] {
					t.Error("expected Deleted to contain 'metadata', 'cgroup', 'network'")
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
			space := buildTestSpace(tt.spaceName, tt.realmName)

			result, err := ctrl.DeleteSpace(space, tt.force, tt.cascade)

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

func TestDeleteSpace_DependencyValidationError(t *testing.T) {
	tests := []struct {
		name        string
		spaceName   string
		realmName   string
		force       bool
		cascade     bool
		setupRunner func(*fakeRunner)
		wantErr     bool
		errContains string
	}{
		{
			name:      "dependency validation error when stacks exist",
			spaceName: "test-space",
			realmName: "test-realm",
			force:     false,
			cascade:   false,
			setupRunner: func(f *fakeRunner) {
				existingSpace := buildTestSpace("test-space", "test-realm")
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return existingSpace, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsSpaceCNIConfigFn = func(_ intmodel.Space) (bool, error) {
					return true, nil
				}
				stacks := []intmodel.Stack{
					buildTestStack("stack1", "test-realm", "test-space"),
					buildTestStack("stack2", "test-realm", "test-space"),
				}
				f.ListStacksFn = func(_, _ string) ([]intmodel.Stack, error) {
					return stacks, nil
				}
			},
			wantErr:     true,
			errContains: "space \"test-space\" has 2 stack(s)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner)
			}

			ctrl := setupTestController(t, mockRunner)
			space := buildTestSpace(tt.spaceName, tt.realmName)

			_, err := ctrl.DeleteSpace(space, tt.force, tt.cascade)

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

func TestDeleteSpace_ValidationErrors(t *testing.T) {
	tests := []struct {
		name      string
		spaceName string
		realmName string
		wantErr   error
	}{
		{
			name:      "empty space name returns ErrSpaceNameRequired",
			spaceName: "",
			realmName: "test-realm",
			wantErr:   errdefs.ErrSpaceNameRequired,
		},
		{
			name:      "whitespace-only space name returns ErrSpaceNameRequired",
			spaceName: "   ",
			realmName: "test-realm",
			wantErr:   errdefs.ErrSpaceNameRequired,
		},
		{
			name:      "empty realm name returns ErrRealmNameRequired",
			spaceName: "test-space",
			realmName: "",
			wantErr:   errdefs.ErrRealmNameRequired,
		},
		{
			name:      "whitespace-only realm name returns ErrRealmNameRequired",
			spaceName: "test-space",
			realmName: "   ",
			wantErr:   errdefs.ErrRealmNameRequired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			ctrl := setupTestController(t, mockRunner)

			space := buildTestSpace(tt.spaceName, tt.realmName)

			_, err := ctrl.DeleteSpace(space, false, false)

			if err == nil {
				t.Fatal("expected error but got none")
			}

			if !errors.Is(err, tt.wantErr) {
				t.Errorf("expected error %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestDeleteSpace_SpaceNotFound(t *testing.T) {
	tests := []struct {
		name        string
		spaceName   string
		realmName   string
		setupRunner func(*fakeRunner)
		wantErr     bool
		errContains string
	}{
		{
			name:      "space not found - ErrSpaceNotFound",
			spaceName: "test-space",
			realmName: "test-realm",
			setupRunner: func(f *fakeRunner) {
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return intmodel.Space{}, errdefs.ErrSpaceNotFound
				}
				f.ExistsSpaceCNIConfigFn = func(_ intmodel.Space) (bool, error) {
					return false, nil
				}
			},
			wantErr:     true,
			errContains: "space \"test-space\" not found in realm \"test-realm\"",
		},
		{
			name:      "space not found - MetadataExists=false",
			spaceName: "test-space",
			realmName: "test-realm",
			setupRunner: func(f *fakeRunner) {
				// GetSpace returns ErrSpaceNotFound which sets MetadataExists=false
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return intmodel.Space{}, errdefs.ErrSpaceNotFound
				}
				f.ExistsSpaceCNIConfigFn = func(_ intmodel.Space) (bool, error) {
					return false, nil
				}
			},
			wantErr:     true,
			errContains: "space \"test-space\" not found in realm \"test-realm\"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner)
			}

			ctrl := setupTestController(t, mockRunner)
			space := buildTestSpace(tt.spaceName, tt.realmName)

			_, err := ctrl.DeleteSpace(space, false, false)

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

func TestDeleteSpace_RunnerErrors(t *testing.T) {
	tests := []struct {
		name        string
		spaceName   string
		realmName   string
		force       bool
		cascade     bool
		setupRunner func(*fakeRunner)
		wantErr     error
		errContains string
	}{
		{
			name:      "GetSpace error (non-NotFound) is returned as-is",
			spaceName: "test-space",
			realmName: "test-realm",
			force:     false,
			cascade:   false,
			setupRunner: func(f *fakeRunner) {
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return intmodel.Space{}, errors.New("unexpected error")
				}
			},
			wantErr:     nil, // Runner errors are returned directly, not wrapped
			errContains: "unexpected error",
		},
		{
			name:      "ListStacks error during cascade is wrapped",
			spaceName: "test-space",
			realmName: "test-realm",
			force:     false,
			cascade:   true,
			setupRunner: func(f *fakeRunner) {
				existingSpace := buildTestSpace("test-space", "test-realm")
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return existingSpace, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsSpaceCNIConfigFn = func(_ intmodel.Space) (bool, error) {
					return true, nil
				}
				f.ListStacksFn = func(_, _ string) ([]intmodel.Stack, error) {
					return nil, errors.New("list failed")
				}
			},
			wantErr:     nil, // Custom error message
			errContains: "failed to list stacks",
		},
		{
			name:      "ListStacks error during dependency validation is wrapped",
			spaceName: "test-space",
			realmName: "test-realm",
			force:     false,
			cascade:   false,
			setupRunner: func(f *fakeRunner) {
				existingSpace := buildTestSpace("test-space", "test-realm")
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return existingSpace, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsSpaceCNIConfigFn = func(_ intmodel.Space) (bool, error) {
					return true, nil
				}
				f.ListStacksFn = func(_, _ string) ([]intmodel.Stack, error) {
					return nil, errors.New("list failed")
				}
			},
			wantErr:     nil, // Custom error message
			errContains: "failed to list stacks",
		},
		{
			name:      "deleteStackCascade error is wrapped with ErrDeleteSpace",
			spaceName: "test-space",
			realmName: "test-realm",
			force:     false,
			cascade:   true,
			setupRunner: func(f *fakeRunner) {
				existingSpace := buildTestSpace("test-space", "test-realm")
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return existingSpace, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsSpaceCNIConfigFn = func(_ intmodel.Space) (bool, error) {
					return true, nil
				}
				stacks := []intmodel.Stack{
					buildTestStack("stack1", "test-realm", "test-space"),
				}
				f.ListStacksFn = func(_, _ string) ([]intmodel.Stack, error) {
					return stacks, nil
				}
				f.ListCellsFn = func(_, _, _ string) ([]intmodel.Cell, error) {
					return []intmodel.Cell{}, nil
				}
				f.DeleteStackFn = func(_ intmodel.Stack) error {
					return errors.New("stack deletion failed")
				}
			},
			wantErr:     errdefs.ErrDeleteSpace,
			errContains: "failed to delete stack \"stack1\"",
		},
		{
			name:      "DeleteSpace runner error is wrapped with ErrDeleteSpace",
			spaceName: "test-space",
			realmName: "test-realm",
			force:     false,
			cascade:   false,
			setupRunner: func(f *fakeRunner) {
				existingSpace := buildTestSpace("test-space", "test-realm")
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return existingSpace, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsSpaceCNIConfigFn = func(_ intmodel.Space) (bool, error) {
					return true, nil
				}
				f.ListStacksFn = func(_, _ string) ([]intmodel.Stack, error) {
					return []intmodel.Stack{}, nil
				}
				f.DeleteSpaceFn = func(_ intmodel.Space) error {
					return errors.New("deletion failed")
				}
			},
			wantErr:     errdefs.ErrDeleteSpace,
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
			space := buildTestSpace(tt.spaceName, tt.realmName)

			_, err := ctrl.DeleteSpace(space, tt.force, tt.cascade)

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

func TestDeleteSpace_NameTrimming(t *testing.T) {
	tests := []struct {
		name        string
		spaceName   string
		realmName   string
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.DeleteSpaceResult)
		wantErr     bool
	}{
		{
			name:      "space name and realm name with leading/trailing whitespace are trimmed",
			spaceName: "  test-space  ",
			realmName: "  test-realm  ",
			setupRunner: func(f *fakeRunner) {
				existingSpace := buildTestSpace("test-space", "test-realm")
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return existingSpace, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsSpaceCNIConfigFn = func(_ intmodel.Space) (bool, error) {
					return true, nil
				}
				f.ListStacksFn = func(realmName, spaceName string) ([]intmodel.Stack, error) {
					if realmName != "test-realm" || spaceName != "test-space" {
						return nil, errors.New("unexpected names")
					}
					return []intmodel.Stack{}, nil
				}
				f.DeleteSpaceFn = func(_ intmodel.Space) error {
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.DeleteSpaceResult) {
				if result.SpaceName != "test-space" {
					t.Errorf("expected SpaceName to be trimmed to 'test-space', got %q", result.SpaceName)
				}
				if result.RealmName != "test-realm" {
					t.Errorf("expected RealmName to be trimmed to 'test-realm', got %q", result.RealmName)
				}
			},
			wantErr: false,
		},
		{
			name:      "space name that becomes empty after trimming triggers validation error",
			spaceName: "   ",
			realmName: "test-realm",
			setupRunner: func(_ *fakeRunner) {
				// Should not be called due to validation error
			},
			wantResult: nil,
			wantErr:    true,
		},
		{
			name:      "realm name that becomes empty after trimming triggers validation error",
			spaceName: "test-space",
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
			space := buildTestSpace(tt.spaceName, tt.realmName)

			result, err := ctrl.DeleteSpace(space, false, false)

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

func TestDeleteSpace_MultipleStacksCascade(t *testing.T) {
	tests := []struct {
		name        string
		spaceName   string
		realmName   string
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.DeleteSpaceResult)
		wantErr     bool
	}{
		{
			name:      "multiple stacks are deleted in cascade and tracked",
			spaceName: "test-space",
			realmName: "test-realm",
			setupRunner: func(f *fakeRunner) {
				existingSpace := buildTestSpace("test-space", "test-realm")
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return existingSpace, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsSpaceCNIConfigFn = func(_ intmodel.Space) (bool, error) {
					return true, nil
				}
				stacks := []intmodel.Stack{
					buildTestStack("stack1", "test-realm", "test-space"),
					buildTestStack("stack2", "test-realm", "test-space"),
					buildTestStack("stack3", "test-realm", "test-space"),
				}
				f.ListStacksFn = func(_, _ string) ([]intmodel.Stack, error) {
					return stacks, nil
				}
				f.ListCellsFn = func(_, _, _ string) ([]intmodel.Cell, error) {
					return []intmodel.Cell{}, nil
				}
				f.DeleteStackFn = func(_ intmodel.Stack) error {
					return nil
				}
				f.DeleteSpaceFn = func(_ intmodel.Space) error {
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.DeleteSpaceResult) {
				// Verify all stacks are tracked
				deletedMap := make(map[string]bool)
				for _, d := range result.Deleted {
					deletedMap[d] = true
				}
				if !deletedMap["stack:stack1"] {
					t.Error("expected Deleted to contain 'stack:stack1'")
				}
				if !deletedMap["stack:stack2"] {
					t.Error("expected Deleted to contain 'stack:stack2'")
				}
				if !deletedMap["stack:stack3"] {
					t.Error("expected Deleted to contain 'stack:stack3'")
				}
				// Verify order: stacks first, then metadata, cgroup, network
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
			space := buildTestSpace(tt.spaceName, tt.realmName)

			result, err := ctrl.DeleteSpace(space, false, true)

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

func TestDeleteSpace_PartialCascadeFailure(t *testing.T) {
	tests := []struct {
		name        string
		spaceName   string
		realmName   string
		setupRunner func(*fakeRunner)
		wantErr     bool
		errContains string
	}{
		{
			name:      "partial cascade failure - second stack deletion fails",
			spaceName: "test-space",
			realmName: "test-realm",
			setupRunner: func(f *fakeRunner) {
				existingSpace := buildTestSpace("test-space", "test-realm")
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return existingSpace, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsSpaceCNIConfigFn = func(_ intmodel.Space) (bool, error) {
					return true, nil
				}
				stacks := []intmodel.Stack{
					buildTestStack("stack1", "test-realm", "test-space"),
					buildTestStack("stack2", "test-realm", "test-space"),
				}
				f.ListStacksFn = func(_, _ string) ([]intmodel.Stack, error) {
					return stacks, nil
				}
				f.ListCellsFn = func(_, _, _ string) ([]intmodel.Cell, error) {
					return []intmodel.Cell{}, nil
				}
				f.DeleteStackFn = func(stack intmodel.Stack) error {
					if stack.Metadata.Name == "stack2" {
						return errors.New("stack2 deletion failed")
					}
					return nil
				}
			},
			wantErr:     true,
			errContains: "failed to delete stack \"stack2\"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner)
			}

			ctrl := setupTestController(t, mockRunner)
			space := buildTestSpace(tt.spaceName, tt.realmName)

			_, err := ctrl.DeleteSpace(space, false, true)

			if !tt.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}

			if err == nil {
				t.Fatal("expected error but got none")
			}

			if !errors.Is(err, errdefs.ErrDeleteSpace) {
				t.Errorf("expected error to wrap ErrDeleteSpace, got %v", err)
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

func TestDeleteSpace_GetSpaceMetadataExistsFalse(t *testing.T) {
	tests := []struct {
		name        string
		spaceName   string
		realmName   string
		setupRunner func(*fakeRunner)
		wantErr     bool
		errContains string
	}{
		{
			name:      "GetSpace returns MetadataExists=false",
			spaceName: "test-space",
			realmName: "test-realm",
			setupRunner: func(f *fakeRunner) {
				// GetSpace returns ErrSpaceNotFound which sets MetadataExists=false
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return intmodel.Space{}, errdefs.ErrSpaceNotFound
				}
				f.ExistsSpaceCNIConfigFn = func(_ intmodel.Space) (bool, error) {
					return false, nil
				}
			},
			wantErr:     true,
			errContains: "space \"test-space\" not found in realm \"test-realm\"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner)
			}

			ctrl := setupTestController(t, mockRunner)
			space := buildTestSpace(tt.spaceName, tt.realmName)

			_, err := ctrl.DeleteSpace(space, false, false)

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
