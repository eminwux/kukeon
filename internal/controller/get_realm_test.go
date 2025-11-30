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

// TestGetRealm_SuccessfulRetrieval tests successful retrieval of realm with all state flags.
func TestGetRealm_SuccessfulRetrieval(t *testing.T) {
	tests := []struct {
		name        string
		realmName   string
		namespace   string
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.GetRealmResult)
		wantErr     bool
	}{
		{
			name:      "realm exists with all resources",
			realmName: "test-realm",
			namespace: "test-namespace",
			setupRunner: func(f *fakeRunner) {
				realm := buildTestRealm("test-realm", "test-namespace")
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return realm, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsRealmContainerdNamespaceFn = func(namespace string) (bool, error) {
					if namespace != "test-namespace" {
						t.Errorf("expected namespace 'test-namespace', got %q", namespace)
					}
					return true, nil
				}
			},
			wantResult: func(t *testing.T, result controller.GetRealmResult) {
				if !result.MetadataExists {
					t.Error("expected MetadataExists to be true")
				}
				if !result.CgroupExists {
					t.Error("expected CgroupExists to be true")
				}
				if !result.ContainerdNamespaceExists {
					t.Error("expected ContainerdNamespaceExists to be true")
				}
				if result.Realm.Metadata.Name != "test-realm" {
					t.Errorf("expected realm name 'test-realm', got %q", result.Realm.Metadata.Name)
				}
			},
			wantErr: false,
		},
		{
			name:      "realm exists with no resources",
			realmName: "test-realm",
			namespace: "test-namespace",
			setupRunner: func(f *fakeRunner) {
				realm := buildTestRealm("test-realm", "test-namespace")
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return realm, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return false, nil
				}
				f.ExistsRealmContainerdNamespaceFn = func(_ string) (bool, error) {
					return false, nil
				}
			},
			wantResult: func(t *testing.T, result controller.GetRealmResult) {
				if !result.MetadataExists {
					t.Error("expected MetadataExists to be true")
				}
				if result.CgroupExists {
					t.Error("expected CgroupExists to be false")
				}
				if result.ContainerdNamespaceExists {
					t.Error("expected ContainerdNamespaceExists to be false")
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
			realm := intmodel.Realm{
				Metadata: intmodel.RealmMetadata{
					Name: tt.realmName,
				},
				Spec: intmodel.RealmSpec{
					Namespace: tt.namespace,
				},
			}

			result, err := ctrl.GetRealm(realm)

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

// TestGetRealm_ValidationErrors tests validation errors for realm name.
func TestGetRealm_ValidationErrors(t *testing.T) {
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
			mockRunner := &fakeRunner{}
			ctrl := setupTestController(t, mockRunner)
			realm := intmodel.Realm{
				Metadata: intmodel.RealmMetadata{
					Name: tt.realmName,
				},
			}

			_, err := ctrl.GetRealm(realm)

			if err == nil {
				t.Fatal("expected error but got none")
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("expected error %v, got %v", tt.wantErr, err)
			}
		})
	}
}

// TestGetRealm_DefaultNamespace tests default namespace handling.
func TestGetRealm_DefaultNamespace(t *testing.T) {
	tests := []struct {
		name              string
		realmName         string
		namespace         string
		expectedNamespace string
		setupRunner       func(*fakeRunner, string)
	}{
		{
			name:              "empty namespace defaults to realm name",
			realmName:         "test-realm",
			namespace:         "",
			expectedNamespace: "test-realm",
			setupRunner: func(f *fakeRunner, expectedNs string) {
				realm := buildTestRealm("test-realm", "test-realm")
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return realm, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsRealmContainerdNamespaceFn = func(namespace string) (bool, error) {
					if namespace != expectedNs {
						t.Errorf("expected namespace %q, got %q", expectedNs, namespace)
					}
					return true, nil
				}
			},
		},
		{
			name:              "whitespace namespace defaults to realm name",
			realmName:         "test-realm",
			namespace:         "   ",
			expectedNamespace: "test-realm",
			setupRunner: func(f *fakeRunner, expectedNs string) {
				realm := buildTestRealm("test-realm", "test-realm")
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return realm, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsRealmContainerdNamespaceFn = func(namespace string) (bool, error) {
					if namespace != expectedNs {
						t.Errorf("expected namespace %q, got %q", expectedNs, namespace)
					}
					return true, nil
				}
			},
		},
		{
			name:              "provided namespace is used as-is",
			realmName:         "test-realm",
			namespace:         "custom-namespace",
			expectedNamespace: "custom-namespace",
			setupRunner: func(f *fakeRunner, expectedNs string) {
				realm := buildTestRealm("test-realm", "custom-namespace")
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return realm, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsRealmContainerdNamespaceFn = func(namespace string) (bool, error) {
					if namespace != expectedNs {
						t.Errorf("expected namespace %q, got %q", expectedNs, namespace)
					}
					return true, nil
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner, tt.expectedNamespace)
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

			result, err := ctrl.GetRealm(realm)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !result.MetadataExists {
				t.Error("expected MetadataExists to be true")
			}
		})
	}
}

