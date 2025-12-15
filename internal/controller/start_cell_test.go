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

func TestStartCell_SuccessfulStart(t *testing.T) {
	tests := []struct {
		name        string
		cellName    string
		realmName   string
		spaceName   string
		stackName   string
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.StartCellResult)
		wantErr     bool
	}{
		{
			name:      "successful start",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				existingCell.Status.State = intmodel.CellStatePending
				existingCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "container1", Image: "alpine:latest"},
					{ID: "container2", Image: "alpine:latest"},
				}
				// Mock GetCell (called by validateAndGetCell via controller.GetCell)
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
				}
				f.StartCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
				f.UpdateCellMetadataFn = func(cell intmodel.Cell) error {
					if cell.Status.State != intmodel.CellStateReady {
						return errors.New("expected cell state to be Ready after start")
					}
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.StartCellResult) {
				if !result.Started {
					t.Error("expected Started to be true")
				}
				if result.Cell.Metadata.Name != "test-cell" {
					t.Errorf("expected cell name to be 'test-cell', got %q", result.Cell.Metadata.Name)
				}
				if result.Cell.Status.State != intmodel.CellStateReady {
					t.Errorf("expected cell state to be Ready, got %v", result.Cell.Status.State)
				}
			},
			wantErr: false,
		},
		{
			name:      "successful start with no containers",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				existingCell.Status.State = intmodel.CellStatePending
				existingCell.Spec.Containers = []intmodel.ContainerSpec{}
				// Mock GetCell (called by validateAndGetCell via controller.GetCell)
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
				}
				f.StartCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
				f.UpdateCellMetadataFn = func(cell intmodel.Cell) error {
					if cell.Status.State != intmodel.CellStateReady {
						return errors.New("expected cell state to be Ready after start")
					}
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.StartCellResult) {
				if !result.Started {
					t.Error("expected Started to be true")
				}
				if result.Cell.Status.State != intmodel.CellStateReady {
					t.Errorf("expected cell state to be Ready, got %v", result.Cell.Status.State)
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

			result, err := ctrl.StartCell(cell)

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

func TestStartCell_ReadyStateValidation(t *testing.T) {
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
			name:      "cell in Ready state with empty Status.Containers returns error (fallback to metadata)",
			cellName:  "ready-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("ready-cell", "test-realm", "test-space", "test-stack")
				existingCell.Status.State = intmodel.CellStateReady
				existingCell.Status.Containers = []intmodel.ContainerStatus{} // Empty - fallback to metadata check
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
				}
			},
			wantErr:     true,
			errContains: "cell \"ready-cell\" is already in Ready state and must first be stopped",
		},
		{
			name:      "cell in Ready state with running containers returns error",
			cellName:  "ready-cell-running",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("ready-cell-running", "test-realm", "test-space", "test-stack")
				existingCell.Status.State = intmodel.CellStateReady
				existingCell.Status.Containers = []intmodel.ContainerStatus{
					{ID: "container1", State: intmodel.ContainerStateReady}, // Actually running
					{ID: "container2", State: intmodel.ContainerStateStopped},
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
			},
			wantErr:     true,
			errContains: "cell \"ready-cell-running\" has running containers and must first be stopped",
		},
		{
			name:      "cell in Ready state but all containers stopped allows start",
			cellName:  "ready-cell-stopped",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("ready-cell-stopped", "test-realm", "test-space", "test-stack")
				existingCell.Status.State = intmodel.CellStateReady // Stale metadata
				existingCell.Status.Containers = []intmodel.ContainerStatus{
					{ID: "container1", State: intmodel.ContainerStateStopped}, // Actually stopped
					{ID: "container2", State: intmodel.ContainerStateStopped},
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
				f.StartCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
				f.UpdateCellMetadataFn = func(_ intmodel.Cell) error {
					return nil
				}
			},
			wantErr: false,
		},
		{
			name:      "cell in Ready state with ContainerStateUnknown allows start",
			cellName:  "ready-cell-unknown",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("ready-cell-unknown", "test-realm", "test-space", "test-stack")
				existingCell.Status.State = intmodel.CellStateReady
				existingCell.Status.Containers = []intmodel.ContainerStatus{
					{ID: "container1", State: intmodel.ContainerStateUnknown}, // Unknown treated as not running
					{ID: "container2", State: intmodel.ContainerStateStopped},
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
				f.StartCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
				f.UpdateCellMetadataFn = func(_ intmodel.Cell) error {
					return nil
				}
			},
			wantErr: false,
		},
		{
			name:      "cell in Stopped state can be started",
			cellName:  "stopped-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("stopped-cell", "test-realm", "test-space", "test-stack")
				existingCell.Status.State = intmodel.CellStateStopped
				existingCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "container1", Image: "alpine:latest"},
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
				f.StartCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
				f.UpdateCellMetadataFn = func(_ intmodel.Cell) error {
					return nil
				}
			},
			wantErr: false,
		},
		{
			name:      "cell in Pending state can be started",
			cellName:  "pending-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("pending-cell", "test-realm", "test-space", "test-stack")
				existingCell.Status.State = intmodel.CellStatePending
				existingCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "container1", Image: "alpine:latest"},
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
				f.StartCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
				f.UpdateCellMetadataFn = func(_ intmodel.Cell) error {
					return nil
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

			_, err := ctrl.StartCell(cell)

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

func TestStartCell_ValidationErrors(t *testing.T) {
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

			_, err := ctrl.StartCell(cell)

			if err == nil {
				t.Fatal("expected error but got none")
			}

			if !errors.Is(err, tt.wantErr) {
				t.Errorf("expected error %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestStartCell_CellNotFound(t *testing.T) {
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
				// GetCell returns ErrCellNotFound (called by validateAndGetCell)
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return intmodel.Cell{}, errdefs.ErrCellNotFound
				}
			},
			wantErr:     true,
			errContains: "cell \"test-cell\" not found in realm \"test-realm\", space \"test-space\", stack \"test-stack\"",
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

			result, err := ctrl.StartCell(cell)

			if !tt.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}

			if err == nil {
				t.Fatal("expected error but got none")
			}

			if result.Started {
				t.Error("expected Started to be false when error occurs")
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

func TestStartCell_RunnerErrors(t *testing.T) {
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
			name:      "GetCell error (non-NotFound) is returned",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				// GetCell error (called by validateAndGetCell via controller.GetCell)
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return intmodel.Cell{}, errors.New("unexpected error")
				}
			},
			wantErr:     true,
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
			wantErr:     true,
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
			wantErr:     true,
			errContains: "failed to check root container",
		},
		{
			name:      "StartCell runner error is wrapped",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				existingCell.Status.State = intmodel.CellStateStopped
				// Mock GetCell (called by validateAndGetCell via controller.GetCell)
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
				}
				f.StartCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return intmodel.Cell{}, errors.New("start failed")
				}
			},
			wantErr:     true,
			errContains: "failed to start cell containers",
		},
		{
			name:      "UpdateCellMetadata runner error is wrapped",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				existingCell.Status.State = intmodel.CellStateStopped
				// Mock GetCell (called by validateAndGetCell via controller.GetCell)
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
				}
				f.StartCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
				f.UpdateCellMetadataFn = func(_ intmodel.Cell) error {
					return errors.New("metadata update failed")
				}
			},
			wantErr:     true,
			errContains: "failed to update cell metadata",
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

			_, err := ctrl.StartCell(cell)

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

func TestStartCell_NameTrimming(t *testing.T) {
	tests := []struct {
		name        string
		cellName    string
		realmName   string
		spaceName   string
		stackName   string
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.StartCellResult)
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
				existingCell.Status.State = intmodel.CellStateStopped
				// Mock GetCell (called by validateAndGetCell via controller.GetCell)
				f.GetCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					// Verify trimmed names are used
					if cell.Metadata.Name != "test-cell" {
						return intmodel.Cell{}, errors.New("unexpected cell name")
					}
					return existingCell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
				}
				f.StartCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
				f.UpdateCellMetadataFn = func(_ intmodel.Cell) error {
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.StartCellResult) {
				if result.Cell.Metadata.Name != "test-cell" {
					t.Errorf("expected cell name to be trimmed to 'test-cell', got %q", result.Cell.Metadata.Name)
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

			result, err := ctrl.StartCell(cell)

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
