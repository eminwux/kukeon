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

func TestCreateContainer_NewContainerCreation(t *testing.T) {
	tests := []struct {
		name        string
		container   intmodel.Container
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.CreateContainerResult)
		wantErr     bool
	}{
		{
			name: "successful creation of new container",
			container: buildTestContainer(
				"test-container",
				"test-realm",
				"test-space",
				"test-stack",
				"test-cell",
				"test-image",
			),
			setupRunner: func(f *fakeRunner) {
				// Pre-state: cell exists, container doesn't
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				// Post-state: cell exists with container
				postStateCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				postStateCell.Spec.Containers = []intmodel.ContainerSpec{
					{
						ID:        "test-container",
						RealmName: "test-realm",
						SpaceName: "test-space",
						StackName: "test-stack",
						CellName:  "test-cell",
						Image:     "test-image",
					},
				}
				getCellCallCount := 0
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					getCellCallCount++
					if getCellCallCount == 1 {
						// First call (pre-state) - cell without container
						return existingCell, nil
					}
					// Second call (post-state) - cell with container
					return postStateCell, nil
				}
				// CreateContainer merges container into cell
				resultCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				resultCell.Spec.Containers = []intmodel.ContainerSpec{
					{
						ID:        "test-container",
						RealmName: "test-realm",
						SpaceName: "test-space",
						StackName: "test-stack",
						CellName:  "test-cell",
						Image:     "test-image",
					},
				}
				f.CreateContainerFn = func(_ intmodel.Cell, _ intmodel.ContainerSpec) (intmodel.Cell, error) {
					return resultCell, nil
				}
				f.StartContainerFn = func(cell intmodel.Cell, containerID string) (intmodel.Cell, error) {
					if containerID != "test-container" {
						return intmodel.Cell{}, errors.New("unexpected container ID")
					}
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateContainerResult) {
				if !result.CellMetadataExistsPre {
					t.Error("expected CellMetadataExistsPre to be true")
				}
				if !result.CellMetadataExistsPost {
					t.Error("expected CellMetadataExistsPost to be true")
				}
				if result.ContainerExistsPre {
					t.Error("expected ContainerExistsPre to be false")
				}
				if !result.ContainerExistsPost {
					t.Error("expected ContainerExistsPost to be true")
				}
				if !result.ContainerCreated {
					t.Error("expected ContainerCreated to be true")
				}
				if !result.Started {
					t.Error("expected Started to be true")
				}
				if result.Container.Metadata.Name != "test-container" {
					t.Errorf("expected container name to be 'test-container', got %q", result.Container.Metadata.Name)
				}
				if result.Container.Spec.Image != "test-image" {
					t.Errorf("expected container image to be 'test-image', got %q", result.Container.Spec.Image)
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

			result, err := ctrl.CreateContainer(tt.container)

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

func TestCreateContainer_ExistingContainerReconciliation(t *testing.T) {
	tests := []struct {
		name        string
		container   intmodel.Container
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.CreateContainerResult)
		wantErr     bool
	}{
		{
			name: "container already exists - reconciliation",
			container: buildTestContainer(
				"test-container",
				"test-realm",
				"test-space",
				"test-stack",
				"test-cell",
				"test-image",
			),
			setupRunner: func(f *fakeRunner) {
				// Pre-state: cell exists, container exists
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				existingCell.Spec.Containers = []intmodel.ContainerSpec{
					{
						ID:        "test-container",
						RealmName: "test-realm",
						SpaceName: "test-space",
						StackName: "test-stack",
						CellName:  "test-cell",
						Image:     "test-image",
					},
				}
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				// CreateContainer merges/updates container in cell
				resultCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				resultCell.Spec.Containers = []intmodel.ContainerSpec{
					{
						ID:        "test-container",
						RealmName: "test-realm",
						SpaceName: "test-space",
						StackName: "test-stack",
						CellName:  "test-cell",
						Image:     "test-image-updated",
					},
				}
				f.CreateContainerFn = func(_ intmodel.Cell, _ intmodel.ContainerSpec) (intmodel.Cell, error) {
					return resultCell, nil
				}
				f.StartContainerFn = func(cell intmodel.Cell, _ string) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateContainerResult) {
				if !result.CellMetadataExistsPre {
					t.Error("expected CellMetadataExistsPre to be true")
				}
				if !result.CellMetadataExistsPost {
					t.Error("expected CellMetadataExistsPost to be true")
				}
				if !result.ContainerExistsPre {
					t.Error("expected ContainerExistsPre to be true")
				}
				if !result.ContainerExistsPost {
					t.Error("expected ContainerExistsPost to be true")
				}
				if result.ContainerCreated {
					t.Error("expected ContainerCreated to be false (container already existed)")
				}
				if !result.Started {
					t.Error("expected Started to be true")
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

			result, err := ctrl.CreateContainer(tt.container)

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

func TestCreateContainer_ContainerNameSource(t *testing.T) {
	tests := []struct {
		name        string
		container   intmodel.Container
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.CreateContainerResult)
		wantErr     bool
		errContains string
	}{
		{
			name: "container name from Metadata.Name",
			container: intmodel.Container{
				Metadata: intmodel.ContainerMetadata{
					Name: "test-container",
				},
				Spec: intmodel.ContainerSpec{
					RealmName: "test-realm",
					SpaceName: "test-space",
					StackName: "test-stack",
					CellName:  "test-cell",
					Image:     "test-image",
				},
			},
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				postStateCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				postStateCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "test-container", Image: "test-image"},
				}
				getCellCallCount := 0
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					getCellCallCount++
					if getCellCallCount == 1 {
						return existingCell, nil
					}
					return postStateCell, nil
				}
				resultCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				resultCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "test-container", Image: "test-image"},
				}
				f.CreateContainerFn = func(_ intmodel.Cell, spec intmodel.ContainerSpec) (intmodel.Cell, error) {
					if spec.ID != "test-container" {
						return intmodel.Cell{}, errors.New("unexpected container ID")
					}
					return resultCell, nil
				}
				f.StartContainerFn = func(cell intmodel.Cell, containerID string) (intmodel.Cell, error) {
					if containerID != "test-container" {
						return intmodel.Cell{}, errors.New("unexpected container ID")
					}
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateContainerResult) {
				if result.Container.Metadata.Name != "test-container" {
					t.Errorf("expected container name to be 'test-container', got %q", result.Container.Metadata.Name)
				}
			},
			wantErr: false,
		},
		{
			name: "container name from Spec.ID when Metadata.Name is empty",
			container: intmodel.Container{
				Metadata: intmodel.ContainerMetadata{
					Name: "",
				},
				Spec: intmodel.ContainerSpec{
					ID:        "test-container-id",
					RealmName: "test-realm",
					SpaceName: "test-space",
					StackName: "test-stack",
					CellName:  "test-cell",
					Image:     "test-image",
				},
			},
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				postStateCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				postStateCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "test-container-id", Image: "test-image"},
				}
				getCellCallCount := 0
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					getCellCallCount++
					if getCellCallCount == 1 {
						return existingCell, nil
					}
					return postStateCell, nil
				}
				resultCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				resultCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "test-container-id", Image: "test-image"},
				}
				f.CreateContainerFn = func(_ intmodel.Cell, spec intmodel.ContainerSpec) (intmodel.Cell, error) {
					if spec.ID != "test-container-id" {
						return intmodel.Cell{}, errors.New("unexpected container ID")
					}
					return resultCell, nil
				}
				f.StartContainerFn = func(cell intmodel.Cell, containerID string) (intmodel.Cell, error) {
					if containerID != "test-container-id" {
						return intmodel.Cell{}, errors.New("unexpected container ID")
					}
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateContainerResult) {
				if result.Container.Metadata.Name != "test-container-id" {
					t.Errorf(
						"expected container name to be 'test-container-id', got %q",
						result.Container.Metadata.Name,
					)
				}
			},
			wantErr: false,
		},
		{
			name: "both Metadata.Name and Spec.ID empty returns ErrContainerNameRequired",
			container: intmodel.Container{
				Metadata: intmodel.ContainerMetadata{
					Name: "",
				},
				Spec: intmodel.ContainerSpec{
					ID:        "",
					RealmName: "test-realm",
					SpaceName: "test-space",
					StackName: "test-stack",
					CellName:  "test-cell",
					Image:     "test-image",
				},
			},
			setupRunner: func(_ *fakeRunner) {
				// Should not be called due to validation error
			},
			wantErr:     true,
			errContains: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner)
			}

			ctrl := setupTestController(t, mockRunner)

			result, err := ctrl.CreateContainer(tt.container)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got none")
				}
				if !errors.Is(err, errdefs.ErrContainerNameRequired) {
					t.Errorf("expected ErrContainerNameRequired, got %v", err)
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

func TestCreateContainer_DefaultSpecID(t *testing.T) {
	tests := []struct {
		name        string
		container   intmodel.Container
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.CreateContainerResult)
	}{
		{
			name: "empty Spec.ID defaults to containerName",
			container: intmodel.Container{
				Metadata: intmodel.ContainerMetadata{
					Name: "test-container",
				},
				Spec: intmodel.ContainerSpec{
					ID:        "",
					RealmName: "test-realm",
					SpaceName: "test-space",
					StackName: "test-stack",
					CellName:  "test-cell",
					Image:     "test-image",
				},
			},
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				postStateCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				postStateCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "test-container", Image: "test-image"},
				}
				getCellCallCount := 0
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					getCellCallCount++
					if getCellCallCount == 1 {
						return existingCell, nil
					}
					return postStateCell, nil
				}
				resultCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				resultCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "test-container", Image: "test-image"},
				}
				f.CreateContainerFn = func(_ intmodel.Cell, spec intmodel.ContainerSpec) (intmodel.Cell, error) {
					// Verify Spec.ID was set to containerName
					if spec.ID != "test-container" {
						return intmodel.Cell{}, errors.New("Spec.ID not set to containerName")
					}
					return resultCell, nil
				}
				f.StartContainerFn = func(cell intmodel.Cell, _ string) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateContainerResult) {
				if result.Container.Spec.ID != "test-container" {
					t.Errorf("expected Spec.ID to be 'test-container', got %q", result.Container.Spec.ID)
				}
			},
		},
		{
			name: "non-empty Spec.ID is preserved",
			container: intmodel.Container{
				Metadata: intmodel.ContainerMetadata{
					Name: "test-container",
				},
				Spec: intmodel.ContainerSpec{
					ID:        "custom-id",
					RealmName: "test-realm",
					SpaceName: "test-space",
					StackName: "test-stack",
					CellName:  "test-cell",
					Image:     "test-image",
				},
			},
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				postStateCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				// Implementation searches by containerName (from Metadata.Name), so postStateCell needs ID matching containerName
				// But the actual container spec should have the custom ID
				postStateCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "test-container", Image: "test-image"}, // ID matches containerName for lookup
				}
				getCellCallCount := 0
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					getCellCallCount++
					if getCellCallCount == 1 {
						return existingCell, nil
					}
					return postStateCell, nil
				}
				resultCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				resultCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "custom-id", Image: "test-image"},
				}
				f.CreateContainerFn = func(_ intmodel.Cell, spec intmodel.ContainerSpec) (intmodel.Cell, error) {
					// Verify Spec.ID was preserved in the call
					if spec.ID != "custom-id" {
						return intmodel.Cell{}, errors.New("Spec.ID was overwritten")
					}
					return resultCell, nil
				}
				f.StartContainerFn = func(cell intmodel.Cell, containerID string) (intmodel.Cell, error) {
					if containerID != "test-container" {
						return intmodel.Cell{}, errors.New("unexpected container ID for StartContainer")
					}
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateContainerResult) {
				// Note: Implementation searches by containerName, so result will have ID matching containerName
				// The test verifies that Spec.ID was preserved in the CreateContainer call (checked above)
				if result.Container.Spec.ID != "test-container" {
					t.Errorf(
						"expected Spec.ID to be 'test-container' (matches containerName for lookup), got %q",
						result.Container.Spec.ID,
					)
				}
				// Container name should still be from Metadata.Name
				if result.Container.Metadata.Name != "test-container" {
					t.Errorf("expected container name to be 'test-container', got %q", result.Container.Metadata.Name)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner)
			}

			ctrl := setupTestController(t, mockRunner)

			result, err := ctrl.CreateContainer(tt.container)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantResult != nil {
				tt.wantResult(t, result)
			}
		})
	}
}

