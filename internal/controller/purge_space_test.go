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

func TestPurgeSpace_SuccessfulPurge(t *testing.T) {
	tests := []struct {
		name        string
		spaceName   string
		realmName   string
		force       bool
		cascade     bool
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.PurgeSpaceResult)
		wantErr     bool
	}{
		{
			name:      "successful purge, no dependencies, no cascade, no force",
			spaceName: "test-space",
			realmName: "test-realm",
			force:     false,
			cascade:   false,
			setupRunner: func(f *fakeRunner) {
				existingSpace := buildTestSpace("test-space", "test-realm")
				// Mock GetSpace (via controller.GetSpace which calls runner methods)
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return existingSpace, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsSpaceCNIConfigFn = func(_ intmodel.Space) (bool, error) {
					return true, nil
				}
				// Mock ListStacks for dependency validation (called in purgeSpaceCascade)
				f.ListStacksFn = func(_, _ string) ([]intmodel.Stack, error) {
					return []intmodel.Stack{}, nil
				}
				// Mock deleteSpaceCascade
				f.DeleteSpaceFn = func(_ intmodel.Space) error {
					return nil
				}
				// Mock comprehensive purge
				f.PurgeSpaceFn = func(_ intmodel.Space) error {
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.PurgeSpaceResult) {
				if !result.MetadataDeleted {
					t.Error("expected MetadataDeleted to be true")
				}
				if !result.CgroupDeleted {
					t.Error("expected CgroupDeleted to be true")
				}
				if !result.CNINetworkDeleted {
					t.Error("expected CNINetworkDeleted to be true")
				}
				if !result.PurgeSucceeded {
					t.Error("expected PurgeSucceeded to be true")
				}
				if result.Force {
					t.Error("expected Force to be false")
				}
				if result.Cascade {
					t.Error("expected Cascade to be false")
				}
				deletedMap := make(map[string]bool)
				for _, d := range result.Deleted {
					deletedMap[d] = true
				}
				if !deletedMap["metadata"] || !deletedMap["cgroup"] || !deletedMap["network"] {
					t.Errorf("expected Deleted to contain 'metadata', 'cgroup', 'network', got %v", result.Deleted)
				}
				purgedMap := make(map[string]bool)
				for _, p := range result.Purged {
					purgedMap[p] = true
				}
				if !purgedMap["cni-network"] || !purgedMap["cni-cache"] || !purgedMap["orphaned-containers"] ||
					!purgedMap["all-metadata"] {
					t.Errorf(
						"expected Purged to contain 'cni-network', 'cni-cache', 'orphaned-containers', 'all-metadata', got %v",
						result.Purged,
					)
				}
				if result.Space.Metadata.Name != "test-space" {
					t.Errorf("expected space name to be 'test-space', got %q", result.Space.Metadata.Name)
				}
			},
			wantErr: false,
		},
		{
			name:      "successful purge, no dependencies, with force",
			spaceName: "test-space",
			realmName: "test-realm",
			force:     true,
			cascade:   false,
			setupRunner: func(f *fakeRunner) {
				existingSpace := buildTestSpace("test-space", "test-realm")
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return existingSpace, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsSpaceCNIConfigFn = func(_ intmodel.Space) (bool, error) {
					return true, nil
				}
				// With force=true, ListStacks is not called for dependency validation
				f.DeleteSpaceFn = func(_ intmodel.Space) error {
					return nil
				}
				f.PurgeSpaceFn = func(_ intmodel.Space) error {
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.PurgeSpaceResult) {
				if !result.MetadataDeleted {
					t.Error("expected MetadataDeleted to be true")
				}
				if !result.PurgeSucceeded {
					t.Error("expected PurgeSucceeded to be true")
				}
				if !result.Force {
					t.Error("expected Force to be true")
				}
				if result.Cascade {
					t.Error("expected Cascade to be false")
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
			space := buildTestSpace(tt.spaceName, tt.realmName)

			result, err := ctrl.PurgeSpace(space, tt.force, tt.cascade)

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
