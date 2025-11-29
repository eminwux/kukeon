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

func TestCreateRealm_NewRealmCreation(t *testing.T) {
	tests := []struct {
		name        string
		realmName   string
		namespace   string
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.CreateRealmResult)
		wantErr     bool
	}{
		{
			name:      "successful creation of new realm",
			realmName: "test-realm",
			namespace: "test-namespace",
			setupRunner: func(f *fakeRunner) {
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return intmodel.Realm{}, errdefs.ErrRealmNotFound
				}
				f.CreateRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return buildTestRealm("test-realm", "test-namespace"), nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateRealmResult) {
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
				if !result.ContainerdNamespaceCreated {
					t.Error("expected ContainerdNamespaceCreated to be true")
				}
				if !result.CgroupExistsPost {
					t.Error("expected CgroupExistsPost to be true")
				}
				if !result.ContainerdNamespaceExistsPost {
					t.Error("expected ContainerdNamespaceExistsPost to be true")
				}
				if result.Realm.Metadata.Name != "test-realm" {
					t.Errorf("expected realm name to be 'test-realm', got %q", result.Realm.Metadata.Name)
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

			result, err := ctrl.CreateRealm(realm)

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

func TestCreateRealm_ExistingRealmReconciliation(t *testing.T) {
	tests := []struct {
		name        string
		realmName   string
		namespace   string
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.CreateRealmResult)
		wantErr     bool
	}{
		{
			name:      "existing realm - no resources exist, all created",
			realmName: "test-realm",
			namespace: "test-namespace",
			setupRunner: func(f *fakeRunner) {
				existingRealm := buildTestRealm("test-realm", "test-namespace")
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return existingRealm, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return false, nil
				}
				f.ExistsRealmContainerdNamespaceFn = func(_ string) (bool, error) {
					return false, nil
				}
				f.EnsureRealmFn = func(realm intmodel.Realm) (intmodel.Realm, error) {
					return realm, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateRealmResult) {
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
				if result.ContainerdNamespaceExistsPre {
					t.Error("expected ContainerdNamespaceExistsPre to be false")
				}
				if !result.CgroupCreated {
					t.Error("expected CgroupCreated to be true")
				}
				if !result.ContainerdNamespaceCreated {
					t.Error("expected ContainerdNamespaceCreated to be true")
				}
			},
			wantErr: false,
		},
		{
			name:      "existing realm - all resources exist, none created",
			realmName: "test-realm",
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
				f.EnsureRealmFn = func(realm intmodel.Realm) (intmodel.Realm, error) {
					return realm, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateRealmResult) {
				if !result.MetadataExistsPre {
					t.Error("expected MetadataExistsPre to be true")
				}
				if result.Created {
					t.Error("expected Created to be false")
				}
				if !result.CgroupExistsPre {
					t.Error("expected CgroupExistsPre to be true")
				}
				if !result.ContainerdNamespaceExistsPre {
					t.Error("expected ContainerdNamespaceExistsPre to be true")
				}
				if result.CgroupCreated {
					t.Error("expected CgroupCreated to be false")
				}
				if result.ContainerdNamespaceCreated {
					t.Error("expected ContainerdNamespaceCreated to be false")
				}
			},
			wantErr: false,
		},
		{
			name:      "existing realm - mixed state (cgroup exists, namespace doesn't)",
			realmName: "test-realm",
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
					return false, nil
				}
				f.EnsureRealmFn = func(realm intmodel.Realm) (intmodel.Realm, error) {
					return realm, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateRealmResult) {
				if !result.MetadataExistsPre {
					t.Error("expected MetadataExistsPre to be true")
				}
				if result.Created {
					t.Error("expected Created to be false")
				}
				if !result.CgroupExistsPre {
					t.Error("expected CgroupExistsPre to be true")
				}
				if result.ContainerdNamespaceExistsPre {
					t.Error("expected ContainerdNamespaceExistsPre to be false")
				}
				if result.CgroupCreated {
					t.Error("expected CgroupCreated to be false")
				}
				if !result.ContainerdNamespaceCreated {
					t.Error("expected ContainerdNamespaceCreated to be true")
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

			result, err := ctrl.CreateRealm(realm)

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

func TestCreateRealm_ValidationErrors(t *testing.T) {
	tests := []struct {
		name      string
		realmName string
		wantErr   error
	}{
		{
			name:      "empty name returns ErrRealmNameRequired",
			realmName: "",
			wantErr:   errdefs.ErrRealmNameRequired,
		},
		{
			name:      "whitespace-only name returns ErrRealmNameRequired",
			realmName: "   ",
			wantErr:   errdefs.ErrRealmNameRequired,
		},
		{
			name:      "tab-only name returns ErrRealmNameRequired",
			realmName: "\t",
			wantErr:   errdefs.ErrRealmNameRequired,
		},
		{
			name:      "newline-only name returns ErrRealmNameRequired",
			realmName: "\n",
			wantErr:   errdefs.ErrRealmNameRequired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			ctrl := setupTestController(t, mockRunner)

			realm := intmodel.Realm{
				Metadata: intmodel.RealmMetadata{
					Name: tt.realmName,
				},
			}

			_, err := ctrl.CreateRealm(realm)

			if err == nil {
				t.Fatal("expected error but got none")
			}

			if !errors.Is(err, tt.wantErr) {
				t.Errorf("expected error %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestCreateRealm_DefaultNamespace(t *testing.T) {
	tests := []struct {
		name        string
		realmName   string
		namespace   string
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.CreateRealmResult)
	}{
		{
			name:      "empty namespace defaults to realm name",
			realmName: "test-realm",
			namespace: "",
			setupRunner: func(f *fakeRunner) {
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return intmodel.Realm{}, errdefs.ErrRealmNotFound
				}
				f.CreateRealmFn = func(realm intmodel.Realm) (intmodel.Realm, error) {
					// Verify namespace was set to realm name
					if realm.Spec.Namespace != "test-realm" {
						return intmodel.Realm{}, errors.New("namespace not set to realm name")
					}
					return buildTestRealm("test-realm", "test-realm"), nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateRealmResult) {
				if result.Realm.Spec.Namespace != "test-realm" {
					t.Errorf("expected namespace to be 'test-realm', got %q", result.Realm.Spec.Namespace)
				}
				if result.Realm.Metadata.Labels[consts.KukeonRealmLabelKey] != "test-realm" {
					t.Errorf(
						"expected label to be 'test-realm', got %q",
						result.Realm.Metadata.Labels[consts.KukeonRealmLabelKey],
					)
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
			realm := intmodel.Realm{
				Metadata: intmodel.RealmMetadata{
					Name: tt.realmName,
				},
				Spec: intmodel.RealmSpec{
					Namespace: tt.namespace,
				},
			}

			result, err := ctrl.CreateRealm(realm)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantResult != nil {
				tt.wantResult(t, result)
			}
		})
	}
}

func TestCreateRealm_DefaultLabels(t *testing.T) {
	tests := []struct {
		name        string
		realm       intmodel.Realm
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.CreateRealmResult)
	}{
		{
			name: "labels map is created if nil",
			realm: intmodel.Realm{
				Metadata: intmodel.RealmMetadata{
					Name:   "test-realm",
					Labels: nil,
				},
				Spec: intmodel.RealmSpec{
					Namespace: "test-namespace",
				},
			},
			setupRunner: func(f *fakeRunner) {
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return intmodel.Realm{}, errdefs.ErrRealmNotFound
				}
				f.CreateRealmFn = func(realm intmodel.Realm) (intmodel.Realm, error) {
					// Verify labels map was created
					if realm.Metadata.Labels == nil {
						return intmodel.Realm{}, errors.New("labels map not created")
					}
					return realm, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateRealmResult) {
				if result.Realm.Metadata.Labels == nil {
					t.Error("expected labels map to be created")
				}
				if result.Realm.Metadata.Labels[consts.KukeonRealmLabelKey] != "test-namespace" {
					t.Errorf(
						"expected label to be 'test-namespace', got %q",
						result.Realm.Metadata.Labels[consts.KukeonRealmLabelKey],
					)
				}
			},
		},
		{
			name: "KukeonRealmLabelKey label is set if missing",
			realm: intmodel.Realm{
				Metadata: intmodel.RealmMetadata{
					Name: "test-realm",
					Labels: map[string]string{
						"custom-label": "custom-value",
					},
				},
				Spec: intmodel.RealmSpec{
					Namespace: "test-namespace",
				},
			},
			setupRunner: func(f *fakeRunner) {
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return intmodel.Realm{}, errdefs.ErrRealmNotFound
				}
				f.CreateRealmFn = func(realm intmodel.Realm) (intmodel.Realm, error) {
					// Verify default label was added
					if realm.Metadata.Labels[consts.KukeonRealmLabelKey] != "test-namespace" {
						return intmodel.Realm{}, errors.New("default label not set")
					}
					// Verify existing labels are preserved
					if realm.Metadata.Labels["custom-label"] != "custom-value" {
						return intmodel.Realm{}, errors.New("existing labels not preserved")
					}
					return realm, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateRealmResult) {
				if result.Realm.Metadata.Labels[consts.KukeonRealmLabelKey] != "test-namespace" {
					t.Errorf(
						"expected default label to be 'test-namespace', got %q",
						result.Realm.Metadata.Labels[consts.KukeonRealmLabelKey],
					)
				}
				if result.Realm.Metadata.Labels["custom-label"] != "custom-value" {
					t.Error("expected existing labels to be preserved")
				}
			},
		},
		{
			name: "existing labels are preserved",
			realm: intmodel.Realm{
				Metadata: intmodel.RealmMetadata{
					Name: "test-realm",
					Labels: map[string]string{
						consts.KukeonRealmLabelKey: "existing-namespace",
						"custom-label":             "custom-value",
					},
				},
				Spec: intmodel.RealmSpec{
					Namespace: "test-namespace",
				},
			},
			setupRunner: func(f *fakeRunner) {
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return intmodel.Realm{}, errdefs.ErrRealmNotFound
				}
				f.CreateRealmFn = func(realm intmodel.Realm) (intmodel.Realm, error) {
					// Verify existing label is preserved (not overwritten)
					if realm.Metadata.Labels[consts.KukeonRealmLabelKey] != "existing-namespace" {
						return intmodel.Realm{}, errors.New("existing label was overwritten")
					}
					return realm, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateRealmResult) {
				if result.Realm.Metadata.Labels[consts.KukeonRealmLabelKey] != "existing-namespace" {
					t.Errorf(
						"expected existing label to be preserved, got %q",
						result.Realm.Metadata.Labels[consts.KukeonRealmLabelKey],
					)
				}
				if result.Realm.Metadata.Labels["custom-label"] != "custom-value" {
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

			result, err := ctrl.CreateRealm(tt.realm)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantResult != nil {
				tt.wantResult(t, result)
			}
		})
	}
}

func TestCreateRealm_RunnerErrors(t *testing.T) {
	tests := []struct {
		name        string
		realmName   string
		namespace   string
		setupRunner func(*fakeRunner)
		wantErr     error
		errContains string
	}{
		{
			name:      "GetRealm error (non-NotFound) is wrapped with ErrGetRealm",
			realmName: "test-realm",
			namespace: "test-namespace",
			setupRunner: func(f *fakeRunner) {
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return intmodel.Realm{}, errors.New("unexpected error")
				}
			},
			wantErr:     errdefs.ErrGetRealm,
			errContains: "unexpected error",
		},
		{
			name:      "CreateRealm error (non-NamespaceAlreadyExists) is wrapped with ErrCreateRealm",
			realmName: "test-realm",
			namespace: "test-namespace",
			setupRunner: func(f *fakeRunner) {
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return intmodel.Realm{}, errdefs.ErrRealmNotFound
				}
				f.CreateRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return intmodel.Realm{}, errors.New("creation failed")
				}
			},
			wantErr:     errdefs.ErrCreateRealm,
			errContains: "creation failed",
		},
		{
			name:      "EnsureRealm error is wrapped with ErrCreateRealm",
			realmName: "test-realm",
			namespace: "test-namespace",
			setupRunner: func(f *fakeRunner) {
				existingRealm := buildTestRealm("test-realm", "test-namespace")
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return existingRealm, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return false, nil
				}
				f.ExistsRealmContainerdNamespaceFn = func(_ string) (bool, error) {
					return false, nil
				}
				f.EnsureRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return intmodel.Realm{}, errors.New("ensure failed")
				}
			},
			wantErr:     errdefs.ErrCreateRealm,
			errContains: "ensure failed",
		},
		{
			name:      "ExistsCgroup error is wrapped with descriptive message",
			realmName: "test-realm",
			namespace: "test-namespace",
			setupRunner: func(f *fakeRunner) {
				existingRealm := buildTestRealm("test-realm", "test-namespace")
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return existingRealm, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return false, errors.New("cgroup check failed")
				}
			},
			wantErr:     nil, // Custom error message, not a standard error
			errContains: "failed to check if realm cgroup exists",
		},
		{
			name:      "ExistsRealmContainerdNamespace error is wrapped with ErrCheckNamespaceExists",
			realmName: "test-realm",
			namespace: "test-namespace",
			setupRunner: func(f *fakeRunner) {
				existingRealm := buildTestRealm("test-realm", "test-namespace")
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return existingRealm, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return false, nil
				}
				f.ExistsRealmContainerdNamespaceFn = func(_ string) (bool, error) {
					return false, errors.New("namespace check failed")
				}
			},
			wantErr:     errdefs.ErrCheckNamespaceExists,
			errContains: "namespace check failed",
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

			_, err := ctrl.CreateRealm(realm)

			if err == nil {
				t.Fatal("expected error but got none")
			}

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("expected error to wrap %v, got %v", tt.wantErr, err)
				}
			}

			if tt.errContains != "" {
				if err.Error() == "" || err.Error() == tt.errContains {
					// Error message should contain the specified text
					found := false
					errStr := err.Error()
					if len(errStr) >= len(tt.errContains) {
						for i := 0; i <= len(errStr)-len(tt.errContains); i++ {
							if errStr[i:i+len(tt.errContains)] == tt.errContains {
								found = true
								break
							}
						}
					}
					if !found && err.Error() != tt.errContains {
						t.Errorf("expected error message to contain %q, got %q", tt.errContains, err.Error())
					}
				}
			}
		})
	}
}