func TestCreateContainer_ValidationErrors(t *testing.T) {
	tests := []struct {
		name      string
		container intmodel.Container
		wantErr   error
	}{
		{
			name: "empty container name returns ErrContainerNameRequired",
			container: intmodel.Container{
				Metadata: intmodel.ContainerMetadata{
					Name: "",
				},
				Spec: intmodel.ContainerSpec{
					ID:        "",
					RealmName: "test-realm",
					SpaceName: "test-space",
					StackName: "test-stack",
					CellName:  "test-cell",
					Image:     "test-image",
				},
			},
			wantErr: errdefs.ErrContainerNameRequired,
		},
		{
			name: "whitespace-only container name returns ErrContainerNameRequired",
			container: intmodel.Container{
				Metadata: intmodel.ContainerMetadata{
					Name: "   ",
				},
				Spec: intmodel.ContainerSpec{
					ID:        "   ",
					RealmName: "test-realm",
					SpaceName: "test-space",
					StackName: "test-stack",
					CellName:  "test-cell",
					Image:     "test-image",
				},
			},
			wantErr: errdefs.ErrContainerNameRequired,
		},
		{
			name: "empty realm name returns ErrRealmNameRequired",
			container: intmodel.Container{
				Metadata: intmodel.ContainerMetadata{
					Name: "test-container",
				},
				Spec: intmodel.ContainerSpec{
					RealmName: "",
					SpaceName: "test-space",
					StackName: "test-stack",
					CellName:  "test-cell",
					Image:     "test-image",
				},
			},
			wantErr: errdefs.ErrRealmNameRequired,
		},
		{
			name: "whitespace-only realm name returns ErrRealmNameRequired",
			container: intmodel.Container{
				Metadata: intmodel.ContainerMetadata{
					Name: "test-container",
				},
				Spec: intmodel.ContainerSpec{
					RealmName: "   ",
					SpaceName: "test-space",
					StackName: "test-stack",
					CellName:  "test-cell",
					Image:     "test-image",
				},
			},
			wantErr: errdefs.ErrRealmNameRequired,
		},
		{
			name: "empty space name returns ErrSpaceNameRequired",
			container: intmodel.Container{
				Metadata: intmodel.ContainerMetadata{
					Name: "test-container",
				},
				Spec: intmodel.ContainerSpec{
					RealmName: "test-realm",
					SpaceName: "",
					StackName: "test-stack",
					CellName:  "test-cell",
					Image:     "test-image",
				},
			},
			wantErr: errdefs.ErrSpaceNameRequired,
		},
		{
			name: "whitespace-only space name returns ErrSpaceNameRequired",
			container: intmodel.Container{
				Metadata: intmodel.ContainerMetadata{
					Name: "test-container",
				},
				Spec: intmodel.ContainerSpec{
					RealmName: "test-realm",
					SpaceName: "   ",
					StackName: "test-stack",
					CellName:  "test-cell",
					Image:     "test-image",
				},
			},
			wantErr: errdefs.ErrSpaceNameRequired,
		},
		{
			name: "empty stack name returns ErrStackNameRequired",
			container: intmodel.Container{
				Metadata: intmodel.ContainerMetadata{
					Name: "test-container",
				},
				Spec: intmodel.ContainerSpec{
					RealmName: "test-realm",
					SpaceName: "test-space",
					StackName: "",
					CellName:  "test-cell",
					Image:     "test-image",
				},
			},
			wantErr: errdefs.ErrStackNameRequired,
		},
		{
			name: "whitespace-only stack name returns ErrStackNameRequired",
			container: intmodel.Container{
				Metadata: intmodel.ContainerMetadata{
					Name: "test-container",
				},
				Spec: intmodel.ContainerSpec{
					RealmName: "test-realm",
					SpaceName: "test-space",
					StackName: "   ",
					CellName:  "test-cell",
					Image:     "test-image",
				},
			},
			wantErr: errdefs.ErrStackNameRequired,
		},
		{
			name: "empty cell name returns ErrCellNameRequired",
			container: intmodel.Container{
				Metadata: intmodel.ContainerMetadata{
					Name: "test-container",
				},
				Spec: intmodel.ContainerSpec{
					RealmName: "test-realm",
					SpaceName: "test-space",
					StackName: "test-stack",
					CellName:  "",
					Image:     "test-image",
				},
			},
			wantErr: errdefs.ErrCellNameRequired,
		},
		{
			name: "whitespace-only cell name returns ErrCellNameRequired",
			container: intmodel.Container{
				Metadata: intmodel.ContainerMetadata{
					Name: "test-container",
				},
				Spec: intmodel.ContainerSpec{
					RealmName: "test-realm",
					SpaceName: "test-space",
					StackName: "test-stack",
					CellName:  "   ",
					Image:     "test-image",
				},
			},
			wantErr: errdefs.ErrCellNameRequired,
		},
		{
			name: "empty image returns ErrInvalidImage",
			container: intmodel.Container{
				Metadata: intmodel.ContainerMetadata{
					Name: "test-container",
				},
				Spec: intmodel.ContainerSpec{
					RealmName: "test-realm",
					SpaceName: "test-space",
					StackName: "test-stack",
					CellName:  "test-cell",
					Image:     "",
				},
			},
			wantErr: errdefs.ErrInvalidImage,
		},
		{
			name: "whitespace-only image returns ErrInvalidImage",
			container: intmodel.Container{
				Metadata: intmodel.ContainerMetadata{
					Name: "test-container",
				},
				Spec: intmodel.ContainerSpec{
					RealmName: "test-realm",
					SpaceName: "test-space",
					StackName: "test-stack",
					CellName:  "test-cell",
					Image:     "   ",
				},
			},
			wantErr: errdefs.ErrInvalidImage,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			ctrl := setupTestController(t, mockRunner)

			_, err := ctrl.CreateContainer(tt.container)

			if err == nil {
				t.Fatal("expected error but got none")
			}

			if !errors.Is(err, tt.wantErr) {
				t.Errorf("expected error %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestCreateContainer_DefaultLabels(t *testing.T) {
	tests := []struct {
		name        string
		container   intmodel.Container
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.CreateContainerResult)
	}{
		{
			name: "labels map is created if nil",
			container: intmodel.Container{
				Metadata: intmodel.ContainerMetadata{
					Name:   "test-container",
					Labels: nil,
				},
				Spec: intmodel.ContainerSpec{
					RealmName: "test-realm",
					SpaceName: "test-space",
					StackName: "test-stack",
					CellName:  "test-cell",
					Image:     "test-image",
				},
			},
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				postStateCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				postStateCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "test-container", Image: "test-image"},
				}
				getCellCallCount := 0
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					getCellCallCount++
					if getCellCallCount == 1 {
						return existingCell, nil
					}
					return postStateCell, nil
				}
				resultCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				resultCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "test-container", Image: "test-image"},
				}
				f.CreateContainerFn = func(_ intmodel.Cell, _ intmodel.ContainerSpec) (intmodel.Cell, error) {
					return resultCell, nil
				}
				f.StartContainerFn = func(cell intmodel.Cell, _ string) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateContainerResult) {
				if result.Container.Metadata.Labels == nil {
					t.Error("expected labels map to be created")
				}
			},
		},
		{
			name: "existing labels are preserved",
			container: intmodel.Container{
				Metadata: intmodel.ContainerMetadata{
					Name: "test-container",
					Labels: map[string]string{
						"custom-label": "custom-value",
					},
				},
				Spec: intmodel.ContainerSpec{
					RealmName: "test-realm",
					SpaceName: "test-space",
					StackName: "test-stack",
					CellName:  "test-cell",
					Image:     "test-image",
				},
			},
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				postStateCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				postStateCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "test-container", Image: "test-image"},
				}
				getCellCallCount := 0
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					getCellCallCount++
					if getCellCallCount == 1 {
						return existingCell, nil
					}
					return postStateCell, nil
				}
				resultCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				resultCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "test-container", Image: "test-image"},
				}
				f.CreateContainerFn = func(_ intmodel.Cell, _ intmodel.ContainerSpec) (intmodel.Cell, error) {
					return resultCell, nil
				}
				f.StartContainerFn = func(cell intmodel.Cell, _ string) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateContainerResult) {
				if result.Container.Metadata.Labels["custom-label"] != "custom-value" {
					t.Error("expected existing labels to be preserved")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner)
			}

			ctrl := setupTestController(t, mockRunner)

			result, err := ctrl.CreateContainer(tt.container)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantResult != nil {
				tt.wantResult(t, result)
			}
		})
	}
}

