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

func TestPurgeCell_SuccessfulPurge(t *testing.T) {
	tests := []struct {
		name        string
		cellName    string
		realmName   string
		spaceName   string
		stackName   string
		force       bool
		cascade     bool
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.PurgeCellResult)
		wantErr     bool
	}{
		{
			name:      "successful purge, no containers, no force, no cascade",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			force:     false,
			cascade:   false,
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				// Mock GetCell (via controller.GetCell which calls runner methods)
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
				}
				// Mock deleteCellInternal (calls runner.DeleteCell)
				f.DeleteCellFn = func(_ intmodel.Cell) error {
					return nil
				}
				// Mock comprehensive purge
				f.PurgeCellFn = func(_ intmodel.Cell) error {
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.PurgeCellResult) {
				if result.ContainersDeleted {
					t.Error("expected ContainersDeleted to be false")
				}
				if !result.CgroupDeleted {
					t.Error("expected CgroupDeleted to be true")
				}
				if !result.MetadataDeleted {
					t.Error("expected MetadataDeleted to be true")
				}
				if !result.PurgeSucceeded {
					t.Error("expected PurgeSucceeded to be true")
				}
				deletedMap := make(map[string]bool)
				for _, d := range result.Deleted {
					deletedMap[d] = true
				}
				if !deletedMap["cgroup"] || !deletedMap["metadata"] {
					t.Errorf("expected Deleted to contain 'cgroup', 'metadata', got %v", result.Deleted)
				}
				if deletedMap["containers"] {
					t.Error("expected Deleted not to contain 'containers' when no containers exist")
				}
				purgedMap := make(map[string]bool)
				for _, p := range result.Purged {
					purgedMap[p] = true
				}
				if !purgedMap["cni-resources"] || !purgedMap["orphaned-containers"] ||
					!purgedMap["all-metadata"] {
					t.Errorf(
						"expected Purged to contain 'cni-resources', 'orphaned-containers', 'all-metadata', got %v",
						result.Purged,
					)
				}
				if result.Cell.Metadata.Name != "test-cell" {
					t.Errorf("expected cell name to be 'test-cell', got %q", result.Cell.Metadata.Name)
				}
			},
			wantErr: false,
		},
		{
			name:      "successful purge, with containers, no force, no cascade",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			force:     false,
			cascade:   false,
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				existingCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "container1"},
					{ID: "container2"},
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
				f.PurgeCellFn = func(_ intmodel.Cell) error {
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.PurgeCellResult) {
				if !result.ContainersDeleted {
					t.Error("expected ContainersDeleted to be true")
				}
				if !result.CgroupDeleted {
					t.Error("expected CgroupDeleted to be true")
				}
				if !result.MetadataDeleted {
					t.Error("expected MetadataDeleted to be true")
				}
				if !result.PurgeSucceeded {
					t.Error("expected PurgeSucceeded to be true")
				}
				deletedMap := make(map[string]bool)
				for _, d := range result.Deleted {
					deletedMap[d] = true
				}
				if !deletedMap["containers"] || !deletedMap["cgroup"] || !deletedMap["metadata"] {
					t.Errorf(
						"expected Deleted to contain 'containers', 'cgroup', 'metadata', got %v",
						result.Deleted,
					)
				}
			},
			wantErr: false,
		},
		{
			name:      "successful purge, with force",
			cellName:  "test-cell",
			realmName: "test-realm",
			spaceName: "test-space",
			stackName: "test-stack",
			force:     true,
			cascade:   false,
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
					return nil
				}
				f.PurgeCellFn = func(_ intmodel.Cell) error {
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.PurgeCellResult) {
				if !result.Force {
					t.Error("expected Force to be true")
				}
				if !result.PurgeSucceeded {
					t.Error("expected PurgeSucceeded to be true")
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

			result, err := ctrl.PurgeCell(cell, tt.force, tt.cascade)

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

func TestPurgeCell_CellNotFound(t *testing.T) {
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

			_, err := ctrl.PurgeCell(cell, false, false)

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

func TestPurgeCell_ValidationErrors(t *testing.T) {
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

			_, err := ctrl.PurgeCell(cell, false, false)

			if err == nil {
				t.Fatal("expected error but got none")
			}

			if !errors.Is(err, tt.wantErr) {
				t.Errorf("expected error %v, got %v", tt.wantErr, err)
			}
		})
	}
}