// TestGetRealm_NotFound tests realm not found scenario.
func TestGetRealm_NotFound(t *testing.T) {
	tests := []struct {
		name        string
		realmName   string
		namespace   string
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.GetRealmResult)
		wantErr     bool
	}{
		{
			name:      "realm not found - ErrRealmNotFound",
			realmName: "test-realm",
			namespace: "test-namespace",
			setupRunner: func(f *fakeRunner) {
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return intmodel.Realm{}, errdefs.ErrRealmNotFound
				}
				f.ExistsRealmContainerdNamespaceFn = func(_ string) (bool, error) {
					return false, nil
				}
			},
			wantResult: func(t *testing.T, result controller.GetRealmResult) {
				if result.MetadataExists {
					t.Error("expected MetadataExists to be false")
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
			realm := intmodel.Realm{
				Metadata: intmodel.RealmMetadata{
					Name: tt.realmName,
				},
				Spec: intmodel.RealmSpec{
					Namespace: tt.namespace,
				},
			}

			result, err := ctrl.GetRealm(realm)

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

// TestGetRealm_RunnerErrors tests error propagation from runner methods.
func TestGetRealm_RunnerErrors(t *testing.T) {
	tests := []struct {
		name        string
		realmName   string
		namespace   string
		setupRunner func(*fakeRunner)
		wantErr     error
		errContains string
	}{
		{
			name:      "GetRealm error (non-NotFound) wrapped with ErrGetRealm",
			realmName: "test-realm",
			namespace: "test-namespace",
			setupRunner: func(f *fakeRunner) {
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return intmodel.Realm{}, errors.New("runner error")
				}
			},
			wantErr: errdefs.ErrGetRealm,
		},
		{
			name:      "ExistsCgroup error wrapped with descriptive message",
			realmName: "test-realm",
			namespace: "test-namespace",
			setupRunner: func(f *fakeRunner) {
				realm := buildTestRealm("test-realm", "test-namespace")
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return realm, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return false, errors.New("cgroup check failed")
				}
			},
			wantErr:     nil,
			errContains: "failed to check if realm cgroup exists",
		},
		{
			name:      "ExistsRealmContainerdNamespace error wrapped with ErrCheckNamespaceExists",
			realmName: "test-realm",
			namespace: "test-namespace",
			setupRunner: func(f *fakeRunner) {
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return intmodel.Realm{}, errdefs.ErrRealmNotFound
				}
				f.ExistsRealmContainerdNamespaceFn = func(_ string) (bool, error) {
					return false, errors.New("namespace check failed")
				}
			},
			wantErr: errdefs.ErrCheckNamespaceExists,
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

			_, err := ctrl.GetRealm(realm)

			if err == nil {
				t.Fatal("expected error but got none")
			}

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("expected error %v, got %v", tt.wantErr, err)
				}
			}

			if tt.errContains != "" {
				if err.Error() == "" || len(err.Error()) < len(tt.errContains) {
					t.Errorf("expected error to contain %q, got %q", tt.errContains, err.Error())
				}
			}
		})
	}
}