func TestCreateContainer_CellNotFound(t *testing.T) {
	tests := []struct {
		name        string
		container   intmodel.Container
		setupRunner func(*fakeRunner)
		wantErr     bool
		errContains string
	}{
		{
			name: "cell not found returns descriptive error",
			container: buildTestContainer(
				"test-container",
				"test-realm",
				"test-space",
				"test-stack",
				"test-cell",
				"test-image",
			),
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

			_, err := ctrl.CreateContainer(tt.container)

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

func TestCreateContainer_RunnerErrors(t *testing.T) {
	tests := []struct {
		name        string
		container   intmodel.Container
		setupRunner func(*fakeRunner)
		wantErr     error
		errContains string
	}{
		{
			name: "GetCell error (non-NotFound) is wrapped with ErrGetCell",
			container: buildTestContainer(
				"test-container",
				"test-realm",
				"test-space",
				"test-stack",
				"test-cell",
				"test-image",
			),
			setupRunner: func(f *fakeRunner) {
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return intmodel.Cell{}, errors.New("unexpected error")
				}
			},
			wantErr:     errdefs.ErrGetCell,
			errContains: "unexpected error",
		},
		{
			name: "CreateContainer error is wrapped with ErrCreateCell",
			container: buildTestContainer(
				"test-container",
				"test-realm",
				"test-space",
				"test-stack",
				"test-cell",
				"test-image",
			),
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.CreateContainerFn = func(_ intmodel.Cell, _ intmodel.ContainerSpec) (intmodel.Cell, error) {
					return intmodel.Cell{}, errors.New("creation failed")
				}
			},
			wantErr:     errdefs.ErrCreateCell,
			errContains: "creation failed",
		},
		{
			name: "StartContainer error is wrapped with descriptive message",
			container: buildTestContainer(
				"test-container",
				"test-realm",
				"test-space",
				"test-stack",
				"test-cell",
				"test-image",
			),
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				resultCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				resultCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "test-container", Image: "test-image"},
				}
				f.CreateContainerFn = func(_ intmodel.Cell, _ intmodel.ContainerSpec) (intmodel.Cell, error) {
					return resultCell, nil
				}
				f.StartContainerFn = func(_ intmodel.Cell, _ string) (intmodel.Cell, error) {
					return intmodel.Cell{}, errors.New("start failed")
				}
			},
			wantErr:     nil, // Custom error message, not a standard error
			errContains: "failed to start container test-container",
		},
		{
			name: "GetCell post-creation error is wrapped with ErrGetCell",
			container: buildTestContainer(
				"test-container",
				"test-realm",
				"test-space",
				"test-stack",
				"test-cell",
				"test-image",
			),
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				getCellCallCount := 0
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					getCellCallCount++
					if getCellCallCount == 1 {
						// First call (pre-state) succeeds
						return existingCell, nil
					}
					// Second call (post-state) fails
					return intmodel.Cell{}, errors.New("post-state check failed")
				}
				resultCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				resultCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "test-container", Image: "test-image"},
				}
				f.CreateContainerFn = func(_ intmodel.Cell, _ intmodel.ContainerSpec) (intmodel.Cell, error) {
					return resultCell, nil
				}
				f.StartContainerFn = func(cell intmodel.Cell, _ string) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
			},
			wantErr:     errdefs.ErrGetCell,
			errContains: "post-state check failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner)
			}

			ctrl := setupTestController(t, mockRunner)

			_, err := ctrl.CreateContainer(tt.container)

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

