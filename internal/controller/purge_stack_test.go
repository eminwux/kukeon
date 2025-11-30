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
	"testing"

	"github.com/eminwux/kukeon/internal/controller"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

func TestPurgeStack_SuccessfulPurge(t *testing.T) {
	tests := []struct {
		name        string
		stackName   string
		realmName   string
		spaceName   string
		force       bool
		cascade     bool
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.PurgeStackResult)
		wantErr     bool
	}{
		{
			name:      "successful purge, no dependencies, no cascade, no force",
			stackName: "test-stack",
			realmName: "test-realm",
			spaceName: "test-space",
			force:     false,
			cascade:   false,
			setupRunner: func(f *fakeRunner) {
				existingStack := buildTestStack("test-stack", "test-realm", "test-space")
				// Mock GetStack (via controller.GetStack which calls runner methods)
				f.GetStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return existingStack, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				// Mock ListCells for dependency validation (called in purgeStackCascade)
				f.ListCellsFn = func(_, _, _ string) ([]intmodel.Cell, error) {
					return []intmodel.Cell{}, nil
				}
				// Mock deleteStackCascade
				f.DeleteStackFn = func(_ intmodel.Stack) error {
					return nil
				}
				// Mock comprehensive purge
				f.PurgeStackFn = func(_ intmodel.Stack) error {
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.PurgeStackResult) {
				deletedMap := make(map[string]bool)
				for _, d := range result.Deleted {
					deletedMap[d] = true
				}
				if !deletedMap["metadata"] || !deletedMap["cgroup"] {
					t.Errorf("expected Deleted to contain 'metadata', 'cgroup', got %v", result.Deleted)
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
				if result.Stack.Metadata.Name != "test-stack" {
					t.Errorf("expected stack name to be 'test-stack', got %q", result.Stack.Metadata.Name)
				}
			},
			wantErr: false,
		},
		{
			name:      "successful purge, no dependencies, with force",
			stackName: "test-stack",
			realmName: "test-realm",
			spaceName: "test-space",
			force:     true,
			cascade:   false,
			setupRunner: func(f *fakeRunner) {
				existingStack := buildTestStack("test-stack", "test-realm", "test-space")
				f.GetStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return existingStack, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				// With force=true, ListCells is not called for dependency validation
				f.DeleteStackFn = func(_ intmodel.Stack) error {
					return nil
				}
				f.PurgeStackFn = func(_ intmodel.Stack) error {
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.PurgeStackResult) {
				if len(result.Deleted) < 2 {
					t.Errorf("expected Deleted to contain at least 2 items, got %d", len(result.Deleted))
				}
				if len(result.Purged) < 3 {
					t.Errorf("expected Purged to contain at least 3 items, got %d", len(result.Purged))
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
			stack := buildTestStack(tt.stackName, tt.realmName, tt.spaceName)

			result, err := ctrl.PurgeStack(stack, tt.force, tt.cascade)

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
