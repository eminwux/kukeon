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

func TestDeleteStack_SuccessfulDeletion(t *testing.T) {
	tests := []struct {
		name        string
		stackName   string
		realmName   string
		spaceName   string
		force       bool
		cascade     bool
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.DeleteStackResult)
		wantErr     bool
	}{
		{
			name:      "successful deletion without dependencies",
			stackName: "test-stack",
			realmName: "test-realm",
			spaceName: "test-space",
			force:     false,
			cascade:   false,
			setupRunner: func(f *fakeRunner) {
				existingStack := buildTestStack("test-stack", "test-realm", "test-space")
				// Mock GetStack (called by DeleteStack)
				f.GetStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return existingStack, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				// No cells exist
				f.ListCellsFn = func(_, _, _ string) ([]intmodel.Cell, error) {
					return []intmodel.Cell{}, nil
				}
				f.DeleteStackFn = func(_ intmodel.Stack) error {
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.DeleteStackResult) {
				if !result.MetadataDeleted {
					t.Error("expected MetadataDeleted to be true")
				}
				if !result.CgroupDeleted {
					t.Error("expected CgroupDeleted to be true")
				}
				if len(result.Deleted) != 2 {
					t.Errorf("expected Deleted to contain 2 items, got %d", len(result.Deleted))
				}
				deletedMap := make(map[string]bool)
				for _, d := range result.Deleted {
					deletedMap[d] = true
				}
				if !deletedMap["metadata"] || !deletedMap["cgroup"] {
					t.Errorf("expected Deleted to contain 'metadata', 'cgroup', got %v", result.Deleted)
				}
				if result.StackName != "test-stack" {
					t.Errorf("expected StackName to be 'test-stack', got %q", result.StackName)
				}
				if result.RealmName != "test-realm" {
					t.Errorf("expected RealmName to be 'test-realm', got %q", result.RealmName)
				}
				if result.SpaceName != "test-space" {
					t.Errorf("expected SpaceName to be 'test-space', got %q", result.SpaceName)
				}
				if result.Stack.Metadata.Name != "test-stack" {
					t.Errorf("expected stack name to be 'test-stack', got %q", result.Stack.Metadata.Name)
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
			stack := buildTestStack(tt.stackName, tt.realmName, tt.spaceName)

			result, err := ctrl.DeleteStack(stack, tt.force, tt.cascade)

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

func TestDeleteStack_CascadeDeletion(t *testing.T) {
	tests := []struct {
		name        string
		stackName   string
		realmName   string
		spaceName   string
		force       bool
		cascade     bool
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.DeleteStackResult)
		wantErr     bool
	}{
		{
			name:      "cascade deletion with multiple cells",
			stackName: "test-stack",
			realmName: "test-realm",
			spaceName: "test-space",
			force:     false,
			cascade:   true,
			setupRunner: func(f *fakeRunner) {
				existingStack := buildTestStack("test-stack", "test-realm", "test-space")
				f.GetStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return existingStack, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				cells := []intmodel.Cell{
					buildTestCell("cell1", "test-realm", "test-space", "test-stack"),
					buildTestCell("cell2", "test-realm", "test-space", "test-stack"),
				}
				f.ListCellsFn = func(_, _, _ string) ([]intmodel.Cell, error) {
					return cells, nil
				}
				// deleteCellInternal will be called for each cell
				// It calls runner.DeleteCell
				f.DeleteCellFn = func(_ intmodel.Cell) error {
					return nil
				}
				f.DeleteStackFn = func(_ intmodel.Stack) error {
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.DeleteStackResult) {
				if !result.MetadataDeleted {
					t.Error("expected MetadataDeleted to be true")
				}
				if !result.CgroupDeleted {
					t.Error("expected CgroupDeleted to be true")
				}
				// Check that cells are tracked in Deleted array
				deletedMap := make(map[string]bool)
				for _, d := range result.Deleted {
					deletedMap[d] = true
				}
				if !deletedMap["cell:cell1"] {
					t.Error("expected Deleted to contain 'cell:cell1'")
				}
				if !deletedMap["cell:cell2"] {
					t.Error("expected Deleted to contain 'cell:cell2'")
				}
				if !deletedMap["metadata"] || !deletedMap["cgroup"] {
					t.Error("expected Deleted to contain 'metadata', 'cgroup'")
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
			stack := buildTestStack(tt.stackName, tt.realmName, tt.spaceName)

			result, err := ctrl.DeleteStack(stack, tt.force, tt.cascade)

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

func TestDeleteStack_ForceDeletion(t *testing.T) {
	tests := []struct {
		name        string
		stackName   string
		realmName   string
		spaceName   string
		force       bool
		cascade     bool
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.DeleteStackResult)
		wantErr     bool
	}{
		{
			name:      "force deletion with dependencies",
			stackName: "test-stack",
			realmName: "test-realm",
			spaceName: "test-space",
			force:     true,
			cascade:   false,
			setupRunner: func(f *fakeRunner) {
				existingStack := buildTestStack("test-stack", "test-realm", "test-space")
				f.GetStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return existingStack, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				// ListCells is not called when force=true and cascade=false
				// (validation is skipped)
				f.DeleteStackFn = func(_ intmodel.Stack) error {
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.DeleteStackResult) {
				if !result.MetadataDeleted {
					t.Error("expected MetadataDeleted to be true")
				}
				// Cells are not tracked when cascade=false
				deletedMap := make(map[string]bool)
				for _, d := range result.Deleted {
					deletedMap[d] = true
				}
				if deletedMap["cell:cell1"] || deletedMap["cell:cell2"] {
					t.Error("expected cells not to be tracked when cascade=false")
				}
				if !deletedMap["metadata"] || !deletedMap["cgroup"] {
					t.Error("expected Deleted to contain 'metadata', 'cgroup'")
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
			stack := buildTestStack(tt.stackName, tt.realmName, tt.spaceName)

			result, err := ctrl.DeleteStack(stack, tt.force, tt.cascade)

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

func TestDeleteStack_DependencyValidationError(t *testing.T) {
	tests := []struct {
		name        string
		stackName   string
		realmName   string
		spaceName   string
		force       bool
		cascade     bool
		setupRunner func(*fakeRunner)
		wantErr     bool
		errContains string
	}{
		{
			name:      "dependency validation error when cells exist",
			stackName: "test-stack",
			realmName: "test-realm",
			spaceName: "test-space",
			force:     false,
			cascade:   false,
			setupRunner: func(f *fakeRunner) {
				existingStack := buildTestStack("test-stack", "test-realm", "test-space")
				f.GetStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return existingStack, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				cells := []intmodel.Cell{
					buildTestCell("cell1", "test-realm", "test-space", "test-stack"),
					buildTestCell("cell2", "test-realm", "test-space", "test-stack"),
				}
				f.ListCellsFn = func(_, _, _ string) ([]intmodel.Cell, error) {
					return cells, nil
				}
			},
			wantErr:     true,
			errContains: "stack \"test-stack\" has 2 cell(s)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner)
			}

			ctrl := setupTestController(t, mockRunner)
			stack := buildTestStack(tt.stackName, tt.realmName, tt.spaceName)

			_, err := ctrl.DeleteStack(stack, tt.force, tt.cascade)

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

func TestDeleteStack_ValidationErrors(t *testing.T) {
	tests := []struct {
		name      string
		stackName string
		realmName string
		spaceName string
		wantErr   error
	}{
		{
			name:      "empty stack name returns ErrStackNameRequired",
			stackName: "",
			realmName: "test-realm",
			spaceName: "test-space",
			wantErr:   errdefs.ErrStackNameRequired,
		},
		{
			name:      "whitespace-only stack name returns ErrStackNameRequired",
			stackName: "   ",
			realmName: "test-realm",
			spaceName: "test-space",
			wantErr:   errdefs.ErrStackNameRequired,
		},
		{
			name:      "empty realm name returns ErrRealmNameRequired",
			stackName: "test-stack",
			realmName: "",
			spaceName: "test-space",
			wantErr:   errdefs.ErrRealmNameRequired,
		},
		{
			name:      "whitespace-only realm name returns ErrRealmNameRequired",
			stackName: "test-stack",
			realmName: "   ",
			spaceName: "test-space",
			wantErr:   errdefs.ErrRealmNameRequired,
		},
		{
			name:      "empty space name returns ErrSpaceNameRequired",
			stackName: "test-stack",
			realmName: "test-realm",
			spaceName: "",
			wantErr:   errdefs.ErrSpaceNameRequired,
		},
		{
			name:      "whitespace-only space name returns ErrSpaceNameRequired",
			stackName: "test-stack",
			realmName: "test-realm",
			spaceName: "   ",
			wantErr:   errdefs.ErrSpaceNameRequired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			ctrl := setupTestController(t, mockRunner)

			stack := buildTestStack(tt.stackName, tt.realmName, tt.spaceName)

			_, err := ctrl.DeleteStack(stack, false, false)

			if err == nil {
				t.Fatal("expected error but got none")
			}

			if !errors.Is(err, tt.wantErr) {
				t.Errorf("expected error %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestDeleteStack_StackNotFound(t *testing.T) {
	tests := []struct {
		name        string
		stackName   string
		realmName   string
		spaceName   string
		setupRunner func(*fakeRunner)
		wantErr     bool
		errContains string
	}{
		{
			name:      "stack not found - ErrStackNotFound",
			stackName: "test-stack",
			realmName: "test-realm",
			spaceName: "test-space",
			setupRunner: func(f *fakeRunner) {
				f.GetStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return intmodel.Stack{}, errdefs.ErrStackNotFound
				}
			},
			wantErr:     true,
			errContains: "stack \"test-stack\" not found in realm \"test-realm\", space \"test-space\"",
		},
		{
			name:      "stack not found - MetadataExists=false",
			stackName: "test-stack",
			realmName: "test-realm",
			spaceName: "test-space",
			setupRunner: func(f *fakeRunner) {
				// GetStack returns ErrStackNotFound which sets MetadataExists=false
				f.GetStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return intmodel.Stack{}, errdefs.ErrStackNotFound
				}
			},
			wantErr:     true,
			errContains: "stack \"test-stack\" not found in realm \"test-realm\", space \"test-space\"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner)
			}

			ctrl := setupTestController(t, mockRunner)
			stack := buildTestStack(tt.stackName, tt.realmName, tt.spaceName)

			_, err := ctrl.DeleteStack(stack, false, false)

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

func TestDeleteStack_RunnerErrors(t *testing.T) {
	tests := []struct {
		name        string
		stackName   string
		realmName   string
		spaceName   string
		force       bool
		cascade     bool
		setupRunner func(*fakeRunner)
		wantErr     error
		errContains string
	}{
		{
			name:      "GetStack error (non-NotFound) is returned as-is",
			stackName: "test-stack",
			realmName: "test-realm",
			spaceName: "test-space",
			force:     false,
			cascade:   false,
			setupRunner: func(f *fakeRunner) {
				f.GetStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return intmodel.Stack{}, errors.New("unexpected error")
				}
			},
			wantErr:     nil, // Runner errors are returned directly, not wrapped
			errContains: "unexpected error",
		},
		{
			name:      "ListCells error during cascade is wrapped",
			stackName: "test-stack",
			realmName: "test-realm",
			spaceName: "test-space",
			force:     false,
			cascade:   true,
			setupRunner: func(f *fakeRunner) {
				existingStack := buildTestStack("test-stack", "test-realm", "test-space")
				f.GetStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return existingStack, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ListCellsFn = func(_, _, _ string) ([]intmodel.Cell, error) {
					return nil, errors.New("list failed")
				}
			},
			wantErr:     nil, // Custom error message
			errContains: "failed to list cells",
		},
		{
			name:      "ListCells error during dependency validation is wrapped",
			stackName: "test-stack",
			realmName: "test-realm",
			spaceName: "test-space",
			force:     false,
			cascade:   false,
			setupRunner: func(f *fakeRunner) {
				existingStack := buildTestStack("test-stack", "test-realm", "test-space")
				f.GetStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return existingStack, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ListCellsFn = func(_, _, _ string) ([]intmodel.Cell, error) {
					return nil, errors.New("list failed")
				}
			},
			wantErr:     nil, // Custom error message
			errContains: "failed to list cells",
		},
		{
			name:      "deleteCellInternal error is wrapped with ErrDeleteStack",
			stackName: "test-stack",
			realmName: "test-realm",
			spaceName: "test-space",
			force:     false,
			cascade:   true,
			setupRunner: func(f *fakeRunner) {
				existingStack := buildTestStack("test-stack", "test-realm", "test-space")
				f.GetStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return existingStack, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				cells := []intmodel.Cell{
					buildTestCell("cell1", "test-realm", "test-space", "test-stack"),
				}
				f.ListCellsFn = func(_, _, _ string) ([]intmodel.Cell, error) {
					return cells, nil
				}
				f.DeleteCellFn = func(_ intmodel.Cell) error {
					return errors.New("cell deletion failed")
				}
			},
			wantErr:     errdefs.ErrDeleteStack,
			errContains: "failed to delete cell \"cell1\"",
		},
		{
			name:      "DeleteStack runner error is wrapped with ErrDeleteStack",
			stackName: "test-stack",
			realmName: "test-realm",
			spaceName: "test-space",
			force:     false,
			cascade:   false,
			setupRunner: func(f *fakeRunner) {
				existingStack := buildTestStack("test-stack", "test-realm", "test-space")
				f.GetStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return existingStack, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ListCellsFn = func(_, _, _ string) ([]intmodel.Cell, error) {
					return []intmodel.Cell{}, nil
				}
				f.DeleteStackFn = func(_ intmodel.Stack) error {
					return errors.New("deletion failed")
				}
			},
			wantErr:     errdefs.ErrDeleteStack,
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
			stack := buildTestStack(tt.stackName, tt.realmName, tt.spaceName)

			_, err := ctrl.DeleteStack(stack, tt.force, tt.cascade)

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

func TestDeleteStack_NameTrimming(t *testing.T) {
	tests := []struct {
		name        string
		stackName   string
		realmName   string
		spaceName   string
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.DeleteStackResult)
		wantErr     bool
	}{
		{
			name:      "stack name, realm name, and space name with leading/trailing whitespace are trimmed",
			stackName: "  test-stack  ",
			realmName: "  test-realm  ",
			spaceName: "  test-space  ",
			setupRunner: func(f *fakeRunner) {
				existingStack := buildTestStack("test-stack", "test-realm", "test-space")
				f.GetStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return existingStack, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ListCellsFn = func(realmName, spaceName, stackName string) ([]intmodel.Cell, error) {
					if realmName != "test-realm" || spaceName != "test-space" || stackName != "test-stack" {
						return nil, errors.New("unexpected names")
					}
					return []intmodel.Cell{}, nil
				}
				f.DeleteStackFn = func(_ intmodel.Stack) error {
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.DeleteStackResult) {
				if result.StackName != "test-stack" {
					t.Errorf("expected StackName to be trimmed to 'test-stack', got %q", result.StackName)
				}
				if result.RealmName != "test-realm" {
					t.Errorf("expected RealmName to be trimmed to 'test-realm', got %q", result.RealmName)
				}
				if result.SpaceName != "test-space" {
					t.Errorf("expected SpaceName to be trimmed to 'test-space', got %q", result.SpaceName)
				}
			},
			wantErr: false,
		},
		{
			name:      "stack name that becomes empty after trimming triggers validation error",
			stackName: "   ",
			realmName: "test-realm",
			spaceName: "test-space",
			setupRunner: func(_ *fakeRunner) {
				// Should not be called due to validation error
			},
			wantResult: nil,
			wantErr:    true,
		},
		{
			name:      "realm name that becomes empty after trimming triggers validation error",
			stackName: "test-stack",
			realmName: "   ",
			spaceName: "test-space",
			setupRunner: func(_ *fakeRunner) {
				// Should not be called due to validation error
			},
			wantResult: nil,
			wantErr:    true,
		},
		{
			name:      "space name that becomes empty after trimming triggers validation error",
			stackName: "test-stack",
			realmName: "test-realm",
			spaceName: "   ",
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
			stack := buildTestStack(tt.stackName, tt.realmName, tt.spaceName)

			result, err := ctrl.DeleteStack(stack, false, false)

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

func TestDeleteStack_MultipleCellsCascade(t *testing.T) {
	tests := []struct {
		name        string
		stackName   string
		realmName   string
		spaceName   string
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.DeleteStackResult)
		wantErr     bool
	}{
		{
			name:      "multiple cells are deleted in cascade and tracked",
			stackName: "test-stack",
			realmName: "test-realm",
			spaceName: "test-space",
			setupRunner: func(f *fakeRunner) {
				existingStack := buildTestStack("test-stack", "test-realm", "test-space")
				f.GetStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return existingStack, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				cells := []intmodel.Cell{
					buildTestCell("cell1", "test-realm", "test-space", "test-stack"),
					buildTestCell("cell2", "test-realm", "test-space", "test-stack"),
					buildTestCell("cell3", "test-realm", "test-space", "test-stack"),
				}
				f.ListCellsFn = func(_, _, _ string) ([]intmodel.Cell, error) {
					return cells, nil
				}
				f.DeleteCellFn = func(_ intmodel.Cell) error {
					return nil
				}
				f.DeleteStackFn = func(_ intmodel.Stack) error {
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.DeleteStackResult) {
				// Verify all cells are tracked
				deletedMap := make(map[string]bool)
				for _, d := range result.Deleted {
					deletedMap[d] = true
				}
				if !deletedMap["cell:cell1"] {
					t.Error("expected Deleted to contain 'cell:cell1'")
				}
				if !deletedMap["cell:cell2"] {
					t.Error("expected Deleted to contain 'cell:cell2'")
				}
				if !deletedMap["cell:cell3"] {
					t.Error("expected Deleted to contain 'cell:cell3'")
				}
				// Verify order: cells first, then metadata, cgroup
				if len(result.Deleted) != 5 {
					t.Errorf("expected 5 items in Deleted, got %d", len(result.Deleted))
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
			stack := buildTestStack(tt.stackName, tt.realmName, tt.spaceName)

			result, err := ctrl.DeleteStack(stack, false, true)

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

func TestDeleteStack_PartialCascadeFailure(t *testing.T) {
	tests := []struct {
		name        string
		stackName   string
		realmName   string
		spaceName   string
		setupRunner func(*fakeRunner)
		wantErr     bool
		errContains string
	}{
		{
			name:      "partial cascade failure - second cell deletion fails",
			stackName: "test-stack",
			realmName: "test-realm",
			spaceName: "test-space",
			setupRunner: func(f *fakeRunner) {
				existingStack := buildTestStack("test-stack", "test-realm", "test-space")
				f.GetStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return existingStack, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				cells := []intmodel.Cell{
					buildTestCell("cell1", "test-realm", "test-space", "test-stack"),
					buildTestCell("cell2", "test-realm", "test-space", "test-stack"),
				}
				f.ListCellsFn = func(_, _, _ string) ([]intmodel.Cell, error) {
					return cells, nil
				}
				f.DeleteCellFn = func(cell intmodel.Cell) error {
					if cell.Metadata.Name == "cell2" {
						return errors.New("cell2 deletion failed")
					}
					return nil
				}
			},
			wantErr:     true,
			errContains: "failed to delete cell \"cell2\"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner)
			}

			ctrl := setupTestController(t, mockRunner)
			stack := buildTestStack(tt.stackName, tt.realmName, tt.spaceName)

			_, err := ctrl.DeleteStack(stack, false, true)

			if !tt.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}

			if err == nil {
				t.Fatal("expected error but got none")
			}

			if !errors.Is(err, errdefs.ErrDeleteStack) {
				t.Errorf("expected error to wrap ErrDeleteStack, got %v", err)
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

func TestDeleteStack_GetStackMetadataExistsFalse(t *testing.T) {
	tests := []struct {
		name        string
		stackName   string
		realmName   string
		spaceName   string
		setupRunner func(*fakeRunner)
		wantErr     bool
		errContains string
	}{
		{
			name:      "GetStack returns MetadataExists=false",
			stackName: "test-stack",
			realmName: "test-realm",
			spaceName: "test-space",
			setupRunner: func(f *fakeRunner) {
				// GetStack returns ErrStackNotFound which sets MetadataExists=false
				f.GetStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return intmodel.Stack{}, errdefs.ErrStackNotFound
				}
			},
			wantErr:     true,
			errContains: "stack \"test-stack\" not found in realm \"test-realm\", space \"test-space\"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner)
			}

			ctrl := setupTestController(t, mockRunner)
			stack := buildTestStack(tt.stackName, tt.realmName, tt.spaceName)

			_, err := ctrl.DeleteStack(stack, false, false)

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
