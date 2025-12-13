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

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

func TestCreateCell_NewCellCreation(t *testing.T) {
	tests := []struct {
		name        string
		cellName    string
		realmName   string
		spaceName   string
		stackName   string
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.CreateCellResult)
		wantErr     bool
	}{
		{
			name:      "successful creation of new cell",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return intmodel.Cell{}, errdefs.ErrCellNotFound
				}
				f.CreateCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return buildTestCell("test-cell", "test-realm", "test-space", "test-stack"), nil
				}
				f.StartCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateCellResult) {
				if result.MetadataExistsPre {
					t.Error("expected MetadataExistsPre to be false")
				}
				if !result.MetadataExistsPost {
					t.Error("expected MetadataExistsPost to be true")
				}
				if !result.Created {
					t.Error("expected Created to be true")
				}
				if !result.CgroupCreated {
					t.Error("expected CgroupCreated to be true")
				}
				if !result.RootContainerCreated {
					t.Error("expected RootContainerCreated to be true")
				}
				if !result.Started {
					t.Error("expected Started to be true")
				}
				if !result.StartedPost {
					t.Error("expected StartedPost to be true")
				}
				if !result.CgroupExistsPost {
					t.Error("expected CgroupExistsPost to be true")
				}
				if !result.RootContainerExistsPost {
					t.Error("expected RootContainerExistsPost to be true")
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

			result, err := ctrl.CreateCell(cell)

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

func TestCreateCell_ExistingCellReconciliation(t *testing.T) {
	tests := []struct {
		name        string
		cellName    string
		realmName   string
		spaceName   string
		stackName   string
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.CreateCellResult)
		wantErr     bool
	}{
		{
			name:      "existing cell - resources don't exist, all created and started",
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
					return false, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return false, nil
				}
				f.EnsureCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					return cell, nil
				}
				f.StartCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateCellResult) {
				if !result.MetadataExistsPre {
					t.Error("expected MetadataExistsPre to be true")
				}
				if !result.MetadataExistsPost {
					t.Error("expected MetadataExistsPost to be true")
				}
				if result.Created {
					t.Error("expected Created to be false")
				}
				if result.CgroupExistsPre {
					t.Error("expected CgroupExistsPre to be false")
				}
				if result.RootContainerExistsPre {
					t.Error("expected RootContainerExistsPre to be false")
				}
				if !result.CgroupCreated {
					t.Error("expected CgroupCreated to be true")
				}
				if !result.RootContainerCreated {
					t.Error("expected RootContainerCreated to be true")
				}
				if !result.Started {
					t.Error("expected Started to be true")
				}
			},
			wantErr: false,
		},
		{
			name:      "existing cell - all resources exist, started",
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
				f.EnsureCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					return cell, nil
				}
				f.StartCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateCellResult) {
				if !result.MetadataExistsPre {
					t.Error("expected MetadataExistsPre to be true")
				}
				if result.Created {
					t.Error("expected Created to be false")
				}
				if !result.CgroupExistsPre {
					t.Error("expected CgroupExistsPre to be true")
				}
				if !result.RootContainerExistsPre {
					t.Error("expected RootContainerExistsPre to be true")
				}
				if result.CgroupCreated {
					t.Error("expected CgroupCreated to be false")
				}
				if result.RootContainerCreated {
					t.Error("expected RootContainerCreated to be false")
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
			cell := buildTestCell(tt.cellName, tt.realmName, tt.spaceName, tt.stackName)

			result, err := ctrl.CreateCell(cell)

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

func TestCreateCell_ContainerOutcomes(t *testing.T) {
	tests := []struct {
		name        string
		cell        intmodel.Cell
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.CreateCellResult)
		wantErr     bool
	}{
		{
			name: "new cell with multiple containers - all containers marked as created",
			cell: intmodel.Cell{
				Metadata: intmodel.CellMetadata{
					Name: "test-cell",
				},
				Spec: intmodel.CellSpec{
					RealmName: "test-realm",
					SpaceName: "test-space",
					StackName: "test-stack",
					Containers: []intmodel.ContainerSpec{
						{ID: "container1", Image: "image1"},
						{ID: "container2", Image: "image2"},
					},
				},
			},
			setupRunner: func(f *fakeRunner) {
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return intmodel.Cell{}, errdefs.ErrCellNotFound
				}
				createdCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				createdCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "container1", Image: "image1"},
					{ID: "container2", Image: "image2"},
				}
				f.CreateCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return createdCell, nil
				}
				f.StartCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateCellResult) {
				if len(result.Containers) != 2 {
					t.Errorf("expected 2 container outcomes, got %d", len(result.Containers))
					return
				}
				for _, container := range result.Containers {
					if container.ExistsPre {
						t.Errorf("expected container %q ExistsPre to be false", container.Name)
					}
					if !container.ExistsPost {
						t.Errorf("expected container %q ExistsPost to be true", container.Name)
					}
					if !container.Created {
						t.Errorf("expected container %q Created to be true", container.Name)
					}
				}
			},
			wantErr: false,
		},
		{
			name: "existing cell with existing containers - containers marked as existing",
			cell: intmodel.Cell{
				Metadata: intmodel.CellMetadata{
					Name: "test-cell",
				},
				Spec: intmodel.CellSpec{
					RealmName: "test-realm",
					SpaceName: "test-space",
					StackName: "test-stack",
					Containers: []intmodel.ContainerSpec{
						{ID: "container1", Image: "image1"},
						{ID: "container2", Image: "image2"},
					},
				},
			},
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				existingCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "container1", Image: "image1"},
					{ID: "container2", Image: "image2"},
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
				f.EnsureCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					return cell, nil
				}
				f.StartCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateCellResult) {
				if len(result.Containers) != 2 {
					t.Errorf("expected 2 container outcomes, got %d", len(result.Containers))
					return
				}
				for _, container := range result.Containers {
					if !container.ExistsPre {
						t.Errorf("expected container %q ExistsPre to be true", container.Name)
					}
					if !container.ExistsPost {
						t.Errorf("expected container %q ExistsPost to be true", container.Name)
					}
					if container.Created {
						t.Errorf("expected container %q Created to be false", container.Name)
					}
				}
			},
			wantErr: false,
		},
		{
			name: "existing cell with new containers added - mixed outcomes",
			cell: intmodel.Cell{
				Metadata: intmodel.CellMetadata{
					Name: "test-cell",
				},
				Spec: intmodel.CellSpec{
					RealmName: "test-realm",
					SpaceName: "test-space",
					StackName: "test-stack",
					Containers: []intmodel.ContainerSpec{
						{ID: "container1", Image: "image1"}, // existing
						{ID: "container2", Image: "image2"}, // existing
						{ID: "container3", Image: "image3"}, // new
					},
				},
			},
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				existingCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "container1", Image: "image1"},
					{ID: "container2", Image: "image2"},
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
				updatedCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				updatedCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "container1", Image: "image1"},
					{ID: "container2", Image: "image2"},
					{ID: "container3", Image: "image3"},
				}
				f.EnsureCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return updatedCell, nil
				}
				f.StartCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateCellResult) {
				if len(result.Containers) != 3 {
					t.Errorf("expected 3 container outcomes, got %d", len(result.Containers))
					return
				}
				// Find container outcomes by name
				containerMap := make(map[string]controller.ContainerCreationOutcome)
				for _, container := range result.Containers {
					containerMap[container.Name] = container
				}

				// container1 should be existing
				if container1, ok := containerMap["container1"]; ok {
					if !container1.ExistsPre || !container1.ExistsPost || container1.Created {
						t.Error("expected container1 to be existing (not created)")
					}
				} else {
					t.Error("expected container1 in outcomes")
				}

				// container2 should be existing
				if container2, ok := containerMap["container2"]; ok {
					if !container2.ExistsPre || !container2.ExistsPost || container2.Created {
						t.Error("expected container2 to be existing (not created)")
					}
				} else {
					t.Error("expected container2 in outcomes")
				}

				// container3 should be created
				if container3, ok := containerMap["container3"]; ok {
					if container3.ExistsPre || !container3.ExistsPost || !container3.Created {
						t.Error("expected container3 to be created")
					}
				} else {
					t.Error("expected container3 in outcomes")
				}
			},
			wantErr: false,
		},
		{
			name: "empty container ID is skipped in outcomes",
			cell: intmodel.Cell{
				Metadata: intmodel.CellMetadata{
					Name: "test-cell",
				},
				Spec: intmodel.CellSpec{
					RealmName: "test-realm",
					SpaceName: "test-space",
					StackName: "test-stack",
					Containers: []intmodel.ContainerSpec{
						{ID: "container1", Image: "image1"},
						{ID: "", Image: "image2"}, // empty ID should be skipped
						{ID: "container3", Image: "image3"},
					},
				},
			},
			setupRunner: func(f *fakeRunner) {
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return intmodel.Cell{}, errdefs.ErrCellNotFound
				}
				createdCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				createdCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "container1", Image: "image1"},
					{ID: "", Image: "image2"},
					{ID: "container3", Image: "image3"},
				}
				f.CreateCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return createdCell, nil
				}
				f.StartCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateCellResult) {
				// Only container1 and container3 should be in outcomes (empty ID skipped)
				if len(result.Containers) != 2 {
					t.Errorf("expected 2 container outcomes (empty ID skipped), got %d", len(result.Containers))
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

			result, err := ctrl.CreateCell(tt.cell)

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

func TestCreateCell_ValidationErrors(t *testing.T) {
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

			_, err := ctrl.CreateCell(cell)

			if err == nil {
				t.Fatal("expected error but got none")
			}

			if !errors.Is(err, tt.wantErr) {
				t.Errorf("expected error %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestCreateCell_DefaultLabels(t *testing.T) {
	tests := []struct {
		name        string
		cell        intmodel.Cell
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.CreateCellResult)
	}{
		{
			name: "labels map is created if nil",
			cell: intmodel.Cell{
				Metadata: intmodel.CellMetadata{
					Name:   "test-cell",
					Labels: nil,
				},
				Spec: intmodel.CellSpec{
					RealmName: "test-realm",
					SpaceName: "test-space",
					StackName: "test-stack",
				},
			},
			setupRunner: func(f *fakeRunner) {
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return intmodel.Cell{}, errdefs.ErrCellNotFound
				}
				f.CreateCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					// Verify labels map was created
					if cell.Metadata.Labels == nil {
						return intmodel.Cell{}, errors.New("labels map not created")
					}
					return cell, nil
				}
				f.StartCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateCellResult) {
				if result.Cell.Metadata.Labels == nil {
					t.Error("expected labels map to be created")
				}
				if result.Cell.Metadata.Labels[consts.KukeonRealmLabelKey] != "test-realm" {
					t.Errorf(
						"expected realm label to be 'test-realm', got %q",
						result.Cell.Metadata.Labels[consts.KukeonRealmLabelKey],
					)
				}
				if result.Cell.Metadata.Labels[consts.KukeonSpaceLabelKey] != "test-space" {
					t.Errorf(
						"expected space label to be 'test-space', got %q",
						result.Cell.Metadata.Labels[consts.KukeonSpaceLabelKey],
					)
				}
				if result.Cell.Metadata.Labels[consts.KukeonStackLabelKey] != "test-stack" {
					t.Errorf(
						"expected stack label to be 'test-stack', got %q",
						result.Cell.Metadata.Labels[consts.KukeonStackLabelKey],
					)
				}
				if result.Cell.Metadata.Labels[consts.KukeonCellLabelKey] != "test-cell" {
					t.Errorf(
						"expected cell label to be 'test-cell', got %q",
						result.Cell.Metadata.Labels[consts.KukeonCellLabelKey],
					)
				}
			},
		},
		{
			name: "all four labels are set if missing",
			cell: intmodel.Cell{
				Metadata: intmodel.CellMetadata{
					Name: "test-cell",
					Labels: map[string]string{
						"custom-label": "custom-value",
					},
				},
				Spec: intmodel.CellSpec{
					RealmName: "test-realm",
					SpaceName: "test-space",
					StackName: "test-stack",
				},
			},
			setupRunner: func(f *fakeRunner) {
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return intmodel.Cell{}, errdefs.ErrCellNotFound
				}
				f.CreateCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					// Verify all four default labels were added
					if cell.Metadata.Labels[consts.KukeonRealmLabelKey] != "test-realm" {
						return intmodel.Cell{}, errors.New("realm label not set")
					}
					if cell.Metadata.Labels[consts.KukeonSpaceLabelKey] != "test-space" {
						return intmodel.Cell{}, errors.New("space label not set")
					}
					if cell.Metadata.Labels[consts.KukeonStackLabelKey] != "test-stack" {
						return intmodel.Cell{}, errors.New("stack label not set")
					}
					if cell.Metadata.Labels[consts.KukeonCellLabelKey] != "test-cell" {
						return intmodel.Cell{}, errors.New("cell label not set")
					}
					// Verify existing labels are preserved
					if cell.Metadata.Labels["custom-label"] != "custom-value" {
						return intmodel.Cell{}, errors.New("existing labels not preserved")
					}
					return cell, nil
				}
				f.StartCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateCellResult) {
				if result.Cell.Metadata.Labels[consts.KukeonRealmLabelKey] != "test-realm" {
					t.Errorf(
						"expected realm label to be 'test-realm', got %q",
						result.Cell.Metadata.Labels[consts.KukeonRealmLabelKey],
					)
				}
				if result.Cell.Metadata.Labels[consts.KukeonSpaceLabelKey] != "test-space" {
					t.Errorf(
						"expected space label to be 'test-space', got %q",
						result.Cell.Metadata.Labels[consts.KukeonSpaceLabelKey],
					)
				}
				if result.Cell.Metadata.Labels[consts.KukeonStackLabelKey] != "test-stack" {
					t.Errorf(
						"expected stack label to be 'test-stack', got %q",
						result.Cell.Metadata.Labels[consts.KukeonStackLabelKey],
					)
				}
				if result.Cell.Metadata.Labels[consts.KukeonCellLabelKey] != "test-cell" {
					t.Errorf(
						"expected cell label to be 'test-cell', got %q",
						result.Cell.Metadata.Labels[consts.KukeonCellLabelKey],
					)
				}
				if result.Cell.Metadata.Labels["custom-label"] != "custom-value" {
					t.Error("expected existing labels to be preserved")
				}
			},
		},
		{
			name: "existing labels are preserved",
			cell: intmodel.Cell{
				Metadata: intmodel.CellMetadata{
					Name: "test-cell",
					Labels: map[string]string{
						consts.KukeonRealmLabelKey: "existing-realm",
						consts.KukeonSpaceLabelKey: "existing-space",
						consts.KukeonStackLabelKey: "existing-stack",
						consts.KukeonCellLabelKey:  "existing-cell",
						"custom-label":             "custom-value",
					},
				},
				Spec: intmodel.CellSpec{
					RealmName: "test-realm",
					SpaceName: "test-space",
					StackName: "test-stack",
				},
			},
			setupRunner: func(f *fakeRunner) {
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return intmodel.Cell{}, errdefs.ErrCellNotFound
				}
				f.CreateCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					// Verify existing labels are preserved (not overwritten)
					if cell.Metadata.Labels[consts.KukeonRealmLabelKey] != "existing-realm" {
						return intmodel.Cell{}, errors.New("existing realm label was overwritten")
					}
					if cell.Metadata.Labels[consts.KukeonSpaceLabelKey] != "existing-space" {
						return intmodel.Cell{}, errors.New("existing space label was overwritten")
					}
					if cell.Metadata.Labels[consts.KukeonStackLabelKey] != "existing-stack" {
						return intmodel.Cell{}, errors.New("existing stack label was overwritten")
					}
					if cell.Metadata.Labels[consts.KukeonCellLabelKey] != "existing-cell" {
						return intmodel.Cell{}, errors.New("existing cell label was overwritten")
					}
					return cell, nil
				}
				f.StartCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateCellResult) {
				if result.Cell.Metadata.Labels[consts.KukeonRealmLabelKey] != "existing-realm" {
					t.Errorf(
						"expected existing realm label to be preserved, got %q",
						result.Cell.Metadata.Labels[consts.KukeonRealmLabelKey],
					)
				}
				if result.Cell.Metadata.Labels[consts.KukeonSpaceLabelKey] != "existing-space" {
					t.Errorf(
						"expected existing space label to be preserved, got %q",
						result.Cell.Metadata.Labels[consts.KukeonSpaceLabelKey],
					)
				}
				if result.Cell.Metadata.Labels[consts.KukeonStackLabelKey] != "existing-stack" {
					t.Errorf(
						"expected existing stack label to be preserved, got %q",
						result.Cell.Metadata.Labels[consts.KukeonStackLabelKey],
					)
				}
				if result.Cell.Metadata.Labels[consts.KukeonCellLabelKey] != "existing-cell" {
					t.Errorf(
						"expected existing cell label to be preserved, got %q",
						result.Cell.Metadata.Labels[consts.KukeonCellLabelKey],
					)
				}
				if result.Cell.Metadata.Labels["custom-label"] != "custom-value" {
					t.Error("expected custom labels to be preserved")
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

			result, err := ctrl.CreateCell(tt.cell)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantResult != nil {
				tt.wantResult(t, result)
			}
		})
	}
}

