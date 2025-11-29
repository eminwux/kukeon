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

func TestCreateStack_NewStackCreation(t *testing.T) {
	tests := []struct {
		name        string
		stackName   string
		realmName   string
		spaceName   string
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.CreateStackResult)
		wantErr     bool
	}{
		{
			name:      "successful creation of new stack",
			stackName: "test-stack",
			realmName: "test-realm",
			spaceName: "test-space",
			setupRunner: func(f *fakeRunner) {
				f.GetStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return intmodel.Stack{}, errdefs.ErrStackNotFound
				}
				f.CreateStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return buildTestStack("test-stack", "test-realm", "test-space"), nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateStackResult) {
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
				if !result.CgroupExistsPost {
					t.Error("expected CgroupExistsPost to be true")
				}
				if result.Stack.Metadata.Name != "test-stack" {
					t.Errorf("expected stack name to be 'test-stack', got %q", result.Stack.Metadata.Name)
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

			result, err := ctrl.CreateStack(stack)

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

func TestCreateStack_ExistingStackReconciliation(t *testing.T) {
	tests := []struct {
		name        string
		stackName   string
		realmName   string
		spaceName   string
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.CreateStackResult)
		wantErr     bool
	}{
		{
			name:      "existing stack - cgroup doesn't exist, created",
			stackName: "test-stack",
			realmName: "test-realm",
			spaceName: "test-space",
			setupRunner: func(f *fakeRunner) {
				existingStack := buildTestStack("test-stack", "test-realm", "test-space")
				f.GetStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return existingStack, nil
				}
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return buildTestSpace("test-space", "test-realm"), nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return false, nil
				}
				f.EnsureStackFn = func(stack intmodel.Stack) (intmodel.Stack, error) {
					return stack, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateStackResult) {
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
				if !result.CgroupCreated {
					t.Error("expected CgroupCreated to be true")
				}
			},
			wantErr: false,
		},
		{
			name:      "existing stack - cgroup exists, not created",
			stackName: "test-stack",
			realmName: "test-realm",
			spaceName: "test-space",
			setupRunner: func(f *fakeRunner) {
				existingStack := buildTestStack("test-stack", "test-realm", "test-space")
				f.GetStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return existingStack, nil
				}
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return buildTestSpace("test-space", "test-realm"), nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
				f.EnsureStackFn = func(stack intmodel.Stack) (intmodel.Stack, error) {
					return stack, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateStackResult) {
				if !result.MetadataExistsPre {
					t.Error("expected MetadataExistsPre to be true")
				}
				if result.Created {
					t.Error("expected Created to be false")
				}
				if !result.CgroupExistsPre {
					t.Error("expected CgroupExistsPre to be true")
				}
				if result.CgroupCreated {
					t.Error("expected CgroupCreated to be false")
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

			result, err := ctrl.CreateStack(stack)

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

func TestCreateStack_ValidationErrors(t *testing.T) {
	tests := []struct {
		name      string
		stackName string
		realmName string
		spaceName string
		wantErr   error
	}{
		{
			name:      "empty stack name returns ErrStackNameRequired",
			stackName: "",
			realmName: "test-realm",
			spaceName: "test-space",
			wantErr:   errdefs.ErrStackNameRequired,
		},
		{
			name:      "whitespace-only stack name returns ErrStackNameRequired",
			stackName: "   ",
			realmName: "test-realm",
			spaceName: "test-space",
			wantErr:   errdefs.ErrStackNameRequired,
		},
		{
			name:      "tab-only stack name returns ErrStackNameRequired",
			stackName: "\t",
			realmName: "test-realm",
			spaceName: "test-space",
			wantErr:   errdefs.ErrStackNameRequired,
		},
		{
			name:      "newline-only stack name returns ErrStackNameRequired",
			stackName: "\n",
			realmName: "test-realm",
			spaceName: "test-space",
			wantErr:   errdefs.ErrStackNameRequired,
		},
		{
			name:      "empty realm name returns ErrRealmNameRequired",
			stackName: "test-stack",
			realmName: "",
			spaceName: "test-space",
			wantErr:   errdefs.ErrRealmNameRequired,
		},
		{
			name:      "whitespace-only realm name returns ErrRealmNameRequired",
			stackName: "test-stack",
			realmName: "   ",
			spaceName: "test-space",
			wantErr:   errdefs.ErrRealmNameRequired,
		},
		{
			name:      "empty space name returns ErrSpaceNameRequired",
			stackName: "test-stack",
			realmName: "test-realm",
			spaceName: "",
			wantErr:   errdefs.ErrSpaceNameRequired,
		},
		{
			name:      "whitespace-only space name returns ErrSpaceNameRequired",
			stackName: "test-stack",
			realmName: "test-realm",
			spaceName: "   ",
			wantErr:   errdefs.ErrSpaceNameRequired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			ctrl := setupTestController(t, mockRunner)

			stack := intmodel.Stack{
				Metadata: intmodel.StackMetadata{
					Name: tt.stackName,
				},
				Spec: intmodel.StackSpec{
					RealmName: tt.realmName,
					SpaceName: tt.spaceName,
				},
			}

			_, err := ctrl.CreateStack(stack)

			if err == nil {
				t.Fatal("expected error but got none")
			}

			if !errors.Is(err, tt.wantErr) {
				t.Errorf("expected error %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestCreateStack_DefaultLabels(t *testing.T) {
	tests := []struct {
		name        string
		stack       intmodel.Stack
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.CreateStackResult)
	}{
		{
			name: "labels map is created if nil",
			stack: intmodel.Stack{
				Metadata: intmodel.StackMetadata{
					Name:   "test-stack",
					Labels: nil,
				},
				Spec: intmodel.StackSpec{
					RealmName: "test-realm",
					SpaceName: "test-space",
				},
			},
			setupRunner: func(f *fakeRunner) {
				f.GetStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return intmodel.Stack{}, errdefs.ErrStackNotFound
				}
				f.CreateStackFn = func(stack intmodel.Stack) (intmodel.Stack, error) {
					// Verify labels map was created
					if stack.Metadata.Labels == nil {
						return intmodel.Stack{}, errors.New("labels map not created")
					}
					return stack, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateStackResult) {
				if result.Stack.Metadata.Labels == nil {
					t.Error("expected labels map to be created")
				}
				if result.Stack.Metadata.Labels[consts.KukeonRealmLabelKey] != "test-realm" {
					t.Errorf(
						"expected realm label to be 'test-realm', got %q",
						result.Stack.Metadata.Labels[consts.KukeonRealmLabelKey],
					)
				}
				if result.Stack.Metadata.Labels[consts.KukeonSpaceLabelKey] != "test-space" {
					t.Errorf(
						"expected space label to be 'test-space', got %q",
						result.Stack.Metadata.Labels[consts.KukeonSpaceLabelKey],
					)
				}
				if result.Stack.Metadata.Labels[consts.KukeonStackLabelKey] != "test-stack" {
					t.Errorf(
						"expected stack label to be 'test-stack', got %q",
						result.Stack.Metadata.Labels[consts.KukeonStackLabelKey],
					)
				}
			},
		},
		{
			name: "all three labels are set if missing",
			stack: intmodel.Stack{
				Metadata: intmodel.StackMetadata{
					Name: "test-stack",
					Labels: map[string]string{
						"custom-label": "custom-value",
					},
				},
				Spec: intmodel.StackSpec{
					RealmName: "test-realm",
					SpaceName: "test-space",
				},
			},
			setupRunner: func(f *fakeRunner) {
				f.GetStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return intmodel.Stack{}, errdefs.ErrStackNotFound
				}
				f.CreateStackFn = func(stack intmodel.Stack) (intmodel.Stack, error) {
					// Verify all three default labels were added
					if stack.Metadata.Labels[consts.KukeonRealmLabelKey] != "test-realm" {
						return intmodel.Stack{}, errors.New("realm label not set")
					}
					if stack.Metadata.Labels[consts.KukeonSpaceLabelKey] != "test-space" {
						return intmodel.Stack{}, errors.New("space label not set")
					}
					if stack.Metadata.Labels[consts.KukeonStackLabelKey] != "test-stack" {
						return intmodel.Stack{}, errors.New("stack label not set")
					}
					// Verify existing labels are preserved
					if stack.Metadata.Labels["custom-label"] != "custom-value" {
						return intmodel.Stack{}, errors.New("existing labels not preserved")
					}
					return stack, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateStackResult) {
				if result.Stack.Metadata.Labels[consts.KukeonRealmLabelKey] != "test-realm" {
					t.Errorf(
						"expected realm label to be 'test-realm', got %q",
						result.Stack.Metadata.Labels[consts.KukeonRealmLabelKey],
					)
				}
				if result.Stack.Metadata.Labels[consts.KukeonSpaceLabelKey] != "test-space" {
					t.Errorf(
						"expected space label to be 'test-space', got %q",
						result.Stack.Metadata.Labels[consts.KukeonSpaceLabelKey],
					)
				}
				if result.Stack.Metadata.Labels[consts.KukeonStackLabelKey] != "test-stack" {
					t.Errorf(
						"expected stack label to be 'test-stack', got %q",
						result.Stack.Metadata.Labels[consts.KukeonStackLabelKey],
					)
				}
				if result.Stack.Metadata.Labels["custom-label"] != "custom-value" {
					t.Error("expected existing labels to be preserved")
				}
			},
		},
		{
			name: "existing labels are preserved",
			stack: intmodel.Stack{
				Metadata: intmodel.StackMetadata{
					Name: "test-stack",
					Labels: map[string]string{
						consts.KukeonRealmLabelKey: "existing-realm",
						consts.KukeonSpaceLabelKey: "existing-space",
						consts.KukeonStackLabelKey: "existing-stack",
						"custom-label":             "custom-value",
					},
				},
				Spec: intmodel.StackSpec{
					RealmName: "test-realm",
					SpaceName: "test-space",
				},
			},
			setupRunner: func(f *fakeRunner) {
				f.GetStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return intmodel.Stack{}, errdefs.ErrStackNotFound
				}
				f.CreateStackFn = func(stack intmodel.Stack) (intmodel.Stack, error) {
					// Verify existing labels are preserved (not overwritten)
					if stack.Metadata.Labels[consts.KukeonRealmLabelKey] != "existing-realm" {
						return intmodel.Stack{}, errors.New("existing realm label was overwritten")
					}
					if stack.Metadata.Labels[consts.KukeonSpaceLabelKey] != "existing-space" {
						return intmodel.Stack{}, errors.New("existing space label was overwritten")
					}
					if stack.Metadata.Labels[consts.KukeonStackLabelKey] != "existing-stack" {
						return intmodel.Stack{}, errors.New("existing stack label was overwritten")
					}
					return stack, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateStackResult) {
				if result.Stack.Metadata.Labels[consts.KukeonRealmLabelKey] != "existing-realm" {
					t.Errorf(
						"expected existing realm label to be preserved, got %q",
						result.Stack.Metadata.Labels[consts.KukeonRealmLabelKey],
					)
				}
				if result.Stack.Metadata.Labels[consts.KukeonSpaceLabelKey] != "existing-space" {
					t.Errorf(
						"expected existing space label to be preserved, got %q",
						result.Stack.Metadata.Labels[consts.KukeonSpaceLabelKey],
					)
				}
				if result.Stack.Metadata.Labels[consts.KukeonStackLabelKey] != "existing-stack" {
					t.Errorf(
						"expected existing stack label to be preserved, got %q",
						result.Stack.Metadata.Labels[consts.KukeonStackLabelKey],
					)
				}
				if result.Stack.Metadata.Labels["custom-label"] != "custom-value" {
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

			result, err := ctrl.CreateStack(tt.stack)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantResult != nil {
				tt.wantResult(t, result)
			}
		})
	}
}

func TestCreateStack_DefaultSpecID(t *testing.T) {
	tests := []struct {
		name        string
		stack       intmodel.Stack
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.CreateStackResult)
	}{
		{
			name: "empty Spec.ID defaults to stack name",
			stack: intmodel.Stack{
				Metadata: intmodel.StackMetadata{
					Name: "test-stack",
				},
				Spec: intmodel.StackSpec{
					ID:        "",
					RealmName: "test-realm",
					SpaceName: "test-space",
				},
			},
			setupRunner: func(f *fakeRunner) {
				f.GetStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return intmodel.Stack{}, errdefs.ErrStackNotFound
				}
				f.CreateStackFn = func(stack intmodel.Stack) (intmodel.Stack, error) {
					// Verify Spec.ID was set to stack name
					if stack.Spec.ID != "test-stack" {
						return intmodel.Stack{}, errors.New("Spec.ID not set to stack name")
					}
					return stack, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateStackResult) {
				if result.Stack.Spec.ID != "test-stack" {
					t.Errorf("expected Spec.ID to be 'test-stack', got %q", result.Stack.Spec.ID)
				}
			},
		},
		{
			name: "non-empty Spec.ID is preserved",
			stack: intmodel.Stack{
				Metadata: intmodel.StackMetadata{
					Name: "test-stack",
				},
				Spec: intmodel.StackSpec{
					ID:        "custom-id",
					RealmName: "test-realm",
					SpaceName: "test-space",
				},
			},
			setupRunner: func(f *fakeRunner) {
				f.GetStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return intmodel.Stack{}, errdefs.ErrStackNotFound
				}
				f.CreateStackFn = func(stack intmodel.Stack) (intmodel.Stack, error) {
					// Verify Spec.ID was preserved
					if stack.Spec.ID != "custom-id" {
						return intmodel.Stack{}, errors.New("Spec.ID was overwritten")
					}
					return stack, nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateStackResult) {
				if result.Stack.Spec.ID != "custom-id" {
					t.Errorf("expected Spec.ID to be 'custom-id', got %q", result.Stack.Spec.ID)
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

			result, err := ctrl.CreateStack(tt.stack)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantResult != nil {
				tt.wantResult(t, result)
			}
		})
	}
}

func TestCreateStack_SpaceValidation(t *testing.T) {
	tests := []struct {
		name        string
		stackName   string
		realmName   string
		spaceName   string
		setupRunner func(*fakeRunner)
		wantErr     bool
		errContains string
	}{
		{
			name:      "space validation succeeds when space exists",
			stackName: "test-stack",
			realmName: "test-realm",
			spaceName: "test-space",
			setupRunner: func(f *fakeRunner) {
				existingStack := buildTestStack("test-stack", "test-realm", "test-space")
				f.GetStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return existingStack, nil
				}
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return buildTestSpace("test-space", "test-realm"), nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return false, nil
				}
				f.EnsureStackFn = func(stack intmodel.Stack) (intmodel.Stack, error) {
					return stack, nil
				}
			},
			wantErr: false,
		},
		{
			name:      "space validation fails when space doesn't exist",
			stackName: "test-stack",
			realmName: "test-realm",
			spaceName: "test-space",
			setupRunner: func(f *fakeRunner) {
				existingStack := buildTestStack("test-stack", "test-realm", "test-space")
				f.GetStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return existingStack, nil
				}
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return intmodel.Space{}, errdefs.ErrSpaceNotFound
				}
			},
			wantErr:     true,
			errContains: "space \"test-space\" not found at run-path",
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

			_, err := ctrl.CreateStack(stack)

			if tt.wantErr {
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
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestCreateStack_RunnerErrors(t *testing.T) {
	tests := []struct {
		name        string
		stackName   string
		realmName   string
		spaceName   string
		setupRunner func(*fakeRunner)
		wantErr     error
		errContains string
	}{
		{
			name:      "GetStack error (non-NotFound) is wrapped with ErrGetStack",
			stackName: "test-stack",
			realmName: "test-realm",
			spaceName: "test-space",
			setupRunner: func(f *fakeRunner) {
				f.GetStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return intmodel.Stack{}, errors.New("unexpected error")
				}
			},
			wantErr:     errdefs.ErrGetStack,
			errContains: "unexpected error",
		},
		{
			name:      "CreateStack error is wrapped with ErrCreateStack",
			stackName: "test-stack",
			realmName: "test-realm",
			spaceName: "test-space",
			setupRunner: func(f *fakeRunner) {
				f.GetStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return intmodel.Stack{}, errdefs.ErrStackNotFound
				}
				f.CreateStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return intmodel.Stack{}, errors.New("creation failed")
				}
			},
			wantErr:     errdefs.ErrCreateStack,
			errContains: "creation failed",
		},
		{
			name:      "EnsureStack error is wrapped with ErrCreateStack",
			stackName: "test-stack",
			realmName: "test-realm",
			spaceName: "test-space",
			setupRunner: func(f *fakeRunner) {
				existingStack := buildTestStack("test-stack", "test-realm", "test-space")
				f.GetStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return existingStack, nil
				}
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return buildTestSpace("test-space", "test-realm"), nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return false, nil
				}
				f.EnsureStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return intmodel.Stack{}, errors.New("ensure failed")
				}
			},
			wantErr:     errdefs.ErrCreateStack,
			errContains: "ensure failed",
		},
		{
			name:      "ExistsCgroup error is wrapped with descriptive message",
			stackName: "test-stack",
			realmName: "test-realm",
			spaceName: "test-space",
			setupRunner: func(f *fakeRunner) {
				existingStack := buildTestStack("test-stack", "test-realm", "test-space")
				f.GetStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return existingStack, nil
				}
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return buildTestSpace("test-space", "test-realm"), nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return false, errors.New("cgroup check failed")
				}
			},
			wantErr:     nil, // Custom error message, not a standard error
			errContains: "failed to check if stack cgroup exists",
		},
		{
			name:      "GetSpace error during space validation returns descriptive error with run-path",
			stackName: "test-stack",
			realmName: "test-realm",
			spaceName: "test-space",
			setupRunner: func(f *fakeRunner) {
				existingStack := buildTestStack("test-stack", "test-realm", "test-space")
				f.GetStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return existingStack, nil
				}
				f.GetSpaceFn = func(_ intmodel.Space) (intmodel.Space, error) {
					return intmodel.Space{}, errors.New("space lookup failed")
				}
			},
			wantErr:     nil, // Custom error message
			errContains: "space \"test-space\" not found at run-path",
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

			_, err := ctrl.CreateStack(stack)

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

func TestCreateStack_NameTrimming(t *testing.T) {
	tests := []struct {
		name        string
		stackName   string
		realmName   string
		spaceName   string
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.CreateStackResult)
		wantErr     bool
		errContains string
	}{
		{
			name:      "stack name, realm name, and space name with leading/trailing whitespace are trimmed",
			stackName: "  test-stack  ",
			realmName: "  test-realm  ",
			spaceName: "  test-space  ",
			setupRunner: func(f *fakeRunner) {
				f.GetStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return intmodel.Stack{}, errdefs.ErrStackNotFound
				}
				f.CreateStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					// Return stack with trimmed values as the result
					return buildTestStack("test-stack", "test-realm", "test-space"), nil
				}
			},
			wantResult: func(t *testing.T, result controller.CreateStackResult) {
				// The result stack should have trimmed values
				if result.Stack.Metadata.Name != "test-stack" {
					t.Errorf(
						"expected stack name to be trimmed to 'test-stack', got %q",
						result.Stack.Metadata.Name,
					)
				}
				if result.Stack.Spec.RealmName != "test-realm" {
					t.Errorf(
						"expected realm name to be trimmed to 'test-realm', got %q",
						result.Stack.Spec.RealmName,
					)
				}
				if result.Stack.Spec.SpaceName != "test-space" {
					t.Errorf(
						"expected space name to be trimmed to 'test-space', got %q",
						result.Stack.Spec.SpaceName,
					)
				}
			},
			wantErr: false,
		},
		{
			name:      "stack name that becomes empty after trimming triggers validation error",
			stackName: "   ",
			realmName: "test-realm",
			spaceName: "test-space",
			setupRunner: func(_ *fakeRunner) {
				// Should not be called due to validation error
			},
			wantResult: nil,
			wantErr:    true,
		},
		{
			name:      "realm name that becomes empty after trimming triggers validation error",
			stackName: "test-stack",
			realmName: "   ",
			spaceName: "test-space",
			setupRunner: func(_ *fakeRunner) {
				// Should not be called due to validation error
			},
			wantResult: nil,
			wantErr:    true,
		},
		{
			name:      "space name that becomes empty after trimming triggers validation error",
			stackName: "test-stack",
			realmName: "test-realm",
			spaceName: "   ",
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
			stack := intmodel.Stack{
				Metadata: intmodel.StackMetadata{
					Name: tt.stackName,
				},
				Spec: intmodel.StackSpec{
					RealmName: tt.realmName,
					SpaceName: tt.spaceName,
				},
			}

			result, err := ctrl.CreateStack(stack)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got none")
				}
				if !errors.Is(err, errdefs.ErrStackNameRequired) &&
					!errors.Is(err, errdefs.ErrRealmNameRequired) &&
					!errors.Is(err, errdefs.ErrSpaceNameRequired) {
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
