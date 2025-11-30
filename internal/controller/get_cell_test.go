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
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// TestGetCell_SuccessfulRetrieval tests successful retrieval of cell with all state flags.
func TestGetCell_SuccessfulRetrieval(t *testing.T) {
	tests := []struct {
		name        string
		cellName    string
		realmName   string
		spaceName   string
		stackName   string
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.GetCellResult)
		wantErr     bool
	}{
		{
			name:      "cell exists with all resources",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				cell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return cell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
				}
			},
			wantResult: func(t *testing.T, result controller.GetCellResult) {
				if !result.MetadataExists {
					t.Error("expected MetadataExists to be true")
				}
				if !result.CgroupExists {
					t.Error("expected CgroupExists to be true")
				}
				if !result.RootContainerExists {
					t.Error("expected RootContainerExists to be true")
				}
				if result.Cell.Metadata.Name != "test-cell" {
					t.Errorf("expected cell name 'test-cell', got %q", result.Cell.Metadata.Name)
				}
			},
			wantErr: false,
		},
		{
			name:      "cell exists with no resources",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				cell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return cell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return false, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return false, nil
				}
			},
			wantResult: func(t *testing.T, result controller.GetCellResult) {
				if !result.MetadataExists {
					t.Error("expected MetadataExists to be true")
				}
				if result.CgroupExists {
					t.Error("expected CgroupExists to be false")
				}
				if result.RootContainerExists {
					t.Error("expected RootContainerExists to be false")
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
			cell := intmodel.Cell{
				Metadata: intmodel.CellMetadata{
					Name: tt.cellName,
				},
				Spec: intmodel.CellSpec{
					RealmName: tt.realmName,
					SpaceName: tt.spaceName,
					StackName: tt.stackName,
				},
			}

			result, err := ctrl.GetCell(cell)

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

// TestGetCell_ValidationErrors tests validation errors for cell name, realm name, space name, and stack name.
func TestGetCell_ValidationErrors(t *testing.T) {
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
			cell := intmodel.Cell{
				Metadata: intmodel.CellMetadata{
					Name: tt.cellName,
				},
				Spec: intmodel.CellSpec{
					RealmName: tt.realmName,
					SpaceName: tt.spaceName,
					StackName: tt.stackName,
				},
			}

			_, err := ctrl.GetCell(cell)

			if err == nil {
				t.Fatal("expected error but got none")
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("expected error %v, got %v", tt.wantErr, err)
			}
		})
	}
}

// TestGetCell_NotFound tests cell not found scenario.
func TestGetCell_NotFound(t *testing.T) {
	tests := []struct {
		name        string
		cellName    string
		realmName   string
		spaceName   string
		stackName   string
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.GetCellResult)
		wantErr     bool
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
			wantResult: func(t *testing.T, result controller.GetCellResult) {
				if result.MetadataExists {
					t.Error("expected MetadataExists to be false")
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
			cell := intmodel.Cell{
				Metadata: intmodel.CellMetadata{
					Name: tt.cellName,
				},
				Spec: intmodel.CellSpec{
					RealmName: tt.realmName,
					SpaceName: tt.spaceName,
					StackName: tt.stackName,
				},
			}

			result, err := ctrl.GetCell(cell)

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

// TestGetCell_RealmSpaceStackMismatch tests realm/space/stack mismatch errors.
func TestGetCell_RealmSpaceStackMismatch(t *testing.T) {
	tests := []struct {
		name        string
		cellName    string
		realmName   string
		spaceName   string
		stackName   string
		setupRunner func(*fakeRunner)
		errContains string
	}{
		{
			name:      "cell exists but with different realm",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				cell := buildTestCell("test-cell", "different-realm", "test-space", "test-stack")
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return cell, nil
				}
			},
			errContains: "cell \"test-cell\" not found in realm \"test-realm\" (found in realm \"different-realm\")",
		},
		{
			name:      "cell exists but with different space",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				cell := buildTestCell("test-cell", "test-realm", "different-space", "test-stack")
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return cell, nil
				}
			},
			errContains: "cell \"test-cell\" not found in space \"test-space\" (found in space \"different-space\")",
		},
		{
			name:      "cell exists but with different stack",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				cell := buildTestCell("test-cell", "test-realm", "test-space", "different-stack")
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return cell, nil
				}
			},
			errContains: "cell \"test-cell\" not found in stack \"test-stack\" (found in stack \"different-stack\")",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner)
			}

			ctrl := setupTestController(t, mockRunner)
			cell := intmodel.Cell{
				Metadata: intmodel.CellMetadata{
					Name: tt.cellName,
				},
				Spec: intmodel.CellSpec{
					RealmName: tt.realmName,
					SpaceName: tt.spaceName,
					StackName: tt.stackName,
				},
			}

			_, err := ctrl.GetCell(cell)

			if err == nil {
				t.Fatal("expected error but got none")
			}

			if !strings.Contains(err.Error(), tt.errContains) {
				t.Errorf("expected error to contain %q, got %q", tt.errContains, err.Error())
			}
		})
	}
}