func TestCreateCell_DefaultSpecID(t *testing.T) {
	tests := []struct {
		name        string
		cell        intmodel.Cell
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.CreateCellResult)
	}{
		{
			name: "empty Spec.ID defaults to cell name",
			cell: intmodel.Cell{
				Metadata: intmodel.CellMetadata{
					Name: "test-cell",
				},
				Spec: intmodel.CellSpec{
					ID:        "",
					RealmName: "test-realm",
					SpaceName: "test-space",
					StackName: "test-stack",
				},
			},
			setupRunner: func(f *fakeRunner) {
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return intmodel.Cell{}, errdefs.ErrCellNotFound
				}
				f.CreateCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					// Verify Spec.ID was set to cell name
					if cell.Spec.ID != "test-cell" {
						return intmodel.Cell{}, errors.New("Spec.ID not set to cell name")
					}
					return cell, nil
				}
				f.StartCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateCellResult) {
				if result.Cell.Spec.ID != "test-cell" {
					t.Errorf("expected Spec.ID to be 'test-cell', got %q", result.Cell.Spec.ID)
				}
			},
		},
		{
			name: "non-empty Spec.ID is preserved",
			cell: intmodel.Cell{
				Metadata: intmodel.CellMetadata{
					Name: "test-cell",
				},
				Spec: intmodel.CellSpec{
					ID:        "custom-id",
					RealmName: "test-realm",
					SpaceName: "test-space",
					StackName: "test-stack",
				},
			},
			setupRunner: func(f *fakeRunner) {
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return intmodel.Cell{}, errdefs.ErrCellNotFound
				}
				f.CreateCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					// Verify Spec.ID was preserved
					if cell.Spec.ID != "custom-id" {
						return intmodel.Cell{}, errors.New("Spec.ID was overwritten")
					}
					return cell, nil
				}
				f.StartCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateCellResult) {
				if result.Cell.Spec.ID != "custom-id" {
					t.Errorf("expected Spec.ID to be 'custom-id', got %q", result.Cell.Spec.ID)
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

			result, err := ctrl.CreateCell(tt.cell)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantResult != nil {
				tt.wantResult(t, result)
			}
		})
	}
}

