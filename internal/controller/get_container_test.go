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

// TestGetContainer_SuccessfulRetrieval tests successful retrieval of container with all state flags.
func TestGetContainer_SuccessfulRetrieval(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		realmName     string
		spaceName     string
		stackName     string
		cellName      string
		setupRunner   func(*fakeRunner)
		wantResult    func(t *testing.T, result controller.GetContainerResult)
		wantErr       bool
	}{
		{
			name:          "container exists in cell",
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
			},
			wantResult: func(t *testing.T, result controller.GetContainerResult) {
				if !result.CellMetadataExists {
					t.Error("expected CellMetadataExists to be true")
				}
				if !result.ContainerExists {
					t.Error("expected ContainerExists to be true")
				}
				if result.Container.Metadata.Name != "test-container" {
					t.Errorf("expected container name 'test-container', got %q", result.Container.Metadata.Name)
				}
				if result.Container.Spec.Image != "alpine:latest" {
					t.Errorf("expected container image 'alpine:latest', got %q", result.Container.Spec.Image)
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

			result, err := ctrl.GetContainer(container)

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

// TestGetContainer_ValidationErrors tests validation errors for container name, realm name, space name, stack name, and cell name.
func TestGetContainer_ValidationErrors(t *testing.T) {
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

			_, err := ctrl.GetContainer(container)

			if err == nil {
				t.Fatal("expected error but got none")
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("expected error %v, got %v", tt.wantErr, err)
			}
		})
	}
}

// TestGetContainer_CellNotFound tests cell not found scenario.
func TestGetContainer_CellNotFound(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		realmName     string
		spaceName     string
		stackName     string
		cellName      string
		setupRunner   func(*fakeRunner)
		wantResult    func(t *testing.T, result controller.GetContainerResult)
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
			wantResult: func(t *testing.T, result controller.GetContainerResult) {
				if result.CellMetadataExists {
					t.Error("expected CellMetadataExists to be false")
				}
			},
			wantErr:     true,
			errContains: "failed to get cell",
		},
		{
			name:          "cell not found - MetadataExists=false",
			containerName: "test-container",
			realmName:     "test-realm",
			spaceName:     "test-space",
			stackName:     "test-stack",
			cellName:      "test-cell",
			setupRunner: func(f *fakeRunner) {
				// GetCell returns ErrCellNotFound, which sets MetadataExists=false
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return intmodel.Cell{}, errdefs.ErrCellNotFound
				}
			},
			wantResult: func(t *testing.T, result controller.GetContainerResult) {
				if result.CellMetadataExists {
					t.Error("expected CellMetadataExists to be false")
				}
			},
			wantErr:     true,
			errContains: "failed to get cell",
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

			result, err := ctrl.GetContainer(container)

			if !tt.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}

			if err == nil {
				t.Fatal("expected error but got none")
			}

			if tt.wantResult != nil {
				tt.wantResult(t, result)
			}

			if tt.errContains != "" {
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("expected error to contain %q, got %q", tt.errContains, err.Error())
				}
			}
		})
	}
}

// TestGetContainer_ContainerNotFoundInCell tests container not found in cell scenario.
func TestGetContainer_ContainerNotFoundInCell(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		realmName     string
		spaceName     string
		stackName     string
		cellName      string
		setupRunner   func(*fakeRunner)
		wantResult    func(t *testing.T, result controller.GetContainerResult)
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
			wantResult: func(t *testing.T, result controller.GetContainerResult) {
				if !result.CellMetadataExists {
					t.Error("expected CellMetadataExists to be true")
				}
				if result.ContainerExists {
					t.Error("expected ContainerExists to be false")
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

			result, err := ctrl.GetContainer(container)

			if !tt.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}

			if err == nil {
				t.Fatal("expected error but got none")
			}

			if tt.wantResult != nil {
				tt.wantResult(t, result)
			}

			if tt.errContains != "" {
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("expected error to contain %q, got %q", tt.errContains, err.Error())
				}
			}
		})
	}
}

