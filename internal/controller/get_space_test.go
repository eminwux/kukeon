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

// TestGetSpace_SuccessfulRetrieval tests successful retrieval of space with all state flags.
func TestGetSpace_SuccessfulRetrieval(t *testing.T) {
	tests := []struct {
		name        string
		spaceName   string
		realmName   string
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.GetSpaceResult)
		wantErr     bool
	}{
		{
			name:      "space exists with all resources",
			spaceName: "test-space",
			realmName: "test-realm",
			setupRunner: func(f *fakeRunner) {
				space := buildTestSpace("test-space", "test-realm")
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return space, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsSpaceCNIConfigFn = func(_ intmodel.Space) (bool, error) {
					return true, nil
				}
			},
			wantResult: func(t *testing.T, result controller.GetSpaceResult) {
				if !result.MetadataExists {
					t.Error("expected MetadataExists to be true")
				}
				if !result.CgroupExists {
					t.Error("expected CgroupExists to be true")
				}
				if !result.CNINetworkExists {
					t.Error("expected CNINetworkExists to be true")
				}
				if result.Space.Metadata.Name != "test-space" {
					t.Errorf("expected space name 'test-space', got %q", result.Space.Metadata.Name)
				}
			},
			wantErr: false,
		},
		{
			name:      "space exists with no resources",
			spaceName: "test-space",
			realmName: "test-realm",
			setupRunner: func(f *fakeRunner) {
				space := buildTestSpace("test-space", "test-realm")
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return space, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return false, nil
				}
				f.ExistsSpaceCNIConfigFn = func(_ intmodel.Space) (bool, error) {
					return false, nil
				}
			},
			wantResult: func(t *testing.T, result controller.GetSpaceResult) {
				if !result.MetadataExists {
					t.Error("expected MetadataExists to be true")
				}
				if result.CgroupExists {
					t.Error("expected CgroupExists to be false")
				}
				if result.CNINetworkExists {
					t.Error("expected CNINetworkExists to be false")
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
			space := intmodel.Space{
				Metadata: intmodel.SpaceMetadata{
					Name: tt.spaceName,
				},
				Spec: intmodel.SpaceSpec{
					RealmName: tt.realmName,
				},
			}

			result, err := ctrl.GetSpace(space)

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

// TestGetSpace_ValidationErrors tests validation errors for space name and realm name.
func TestGetSpace_ValidationErrors(t *testing.T) {
	tests := []struct {
		name      string
		spaceName string
		realmName string
		wantErr   error
	}{
		{
			name:      "empty space name returns ErrSpaceNameRequired",
			spaceName: "",
			realmName: "test-realm",
			wantErr:   errdefs.ErrSpaceNameRequired,
		},
		{
			name:      "whitespace-only space name returns ErrSpaceNameRequired",
			spaceName: "   ",
			realmName: "test-realm",
			wantErr:   errdefs.ErrSpaceNameRequired,
		},
		{
			name:      "empty realm name returns ErrRealmNameRequired",
			spaceName: "test-space",
			realmName: "",
			wantErr:   errdefs.ErrRealmNameRequired,
		},
		{
			name:      "whitespace-only realm name returns ErrRealmNameRequired",
			spaceName: "test-space",
			realmName: "   ",
			wantErr:   errdefs.ErrRealmNameRequired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			ctrl := setupTestController(t, mockRunner)
			space := intmodel.Space{
				Metadata: intmodel.SpaceMetadata{
					Name: tt.spaceName,
				},
				Spec: intmodel.SpaceSpec{
					RealmName: tt.realmName,
				},
			}

			_, err := ctrl.GetSpace(space)

			if err == nil {
				t.Fatal("expected error but got none")
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("expected error %v, got %v", tt.wantErr, err)
			}
		})
	}
}

// TestGetSpace_NotFound tests space not found scenario.
func TestGetSpace_NotFound(t *testing.T) {
	tests := []struct {
		name        string
		spaceName   string
		realmName   string
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.GetSpaceResult)
		wantErr     bool
	}{
		{
			name:      "space not found - ErrSpaceNotFound",
			spaceName: "test-space",
			realmName: "test-realm",
			setupRunner: func(f *fakeRunner) {
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return intmodel.Space{}, errdefs.ErrSpaceNotFound
				}
				f.ExistsSpaceCNIConfigFn = func(_ intmodel.Space) (bool, error) {
					return false, nil
				}
			},
			wantResult: func(t *testing.T, result controller.GetSpaceResult) {
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
			space := intmodel.Space{
				Metadata: intmodel.SpaceMetadata{
					Name: tt.spaceName,
				},
				Spec: intmodel.SpaceSpec{
					RealmName: tt.realmName,
				},
			}

			result, err := ctrl.GetSpace(space)

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

// TestGetSpace_RunnerErrors tests error propagation from runner methods.
func TestGetSpace_RunnerErrors(t *testing.T) {
	tests := []struct {
		name        string
		spaceName   string
		realmName   string
		setupRunner func(*fakeRunner)
		wantErr     error
		errContains string
	}{
		{
			name:      "GetSpace error (non-NotFound) wrapped with ErrGetSpace",
			spaceName: "test-space",
			realmName: "test-realm",
			setupRunner: func(f *fakeRunner) {
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return intmodel.Space{}, errors.New("runner error")
				}
			},
			wantErr: errdefs.ErrGetSpace,
		},
		{
			name:      "ExistsCgroup error wrapped with descriptive message",
			spaceName: "test-space",
			realmName: "test-realm",
			setupRunner: func(f *fakeRunner) {
				space := buildTestSpace("test-space", "test-realm")
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return space, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return false, errors.New("cgroup check failed")
				}
			},
			wantErr:     nil,
			errContains: "failed to check if space cgroup exists",
		},
		{
			name:      "ExistsSpaceCNIConfig error wrapped with ErrCheckNetworkExists",
			spaceName: "test-space",
			realmName: "test-realm",
			setupRunner: func(f *fakeRunner) {
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return intmodel.Space{}, errdefs.ErrSpaceNotFound
				}
				f.ExistsSpaceCNIConfigFn = func(_ intmodel.Space) (bool, error) {
					return false, errors.New("network check failed")
				}
			},
			wantErr: errdefs.ErrCheckNetworkExists,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner)
			}

			ctrl := setupTestController(t, mockRunner)
			space := intmodel.Space{
				Metadata: intmodel.SpaceMetadata{
					Name: tt.spaceName,
				},
				Spec: intmodel.SpaceSpec{
					RealmName: tt.realmName,
				},
			}

			_, err := ctrl.GetSpace(space)

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

// TestGetSpace_NameTrimming tests name trimming.
func TestGetSpace_NameTrimming(t *testing.T) {
	tests := []struct {
		name        string
		spaceName   string
		realmName   string
		setupRunner func(*fakeRunner, string, string)
		wantResult  func(t *testing.T, result controller.GetSpaceResult)
		wantErr     bool
	}{
		{
			name:      "space name and realm name trimmed",
			spaceName: "  test-space  ",
			realmName: "  test-realm  ",
			setupRunner: func(f *fakeRunner, expectedSpaceName, expectedRealmName string) {
				space := buildTestSpace(expectedSpaceName, expectedRealmName)
				f.GetSpaceFn = func(received intmodel.Space) (intmodel.Space, error) {
					if received.Metadata.Name != expectedSpaceName {
						t.Errorf("expected trimmed space name %q, got %q", expectedSpaceName, received.Metadata.Name)
					}
					if received.Spec.RealmName != expectedRealmName {
						t.Errorf("expected trimmed realm name %q, got %q", expectedRealmName, received.Spec.RealmName)
					}
					return space, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsSpaceCNIConfigFn = func(_ intmodel.Space) (bool, error) {
					return true, nil
				}
			},
			wantResult: func(t *testing.T, result controller.GetSpaceResult) {
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
				tt.setupRunner(mockRunner, "test-space", "test-realm")
			}

			ctrl := setupTestController(t, mockRunner)
			space := intmodel.Space{
				Metadata: intmodel.SpaceMetadata{
					Name: tt.spaceName,
				},
				Spec: intmodel.SpaceSpec{
					RealmName: tt.realmName,
				},
			}

			result, err := ctrl.GetSpace(space)

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

// TestListSpaces_SuccessfulRetrieval tests successful retrieval of spaces list.
func TestListSpaces_SuccessfulRetrieval(t *testing.T) {
	tests := []struct {
		name        string
		realmName   string
		setupRunner func(*fakeRunner, string)
		wantSpaces  []string
		wantErr     bool
	}{
		{
			name:      "returns list of spaces from runner",
			realmName: "test-realm",
			setupRunner: func(f *fakeRunner, realmName string) {
				f.ListSpacesFn = func(filterRealmName string) ([]intmodel.Space, error) {
					if filterRealmName != realmName {
						t.Errorf("expected realm filter %q, got %q", realmName, filterRealmName)
					}
					return []intmodel.Space{
						buildTestSpace("space1", realmName),
						buildTestSpace("space2", realmName),
					}, nil
				}
			},
			wantSpaces: []string{"space1", "space2"},
			wantErr:    false,
		},
		{
			name:      "empty list handled correctly",
			realmName: "test-realm",
			setupRunner: func(f *fakeRunner, _ string) {
				f.ListSpacesFn = func(_ string) ([]intmodel.Space, error) {
					return []intmodel.Space{}, nil
				}
			},
			wantSpaces: []string{},
			wantErr:    false,
		},
		{
			name:      "works with empty realm filter",
			realmName: "",
			setupRunner: func(f *fakeRunner, _ string) {
				f.ListSpacesFn = func(_ string) ([]intmodel.Space, error) {
					return []intmodel.Space{
						buildTestSpace("space1", "realm1"),
						buildTestSpace("space2", "realm2"),
					}, nil
				}
			},
			wantSpaces: []string{"space1", "space2"},
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner, tt.realmName)
			}

			ctrl := setupTestController(t, mockRunner)

			spaces, err := ctrl.ListSpaces(tt.realmName)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(spaces) != len(tt.wantSpaces) {
				t.Errorf("expected %d spaces, got %d", len(tt.wantSpaces), len(spaces))
			}
			for i, wantName := range tt.wantSpaces {
				if i < len(spaces) && spaces[i].Metadata.Name != wantName {
					t.Errorf("expected space[%d].Name = %q, got %q", i, wantName, spaces[i].Metadata.Name)
				}
			}
		})
	}
}

// TestListSpaces_RunnerError tests error propagation from runner.
func TestListSpaces_RunnerError(t *testing.T) {
	mockRunner := &fakeRunner{}
	mockRunner.ListSpacesFn = func(_ string) ([]intmodel.Space, error) {
		return nil, errors.New("runner error")
	}

	ctrl := setupTestController(t, mockRunner)

	_, err := ctrl.ListSpaces("test-realm")

	if err == nil {
		t.Fatal("expected error but got none")
	}
}