func TestCreateCell_ContainerOwnership(t *testing.T) {
	tests := []struct {
		name        string
		cell        intmodel.Cell
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.CreateCellResult)
	}{
		{
			name: "container ownership fields are set on all containers",
			cell: intmodel.Cell{
				Metadata: intmodel.CellMetadata{
					Name: "test-cell",
				},
				Spec: intmodel.CellSpec{
					RealmName: "test-realm",
					SpaceName: "test-space",
					StackName: "test-stack",
					Containers: []intmodel.ContainerSpec{
						{ID: "container1", Image: "image1"},
						{ID: "container2", Image: "image2"},
					},
				},
			},
			setupRunner: func(f *fakeRunner) {
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return intmodel.Cell{}, errdefs.ErrCellNotFound
				}
				f.CreateCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					// Verify container ownership fields were set
					for _, container := range cell.Spec.Containers {
						if container.RealmName != "test-realm" {
							return intmodel.Cell{}, errors.New("container RealmName not set")
						}
						if container.SpaceName != "test-space" {
							return intmodel.Cell{}, errors.New("container SpaceName not set")
						}
						if container.StackName != "test-stack" {
							return intmodel.Cell{}, errors.New("container StackName not set")
						}
						if container.CellName != "test-cell" {
							return intmodel.Cell{}, errors.New("container CellName not set")
						}
					}
					return cell, nil
				}
				f.StartCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateCellResult) {
				// Verify ownership was set in result
				for _, container := range result.Cell.Spec.Containers {
					if container.RealmName != "test-realm" {
						t.Errorf("expected container RealmName to be 'test-realm', got %q", container.RealmName)
					}
					if container.SpaceName != "test-space" {
						t.Errorf("expected container SpaceName to be 'test-space', got %q", container.SpaceName)
					}
					if container.StackName != "test-stack" {
						t.Errorf("expected container StackName to be 'test-stack', got %q", container.StackName)
					}
					if container.CellName != "test-cell" {
						t.Errorf("expected container CellName to be 'test-cell', got %q", container.CellName)
					}
				}
			},
		},
		{
			name: "empty containers slice is handled correctly",
			cell: intmodel.Cell{
				Metadata: intmodel.CellMetadata{
					Name: "test-cell",
				},
				Spec: intmodel.CellSpec{
					RealmName:  "test-realm",
					SpaceName:  "test-space",
					StackName:  "test-stack",
					Containers: []intmodel.ContainerSpec{},
				},
			},
			setupRunner: func(f *fakeRunner) {
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return intmodel.Cell{}, errdefs.ErrCellNotFound
				}
				f.CreateCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					return cell, nil
				}
				f.StartCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateCellResult) {
				if len(result.Containers) != 0 {
					t.Errorf("expected 0 container outcomes, got %d", len(result.Containers))
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

			result, err := ctrl.CreateCell(tt.cell)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantResult != nil {
				tt.wantResult(t, result)
			}
		})
	}
}