// TestGetRealm_NameTrimming tests name and namespace trimming.
func TestGetRealm_NameTrimming(t *testing.T) {
	tests := []struct {
		name        string
		realmName   string
		namespace   string
		setupRunner func(*fakeRunner, string, string)
		wantResult  func(t *testing.T, result controller.GetRealmResult)
		wantErr     bool
	}{
		{
			name:      "realm name with leading/trailing whitespace is trimmed",
			realmName: "  test-realm  ",
			namespace: "test-namespace",
			setupRunner: func(f *fakeRunner, expectedName, expectedNs string) {
				realm := buildTestRealm(expectedName, expectedNs)
				f.GetRealmFn = func(received intmodel.Realm) (intmodel.Realm, error) {
					if received.Metadata.Name != expectedName {
						t.Errorf("expected trimmed name %q, got %q", expectedName, received.Metadata.Name)
					}
					return realm, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsRealmContainerdNamespaceFn = func(namespace string) (bool, error) {
					if namespace != expectedNs {
						t.Errorf("expected namespace %q, got %q", expectedNs, namespace)
					}
					return true, nil
				}
			},
			wantResult: func(t *testing.T, result controller.GetRealmResult) {
				if !result.MetadataExists {
					t.Error("expected MetadataExists to be true")
				}
			},
			wantErr: false,
		},
		{
			name:      "namespace with leading/trailing whitespace is trimmed",
			realmName: "test-realm",
			namespace: "  test-namespace  ",
			setupRunner: func(f *fakeRunner, expectedName, expectedNs string) {
				realm := buildTestRealm(expectedName, expectedNs)
				f.GetRealmFn = func(_ intmodel.Realm) (intmodel.Realm, error) {
					return realm, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsRealmContainerdNamespaceFn = func(namespace string) (bool, error) {
					if namespace != expectedNs {
						t.Errorf("expected trimmed namespace %q, got %q", expectedNs, namespace)
					}
					return true, nil
				}
			},
			wantResult: func(t *testing.T, result controller.GetRealmResult) {
				if !result.MetadataExists {
					t.Error("expected MetadataExists to be true")
				}
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner, "test-realm", "test-namespace")
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

			result, err := ctrl.GetRealm(realm)

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

// TestListRealms_SuccessfulRetrieval tests successful retrieval of realms list.
func TestListRealms_SuccessfulRetrieval(t *testing.T) {
	tests := []struct {
		name        string
		setupRunner func(*fakeRunner)
		wantRealms  []string
		wantErr     bool
	}{
		{
			name: "returns list of realms from runner",
			setupRunner: func(f *fakeRunner) {
				f.ListRealmsFn = func() ([]intmodel.Realm, error) {
					return []intmodel.Realm{
						buildTestRealm("realm1", "ns1"),
						buildTestRealm("realm2", "ns2"),
					}, nil
				}
			},
			wantRealms: []string{"realm1", "realm2"},
			wantErr:    false,
		},
		{
			name: "empty list handled correctly",
			setupRunner: func(f *fakeRunner) {
				f.ListRealmsFn = func() ([]intmodel.Realm, error) {
					return []intmodel.Realm{}, nil
				}
			},
			wantRealms: []string{},
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner)
			}

			ctrl := setupTestController(t, mockRunner)

			realms, err := ctrl.ListRealms()

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(realms) != len(tt.wantRealms) {
				t.Errorf("expected %d realms, got %d", len(tt.wantRealms), len(realms))
			}
			for i, wantName := range tt.wantRealms {
				if i < len(realms) && realms[i].Metadata.Name != wantName {
					t.Errorf("expected realm[%d].Name = %q, got %q", i, wantName, realms[i].Metadata.Name)
				}
			}
		})
	}
}

// TestListRealms_RunnerError tests error propagation from runner.
func TestListRealms_RunnerError(t *testing.T) {
	mockRunner := &fakeRunner{}
	mockRunner.ListRealmsFn = func() ([]intmodel.Realm, error) {
		return nil, errors.New("runner error")
	}

	ctrl := setupTestController(t, mockRunner)

	_, err := ctrl.ListRealms()

	if err == nil {
		t.Fatal("expected error but got none")
	}
}
