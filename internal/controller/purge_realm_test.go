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

func TestPurgeRealm_SuccessfulPurgeWithMetadata(t *testing.T) {
	tests := []struct {
		name        string
		realmName   string
		namespace   string
		force       bool
		cascade     bool
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.PurgeRealmResult)
		wantErr     bool
	}{
		{
			name:      "successful purge with metadata, no dependencies, no cascade, no force",
			realmName: "test-realm",
			namespace: "test-namespace",
			force:     false,
			cascade:   false,
			setupRunner: func(f *fakeRunner) {
				existingRealm := buildTestRealm("test-realm", "test-namespace")
				// Mock GetRealm (via controller.GetRealm which calls runner methods)
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return existingRealm, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsRealmContainerdNamespaceFn = func(_ string) (bool, error) {
					return true, nil
				}
				// Mock ListSpaces for dependency validation (called in purgeRealmCascade)
				f.ListSpacesFn = func(_ string) ([]intmodel.Space, error) {
					return []intmodel.Space{}, nil
				}
				// Mock deleteRealmCascade
				f.DeleteRealmFn = func(_ intmodel.Realm) error {
					return nil
				}
				// Mock comprehensive purge
				f.PurgeRealmFn = func(_ intmodel.Realm) error {
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.PurgeRealmResult) {
				if !result.RealmDeleted {
					t.Error("expected RealmDeleted to be true")
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
				if !deletedMap["metadata"] || !deletedMap["cgroup"] || !deletedMap["namespace"] {
					t.Errorf("expected Deleted to contain 'metadata', 'cgroup', 'namespace', got %v", result.Deleted)
				}
				purgedMap := make(map[string]bool)
				for _, p := range result.Purged {
					purgedMap[p] = true
				}
				if !purgedMap["orphaned-containers"] || !purgedMap["cni-resources"] || !purgedMap["all-metadata"] {
					t.Errorf(
						"expected Purged to contain 'orphaned-containers', 'cni-resources', 'all-metadata', got %v",
						result.Purged,
					)
				}
				if result.Realm.Metadata.Name != "test-realm" {
					t.Errorf("expected realm name to be 'test-realm', got %q", result.Realm.Metadata.Name)
				}
			},
			wantErr: false,
		},
		{
			name:      "successful purge with metadata, no dependencies, with force",
			realmName: "test-realm",
			namespace: "test-namespace",
			force:     true,
			cascade:   false,
			setupRunner: func(f *fakeRunner) {
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
				// With force=true, ListSpaces is not called for dependency validation
				f.DeleteRealmFn = func(_ intmodel.Realm) error {
					return nil
				}
				f.PurgeRealmFn = func(_ intmodel.Realm) error {
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.PurgeRealmResult) {
				if !result.RealmDeleted {
					t.Error("expected RealmDeleted to be true")
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
			realm := buildTestRealm(tt.realmName, tt.namespace)

			result, err := ctrl.PurgeRealm(realm, tt.force, tt.cascade)

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

func TestPurgeRealm_SuccessfulPurgeWithoutMetadata(t *testing.T) {
	tests := []struct {
		name        string
		realmName   string
		namespace   string
		force       bool
		cascade     bool
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.PurgeRealmResult)
		wantErr     bool
	}{
		{
			name:      "successful purge when realm doesn't exist (metadata doesn't exist)",
			realmName: "test-realm",
			namespace: "",
			force:     false,
			cascade:   false,
			setupRunner: func(f *fakeRunner) {
				// GetRealm returns ErrRealmNotFound (handled gracefully)
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return intmodel.Realm{}, errdefs.ErrRealmNotFound
				}
				f.ExistsRealmContainerdNamespaceFn = func(_ string) (bool, error) {
					return false, nil
				}
				// PurgeRealm is called even without metadata
				f.PurgeRealmFn = func(_ intmodel.Realm) error {
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.PurgeRealmResult) {
				if result.RealmDeleted {
					t.Error("expected RealmDeleted to be false (metadata didn't exist)")
				}
				if !result.PurgeSucceeded {
					t.Error("expected PurgeSucceeded to be true")
				}
				if len(result.Deleted) != 0 {
					t.Errorf("expected Deleted to be empty (no standard deletion), got %v", result.Deleted)
				}
				purgedMap := make(map[string]bool)
				for _, p := range result.Purged {
					purgedMap[p] = true
				}
				if !purgedMap["orphaned-containers"] || !purgedMap["cni-resources"] || !purgedMap["all-metadata"] {
					t.Errorf(
						"expected Purged to contain 'orphaned-containers', 'cni-resources', 'all-metadata', got %v",
						result.Purged,
					)
				}
				// Realm is constructed from input with default namespace
				if result.Realm.Metadata.Name != "test-realm" {
					t.Errorf("expected realm name to be 'test-realm', got %q", result.Realm.Metadata.Name)
				}
				if result.Realm.Spec.Namespace != "test-realm" {
					t.Errorf(
						"expected namespace to default to realm name 'test-realm', got %q",
						result.Realm.Spec.Namespace,
					)
				}
				if result.Realm.Status.State != intmodel.RealmStateUnknown {
					t.Errorf("expected realm state to be Unknown, got %v", result.Realm.Status.State)
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
			realm := buildTestRealm(tt.realmName, tt.namespace)

			result, err := ctrl.PurgeRealm(realm, tt.force, tt.cascade)

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

func TestPurgeRealm_CascadePurge(t *testing.T) {
	tests := []struct {
		name        string
		realmName   string
		namespace   string
		force       bool
		cascade     bool
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.PurgeRealmResult)
		wantErr     bool
	}{
		{
			name:      "cascade purge with multiple spaces",
			realmName: "test-realm",
			namespace: "test-namespace",
			force:     false,
			cascade:   true,
			setupRunner: func(f *fakeRunner) {
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
				spaces := []intmodel.Space{
					buildTestSpace("space1", "test-realm"),
					buildTestSpace("space2", "test-realm"),
				}
				// ListSpaces called twice: once for tracking in result.Deleted, once in purgeRealmCascade
				listSpacesCallCount := 0
				f.ListSpacesFn = func(_ string) ([]intmodel.Space, error) {
					listSpacesCallCount++
					return spaces, nil
				}
				// purgeSpaceCascade will be called for each space
				f.ListStacksFn = func(_, _ string) ([]intmodel.Stack, error) {
					return []intmodel.Stack{}, nil
				}
				f.DeleteSpaceFn = func(_ intmodel.Space) error {
					return nil
				}
				f.PurgeSpaceFn = func(_ intmodel.Space) error {
					return nil
				}
				f.DeleteRealmFn = func(_ intmodel.Realm) error {
					return nil
				}
				f.PurgeRealmFn = func(_ intmodel.Realm) error {
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.PurgeRealmResult) {
				if !result.RealmDeleted {
					t.Error("expected RealmDeleted to be true")
				}
				if !result.PurgeSucceeded {
					t.Error("expected PurgeSucceeded to be true")
				}
				if !result.Cascade {
					t.Error("expected Cascade to be true")
				}
				deletedMap := make(map[string]bool)
				for _, d := range result.Deleted {
					deletedMap[d] = true
				}
				if !deletedMap["space:space1"] {
					t.Error("expected Deleted to contain 'space:space1'")
				}
				if !deletedMap["space:space2"] {
					t.Error("expected Deleted to contain 'space:space2'")
				}
				if !deletedMap["metadata"] || !deletedMap["cgroup"] || !deletedMap["namespace"] {
					t.Error("expected Deleted to contain 'metadata', 'cgroup', 'namespace'")
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
			realm := buildTestRealm(tt.realmName, tt.namespace)

			result, err := ctrl.PurgeRealm(realm, tt.force, tt.cascade)

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

func TestPurgeRealm_ForcePurge(t *testing.T) {
	tests := []struct {
		name        string
		realmName   string
		namespace   string
		force       bool
		cascade     bool
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.PurgeRealmResult)
		wantErr     bool
	}{
		{
			name:      "force purge with dependencies (skips validation)",
			realmName: "test-realm",
			namespace: "test-namespace",
			force:     true,
			cascade:   false,
			setupRunner: func(f *fakeRunner) {
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
				// With force=true, ListSpaces is not called for dependency validation
				f.DeleteRealmFn = func(_ intmodel.Realm) error {
					return nil
				}
				f.PurgeRealmFn = func(_ intmodel.Realm) error {
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.PurgeRealmResult) {
				if !result.RealmDeleted {
					t.Error("expected RealmDeleted to be true")
				}
				if !result.PurgeSucceeded {
					t.Error("expected PurgeSucceeded to be true")
				}
				if !result.Force {
					t.Error("expected Force to be true")
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
			realm := buildTestRealm(tt.realmName, tt.namespace)

			result, err := ctrl.PurgeRealm(realm, tt.force, tt.cascade)

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

func TestPurgeRealm_ValidationErrors(t *testing.T) {
	tests := []struct {
		name      string
		realmName string
		wantErr   error
	}{
		{
			name:      "empty realm name returns ErrRealmNameRequired",
			realmName: "",
			wantErr:   errdefs.ErrRealmNameRequired,
		},
		{
			name:      "whitespace-only realm name returns ErrRealmNameRequired",
			realmName: "   ",
			wantErr:   errdefs.ErrRealmNameRequired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := setupTestController(t, &fakeRunner{})
			realm := buildTestRealm(tt.realmName, "")

			_, err := ctrl.PurgeRealm(realm, false, false)

			if err == nil {
				t.Fatal("expected error but got none")
			}

			if !errors.Is(err, tt.wantErr) {
				t.Errorf("expected error to be %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestPurgeRealm_DefaultNamespace(t *testing.T) {
	tests := []struct {
		name        string
		realmName   string
		namespace   string
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.PurgeRealmResult)
		wantErr     bool
	}{
		{
			name:      "empty namespace defaults to realm name",
			realmName: "test-realm",
			namespace: "",
			setupRunner: func(f *fakeRunner) {
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return intmodel.Realm{}, errdefs.ErrRealmNotFound
				}
				f.ExistsRealmContainerdNamespaceFn = func(namespace string) (bool, error) {
					if namespace != "test-realm" {
						t.Errorf("expected namespace to be 'test-realm', got %q", namespace)
					}
					return false, nil
				}
				f.PurgeRealmFn = func(realm intmodel.Realm) error {
					if realm.Spec.Namespace != "test-realm" {
						t.Errorf("expected namespace to be 'test-realm', got %q", realm.Spec.Namespace)
					}
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.PurgeRealmResult) {
				if result.Realm.Spec.Namespace != "test-realm" {
					t.Errorf("expected namespace to default to 'test-realm', got %q", result.Realm.Spec.Namespace)
				}
			},
			wantErr: false,
		},
		{
			name:      "whitespace namespace defaults to realm name",
			realmName: "test-realm",
			namespace: "   ",
			setupRunner: func(f *fakeRunner) {
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return intmodel.Realm{}, errdefs.ErrRealmNotFound
				}
				f.ExistsRealmContainerdNamespaceFn = func(_ string) (bool, error) {
					return false, nil
				}
				f.PurgeRealmFn = func(realm intmodel.Realm) error {
					if realm.Spec.Namespace != "test-realm" {
						t.Errorf("expected namespace to be 'test-realm', got %q", realm.Spec.Namespace)
					}
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.PurgeRealmResult) {
				if result.Realm.Spec.Namespace != "test-realm" {
					t.Errorf("expected namespace to default to 'test-realm', got %q", result.Realm.Spec.Namespace)
				}
			},
			wantErr: false,
		},
		{
			name:      "provided namespace is used as-is",
			realmName: "test-realm",
			namespace: "custom-namespace",
			setupRunner: func(f *fakeRunner) {
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return intmodel.Realm{}, errdefs.ErrRealmNotFound
				}
				f.ExistsRealmContainerdNamespaceFn = func(namespace string) (bool, error) {
					if namespace != "custom-namespace" {
						t.Errorf("expected namespace to be 'custom-namespace', got %q", namespace)
					}
					return false, nil
				}
				f.PurgeRealmFn = func(realm intmodel.Realm) error {
					if realm.Spec.Namespace != "custom-namespace" {
						t.Errorf("expected namespace to be 'custom-namespace', got %q", realm.Spec.Namespace)
					}
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.PurgeRealmResult) {
				if result.Realm.Spec.Namespace != "custom-namespace" {
					t.Errorf("expected namespace to be 'custom-namespace', got %q", result.Realm.Spec.Namespace)
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
			realm := buildTestRealm(tt.realmName, tt.namespace)

			result, err := ctrl.PurgeRealm(realm, false, false)

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

func TestPurgeRealm_DependencyValidationError(t *testing.T) {
	tests := []struct {
		name        string
		realmName   string
		namespace   string
		force       bool
		cascade     bool
		setupRunner func(*fakeRunner)
		wantErr     bool
		errContains string
	}{
		{
			name:      "realm with spaces, force=false, cascade=false returns ErrResourceHasDependencies",
			realmName: "test-realm",
			namespace: "test-namespace",
			force:     false,
			cascade:   false,
			setupRunner: func(f *fakeRunner) {
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
				spaces := []intmodel.Space{
					buildTestSpace("space1", "test-realm"),
					buildTestSpace("space2", "test-realm"),
				}
				f.ListSpacesFn = func(_ string) ([]intmodel.Space, error) {
					return spaces, nil
				}
			},
			wantErr:     true,
			errContains: "realm \"test-realm\" has 2 space(s)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner)
			}

			ctrl := setupTestController(t, mockRunner)
			realm := buildTestRealm(tt.realmName, tt.namespace)

			_, err := ctrl.PurgeRealm(realm, tt.force, tt.cascade)

			if !tt.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}

			if err == nil {
				t.Fatal("expected error but got none")
			}

			if !errors.Is(err, errdefs.ErrResourceHasDependencies) {
				t.Errorf("expected error to wrap ErrResourceHasDependencies, got %v", err)
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

func TestPurgeRealm_GetRealmErrors(t *testing.T) {
	tests := []struct {
		name        string
		realmName   string
		namespace   string
		setupRunner func(*fakeRunner)
		wantErr     bool
		errContains string
	}{
		{
			name:      "GetRealm non-NotFound error is propagated",
			realmName: "test-realm",
			namespace: "test-namespace",
			setupRunner: func(f *fakeRunner) {
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return intmodel.Realm{}, errors.New("unexpected error")
				}
			},
			wantErr:     true,
			errContains: "unexpected error",
		},
		{
			name:      "GetRealm ErrRealmNotFound is handled gracefully (metadataExists=false)",
			realmName: "test-realm",
			namespace: "test-namespace",
			setupRunner: func(f *fakeRunner) {
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return intmodel.Realm{}, errdefs.ErrRealmNotFound
				}
				f.ExistsRealmContainerdNamespaceFn = func(_ string) (bool, error) {
					return false, nil
				}
				f.PurgeRealmFn = func(_ intmodel.Realm) error {
					return nil
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
			realm := buildTestRealm(tt.realmName, tt.namespace)

			result, err := ctrl.PurgeRealm(realm, false, false)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got none")
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
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Verify result indicates metadata didn't exist
			if result.RealmDeleted {
				t.Error("expected RealmDeleted to be false when metadata doesn't exist")
			}
		})
	}
}

func TestPurgeRealm_RunnerErrors(t *testing.T) {
	tests := []struct {
		name        string
		realmName   string
		namespace   string
		force       bool
		cascade     bool
		setupRunner func(*fakeRunner)
		wantErr     bool
		errContains string
		wantResult  func(t *testing.T, result controller.PurgeRealmResult)
	}{
		{
			name:      "ListSpaces error during cascade tracking",
			realmName: "test-realm",
			namespace: "test-namespace",
			force:     false,
			cascade:   true,
			setupRunner: func(f *fakeRunner) {
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
				f.ListSpacesFn = func(_ string) ([]intmodel.Space, error) {
					return nil, errors.New("list failed")
				}
			},
			wantErr:     true,
			errContains: "failed to list spaces",
		},
		{
			name:      "ListSpaces error during cascade purging",
			realmName: "test-realm",
			namespace: "test-namespace",
			force:     false,
			cascade:   true,
			setupRunner: func(f *fakeRunner) {
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
				spaces := []intmodel.Space{
					buildTestSpace("space1", "test-realm"),
				}
				// First call succeeds (for tracking), second call fails (in purgeRealmCascade)
				listSpacesCallCount := 0
				f.ListSpacesFn = func(_ string) ([]intmodel.Space, error) {
					listSpacesCallCount++
					if listSpacesCallCount == 1 {
						return spaces, nil
					}
					return nil, errors.New("list failed in cascade")
				}
			},
			wantErr:     true,
			errContains: "failed to list spaces",
		},
		{
			name:      "ListSpaces error during dependency validation",
			realmName: "test-realm",
			namespace: "test-namespace",
			force:     false,
			cascade:   false,
			setupRunner: func(f *fakeRunner) {
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
				f.ListSpacesFn = func(_ string) ([]intmodel.Space, error) {
					return nil, errors.New("list failed")
				}
			},
			wantErr:     true,
			errContains: "failed to list spaces",
		},
		{
			name:      "purgeSpaceCascade error",
			realmName: "test-realm",
			namespace: "test-namespace",
			force:     false,
			cascade:   true,
			setupRunner: func(f *fakeRunner) {
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
				spaces := []intmodel.Space{
					buildTestSpace("space1", "test-realm"),
				}
				f.ListSpacesFn = func(_ string) ([]intmodel.Space, error) {
					return spaces, nil
				}
				f.ListStacksFn = func(_, _ string) ([]intmodel.Stack, error) {
					return []intmodel.Stack{}, nil
				}
				f.DeleteSpaceFn = func(_ intmodel.Space) error {
					return errors.New("space deletion failed")
				}
			},
			wantErr:     true,
			errContains: "failed to purge space",
		},
		{
			name:      "deleteRealmCascade error",
			realmName: "test-realm",
			namespace: "test-namespace",
			force:     false,
			cascade:   false,
			setupRunner: func(f *fakeRunner) {
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
				f.ListSpacesFn = func(_ string) ([]intmodel.Space, error) {
					return []intmodel.Space{}, nil
				}
				f.DeleteRealmFn = func(_ intmodel.Realm) error {
					return errors.New("deletion failed")
				}
			},
			wantErr:     true,
			errContains: "failed to delete realm",
		},
		{
			name:      "runner.PurgeRealm error",
			realmName: "test-realm",
			namespace: "test-namespace",
			force:     false,
			cascade:   false,
			setupRunner: func(f *fakeRunner) {
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
				f.ListSpacesFn = func(_ string) ([]intmodel.Space, error) {
					return []intmodel.Space{}, nil
				}
				f.DeleteRealmFn = func(_ intmodel.Realm) error {
					return nil
				}
				f.PurgeRealmFn = func(_ intmodel.Realm) error {
					return errors.New("purge failed")
				}
			},
			wantErr: true,
			wantResult: func(t *testing.T, result controller.PurgeRealmResult) {
				if result.PurgeSucceeded {
					t.Error("expected PurgeSucceeded to be false on purge error")
				}
				// Error should be appended to Purged slice
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
			realm := buildTestRealm(tt.realmName, tt.namespace)

			result, err := ctrl.PurgeRealm(realm, tt.force, tt.cascade)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got none")
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

func TestPurgeRealm_PartialCascadeFailure(t *testing.T) {
	tests := []struct {
		name        string
		realmName   string
		namespace   string
		force       bool
		cascade     bool
		setupRunner func(*fakeRunner)
		wantErr     bool
		errContains string
	}{
		{
			name:      "multiple spaces, one purgeSpaceCascade fails",
			realmName: "test-realm",
			namespace: "test-namespace",
			force:     false,
			cascade:   true,
			setupRunner: func(f *fakeRunner) {
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
				spaces := []intmodel.Space{
					buildTestSpace("space1", "test-realm"),
					buildTestSpace("space2", "test-realm"),
				}
				f.ListSpacesFn = func(_ string) ([]intmodel.Space, error) {
					return spaces, nil
				}
				purgeCallCount := 0
				f.ListStacksFn = func(_, _ string) ([]intmodel.Stack, error) {
					return []intmodel.Stack{}, nil
				}
				f.DeleteSpaceFn = func(space intmodel.Space) error {
					purgeCallCount++
					if space.Metadata.Name == "space2" {
						return errors.New("space2 purge failed")
					}
					return nil
				}
				f.PurgeSpaceFn = func(_ intmodel.Space) error {
					return nil
				}
			},
			wantErr:     true,
			errContains: "failed to purge space \"space2\"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner)
			}

			ctrl := setupTestController(t, mockRunner)
			realm := buildTestRealm(tt.realmName, tt.namespace)

			_, err := ctrl.PurgeRealm(realm, tt.force, tt.cascade)

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

func TestPurgeRealm_ResultConstruction(t *testing.T) {
	tests := []struct {
		name        string
		realmName   string
		namespace   string
		force       bool
		cascade     bool
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.PurgeRealmResult)
		wantErr     bool
	}{
		{
			name:      "all result fields verified for success case",
			realmName: "test-realm",
			namespace: "test-namespace",
			force:     true,
			cascade:   true,
			setupRunner: func(f *fakeRunner) {
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
				spaces := []intmodel.Space{
					buildTestSpace("space1", "test-realm"),
				}
				f.ListSpacesFn = func(_ string) ([]intmodel.Space, error) {
					return spaces, nil
				}
				f.ListStacksFn = func(_, _ string) ([]intmodel.Stack, error) {
					return []intmodel.Stack{}, nil
				}
				f.DeleteSpaceFn = func(_ intmodel.Space) error {
					return nil
				}
				f.PurgeSpaceFn = func(_ intmodel.Space) error {
					return nil
				}
				f.DeleteRealmFn = func(_ intmodel.Realm) error {
					return nil
				}
				f.PurgeRealmFn = func(_ intmodel.Realm) error {
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.PurgeRealmResult) {
				if !result.RealmDeleted {
					t.Error("expected RealmDeleted to be true")
				}
				if !result.PurgeSucceeded {
					t.Error("expected PurgeSucceeded to be true")
				}
				if !result.Force {
					t.Error("expected Force to be true")
				}
				if !result.Cascade {
					t.Error("expected Cascade to be true")
				}
				deletedMap := make(map[string]bool)
				for _, d := range result.Deleted {
					deletedMap[d] = true
				}
				if !deletedMap["space:space1"] {
					t.Error("expected Deleted to contain 'space:space1'")
				}
				if !deletedMap["metadata"] || !deletedMap["cgroup"] || !deletedMap["namespace"] {
					t.Error("expected Deleted to contain 'metadata', 'cgroup', 'namespace'")
				}
				purgedMap := make(map[string]bool)
				for _, p := range result.Purged {
					purgedMap[p] = true
				}
				if !purgedMap["orphaned-containers"] || !purgedMap["cni-resources"] || !purgedMap["all-metadata"] {
					t.Error("expected Purged to contain 'orphaned-containers', 'cni-resources', 'all-metadata'")
				}
			},
			wantErr: false,
		},
		{
			name:      "all result fields verified for failure case",
			realmName: "test-realm",
			namespace: "test-namespace",
			force:     false,
			cascade:   false,
			setupRunner: func(f *fakeRunner) {
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
				f.ListSpacesFn = func(_ string) ([]intmodel.Space, error) {
					return []intmodel.Space{}, nil
				}
				f.DeleteRealmFn = func(_ intmodel.Realm) error {
					return nil
				}
				f.PurgeRealmFn = func(_ intmodel.Realm) error {
					return errors.New("purge failed")
				}
			},
			wantResult: func(t *testing.T, result controller.PurgeRealmResult) {
				if result.PurgeSucceeded {
					t.Error("expected PurgeSucceeded to be false")
				}
				if result.RealmDeleted {
					t.Error("expected RealmDeleted to be false on failure")
				}
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
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner)
			}

			ctrl := setupTestController(t, mockRunner)
			realm := buildTestRealm(tt.realmName, tt.namespace)

			result, err := ctrl.PurgeRealm(realm, tt.force, tt.cascade)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got none")
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}

			if tt.wantResult != nil {
				tt.wantResult(t, result)
			}
		})
	}
}

func TestPurgeRealm_NameTrimming(t *testing.T) {
	tests := []struct {
		name        string
		realmName   string
		namespace   string
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.PurgeRealmResult)
		wantErr     bool
	}{
		{
			name:      "realm name with leading/trailing whitespace is trimmed",
			realmName: "  test-realm  ",
			namespace: "test-namespace",
			setupRunner: func(f *fakeRunner) {
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
				f.ListSpacesFn = func(realmName string) ([]intmodel.Space, error) {
					if realmName != "test-realm" {
						return nil, errors.New("unexpected realm name")
					}
					return []intmodel.Space{}, nil
				}
				f.DeleteRealmFn = func(realm intmodel.Realm) error {
					if realm.Metadata.Name != "test-realm" {
						return errors.New("unexpected realm name")
					}
					return nil
				}
				f.PurgeRealmFn = func(realm intmodel.Realm) error {
					if realm.Metadata.Name != "test-realm" {
						return errors.New("unexpected realm name")
					}
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.PurgeRealmResult) {
				if result.Realm.Metadata.Name != "test-realm" {
					t.Errorf("expected realm name to be trimmed to 'test-realm', got %q", result.Realm.Metadata.Name)
				}
			},
			wantErr: false,
		},
		{
			name:      "namespace with leading/trailing whitespace is trimmed",
			realmName: "test-realm",
			namespace: "  test-namespace  ",
			setupRunner: func(f *fakeRunner) {
				existingRealm := buildTestRealm("test-realm", "test-namespace")
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return existingRealm, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsRealmContainerdNamespaceFn = func(namespace string) (bool, error) {
					if namespace != "test-namespace" {
						return false, errors.New("unexpected namespace")
					}
					return true, nil
				}
				f.ListSpacesFn = func(_ string) ([]intmodel.Space, error) {
					return []intmodel.Space{}, nil
				}
				f.DeleteRealmFn = func(_ intmodel.Realm) error {
					return nil
				}
				f.PurgeRealmFn = func(realm intmodel.Realm) error {
					if realm.Spec.Namespace != "test-namespace" {
						return errors.New("unexpected namespace")
					}
					return nil
				}
			},
			wantResult: func(t *testing.T, result controller.PurgeRealmResult) {
				if result.Realm.Spec.Namespace != "test-namespace" {
					t.Errorf(
						"expected namespace to be trimmed to 'test-namespace', got %q",
						result.Realm.Spec.Namespace,
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
			realm := buildTestRealm(tt.realmName, tt.namespace)

			result, err := ctrl.PurgeRealm(realm, false, false)

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