func TestCreateCell_RunnerErrors(t *testing.T) {
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
			name:      "GetCell error (non-NotFound) is wrapped with ErrGetCell",
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
			name:      "CreateCell error is wrapped with ErrCreateCell",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return intmodel.Cell{}, errdefs.ErrCellNotFound
				}
				f.CreateCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return intmodel.Cell{}, errors.New("creation failed")
				}
			},
			wantErr:     errdefs.ErrCreateCell,
			errContains: "creation failed",
		},
		{
			name:      "EnsureCell error is wrapped with ErrCreateCell",
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
					return false, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return false, nil
				}
				f.EnsureCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return intmodel.Cell{}, errors.New("ensure failed")
				}
			},
			wantErr:     errdefs.ErrCreateCell,
			errContains: "ensure failed",
		},
		{
			name:      "ExistsCgroup error is wrapped with descriptive message",
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
			wantErr:     nil, // Custom error message, not a standard error
			errContains: "failed to check if cell cgroup exists",
		},
		{
			name:      "ExistsCellRootContainer error is wrapped with descriptive message",
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
					return false, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return false, errors.New("root container check failed")
				}
			},
			wantErr:     nil, // Custom error message, not a standard error
			errContains: "failed to check root container",
		},
		{
			name:      "StartCell error is wrapped with descriptive message",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			setupRunner: func(f *fakeRunner) {
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return intmodel.Cell{}, errdefs.ErrCellNotFound
				}
				f.CreateCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return buildTestCell("test-cell", "test-realm", "test-space", "test-stack"), nil
				}
				f.StartCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return intmodel.Cell{}, errors.New("start failed")
				}
			},
			wantErr:     nil, // Custom error message, not a standard error
			errContains: "failed to start cell containers",
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

			_, err := ctrl.CreateCell(cell)

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

