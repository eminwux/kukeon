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

func TestPurgeContainer_SuccessfulPurge(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		realmName     string
		spaceName     string
		stackName     string
		cellName      string
		setupRunner   func(*fakeRunner)
		wantResult    func(t *testing.T, result controller.PurgeContainerResult)
		wantErr       bool
	}{
		{
			name:          "successful purge when container exists in metadata",
			containerName: "test-container",
			realmName:     "test-realm",
			spaceName:     "test-space",
			stackName:     "test-stack",
			cellName:      "test-cell",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				existingCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "test-container", Image: "alpine:latest", ContainerdID: "containerd-id-123"},
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
				existingRealm := buildTestRealm("test-realm", "test-namespace")
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return existingRealm, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsRealmContainerdNamespaceFn = func(_ string) (bool, error) {
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
				f.PurgeContainerFn = func(_ intmodel.Realm, containerdID string) error {
					if containerdID != "containerd-id-123" {
						return errors.New("unexpected containerd ID")
					}
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.PurgeContainerResult) {
				if !result.CellMetadataExists {
					t.Error("expected CellMetadataExists to be true")
				}
				if !result.ContainerExists {
					t.Error("expected ContainerExists to be true")
				}
				deletedMap := make(map[string]bool)
				for _, d := range result.Deleted {
					deletedMap[d] = true
				}
				if !deletedMap["container"] || !deletedMap["task"] {
					t.Errorf("expected Deleted to contain 'container', 'task', got %v", result.Deleted)
				}
				purgedMap := make(map[string]bool)
				for _, p := range result.Purged {
					purgedMap[p] = true
				}
				if !purgedMap["cni-resources"] || !purgedMap["ipam-allocation"] ||
					!purgedMap["cache-entries"] {
					t.Errorf(
						"expected Purged to contain 'cni-resources', 'ipam-allocation', 'cache-entries', got %v",
						result.Purged,
					)
				}
				if result.Container.Metadata.Name != "test-container" {
					t.Errorf("expected container name to be 'test-container', got %q", result.Container.Metadata.Name)
				}
			},
			wantErr: false,
		},
		{
			name:          "successful purge when container not in metadata (orphaned)",
			containerName: "orphaned-container",
			realmName:     "test-realm",
			spaceName:     "test-space",
			stackName:     "test-stack",
			cellName:      "test-cell",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				existingCell.Spec.Containers = []intmodel.ContainerSpec{} // No containers
				f.GetCellFn = func(_ intmodel.Cell) (intmodel.Cell, error) {
					return existingCell, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsCellRootContainerFn = func(_ intmodel.Cell) (bool, error) {
					return true, nil
				}
				existingRealm := buildTestRealm("test-realm", "test-namespace")
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return existingRealm, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsRealmContainerdNamespaceFn = func(_ string) (bool, error) {
					return true, nil
				}
				// PurgeContainer called with container name (orphaned container)
				f.PurgeContainerFn = func(_ intmodel.Realm, containerdID string) error {
					if containerdID != "orphaned-container" {
						return errors.New("unexpected container name")
					}
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.PurgeContainerResult) {
				if !result.CellMetadataExists {
					t.Error("expected CellMetadataExists to be true")
				}
				if result.ContainerExists {
					t.Error("expected ContainerExists to be false for orphaned container")
				}
				// No container/task in Deleted for orphaned containers
				if len(result.Deleted) != 0 {
					t.Errorf("expected Deleted to be empty for orphaned container, got %v", result.Deleted)
				}
				purgedMap := make(map[string]bool)
				for _, p := range result.Purged {
					purgedMap[p] = true
				}
				if !purgedMap["cni-resources"] || !purgedMap["ipam-allocation"] ||
					!purgedMap["cache-entries"] {
					t.Errorf(
						"expected Purged to contain 'cni-resources', 'ipam-allocation', 'cache-entries', got %v",
						result.Purged,
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

			result, err := ctrl.PurgeContainer(container)

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

func TestPurgeContainer_CellNotFound(t *testing.T) {
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
		{
			name:          "cell not found - MetadataExists=false",
			containerName: "test-container",
			realmName:     "test-realm",
			spaceName:     "test-space",
			stackName:     "test-stack",
			cellName:      "test-cell",
			setupRunner: func(f *fakeRunner) {
				// GetCell returns ErrCellNotFound which sets MetadataExists=false
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

			result, err := ctrl.PurgeContainer(container)

			if !tt.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}

			if err == nil {
				t.Fatal("expected error but got none")
			}

			if result.CellMetadataExists {
				t.Error("expected CellMetadataExists to be false when cell not found")
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

func TestPurgeContainer_RealmNotFound(t *testing.T) {
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
			name:          "realm not found - ErrRealmNotFound",
			containerName: "test-container",
			realmName:     "test-realm",
			spaceName:     "test-space",
			stackName:     "test-stack",
			cellName:      "test-cell",
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
				// Runner's GetRealm returns ErrRealmNotFound directly
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return intmodel.Realm{}, errdefs.ErrRealmNotFound
				}
			},
			wantErr:     true,
			errContains: "realm \"test-realm\" not found",
		},
		{
			name:          "realm not found - other error",
			containerName: "test-container",
			realmName:     "test-realm",
			spaceName:     "test-space",
			stackName:     "test-stack",
			cellName:      "test-cell",
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
				// Runner's GetRealm returns other error
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return intmodel.Realm{}, errdefs.ErrGetRealm
				}
			},
			wantErr:     true,
			errContains: "failed to get realm",
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

			_, err := ctrl.PurgeContainer(container)

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

func TestPurgeContainer_ValidationErrors(t *testing.T) {
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
			name:          "empty container name and ID returns ErrContainerNameRequired",
			containerName: "",
			realmName:     "test-realm",
			spaceName:     "test-space",
			stackName:     "test-stack",
			cellName:      "test-cell",
			wantErr:       errdefs.ErrContainerNameRequired,
		},
		{
			name:          "whitespace-only container name and ID returns ErrContainerNameRequired",
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

			_, err := ctrl.PurgeContainer(container)

			if err == nil {
				t.Fatal("expected error but got none")
			}

			if !errors.Is(err, tt.wantErr) {
				t.Errorf("expected error %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestPurgeContainer_RunnerErrors(t *testing.T) {
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
		wantResult    func(t *testing.T, result controller.PurgeContainerResult)
	}{
		{
			name:          "deleteContainerInternal error is wrapped",
			containerName: "test-container",
			realmName:     "test-realm",
			spaceName:     "test-space",
			stackName:     "test-stack",
			cellName:      "test-cell",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				existingCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "test-container", Image: "alpine:latest", ContainerdID: "containerd-id-123"},
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
				existingRealm := buildTestRealm("test-realm", "test-namespace")
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return existingRealm, nil
				}
				f.DeleteContainerFn = func(_ intmodel.Cell, _ string) error {
					return errors.New("deletion failed")
				}
			},
			wantErr:     false,
			errContains: "",
			wantResult: func(t *testing.T, result controller.PurgeContainerResult) {
				hasError := false
				for _, p := range result.Purged {
					if len(p) > 12 && p[:12] == "purge-error:" {
						hasError = true
						break
					}
				}
				if !hasError {
					t.Error("expected Purged to contain 'purge-error:' entry")
				}
			},
		},
		{
			name:          "PurgeContainer runner error is appended to Purged",
			containerName: "test-container",
			realmName:     "test-realm",
			spaceName:     "test-space",
			stackName:     "test-stack",
			cellName:      "test-cell",
			setupRunner: func(f *fakeRunner) {
				existingCell := buildTestCell("test-cell", "test-realm", "test-space", "test-stack")
				existingCell.Spec.Containers = []intmodel.ContainerSpec{
					{ID: "test-container", Image: "alpine:latest", ContainerdID: "containerd-id-123"},
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
				existingRealm := buildTestRealm("test-realm", "test-namespace")
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return existingRealm, nil
				}
				f.DeleteContainerFn = func(_ intmodel.Cell, _ string) error {
					return nil
				}
				f.UpdateCellMetadataFn = func(_ intmodel.Cell) error {
					return nil
				}
				f.PurgeContainerFn = func(_ intmodel.Realm, _ string) error {
					return errors.New("purge failed")
				}
			},
			wantErr: false,
			wantResult: func(t *testing.T, result controller.PurgeContainerResult) {
				hasError := false
				for _, p := range result.Purged {
					if len(p) > 12 && p[:12] == "purge-error:" {
						hasError = true
						break
					}
				}
				if !hasError {
					t.Error("expected Purged to contain 'purge-error:' entry")
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
			container := buildTestContainer(
				tt.containerName,
				tt.realmName,
				tt.spaceName,
				tt.stackName,
				tt.cellName,
				"alpine:latest",
			)

			result, err := ctrl.PurgeContainer(container)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got none")
					return
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
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantResult != nil {
				tt.wantResult(t, result)
			}
		})
	}
}