func TestCreateContainer_NameTrimming(t *testing.T) {
	tests := []struct {
		name        string
		container   intmodel.Container
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.CreateContainerResult)
		wantErr     bool
		errContains string
	}{
		{
			name: "all names and image with leading/trailing whitespace are trimmed",
			container: intmodel.Container{
				Metadata: intmodel.ContainerMetadata{
					Name: "  test-container  ",
				},
				Spec: intmodel.ContainerSpec{
					RealmName: "  test-realm  ",
					SpaceName: "  test-space  ",
					StackName: "  test-stack  ",
					CellName:  "  test-cell  ",
					Image:     "  test-image  ",
				},
			},
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				postStateCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				postStateCell.Spec.Containers = []intmodel.ContainerSpec{
					{
						ID:        "test-container",
						RealmName: "test-realm",
						SpaceName: "test-space",
						StackName: "test-stack",
						CellName:  "test-cell",
						Image:     "test-image",
					},
				}
				getCellCallCount := 0
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					getCellCallCount++
					if getCellCallCount == 1 {
						return existingCell, nil
					}
					return postStateCell, nil
				}
				resultCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				resultCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "test-container", Image: "test-image"},
				}
				f.CreateContainerFn = func(_ intmodel.Cell, spec intmodel.ContainerSpec) (intmodel.Cell, error) {
					// Verify trimmed values
					if spec.RealmName != "test-realm" {
						return intmodel.Cell{}, errors.New("realm name not trimmed")
					}
					if spec.SpaceName != "test-space" {
						return intmodel.Cell{}, errors.New("space name not trimmed")
					}
					if spec.StackName != "test-stack" {
						return intmodel.Cell{}, errors.New("stack name not trimmed")
					}
					if spec.CellName != "test-cell" {
						return intmodel.Cell{}, errors.New("cell name not trimmed")
					}
					if spec.Image != "test-image" {
						return intmodel.Cell{}, errors.New("image not trimmed")
					}
					return resultCell, nil
				}
				f.StartContainerFn = func(cell intmodel.Cell, containerID string) (intmodel.Cell, error) {
					if containerID != "test-container" {
						return intmodel.Cell{}, errors.New("container ID not trimmed")
					}
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateContainerResult) {
				if result.Container.Metadata.Name != "test-container" {
					t.Errorf(
						"expected container name to be trimmed to 'test-container', got %q",
						result.Container.Metadata.Name,
					)
				}
				if result.Container.Spec.RealmName != "test-realm" {
					t.Errorf(
						"expected realm name to be trimmed to 'test-realm', got %q",
						result.Container.Spec.RealmName,
					)
				}
				if result.Container.Spec.SpaceName != "test-space" {
					t.Errorf(
						"expected space name to be trimmed to 'test-space', got %q",
						result.Container.Spec.SpaceName,
					)
				}
				if result.Container.Spec.StackName != "test-stack" {
					t.Errorf(
						"expected stack name to be trimmed to 'test-stack', got %q",
						result.Container.Spec.StackName,
					)
				}
				if result.Container.Spec.CellName != "test-cell" {
					t.Errorf("expected cell name to be trimmed to 'test-cell', got %q", result.Container.Spec.CellName)
				}
				if result.Container.Spec.Image != "test-image" {
					t.Errorf("expected image to be trimmed to 'test-image', got %q", result.Container.Spec.Image)
				}
			},
			wantErr: false,
		},
		{
			name: "container name that becomes empty after trimming triggers validation error",
			container: intmodel.Container{
				Metadata: intmodel.ContainerMetadata{
					Name: "   ",
				},
				Spec: intmodel.ContainerSpec{
					ID:        "   ",
					RealmName: "test-realm",
					SpaceName: "test-space",
					StackName: "test-stack",
					CellName:  "test-cell",
					Image:     "test-image",
				},
			},
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

			result, err := ctrl.CreateContainer(tt.container)

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

func TestCreateContainer_ContainerObjectConstruction(t *testing.T) {
	tests := []struct {
		name        string
		container   intmodel.Container
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.CreateContainerResult)
		wantErr     bool
	}{
		{
			name: "container object is constructed from container spec found in post-state cell",
			container: buildTestContainer(
				"test-container",
				"test-realm",
				"test-space",
				"test-stack",
				"test-cell",
				"test-image",
			),
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				postStateCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				postStateCell.Spec.Containers = []intmodel.ContainerSpec{
					{
						ID:        "test-container",
						RealmName: "test-realm",
						SpaceName: "test-space",
						StackName: "test-stack",
						CellName:  "test-cell",
						Image:     "test-image",
						Command:   "cmd",
						Args:      []string{"arg1"},
					},
				}
				getCellCallCount := 0
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					getCellCallCount++
					if getCellCallCount == 1 {
						return existingCell, nil
					}
					return postStateCell, nil
				}
				resultCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				resultCell.Spec.Containers = []intmodel.ContainerSpec{
					{
						ID:        "test-container",
						RealmName: "test-realm",
						SpaceName: "test-space",
						StackName: "test-stack",
						CellName:  "test-cell",
						Image:     "test-image",
						Command:   "cmd",
						Args:      []string{"arg1"},
					},
				}
				f.CreateContainerFn = func(_ intmodel.Cell, _ intmodel.ContainerSpec) (intmodel.Cell, error) {
					return resultCell, nil
				}
				f.StartContainerFn = func(cell intmodel.Cell, _ string) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateContainerResult) {
				// Verify Container object was constructed correctly
				if result.Container.Metadata.Name != "test-container" {
					t.Errorf("expected container name to be 'test-container', got %q", result.Container.Metadata.Name)
				}
				if result.Container.Spec.ID != "test-container" {
					t.Errorf("expected container Spec.ID to be 'test-container', got %q", result.Container.Spec.ID)
				}
				if result.Container.Spec.Image != "test-image" {
					t.Errorf("expected container image to be 'test-image', got %q", result.Container.Spec.Image)
				}
				if result.Container.Spec.Command != "cmd" {
					t.Errorf("expected container command to be 'cmd', got %q", result.Container.Spec.Command)
				}
				if len(result.Container.Spec.Args) != 1 || result.Container.Spec.Args[0] != "arg1" {
					t.Errorf("expected container args to be ['arg1'], got %v", result.Container.Spec.Args)
				}
				if result.Container.Status.State != intmodel.ContainerStateReady {
					t.Errorf(
						"expected container state to be ContainerStateReady, got %v",
						result.Container.Status.State,
					)
				}
			},
			wantErr: false,
		},
		{
			name: "container is found correctly even if cell has multiple containers",
			container: buildTestContainer(
				"target-container",
				"test-realm",
				"test-space",
				"test-stack",
				"test-cell",
				"test-image",
			),
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				existingCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "other-container-1", Image: "image1"},
					{ID: "other-container-2", Image: "image2"},
				}
				postStateCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				postStateCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "other-container-1", Image: "image1"},
					{ID: "other-container-2", Image: "image2"},
					{
						ID:        "target-container",
						RealmName: "test-realm",
						SpaceName: "test-space",
						StackName: "test-stack",
						CellName:  "test-cell",
						Image:     "test-image",
					},
				}
				getCellCallCount := 0
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					getCellCallCount++
					if getCellCallCount == 1 {
						return existingCell, nil
					}
					return postStateCell, nil
				}
				resultCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				resultCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "other-container-1", Image: "image1"},
					{ID: "other-container-2", Image: "image2"},
					{
						ID:        "target-container",
						RealmName: "test-realm",
						SpaceName: "test-space",
						StackName: "test-stack",
						CellName:  "test-cell",
						Image:     "test-image",
					},
				}
				f.CreateContainerFn = func(_ intmodel.Cell, _ intmodel.ContainerSpec) (intmodel.Cell, error) {
					return resultCell, nil
				}
				f.StartContainerFn = func(cell intmodel.Cell, containerID string) (intmodel.Cell, error) {
					if containerID != "target-container" {
						return intmodel.Cell{}, errors.New("unexpected container ID")
					}
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateContainerResult) {
				// Verify correct container was found and constructed
				if result.Container.Metadata.Name != "target-container" {
					t.Errorf("expected container name to be 'target-container', got %q", result.Container.Metadata.Name)
				}
				if result.Container.Spec.Image != "test-image" {
					t.Errorf("expected container image to be 'test-image', got %q", result.Container.Spec.Image)
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

			result, err := ctrl.CreateContainer(tt.container)

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