func TestCreateCell_NameTrimming(t *testing.T) {
	tests := []struct {
		name        string
		cellName    string
		realmName   string
		spaceName   string
		stackName   string
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.CreateCellResult)
		wantErr     bool
		errContains string
	}{
		{
			name:      "cell name, realm name, space name, and stack name with leading/trailing whitespace are trimmed",
			cellName:  "  test-cell  ",
			realmName: "  test-realm  ",
			spaceName: "  test-space  ",
			stackName: "  test-stack  ",
			setupRunner: func(f *fakeRunner) {
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return intmodel.Cell{}, errdefs.ErrCellNotFound
				}
				f.CreateCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					// Return cell with trimmed values as the result
					return buildTestCell("test-cell", "test-realm", "test-space", "test-stack"), nil
				}
				f.StartCellFn = func(cell intmodel.Cell) (intmodel.Cell, error) {
					cell.Status.State = intmodel.CellStateReady
					return cell, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateCellResult) {
				// The result cell should have trimmed values
				if result.Cell.Metadata.Name != "test-cell" {
					t.Errorf(
						"expected cell name to be trimmed to 'test-cell', got %q",
						result.Cell.Metadata.Name,
					)
				}
				if result.Cell.Spec.RealmName != "test-realm" {
					t.Errorf(
						"expected realm name to be trimmed to 'test-realm', got %q",
						result.Cell.Spec.RealmName,
					)
				}
				if result.Cell.Spec.SpaceName != "test-space" {
					t.Errorf(
						"expected space name to be trimmed to 'test-space', got %q",
						result.Cell.Spec.SpaceName,
					)
				}
				if result.Cell.Spec.StackName != "test-stack" {
					t.Errorf(
						"expected stack name to be trimmed to 'test-stack', got %q",
						result.Cell.Spec.StackName,
					)
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

			result, err := ctrl.CreateCell(cell)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got none")
				}
				if !errors.Is(err, errdefs.ErrCellNameRequired) &&
					!errors.Is(err, errdefs.ErrRealmNameRequired) &&
					!errors.Is(err, errdefs.ErrSpaceNameRequired) &&
					!errors.Is(err, errdefs.ErrStackNameRequired) {
					t.Errorf("expected validation error, got %v", err)
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