// TestGetCell_RunnerErrors tests error propagation from runner methods.
func TestGetCell_RunnerErrors(t *testing.T) {
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
			name:      "GetCell error (non-NotFound) wrapped with ErrGetCell",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return intmodel.Cell{}, errors.New("runner error")
				}
			},
			wantErr: errdefs.ErrGetCell,
		},
		{
			name:      "ExistsCgroup error wrapped with descriptive message",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				cell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return cell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return false, errors.New("cgroup check failed")
				}
			},
			wantErr:     nil,
			errContains: "failed to check if cell cgroup exists",
		},
		{
			name:      "ExistsCellRootContainer error wrapped with descriptive message",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				cell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return cell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return false, errors.New("root container check failed")
				}
			},
			wantErr:     nil,
			errContains: "failed to check root container",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner)
			}

			ctrl := setupTestController(t, mockRunner)
			cell := intmodel.Cell{
				Metadata: intmodel.CellMetadata{
					Name: tt.cellName,
				},
				Spec: intmodel.CellSpec{
					RealmName: tt.realmName,
					SpaceName: tt.spaceName,
					StackName: tt.stackName,
				},
			}

			_, err := ctrl.GetCell(cell)

			if err == nil {
				t.Fatal("expected error but got none")
			}

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("expected error %v, got %v", tt.wantErr, err)
				}
			}

			if tt.errContains != "" {
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("expected error to contain %q, got %q", tt.errContains, err.Error())
				}
			}
		})
	}
}