// TestGetContainer_ContainerLookupByID tests container lookup by ContainerSpec.ID.
func TestGetContainer_ContainerLookupByID(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		realmName     string
		spaceName     string
		stackName     string
		cellName      string
		setupRunner   func(*fakeRunner)
		wantResult    func(t *testing.T, result controller.GetContainerResult)
		wantErr       bool
	}{
		{
			name:          "container found by matching ContainerSpec.ID with container name",
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
			},
			wantResult: func(t *testing.T, result controller.GetContainerResult) {
				if result.Container.Spec.ID != "target-container" {
					t.Errorf("expected container spec ID to be 'target-container', got %q", result.Container.Spec.ID)
				}
				if result.Container.Spec.Image != "nginx:latest" {
					t.Errorf("expected container image to be 'nginx:latest', got %q", result.Container.Spec.Image)
				}
				if result.Container.Metadata.Name != "target-container" {
					t.Errorf("expected container name to be 'target-container', got %q", result.Container.Metadata.Name)
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

			result, err := ctrl.GetContainer(container)

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

// TestGetContainer_ResultContainerConstruction tests container object construction from ContainerSpec.
func TestGetContainer_ResultContainerConstruction(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		realmName     string
		spaceName     string
		stackName     string
		cellName      string
		inputLabels   map[string]string
		setupRunner   func(*fakeRunner)
		wantResult    func(t *testing.T, result controller.GetContainerResult)
		wantErr       bool
	}{
		{
			name:          "container object built correctly from found ContainerSpec with labels preserved",
			containerName: "test-container",
			realmName:     "test-realm",
			spaceName:     "test-space",
			stackName:     "test-stack",
			cellName:      "test-cell",
			inputLabels:   map[string]string{"custom": "label"},
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				existingCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "test-container", Image: "alpine:latest"},
				}
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.GetContainerStateFn = func(_ intmodel.Cell, _ string) (intmodel.ContainerState, error) {
					return intmodel.ContainerStateReady, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
				}
			},
			wantResult: func(t *testing.T, result controller.GetContainerResult) {
				if result.Container.Metadata.Name != "test-container" {
					t.Errorf("expected container name 'test-container', got %q", result.Container.Metadata.Name)
				}
				if result.Container.Spec.Image != "alpine:latest" {
					t.Errorf("expected container image 'alpine:latest', got %q", result.Container.Spec.Image)
				}
				if result.Container.Metadata.Labels["custom"] != "label" {
					t.Errorf("expected label to be preserved, got %v", result.Container.Metadata.Labels)
				}
				if result.Container.Status.State != intmodel.ContainerStateReady {
					t.Errorf("expected state to be Ready, got %v", result.Container.Status.State)
				}
			},
			wantErr: false,
		},
		{
			name:          "container object built with empty labels map when input has nil labels",
			containerName: "test-container",
			realmName:     "test-realm",
			spaceName:     "test-space",
			stackName:     "test-stack",
			cellName:      "test-cell",
			inputLabels:   nil,
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
			},
			wantResult: func(t *testing.T, result controller.GetContainerResult) {
				if result.Container.Metadata.Labels == nil {
					t.Error("expected labels to be non-nil (empty map)")
				}
				if len(result.Container.Metadata.Labels) != 0 {
					t.Errorf("expected empty labels map, got %v", result.Container.Metadata.Labels)
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
					Name:   tt.containerName,
					Labels: tt.inputLabels,
				},
				Spec: intmodel.ContainerSpec{
					ID:        tt.containerName,
					RealmName: tt.realmName,
					SpaceName: tt.spaceName,
					StackName: tt.stackName,
					CellName:  tt.cellName,
					Image:     "alpine:latest",
				},
			}

			result, err := ctrl.GetContainer(container)

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

// TestGetContainer_RunnerErrors tests error propagation from GetCell integration.
func TestGetContainer_RunnerErrors(t *testing.T) {
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
			name:          "GetCell error (non-NotFound) is propagated",
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
			errContains: "failed to get cell",
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

			_, err := ctrl.GetContainer(container)

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
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("expected error to contain %q, got %q", tt.errContains, err.Error())
				}
			}
		})
	}
}