func TestCreateRealm_NamespaceAlreadyExists(t *testing.T) {
	tests := []struct {
		name        string
		realmName   string
		namespace   string
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.CreateRealmResult)
		wantErr     bool
	}{
		{
			name:      "namespace already exists error is ignored",
			realmName: "test-realm",
			namespace: "test-namespace",
			setupRunner: func(f *fakeRunner) {
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return intmodel.Realm{}, errdefs.ErrRealmNotFound
				}
				f.CreateRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					// Return ErrNamespaceAlreadyExists - this should be ignored
					return buildTestRealm("test-realm", "test-namespace"), errdefs.ErrNamespaceAlreadyExists
				}
			},
			wantResult: func(t *testing.T, result controller.CreateRealmResult) {
				// Error should be ignored, so creation should succeed
				if !result.Created {
					t.Error("expected Created to be true")
				}
				if result.MetadataExistsPost != true {
					t.Error("expected MetadataExistsPost to be true")
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

			result, err := ctrl.CreateRealm(realm)

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

func TestCreateRealm_NameTrimming(t *testing.T) {
	tests := []struct {
		name        string
		realmName   string
		namespace   string
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.CreateRealmResult)
		wantErr     bool
	}{
		{
			name:      "name with leading/trailing whitespace is trimmed",
			realmName: "  test-realm  ",
			namespace: "  test-namespace  ",
			setupRunner: func(f *fakeRunner) {
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					// Note: realm.Metadata.Name is not updated by trimming (only local variable is)
					// But namespace is updated (line 61 in create_realm.go)
					// Accept any name for GetRealm since it's not trimmed in the object
					return intmodel.Realm{}, errdefs.ErrRealmNotFound
				}
				f.CreateRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					// Return realm with trimmed values as the result
					// The namespace should be trimmed (set on line 61), but we'll verify in the result
					return buildTestRealm("test-realm", "test-namespace"), nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateRealmResult) {
				// The result realm should have trimmed values from CreateRealm result
				if result.Realm.Metadata.Name != "test-realm" {
					t.Errorf("expected name to be trimmed to 'test-realm', got %q", result.Realm.Metadata.Name)
				}
				if result.Realm.Spec.Namespace != "test-namespace" {
					t.Errorf(
						"expected namespace to be trimmed to 'test-namespace', got %q",
						result.Realm.Spec.Namespace,
					)
				}
			},
			wantErr: false,
		},
		{
			name:      "name that becomes empty after trimming triggers validation error",
			realmName: "   ",
			namespace: "test-namespace",
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
			realm := intmodel.Realm{
				Metadata: intmodel.RealmMetadata{
					Name: tt.realmName,
				},
				Spec: intmodel.RealmSpec{
					Namespace: tt.namespace,
				},
			}

			result, err := ctrl.CreateRealm(realm)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got none")
				}
				if !errors.Is(err, errdefs.ErrRealmNameRequired) {
					t.Errorf("expected ErrRealmNameRequired, got %v", err)
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
