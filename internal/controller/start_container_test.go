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

func TestStartContainer_SuccessfulStart(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		realmName     string
		spaceName     string
		stackName     string
		cellName      string
		setupRunner   func(*fakeRunner)
		wantResult    func(t *testing.T, result controller.StartContainerResult)
		wantErr       bool
	}{
		{
			name:          "successful start",
			containerName: "test-container",
			realmName:     "test-realm",
			spaceName:     "test-space",
			stackName:     "test-stack",
			cellName:      "test-cell",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				existingCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "test-container", Image: "alpine:latest"},
					{ID: "other-container", Image: "nginx:latest"},
				}
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.StartContainerFn = func(cell intmodel.Cell, containerID string) (intmodel.Cell, error) {
					// Verify container ID matches
					if containerID != "test-container" {
						return intmodel.Cell{}, errors.New("unexpected container ID")
					}
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
				f.UpdateCellMetadataFn = func(cell intmodel.Cell) error {
					// Container should still be in cell (start doesn't remove it)
					if len(cell.Spec.Containers) != 2 {
						return errors.New("expected container to remain in cell after start")
					}
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.StartContainerResult) {
				if !result.Started {
					t.Error("expected Started to be true")
				}
				if result.Container.Metadata.Name != "test-container" {
					t.Errorf("expected container name to be 'test-container', got %q", result.Container.Metadata.Name)
				}
				if result.Container.Status.State != intmodel.ContainerStateReady {
					t.Errorf("expected container state to be Ready, got %v", result.Container.Status.State)
				}
				if result.Container.Spec.ID != "test-container" {
					t.Errorf("expected container spec ID to be 'test-container', got %q", result.Container.Spec.ID)
				}
			},
			wantErr: false,
		},
		{
			name:          "successful start with labels preserved",
			containerName: "test-container",
			realmName:     "test-realm",
			spaceName:     "test-space",
			stackName:     "test-stack",
			cellName:      "test-cell",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				existingCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "test-container", Image: "alpine:latest"},
				}
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.StartContainerFn = func(cell intmodel.Cell, _ string) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
				f.UpdateCellMetadataFn = func(_ intmodel.Cell) error {
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.StartContainerResult) {
				if result.Container.Metadata.Name != "test-container" {
					t.Errorf("expected container name to be 'test-container', got %q", result.Container.Metadata.Name)
				}
				// Labels should be preserved from input
				if result.Container.Metadata.Labels == nil {
					t.Error("expected labels map to be initialized")
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
			container := buildTestContainer(
				tt.containerName,
				tt.realmName,
				tt.spaceName,
				tt.stackName,
				tt.cellName,
				"alpine:latest",
			)

			result, err := ctrl.StartContainer(container)

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

func TestStartContainer_ValidationErrors(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		containerID   string
		realmName     string
		spaceName     string
		stackName     string
		cellName      string
		wantErr       error
	}{
		{
			name:          "empty container name and ID returns ErrContainerNameRequired",
			containerName: "",
			containerID:   "",
			realmName:     "test-realm",
			spaceName:     "test-space",
			stackName:     "test-stack",
			cellName:      "test-cell",
			wantErr:       errdefs.ErrContainerNameRequired,
		},
		{
			name:          "whitespace-only container name and ID returns ErrContainerNameRequired",
			containerName: "   ",
			containerID:   "   ",
			realmName:     "test-realm",
			spaceName:     "test-space",
			stackName:     "test-stack",
			cellName:      "test-cell",
			wantErr:       errdefs.ErrContainerNameRequired,
		},
		{
			name:          "empty realm name returns ErrRealmNameRequired",
			containerName: "test-container",
			realmName:     "",
			spaceName:     "test-space",
			stackName:     "test-stack",
			cellName:      "test-cell",
			wantErr:       errdefs.ErrRealmNameRequired,
		},
		{
			name:          "whitespace-only realm name returns ErrRealmNameRequired",
			containerName: "test-container",
			realmName:     "   ",
			spaceName:     "test-space",
			stackName:     "test-stack",
			cellName:      "test-cell",
			wantErr:       errdefs.ErrRealmNameRequired,
		},
		{
			name:          "empty space name returns ErrSpaceNameRequired",
			containerName: "test-container",
			realmName:     "test-realm",
			spaceName:     "",
			stackName:     "test-stack",
			cellName:      "test-cell",
			wantErr:       errdefs.ErrSpaceNameRequired,
		},
		{
			name:          "whitespace-only space name returns ErrSpaceNameRequired",
			containerName: "test-container",
			realmName:     "test-realm",
			spaceName:     "   ",
			stackName:     "test-stack",
			cellName:      "test-cell",
			wantErr:       errdefs.ErrSpaceNameRequired,
		},
		{
			name:          "empty stack name returns ErrStackNameRequired",
			containerName: "test-container",
			realmName:     "test-realm",
			spaceName:     "test-space",
			stackName:     "",
			cellName:      "test-cell",
			wantErr:       errdefs.ErrStackNameRequired,
		},
		{
			name:          "whitespace-only stack name returns ErrStackNameRequired",
			containerName: "test-container",
			realmName:     "test-realm",
			spaceName:     "test-space",
			stackName:     "   ",
			cellName:      "test-cell",
			wantErr:       errdefs.ErrStackNameRequired,
		},
		{
			name:          "empty cell name returns ErrCellNameRequired",
			containerName: "test-container",
			realmName:     "test-realm",
			spaceName:     "test-space",
			stackName:     "test-stack",
			cellName:      "",
			wantErr:       errdefs.ErrCellNameRequired,
		},
		{
			name:          "whitespace-only cell name returns ErrCellNameRequired",
			containerName: "test-container",
			realmName:     "test-realm",
			spaceName:     "test-space",
			stackName:     "test-stack",
			cellName:      "   ",
			wantErr:       errdefs.ErrCellNameRequired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			ctrl := setupTestController(t, mockRunner)

			container := intmodel.Container{
				Metadata: intmodel.ContainerMetadata{
					Name: tt.containerName,
				},
				Spec: intmodel.ContainerSpec{
					ID:        tt.containerID,
					RealmName: tt.realmName,
					SpaceName: tt.spaceName,
					StackName: tt.stackName,
					CellName:  tt.cellName,
					Image:     "alpine:latest",
				},
			}

			_, err := ctrl.StartContainer(container)

			if err == nil {
				t.Fatal("expected error but got none")
			}

			if !errors.Is(err, tt.wantErr) {
				t.Errorf("expected error %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestStartContainer_CellNotFound(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		realmName     string
		spaceName     string
		stackName     string
		cellName      string
		setupRunner   func(*fakeRunner)
		wantErr       bool
		errContains   string
	}{
		{
			name:          "cell not found - ErrCellNotFound",
			containerName: "test-container",
			realmName:     "test-realm",
			spaceName:     "test-space",
			stackName:     "test-stack",
			cellName:      "test-cell",
			setupRunner: func(f *fakeRunner) {
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
			container := buildTestContainer(
				tt.containerName,
				tt.realmName,
				tt.spaceName,
				tt.stackName,
				tt.cellName,
				"alpine:latest",
			)

			result, err := ctrl.StartContainer(container)

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

func TestStartContainer_ContainerNotFound(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		realmName     string
		spaceName     string
		stackName     string
		cellName      string
		setupRunner   func(*fakeRunner)
		wantErr       bool
		errContains   string
	}{
		{
			name:          "container not found in cell",
			containerName: "nonexistent-container",
			realmName:     "test-realm",
			spaceName:     "test-space",
			stackName:     "test-stack",
			cellName:      "test-cell",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				existingCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "other-container", Image: "alpine:latest"},
				}
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
			},
			wantErr:     true,
			errContains: "container \"nonexistent-container\" not found in cell \"test-cell\"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner)
			}

			ctrl := setupTestController(t, mockRunner)
			container := buildTestContainer(
				tt.containerName,
				tt.realmName,
				tt.spaceName,
				tt.stackName,
				tt.cellName,
				"alpine:latest",
			)

			result, err := ctrl.StartContainer(container)

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
				t.Error("expected Started to be false when container not found")
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

func TestStartContainer_RunnerErrors(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		realmName     string
		spaceName     string
		stackName     string
		cellName      string
		setupRunner   func(*fakeRunner)
		wantErr       bool
		errContains   string
	}{
		{
			name:          "GetCell error (non-NotFound) is returned",
			containerName: "test-container",
			realmName:     "test-realm",
			spaceName:     "test-space",
			stackName:     "test-stack",
			cellName:      "test-cell",
			setupRunner: func(f *fakeRunner) {
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return intmodel.Cell{}, errors.New("unexpected error")
				}
			},
			wantErr:     true,
			errContains: "unexpected error",
		},
		{
			name:          "StartCell runner error is wrapped",
			containerName: "test-container",
			realmName:     "test-realm",
			spaceName:     "test-space",
			stackName:     "test-stack",
			cellName:      "test-cell",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				existingCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "test-container", Image: "alpine:latest"},
				}
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.StartContainerFn = func(_ intmodel.Cell, _ string) (intmodel.Cell, error) {
					return intmodel.Cell{}, errors.New("start failed")
				}
			},
			wantErr:     true,
			errContains: "failed to start container test-container",
		},
		{
			name:          "UpdateCellMetadata runner error is wrapped",
			containerName: "test-container",
			realmName:     "test-realm",
			spaceName:     "test-space",
			stackName:     "test-stack",
			cellName:      "test-cell",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				existingCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "test-container", Image: "alpine:latest"},
				}
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.StartContainerFn = func(cell intmodel.Cell, _ string) (intmodel.Cell, error) {
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
			container := buildTestContainer(
				tt.containerName,
				tt.realmName,
				tt.spaceName,
				tt.stackName,
				tt.cellName,
				"alpine:latest",
			)

			_, err := ctrl.StartContainer(container)

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

func TestStartContainer_ContainerLookupByID(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		containerID   string
		realmName     string
		spaceName     string
		stackName     string
		cellName      string
		setupRunner   func(*fakeRunner)
		wantResult    func(t *testing.T, result controller.StartContainerResult)
		wantErr       bool
	}{
		{
			name:          "container is found by matching ContainerSpec.ID with container name from Metadata.Name",
			containerName: "target-container",
			containerID:   "",
			realmName:     "test-realm",
			spaceName:     "test-space",
			stackName:     "test-stack",
			cellName:      "test-cell",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				existingCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "container1", Image: "alpine:latest"},
					{ID: "target-container", Image: "nginx:latest"},
					{ID: "container2", Image: "alpine:latest"},
				}
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.StartContainerFn = func(cell intmodel.Cell, containerID string) (intmodel.Cell, error) {
					if containerID != "target-container" {
						return intmodel.Cell{}, errors.New("unexpected container ID")
					}
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
				f.UpdateCellMetadataFn = func(_ intmodel.Cell) error {
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.StartContainerResult) {
				if result.Container.Spec.ID != "target-container" {
					t.Errorf("expected container spec ID to be 'target-container', got %q", result.Container.Spec.ID)
				}
				if result.Container.Metadata.Name != "target-container" {
					t.Errorf("expected container name to be 'target-container', got %q", result.Container.Metadata.Name)
				}
			},
			wantErr: false,
		},
		{
			name:          "container name from Spec.ID is used when Metadata.Name is empty",
			containerName: "",
			containerID:   "target-container-id",
			realmName:     "test-realm",
			spaceName:     "test-space",
			stackName:     "test-stack",
			cellName:      "test-cell",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				existingCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "target-container-id", Image: "alpine:latest"},
				}
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.StartContainerFn = func(cell intmodel.Cell, containerID string) (intmodel.Cell, error) {
					if containerID != "target-container-id" {
						return intmodel.Cell{}, errors.New("unexpected container ID")
					}
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
				f.UpdateCellMetadataFn = func(_ intmodel.Cell) error {
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.StartContainerResult) {
				if result.Container.Metadata.Name != "target-container-id" {
					t.Errorf(
						"expected container name to be 'target-container-id', got %q",
						result.Container.Metadata.Name,
					)
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
			container := intmodel.Container{
				Metadata: intmodel.ContainerMetadata{
					Name: tt.containerName,
				},
				Spec: intmodel.ContainerSpec{
					ID:        tt.containerID,
					RealmName: tt.realmName,
					SpaceName: tt.spaceName,
					StackName: tt.stackName,
					CellName:  tt.cellName,
					Image:     "alpine:latest",
				},
			}

			result, err := ctrl.StartContainer(container)

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

func TestStartContainer_NameTrimming(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		realmName     string
		spaceName     string
		stackName     string
		cellName      string
		setupRunner   func(*fakeRunner)
		wantResult    func(t *testing.T, result controller.StartContainerResult)
		wantErr       bool
	}{
		{
			name:          "all names with leading/trailing whitespace are trimmed",
			containerName: "  test-container  ",
			realmName:     "  test-realm  ",
			spaceName:     "  test-space  ",
			stackName:     "  test-stack  ",
			cellName:      "  test-cell  ",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				existingCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "test-container", Image: "alpine:latest"},
				}
				f.GetCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					// Verify trimmed names are used
					if cell.Metadata.Name != "test-cell" {
						return intmodel.Cell{}, errors.New("unexpected cell name")
					}
					return existingCell, nil
				}
				f.StartContainerFn = func(cell intmodel.Cell, containerID string) (intmodel.Cell, error) {
					if containerID != "test-container" {
						return intmodel.Cell{}, errors.New("unexpected container ID")
					}
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
				f.UpdateCellMetadataFn = func(_ intmodel.Cell) error {
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.StartContainerResult) {
				if result.Container.Metadata.Name != "test-container" {
					t.Errorf(
						"expected container name to be trimmed to 'test-container', got %q",
						result.Container.Metadata.Name,
					)
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
			container := buildTestContainer(
				tt.containerName,
				tt.realmName,
				tt.spaceName,
				tt.stackName,
				tt.cellName,
				"alpine:latest",
			)

			result, err := ctrl.StartContainer(container)

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