// TestGetContainer_NameTrimming tests name trimming.
func TestGetContainer_NameTrimming(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		realmName     string
		spaceName     string
		stackName     string
		cellName      string
		setupRunner   func(*fakeRunner)
		wantResult    func(t *testing.T, result controller.GetContainerResult)
		wantErr       bool
	}{
		{
			name:          "all five names trimmed",
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
				f.GetCellFn = func(received intmodel.Cell) (intmodel.Cell, error) {
					// Verify trimmed names were passed to GetCell
					if received.Metadata.Name != "test-cell" {
						t.Errorf("expected trimmed cell name 'test-cell', got %q", received.Metadata.Name)
					}
					if received.Spec.RealmName != "test-realm" {
						t.Errorf("expected trimmed realm name 'test-realm', got %q", received.Spec.RealmName)
					}
					if received.Spec.SpaceName != "test-space" {
						t.Errorf("expected trimmed space name 'test-space', got %q", received.Spec.SpaceName)
					}
					if received.Spec.StackName != "test-stack" {
						t.Errorf("expected trimmed stack name 'test-stack', got %q", received.Spec.StackName)
					}
					return existingCell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
				}
			},
			wantResult: func(t *testing.T, result controller.GetContainerResult) {
				if result.Container.Metadata.Name != "test-container" {
					t.Errorf("expected trimmed container name 'test-container', got %q", result.Container.Metadata.Name)
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

			result, err := ctrl.GetContainer(container)

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

// TestListContainers_SuccessfulRetrieval tests successful retrieval of containers list.
func TestListContainers_SuccessfulRetrieval(t *testing.T) {
	tests := []struct {
		name           string
		realmName      string
		spaceName      string
		stackName      string
		cellName       string
		setupRunner    func(*fakeRunner, string, string, string, string)
		wantContainers []string
		wantErr        bool
	}{
		{
			name:      "returns list of containers from runner",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			cellName:  "test-cell",
			setupRunner: func(f *fakeRunner, realmName, spaceName, stackName, cellName string) {
				f.ListContainersFn = func(filterRealmName, filterSpaceName, filterStackName, filterCellName string) ([]intmodel.ContainerSpec, error) {
					if filterRealmName != realmName {
						t.Errorf("expected realm filter %q, got %q", realmName, filterRealmName)
					}
					if filterSpaceName != spaceName {
						t.Errorf("expected space filter %q, got %q", spaceName, filterSpaceName)
					}
					if filterStackName != stackName {
						t.Errorf("expected stack filter %q, got %q", stackName, filterStackName)
					}
					if filterCellName != cellName {
						t.Errorf("expected cell filter %q, got %q", cellName, filterCellName)
					}
					return []intmodel.ContainerSpec{
						{ID: "container1", Image: "alpine:latest"},
						{ID: "container2", Image: "nginx:latest"},
					}, nil
				}
			},
			wantContainers: []string{"container1", "container2"},
			wantErr:        false,
		},
		{
			name:      "empty list handled correctly",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			cellName:  "test-cell",
			setupRunner: func(f *fakeRunner, _ string, _ string, _ string, _ string) {
				f.ListContainersFn = func(_ string, _ string, _ string, _ string) ([]intmodel.ContainerSpec, error) {
					return []intmodel.ContainerSpec{}, nil
				}
			},
			wantContainers: []string{},
			wantErr:        false,
		},
		{
			name:      "works with empty filters",
			realmName: "",
			spaceName: "",
			stackName: "",
			cellName:  "",
			setupRunner: func(f *fakeRunner, _ string, _ string, _ string, _ string) {
				f.ListContainersFn = func(_ string, _ string, _ string, _ string) ([]intmodel.ContainerSpec, error) {
					return []intmodel.ContainerSpec{
						{ID: "container1", Image: "alpine:latest"},
						{ID: "container2", Image: "nginx:latest"},
					}, nil
				}
			},
			wantContainers: []string{"container1", "container2"},
			wantErr:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner, tt.realmName, tt.spaceName, tt.stackName, tt.cellName)
			}

			ctrl := setupTestController(t, mockRunner)

			containers, err := ctrl.ListContainers(tt.realmName, tt.spaceName, tt.stackName, tt.cellName)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(containers) != len(tt.wantContainers) {
				t.Errorf("expected %d containers, got %d", len(tt.wantContainers), len(containers))
			}
			for i, wantID := range tt.wantContainers {
				if i < len(containers) && containers[i].ID != wantID {
					t.Errorf("expected container[%d].ID = %q, got %q", i, wantID, containers[i].ID)
				}
			}
		})
	}
}

// TestListContainers_RunnerError tests error propagation from runner.
func TestListContainers_RunnerError(t *testing.T) {
	mockRunner := &fakeRunner{}
	mockRunner.ListContainersFn = func(_ string, _ string, _ string, _ string) ([]intmodel.ContainerSpec, error) {
		return nil, errors.New("runner error")
	}

	ctrl := setupTestController(t, mockRunner)

	_, err := ctrl.ListContainers("test-realm", "test-space", "test-stack", "test-cell")

	if err == nil {
		t.Fatal("expected error but got none")
	}
}
