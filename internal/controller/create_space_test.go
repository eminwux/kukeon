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

func TestCreateSpace_NewSpaceCreation(t *testing.T) {
	tests := []struct {
		name        string
		spaceName   string
		realmName   string
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.CreateSpaceResult)
		wantErr     bool
	}{
		{
			name:      "successful creation of new space",
			spaceName: "test-space",
			realmName: "test-realm",
			setupRunner: func(f *fakeRunner) {
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return intmodel.Space{}, errdefs.ErrSpaceNotFound
				}
				f.CreateSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return buildTestSpace("test-space", "test-realm"), nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateSpaceResult) {
				if result.MetadataExistsPre {
					t.Error("expected MetadataExistsPre to be false")
				}
				if !result.MetadataExistsPost {
					t.Error("expected MetadataExistsPost to be true")
				}
				if !result.Created {
					t.Error("expected Created to be true")
				}
				if !result.CNINetworkCreated {
					t.Error("expected CNINetworkCreated to be true")
				}
				if !result.CgroupCreated {
					t.Error("expected CgroupCreated to be true")
				}
				if !result.CNINetworkExistsPost {
					t.Error("expected CNINetworkExistsPost to be true")
				}
				if !result.CgroupExistsPost {
					t.Error("expected CgroupExistsPost to be true")
				}
				if result.Space.Metadata.Name != "test-space" {
					t.Errorf("expected space name to be 'test-space', got %q", result.Space.Metadata.Name)
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

			result, err := ctrl.CreateSpace(space)

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

func TestCreateSpace_ExistingSpaceReconciliation(t *testing.T) {
	tests := []struct {
		name        string
		spaceName   string
		realmName   string
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.CreateSpaceResult)
		wantErr     bool
	}{
		{
			name:      "existing space - no resources exist, all created",
			spaceName: "test-space",
			realmName: "test-realm",
			setupRunner: func(f *fakeRunner) {
				existingSpace := buildTestSpace("test-space", "test-realm")
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return existingSpace, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return false, nil
				}
				f.ExistsSpaceCNIConfigFn = func(_ intmodel.Space) (bool, error) {
					return false, nil
				}
				f.EnsureSpaceFn = func(space intmodel.Space) (intmodel.Space, error) {
					return space, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateSpaceResult) {
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
				if result.CNINetworkExistsPre {
					t.Error("expected CNINetworkExistsPre to be false")
				}
				if !result.CgroupCreated {
					t.Error("expected CgroupCreated to be true")
				}
				if !result.CNINetworkCreated {
					t.Error("expected CNINetworkCreated to be true")
				}
			},
			wantErr: false,
		},
		{
			name:      "existing space - all resources exist, none created",
			spaceName: "test-space",
			realmName: "test-realm",
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
				f.EnsureSpaceFn = func(space intmodel.Space) (intmodel.Space, error) {
					return space, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateSpaceResult) {
				if !result.MetadataExistsPre {
					t.Error("expected MetadataExistsPre to be true")
				}
				if result.Created {
					t.Error("expected Created to be false")
				}
				if !result.CgroupExistsPre {
					t.Error("expected CgroupExistsPre to be true")
				}
				if !result.CNINetworkExistsPre {
					t.Error("expected CNINetworkExistsPre to be true")
				}
				if result.CgroupCreated {
					t.Error("expected CgroupCreated to be false")
				}
				if result.CNINetworkCreated {
					t.Error("expected CNINetworkCreated to be false")
				}
			},
			wantErr: false,
		},
		{
			name:      "existing space - mixed state (cgroup exists, CNI network doesn't)",
			spaceName: "test-space",
			realmName: "test-realm",
			setupRunner: func(f *fakeRunner) {
				existingSpace := buildTestSpace("test-space", "test-realm")
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return existingSpace, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.ExistsSpaceCNIConfigFn = func(_ intmodel.Space) (bool, error) {
					return false, nil
				}
				f.EnsureSpaceFn = func(space intmodel.Space) (intmodel.Space, error) {
					return space, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateSpaceResult) {
				if !result.MetadataExistsPre {
					t.Error("expected MetadataExistsPre to be true")
				}
				if result.Created {
					t.Error("expected Created to be false")
				}
				if !result.CgroupExistsPre {
					t.Error("expected CgroupExistsPre to be true")
				}
				if result.CNINetworkExistsPre {
					t.Error("expected CNINetworkExistsPre to be false")
				}
				if result.CgroupCreated {
					t.Error("expected CgroupCreated to be false")
				}
				if !result.CNINetworkCreated {
					t.Error("expected CNINetworkCreated to be true")
				}
			},
			wantErr: false,
		},
		{
			name:      "existing space - mixed state (cgroup doesn't exist, CNI network exists)",
			spaceName: "test-space",
			realmName: "test-realm",
			setupRunner: func(f *fakeRunner) {
				existingSpace := buildTestSpace("test-space", "test-realm")
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return existingSpace, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return false, nil
				}
				f.ExistsSpaceCNIConfigFn = func(_ intmodel.Space) (bool, error) {
					return true, nil
				}
				f.EnsureSpaceFn = func(space intmodel.Space) (intmodel.Space, error) {
					return space, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateSpaceResult) {
				if !result.MetadataExistsPre {
					t.Error("expected MetadataExistsPre to be true")
				}
				if result.Created {
					t.Error("expected Created to be false")
				}
				if result.CgroupExistsPre {
					t.Error("expected CgroupExistsPre to be false")
				}
				if !result.CNINetworkExistsPre {
					t.Error("expected CNINetworkExistsPre to be true")
				}
				if !result.CgroupCreated {
					t.Error("expected CgroupCreated to be true")
				}
				if result.CNINetworkCreated {
					t.Error("expected CNINetworkCreated to be false")
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

			result, err := ctrl.CreateSpace(space)

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

func TestCreateSpace_ValidationErrors(t *testing.T) {
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
			name:      "tab-only space name returns ErrSpaceNameRequired",
			spaceName: "\t",
			realmName: "test-realm",
			wantErr:   errdefs.ErrSpaceNameRequired,
		},
		{
			name:      "newline-only space name returns ErrSpaceNameRequired",
			spaceName: "\n",
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
		{
			name:      "tab-only realm name returns ErrRealmNameRequired",
			spaceName: "test-space",
			realmName: "\t",
			wantErr:   errdefs.ErrRealmNameRequired,
		},
		{
			name:      "newline-only realm name returns ErrRealmNameRequired",
			spaceName: "test-space",
			realmName: "\n",
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

			_, err := ctrl.CreateSpace(space)

			if err == nil {
				t.Fatal("expected error but got none")
			}

			if !errors.Is(err, tt.wantErr) {
				t.Errorf("expected error %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestCreateSpace_DefaultLabels(t *testing.T) {
	tests := []struct {
		name        string
		space       intmodel.Space
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.CreateSpaceResult)
	}{
		{
			name: "labels map is created if nil",
			space: intmodel.Space{
				Metadata: intmodel.SpaceMetadata{
					Name:   "test-space",
					Labels: nil,
				},
				Spec: intmodel.SpaceSpec{
					RealmName: "test-realm",
				},
			},
			setupRunner: func(f *fakeRunner) {
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return intmodel.Space{}, errdefs.ErrSpaceNotFound
				}
				f.CreateSpaceFn = func(space intmodel.Space) (intmodel.Space, error) {
					// Verify labels map was created
					if space.Metadata.Labels == nil {
						return intmodel.Space{}, errors.New("labels map not created")
					}
					return space, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateSpaceResult) {
				if result.Space.Metadata.Labels == nil {
					t.Error("expected labels map to be created")
				}
				if result.Space.Metadata.Labels[consts.KukeonRealmLabelKey] != "test-realm" {
					t.Errorf(
						"expected realm label to be 'test-realm', got %q",
						result.Space.Metadata.Labels[consts.KukeonRealmLabelKey],
					)
				}
				if result.Space.Metadata.Labels[consts.KukeonSpaceLabelKey] != "test-space" {
					t.Errorf(
						"expected space label to be 'test-space', got %q",
						result.Space.Metadata.Labels[consts.KukeonSpaceLabelKey],
					)
				}
			},
		},
		{
			name: "KukeonRealmLabelKey label is set if missing",
			space: intmodel.Space{
				Metadata: intmodel.SpaceMetadata{
					Name: "test-space",
					Labels: map[string]string{
						"custom-label": "custom-value",
					},
				},
				Spec: intmodel.SpaceSpec{
					RealmName: "test-realm",
				},
			},
			setupRunner: func(f *fakeRunner) {
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return intmodel.Space{}, errdefs.ErrSpaceNotFound
				}
				f.CreateSpaceFn = func(space intmodel.Space) (intmodel.Space, error) {
					// Verify default labels were added
					if space.Metadata.Labels[consts.KukeonRealmLabelKey] != "test-realm" {
						return intmodel.Space{}, errors.New("realm label not set")
					}
					if space.Metadata.Labels[consts.KukeonSpaceLabelKey] != "test-space" {
						return intmodel.Space{}, errors.New("space label not set")
					}
					// Verify existing labels are preserved
					if space.Metadata.Labels["custom-label"] != "custom-value" {
						return intmodel.Space{}, errors.New("existing labels not preserved")
					}
					return space, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateSpaceResult) {
				if result.Space.Metadata.Labels[consts.KukeonRealmLabelKey] != "test-realm" {
					t.Errorf(
						"expected realm label to be 'test-realm', got %q",
						result.Space.Metadata.Labels[consts.KukeonRealmLabelKey],
					)
				}
				if result.Space.Metadata.Labels[consts.KukeonSpaceLabelKey] != "test-space" {
					t.Errorf(
						"expected space label to be 'test-space', got %q",
						result.Space.Metadata.Labels[consts.KukeonSpaceLabelKey],
					)
				}
				if result.Space.Metadata.Labels["custom-label"] != "custom-value" {
					t.Error("expected existing labels to be preserved")
				}
			},
		},
		{
			name: "KukeonSpaceLabelKey label is set if missing",
			space: intmodel.Space{
				Metadata: intmodel.SpaceMetadata{
					Name: "test-space",
					Labels: map[string]string{
						consts.KukeonRealmLabelKey: "existing-realm",
					},
				},
				Spec: intmodel.SpaceSpec{
					RealmName: "test-realm",
				},
			},
			setupRunner: func(f *fakeRunner) {
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return intmodel.Space{}, errdefs.ErrSpaceNotFound
				}
				f.CreateSpaceFn = func(space intmodel.Space) (intmodel.Space, error) {
					// Verify space label was added
					if space.Metadata.Labels[consts.KukeonSpaceLabelKey] != "test-space" {
						return intmodel.Space{}, errors.New("space label not set")
					}
					// Verify existing realm label is preserved (not overwritten)
					if space.Metadata.Labels[consts.KukeonRealmLabelKey] != "existing-realm" {
						return intmodel.Space{}, errors.New("existing realm label was overwritten")
					}
					return space, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateSpaceResult) {
				if result.Space.Metadata.Labels[consts.KukeonRealmLabelKey] != "existing-realm" {
					t.Errorf(
						"expected existing realm label to be preserved, got %q",
						result.Space.Metadata.Labels[consts.KukeonRealmLabelKey],
					)
				}
				if result.Space.Metadata.Labels[consts.KukeonSpaceLabelKey] != "test-space" {
					t.Errorf(
						"expected space label to be 'test-space', got %q",
						result.Space.Metadata.Labels[consts.KukeonSpaceLabelKey],
					)
				}
			},
		},
		{
			name: "existing labels are preserved",
			space: intmodel.Space{
				Metadata: intmodel.SpaceMetadata{
					Name: "test-space",
					Labels: map[string]string{
						consts.KukeonRealmLabelKey: "existing-realm",
						consts.KukeonSpaceLabelKey: "existing-space",
						"custom-label":             "custom-value",
					},
				},
				Spec: intmodel.SpaceSpec{
					RealmName: "test-realm",
				},
			},
			setupRunner: func(f *fakeRunner) {
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return intmodel.Space{}, errdefs.ErrSpaceNotFound
				}
				f.CreateSpaceFn = func(space intmodel.Space) (intmodel.Space, error) {
					// Verify existing labels are preserved (not overwritten)
					if space.Metadata.Labels[consts.KukeonRealmLabelKey] != "existing-realm" {
						return intmodel.Space{}, errors.New("existing realm label was overwritten")
					}
					if space.Metadata.Labels[consts.KukeonSpaceLabelKey] != "existing-space" {
						return intmodel.Space{}, errors.New("existing space label was overwritten")
					}
					return space, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateSpaceResult) {
				if result.Space.Metadata.Labels[consts.KukeonRealmLabelKey] != "existing-realm" {
					t.Errorf(
						"expected existing realm label to be preserved, got %q",
						result.Space.Metadata.Labels[consts.KukeonRealmLabelKey],
					)
				}
				if result.Space.Metadata.Labels[consts.KukeonSpaceLabelKey] != "existing-space" {
					t.Errorf(
						"expected existing space label to be preserved, got %q",
						result.Space.Metadata.Labels[consts.KukeonSpaceLabelKey],
					)
				}
				if result.Space.Metadata.Labels["custom-label"] != "custom-value" {
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

			result, err := ctrl.CreateSpace(tt.space)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantResult != nil {
				tt.wantResult(t, result)
			}
		})
	}
}

func TestCreateSpace_RunnerErrors(t *testing.T) {
	tests := []struct {
		name        string
		spaceName   string
		realmName   string
		setupRunner func(*fakeRunner)
		wantErr     error
		errContains string
	}{
		{
			name:      "GetSpace error (non-NotFound) is wrapped with ErrGetSpace",
			spaceName: "test-space",
			realmName: "test-realm",
			setupRunner: func(f *fakeRunner) {
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return intmodel.Space{}, errors.New("unexpected error")
				}
			},
			wantErr:     errdefs.ErrGetSpace,
			errContains: "unexpected error",
		},
		{
			name:      "CreateSpace error is wrapped with ErrCreateSpace",
			spaceName: "test-space",
			realmName: "test-realm",
			setupRunner: func(f *fakeRunner) {
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return intmodel.Space{}, errdefs.ErrSpaceNotFound
				}
				f.CreateSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return intmodel.Space{}, errors.New("creation failed")
				}
			},
			wantErr:     errdefs.ErrCreateSpace,
			errContains: "creation failed",
		},
		{
			name:      "EnsureSpace error is wrapped with ErrCreateSpace",
			spaceName: "test-space",
			realmName: "test-realm",
			setupRunner: func(f *fakeRunner) {
				existingSpace := buildTestSpace("test-space", "test-realm")
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return existingSpace, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return false, nil
				}
				f.ExistsSpaceCNIConfigFn = func(_ intmodel.Space) (bool, error) {
					return false, nil
				}
				f.EnsureSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return intmodel.Space{}, errors.New("ensure failed")
				}
			},
			wantErr:     errdefs.ErrCreateSpace,
			errContains: "ensure failed",
		},
		{
			name:      "ExistsCgroup error is wrapped with descriptive message",
			spaceName: "test-space",
			realmName: "test-realm",
			setupRunner: func(f *fakeRunner) {
				existingSpace := buildTestSpace("test-space", "test-realm")
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return existingSpace, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return false, errors.New("cgroup check failed")
				}
			},
			wantErr:     nil, // Custom error message, not a standard error
			errContains: "failed to check if space cgroup exists",
		},
		{
			name:      "ExistsSpaceCNIConfig error is wrapped with ErrCheckNetworkExists",
			spaceName: "test-space",
			realmName: "test-realm",
			setupRunner: func(f *fakeRunner) {
				existingSpace := buildTestSpace("test-space", "test-realm")
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return existingSpace, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return false, nil
				}
				f.ExistsSpaceCNIConfigFn = func(_ intmodel.Space) (bool, error) {
					return false, errors.New("network check failed")
				}
			},
			wantErr:     errdefs.ErrCheckNetworkExists,
			errContains: "network check failed",
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

			_, err := ctrl.CreateSpace(space)

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

func TestCreateSpace_NameTrimming(t *testing.T) {
	tests := []struct {
		name        string
		spaceName   string
		realmName   string
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.CreateSpaceResult)
		wantErr     bool
		errContains string
	}{
		{
			name:      "space name and realm name with leading/trailing whitespace are trimmed",
			spaceName: "  test-space  ",
			realmName: "  test-realm  ",
			setupRunner: func(f *fakeRunner) {
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return intmodel.Space{}, errdefs.ErrSpaceNotFound
				}
				f.CreateSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					// Return space with trimmed values as the result
					return buildTestSpace("test-space", "test-realm"), nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateSpaceResult) {
				// The result space should have trimmed values
				if result.Space.Metadata.Name != "test-space" {
					t.Errorf(
						"expected space name to be trimmed to 'test-space', got %q",
						result.Space.Metadata.Name,
					)
				}
				if result.Space.Spec.RealmName != "test-realm" {
					t.Errorf(
						"expected realm name to be trimmed to 'test-realm', got %q",
						result.Space.Spec.RealmName,
					)
				}
			},
			wantErr: false,
		},
		{
			name:      "space name that becomes empty after trimming triggers validation error",
			spaceName: "   ",
			realmName: "test-realm",
			setupRunner: func(_ *fakeRunner) {
				// Should not be called due to validation error
			},
			wantResult: nil,
			wantErr:    true,
		},
		{
			name:      "realm name that becomes empty after trimming triggers validation error",
			spaceName: "test-space",
			realmName: "   ",
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
			space := intmodel.Space{
				Metadata: intmodel.SpaceMetadata{
					Name: tt.spaceName,
				},
				Spec: intmodel.SpaceSpec{
					RealmName: tt.realmName,
				},
			}

			result, err := ctrl.CreateSpace(space)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got none")
				}
				if !errors.Is(err, errdefs.ErrSpaceNameRequired) && !errors.Is(err, errdefs.ErrRealmNameRequired) {
					t.Errorf("expected ErrSpaceNameRequired or ErrRealmNameRequired, got %v", err)
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