// TestGetCell_NameTrimming tests name trimming.
func TestGetCell_NameTrimming(t *testing.T) {
	tests := []struct {
		name        string
		cellName    string
		realmName   string
		spaceName   string
		stackName   string
		setupRunner func(*fakeRunner, string, string, string, string)
		wantResult  func(t *testing.T, result controller.GetCellResult)
		wantErr     bool
	}{
		{
			name:      "all four names (cell, realm, space, stack) trimmed",
			cellName:  "  test-cell  ",
			realmName: "  test-realm  ",
			spaceName: "  test-space  ",
			stackName: "  test-stack  ",
			setupRunner: func(f *fakeRunner, expectedCellName, expectedRealmName, expectedSpaceName, expectedStackName string) {
				cell := buildTestCell(expectedCellName, expectedRealmName, expectedSpaceName, expectedStackName)
				f.GetCellFn = func(received intmodel.Cell) (intmodel.Cell, error) {
					if received.Metadata.Name != expectedCellName {
						t.Errorf("expected trimmed cell name %q, got %q", expectedCellName, received.Metadata.Name)
					}
					if received.Spec.RealmName != expectedRealmName {
						t.Errorf("expected trimmed realm name %q, got %q", expectedRealmName, received.Spec.RealmName)
					}
					if received.Spec.SpaceName != expectedSpaceName {
						t.Errorf("expected trimmed space name %q, got %q", expectedSpaceName, received.Spec.SpaceName)
					}
					if received.Spec.StackName != expectedStackName {
						t.Errorf("expected trimmed stack name %q, got %q", expectedStackName, received.Spec.StackName)
					}
					return cell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
				}
			},
			wantResult: func(t *testing.T, result controller.GetCellResult) {
				if !result.MetadataExists {
					t.Error("expected MetadataExists to be true")
				}
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner, "test-cell", "test-realm", "test-space", "test-stack")
			}

			ctrl := setupTestController(t, mockRunner)
			cell := intmodel.Cell{
				Metadata: intmodel.CellMetadata{
					Name: tt.cellName,
				},
				Spec: intmodel.CellSpec{
					RealmName: tt.realmName,
					SpaceName: tt.spaceName,
					StackName: tt.stackName,
				},
			}

			result, err := ctrl.GetCell(cell)

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

// TestListCells_SuccessfulRetrieval tests successful retrieval of cells list.
func TestListCells_SuccessfulRetrieval(t *testing.T) {
	tests := []struct {
		name        string
		realmName   string
		spaceName   string
		stackName   string
		setupRunner func(*fakeRunner, string, string, string)
		wantCells   []string
		wantErr     bool
	}{
		{
			name:      "returns list of cells from runner",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner, realmName, spaceName, stackName string) {
				f.ListCellsFn = func(filterRealmName, filterSpaceName, filterStackName string) ([]intmodel.Cell, error) {
					if filterRealmName != realmName {
						t.Errorf("expected realm filter %q, got %q", realmName, filterRealmName)
					}
					if filterSpaceName != spaceName {
						t.Errorf("expected space filter %q, got %q", spaceName, filterSpaceName)
					}
					if filterStackName != stackName {
						t.Errorf("expected stack filter %q, got %q", stackName, filterStackName)
					}
					return []intmodel.Cell{
						buildTestCell("cell1", realmName, spaceName, stackName),
						buildTestCell("cell2", realmName, spaceName, stackName),
					}, nil
				}
			},
			wantCells: []string{"cell1", "cell2"},
			wantErr:   false,
		},
		{
			name:      "empty list handled correctly",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner, _ string, _ string, _ string) {
				f.ListCellsFn = func(_ string, _ string, _ string) ([]intmodel.Cell, error) {
					return []intmodel.Cell{}, nil
				}
			},
			wantCells: []string{},
			wantErr:   false,
		},
		{
			name:      "works with empty filters",
			realmName: "",
			spaceName: "",
			stackName: "",
			setupRunner: func(f *fakeRunner, _ string, _ string, _ string) {
				f.ListCellsFn = func(_ string, _ string, _ string) ([]intmodel.Cell, error) {
					return []intmodel.Cell{
						buildTestCell("cell1", "realm1", "space1", "stack1"),
						buildTestCell("cell2", "realm2", "space2", "stack2"),
					}, nil
				}
			},
			wantCells: []string{"cell1", "cell2"},
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner, tt.realmName, tt.spaceName, tt.stackName)
			}

			ctrl := setupTestController(t, mockRunner)

			cells, err := ctrl.ListCells(tt.realmName, tt.spaceName, tt.stackName)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(cells) != len(tt.wantCells) {
				t.Errorf("expected %d cells, got %d", len(tt.wantCells), len(cells))
			}
			for i, wantName := range tt.wantCells {
				if i < len(cells) && cells[i].Metadata.Name != wantName {
					t.Errorf("expected cell[%d].Name = %q, got %q", i, wantName, cells[i].Metadata.Name)
				}
			}
		})
	}
}

// TestListCells_RunnerError tests error propagation from runner.
func TestListCells_RunnerError(t *testing.T) {
	mockRunner := &fakeRunner{}
	mockRunner.ListCellsFn = func(_ string, _ string, _ string) ([]intmodel.Cell, error) {
		return nil, errors.New("runner error")
	}

	ctrl := setupTestController(t, mockRunner)

	_, err := ctrl.ListCells("test-realm", "test-space", "test-stack")

	if err == nil {
		t.Fatal("expected error but got none")
	}
}
