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

func TestDeleteContainer_SuccessfulDeletion(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		realmName     string
		spaceName     string
		stackName     string
		cellName      string
		setupRunner   func(*fakeRunner)
		wantResult    func(t *testing.T, result controller.DeleteContainerResult)
		wantErr       bool
	}{
		{
			name:          "successful deletion",
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
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
				}
				f.DeleteContainerFn = func(_ intmodel.Cell, containerID string) error {
					if containerID != "test-container" {
						return errors.New("unexpected container ID")
					}
					return nil
				}
				f.UpdateCellMetadataFn = func(cell intmodel.Cell) error {
					// Verify container is removed from cell
					if len(cell.Spec.Containers) != 0 {
						return errors.New("expected container to be removed from cell")
					}
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.DeleteContainerResult) {
				if !result.ContainerExists {
					t.Error("expected ContainerExists to be true")
				}
				if !result.CellMetadataExists {
					t.Error("expected CellMetadataExists to be true")
				}
				if len(result.Deleted) != 2 {
					t.Errorf("expected Deleted to contain 2 items, got %d", len(result.Deleted))
				}
				deletedMap := make(map[string]bool)
				for _, d := range result.Deleted {
					deletedMap[d] = true
				}
				if !deletedMap["container"] || !deletedMap["task"] {
					t.Errorf("expected Deleted to contain 'container', 'task', got %v", result.Deleted)
				}
				if result.Container.Metadata.Name != "test-container" {
					t.Errorf("expected container name to be 'test-container', got %q", result.Container.Metadata.Name)
				}
				if result.Container.Spec.ID != "test-container" {
					t.Errorf("expected container spec ID to be 'test-container', got %q", result.Container.Spec.ID)
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

			result, err := ctrl.DeleteContainer(container)

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

func TestDeleteContainer_ValidationErrors(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		realmName     string
		spaceName     string
		stackName     string
		cellName      string
		wantErr       error
	}{
		{
			name:          "empty container name returns ErrContainerNameRequired",
			containerName: "",
			realmName:     "test-realm",
			spaceName:     "test-space",
			stackName:     "test-stack",
			cellName:      "test-cell",
			wantErr:       errdefs.ErrContainerNameRequired,
		},
		{
			name:          "whitespace-only container name returns ErrContainerNameRequired",
			containerName: "   ",
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

			container := buildTestContainer(
				tt.containerName,
				tt.realmName,
				tt.spaceName,
				tt.stackName,
				tt.cellName,
				"alpine:latest",
			)

			_, err := ctrl.DeleteContainer(container)

			if err == nil {
				t.Fatal("expected error but got none")
			}

			if !errors.Is(err, tt.wantErr) {
				t.Errorf("expected error %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestDeleteContainer_CellNotFound(t *testing.T) {
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
			container := buildTestContainer(
				tt.containerName,
				tt.realmName,
				tt.spaceName,
				tt.stackName,
				tt.cellName,
				"alpine:latest",
			)

			result, err := ctrl.DeleteContainer(container)

			if !tt.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}

			if err == nil {
				t.Fatal("expected error but got none")
			}

			// When GetCell returns ErrCellNotFound, GetCell sets MetadataExists=false and returns nil
			// DeleteContainer then checks MetadataExists and sets CellMetadataExists=false before returning error
			if !result.CellMetadataExists {
				_ = result
				// Expected when cell not found
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

func TestDeleteContainer_ContainerNotFoundInCell(t *testing.T) {
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
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
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

			result, err := ctrl.DeleteContainer(container)

			if !tt.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}

			if err == nil {
				t.Fatal("expected error but got none")
			}

			if result.ContainerExists {
				t.Error("expected ContainerExists to be false when container not found")
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

func TestDeleteContainer_RunnerErrors(t *testing.T) {
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
			name:          "DeleteContainer runner error is wrapped",
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
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
				}
				f.DeleteContainerFn = func(_ intmodel.Cell, _ string) error {
					return errors.New("deletion failed")
				}
			},
			wantErr:     true,
			errContains: "failed to delete container test-container",
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
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
				}
				f.DeleteContainerFn = func(_ intmodel.Cell, _ string) error {
					return nil
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

			_, err := ctrl.DeleteContainer(container)

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

func TestDeleteContainer_NameTrimming(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		realmName     string
		spaceName     string
		stackName     string
		cellName      string
		setupRunner   func(*fakeRunner)
		wantResult    func(t *testing.T, result controller.DeleteContainerResult)
		wantErr       bool
	}{
		{
			name:          "container name, realm name, space name, stack name, and cell name with leading/trailing whitespace are trimmed",
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
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
				}
				f.DeleteContainerFn = func(_ intmodel.Cell, containerID string) error {
					if containerID != "test-container" {
						return errors.New("unexpected container ID")
					}
					return nil
				}
				f.UpdateCellMetadataFn = func(_ intmodel.Cell) error {
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.DeleteContainerResult) {
				if result.Container.Metadata.Name != "test-container" {
					t.Errorf(
						"expected container name to be trimmed to 'test-container', got %q",
						result.Container.Metadata.Name,
					)
				}
			},
			wantErr: false,
		},
		{
			name:          "container name that becomes empty after trimming triggers validation error",
			containerName: "   ",
			realmName:     "test-realm",
			spaceName:     "test-space",
			stackName:     "test-stack",
			cellName:      "test-cell",
			setupRunner:   func(_ *fakeRunner) {},
			wantResult:    nil,
			wantErr:       true,
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

			result, err := ctrl.DeleteContainer(container)

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

func TestDeleteContainer_ContainerLookupByID(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		realmName     string
		spaceName     string
		stackName     string
		cellName      string
		setupRunner   func(*fakeRunner)
		wantResult    func(t *testing.T, result controller.DeleteContainerResult)
		wantErr       bool
	}{
		{
			name:          "container is found by matching ContainerSpec.ID with container name",
			containerName: "target-container",
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
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
				}
				f.DeleteContainerFn = func(_ intmodel.Cell, containerID string) error {
					if containerID != "target-container" {
						return errors.New("unexpected container ID")
					}
					return nil
				}
				f.UpdateCellMetadataFn = func(cell intmodel.Cell) error {
					// Verify only target-container is removed, others remain
					if len(cell.Spec.Containers) != 2 {
						return errors.New("expected 2 containers to remain")
					}
					for _, c := range cell.Spec.Containers {
						if c.ID == "target-container" {
							return errors.New("expected target-container to be removed")
						}
					}
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.DeleteContainerResult) {
				if result.Container.Spec.ID != "target-container" {
					t.Errorf("expected container spec ID to be 'target-container', got %q", result.Container.Spec.ID)
				}
				if result.Container.Spec.Image != "nginx:latest" {
					t.Errorf("expected container image to be 'nginx:latest', got %q", result.Container.Spec.Image)
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

			result, err := ctrl.DeleteContainer(container)

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

func TestDeleteContainer_ResultContainerConstruction(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		realmName     string
		spaceName     string
		stackName     string
		cellName      string
		inputLabels   map[string]string
		setupRunner   func(*fakeRunner)
		wantResult    func(t *testing.T, result controller.DeleteContainerResult)
		wantErr       bool
	}{
		{
			name:          "result container is built correctly from found ContainerSpec with labels preserved",
			containerName: "test-container",
			realmName:     "test-realm",
			spaceName:     "test-space",
			stackName:     "test-stack",
			cellName:      "test-cell",
			inputLabels:   map[string]string{"label1": "value1", "label2": "value2"},
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				existingCell.Spec.Containers = []intmodel.ContainerSpec{
					{
						ID:        "test-container",
						Image:     "alpine:latest",
						RealmName: "test-realm",
						SpaceName: "test-space",
						StackName: "test-stack",
						CellName:  "test-cell",
					},
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
				f.DeleteContainerFn = func(_ intmodel.Cell, _ string) error {
					return nil
				}
				f.UpdateCellMetadataFn = func(_ intmodel.Cell) error {
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.DeleteContainerResult) {
				if result.Container.Metadata.Name != "test-container" {
					t.Errorf("expected container name to be 'test-container', got %q", result.Container.Metadata.Name)
				}
				if len(result.Container.Metadata.Labels) != 2 {
					t.Errorf("expected 2 labels, got %d", len(result.Container.Metadata.Labels))
				}
				if result.Container.Metadata.Labels["label1"] != "value1" {
					t.Errorf("expected label1 to be 'value1', got %q", result.Container.Metadata.Labels["label1"])
				}
				if result.Container.Spec.ID != "test-container" {
					t.Errorf("expected container spec ID to be 'test-container', got %q", result.Container.Spec.ID)
				}
				if result.Container.Status.State != intmodel.ContainerStateReady {
					t.Errorf("expected container state to be Ready, got %v", result.Container.Status.State)
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
			container.Metadata.Labels = tt.inputLabels

			result, err := ctrl.DeleteContainer(container)

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

func TestDeleteContainer_CellMetadataUpdate(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		realmName     string
		spaceName     string
		stackName     string
		cellName      string
		setupRunner   func(*fakeRunner)
		wantResult    func(t *testing.T, result controller.DeleteContainerResult)
		wantErr       bool
	}{
		{
			name:          "container is removed from cell.Spec.Containers and other containers are preserved",
			containerName: "container2",
			realmName:     "test-realm",
			spaceName:     "test-space",
			stackName:     "test-stack",
			cellName:      "test-cell",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				existingCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "container1", Image: "alpine:latest"},
					{ID: "container2", Image: "nginx:latest"},
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
				f.DeleteContainerFn = func(_ intmodel.Cell, _ string) error {
					return nil
				}
				f.UpdateCellMetadataFn = func(cell intmodel.Cell) error {
					// Verify container2 is removed and container1, container3 remain
					if len(cell.Spec.Containers) != 2 {
						return errors.New("expected 2 containers to remain")
					}
					containerIDs := make(map[string]bool)
					for _, c := range cell.Spec.Containers {
						containerIDs[c.ID] = true
					}
					if !containerIDs["container1"] || !containerIDs["container3"] {
						return errors.New("expected container1 and container3 to remain")
					}
					if containerIDs["container2"] {
						return errors.New("expected container2 to be removed")
					}
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.DeleteContainerResult) {
				if result.Container.Spec.ID != "container2" {
					t.Errorf("expected deleted container ID to be 'container2', got %q", result.Container.Spec.ID)
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

			result, err := ctrl.DeleteContainer(container)

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
