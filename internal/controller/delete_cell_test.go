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

func TestDeleteCell_SuccessfulDeletionNoContainers(t *testing.T) {
	tests := []struct {
		name        string
		cellName    string
		realmName   string
		spaceName   string
		stackName   string
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.DeleteCellResult)
		wantErr     bool
	}{
		{
			name:      "successful deletion without containers",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				// Mock GetCell (called by validateAndGetCell)
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
				}
				f.DeleteCellFn = func(_ intmodel.Cell) error {
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.DeleteCellResult) {
				if result.ContainersDeleted {
					t.Error("expected ContainersDeleted to be false")
				}
				if !result.CgroupDeleted {
					t.Error("expected CgroupDeleted to be true")
				}
				if !result.MetadataDeleted {
					t.Error("expected MetadataDeleted to be true")
				}
				if result.Cell.Metadata.Name != "test-cell" {
					t.Errorf("expected cell name to be 'test-cell', got %q", result.Cell.Metadata.Name)
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
			cell := buildTestCell(tt.cellName, tt.realmName, tt.spaceName, tt.stackName)

			result, err := ctrl.DeleteCell(cell)

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

func TestDeleteCell_SuccessfulDeletionWithContainers(t *testing.T) {
	tests := []struct {
		name        string
		cellName    string
		realmName   string
		spaceName   string
		stackName   string
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.DeleteCellResult)
		wantErr     bool
	}{
		{
			name:      "successful deletion with containers",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				existingCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "container1", Image: "alpine:latest"},
					{ID: "container2", Image: "alpine:latest"},
				}
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
				}
				f.DeleteCellFn = func(_ intmodel.Cell) error {
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.DeleteCellResult) {
				if !result.ContainersDeleted {
					t.Error("expected ContainersDeleted to be true")
				}
				if !result.CgroupDeleted {
					t.Error("expected CgroupDeleted to be true")
				}
				if !result.MetadataDeleted {
					t.Error("expected MetadataDeleted to be true")
				}
				if len(result.Cell.Spec.Containers) != 2 {
					t.Errorf("expected cell to have 2 containers, got %d", len(result.Cell.Spec.Containers))
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
			cell := buildTestCell(tt.cellName, tt.realmName, tt.spaceName, tt.stackName)

			result, err := ctrl.DeleteCell(cell)

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

func TestDeleteCell_ValidationErrors(t *testing.T) {
	tests := []struct {
		name      string
		cellName  string
		realmName string
		spaceName string
		stackName string
		wantErr   error
	}{
		{
			name:      "empty cell name returns ErrCellNameRequired",
			cellName:  "",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			wantErr:   errdefs.ErrCellNameRequired,
		},
		{
			name:      "whitespace-only cell name returns ErrCellNameRequired",
			cellName:  "   ",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			wantErr:   errdefs.ErrCellNameRequired,
		},
		{
			name:      "empty realm name returns ErrRealmNameRequired",
			cellName:  "test-cell",
			realmName: "",
			spaceName: "test-space",
			stackName: "test-stack",
			wantErr:   errdefs.ErrRealmNameRequired,
		},
		{
			name:      "whitespace-only realm name returns ErrRealmNameRequired",
			cellName:  "test-cell",
			realmName: "   ",
			spaceName: "test-space",
			stackName: "test-stack",
			wantErr:   errdefs.ErrRealmNameRequired,
		},
		{
			name:      "empty space name returns ErrSpaceNameRequired",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "",
			stackName: "test-stack",
			wantErr:   errdefs.ErrSpaceNameRequired,
		},
		{
			name:      "whitespace-only space name returns ErrSpaceNameRequired",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "   ",
			stackName: "test-stack",
			wantErr:   errdefs.ErrSpaceNameRequired,
		},
		{
			name:      "empty stack name returns ErrStackNameRequired",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "",
			wantErr:   errdefs.ErrStackNameRequired,
		},
		{
			name:      "whitespace-only stack name returns ErrStackNameRequired",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "   ",
			wantErr:   errdefs.ErrStackNameRequired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			ctrl := setupTestController(t, mockRunner)

			cell := buildTestCell(tt.cellName, tt.realmName, tt.spaceName, tt.stackName)

			_, err := ctrl.DeleteCell(cell)

			if err == nil {
				t.Fatal("expected error but got none")
			}

			if !errors.Is(err, tt.wantErr) {
				t.Errorf("expected error %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestDeleteCell_CellNotFound(t *testing.T) {
	tests := []struct {
		name        string
		cellName    string
		realmName   string
		spaceName   string
		stackName   string
		setupRunner func(*fakeRunner)
		wantErr     bool
		errContains string
	}{
		{
			name:      "cell not found - ErrCellNotFound",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return intmodel.Cell{}, errdefs.ErrCellNotFound
				}
			},
			wantErr:     true,
			errContains: "cell \"test-cell\" not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner)
			}

			ctrl := setupTestController(t, mockRunner)
			cell := buildTestCell(tt.cellName, tt.realmName, tt.spaceName, tt.stackName)

			_, err := ctrl.DeleteCell(cell)

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

func TestDeleteCell_RunnerErrors(t *testing.T) {
	tests := []struct {
		name        string
		cellName    string
		realmName   string
		spaceName   string
		stackName   string
		setupRunner func(*fakeRunner)
		wantErr     error
		errContains string
	}{
		{
			name:      "GetCell error (non-NotFound) is wrapped",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return intmodel.Cell{}, errors.New("unexpected error")
				}
			},
			wantErr:     errdefs.ErrGetCell,
			errContains: "unexpected error",
		},
		{
			name:      "ExistsCgroup error from GetCell is wrapped",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return false, errors.New("cgroup check failed")
				}
			},
			wantErr:     nil, // Custom error message from GetCell
			errContains: "failed to check if cell cgroup exists",
		},
		{
			name:      "ExistsCellRootContainer error from GetCell is wrapped",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return false, errors.New("root container check failed")
				}
			},
			wantErr:     nil, // Custom error message from GetCell
			errContains: "failed to check root container",
		},
		{
			name:      "DeleteCell runner error is wrapped with ErrDeleteCell",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
				}
				f.DeleteCellFn = func(_ intmodel.Cell) error {
					return errors.New("deletion failed")
				}
			},
			wantErr:     errdefs.ErrDeleteCell,
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
			cell := buildTestCell(tt.cellName, tt.realmName, tt.spaceName, tt.stackName)

			_, err := ctrl.DeleteCell(cell)

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

func TestDeleteCell_NameTrimming(t *testing.T) {
	tests := []struct {
		name        string
		cellName    string
		realmName   string
		spaceName   string
		stackName   string
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.DeleteCellResult)
		wantErr     bool
	}{
		{
			name:      "cell name, realm name, space name, and stack name with leading/trailing whitespace are trimmed",
			cellName:  "  test-cell  ",
			realmName: "  test-realm  ",
			spaceName: "  test-space  ",
			stackName: "  test-stack  ",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
				}
				f.DeleteCellFn = func(cell intmodel.Cell) error {
					if cell.Metadata.Name != "test-cell" {
						return errors.New("unexpected cell name")
					}
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.DeleteCellResult) {
				if result.Cell.Metadata.Name != "test-cell" {
					t.Errorf("expected cell name to be trimmed to 'test-cell', got %q", result.Cell.Metadata.Name)
				}
			},
			wantErr: false,
		},
		{
			name:      "cell name that becomes empty after trimming triggers validation error",
			cellName:  "   ",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(_ *fakeRunner) {
				// Should not be called due to validation error
			},
			wantResult: nil,
			wantErr:    true,
		},
		{
			name:      "realm name that becomes empty after trimming triggers validation error",
			cellName:  "test-cell",
			realmName: "   ",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(_ *fakeRunner) {
				// Should not be called due to validation error
			},
			wantResult: nil,
			wantErr:    true,
		},
		{
			name:      "space name that becomes empty after trimming triggers validation error",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "   ",
			stackName: "test-stack",
			setupRunner: func(_ *fakeRunner) {
				// Should not be called due to validation error
			},
			wantResult: nil,
			wantErr:    true,
		},
		{
			name:      "stack name that becomes empty after trimming triggers validation error",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "   ",
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
			cell := buildTestCell(tt.cellName, tt.realmName, tt.spaceName, tt.stackName)

			result, err := ctrl.DeleteCell(cell)

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

func TestDeleteCell_MultipleContainers(t *testing.T) {
	tests := []struct {
		name        string
		cellName    string
		realmName   string
		spaceName   string
		stackName   string
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.DeleteCellResult)
		wantErr     bool
	}{
		{
			name:      "multiple containers are tracked",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				existingCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "container1", Image: "alpine:latest"},
					{ID: "container2", Image: "alpine:latest"},
					{ID: "container3", Image: "alpine:latest"},
				}
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
				}
				f.DeleteCellFn = func(_ intmodel.Cell) error {
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.DeleteCellResult) {
				if !result.ContainersDeleted {
					t.Error("expected ContainersDeleted to be true")
				}
				if len(result.Cell.Spec.Containers) != 3 {
					t.Errorf("expected cell to have 3 containers, got %d", len(result.Cell.Spec.Containers))
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
			cell := buildTestCell(tt.cellName, tt.realmName, tt.spaceName, tt.stackName)

			result, err := ctrl.DeleteCell(cell)

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

func TestDeleteCell_GetCellMetadataExistsFalse(t *testing.T) {
	tests := []struct {
		name        string
		cellName    string
		realmName   string
		spaceName   string
		stackName   string
		setupRunner func(*fakeRunner)
		wantErr     bool
		errContains string
	}{
		{
			name:      "GetCell returns MetadataExists=false",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				// GetCell returns ErrCellNotFound which sets MetadataExists=false
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return intmodel.Cell{}, errdefs.ErrCellNotFound
				}
			},
			wantErr:     true,
			errContains: "cell \"test-cell\" not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner)
			}

			ctrl := setupTestController(t, mockRunner)
			cell := buildTestCell(tt.cellName, tt.realmName, tt.spaceName, tt.stackName)

			_, err := ctrl.DeleteCell(cell)

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
