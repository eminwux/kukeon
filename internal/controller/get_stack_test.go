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

// TestGetStack_SuccessfulRetrieval tests successful retrieval of stack with all state flags.
func TestGetStack_SuccessfulRetrieval(t *testing.T) {
	tests := []struct {
		name        string
		stackName   string
		realmName   string
		spaceName   string
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.GetStackResult)
		wantErr     bool
	}{
		{
			name:      "stack exists with all resources",
			stackName: "test-stack",
			realmName: "test-realm",
			spaceName: "test-space",
			setupRunner: func(f *fakeRunner) {
				stack := buildTestStack("test-stack", "test-realm", "test-space")
				f.GetStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return stack, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
			},
			wantResult: func(t *testing.T, result controller.GetStackResult) {
				if !result.MetadataExists {
					t.Error("expected MetadataExists to be true")
				}
				if !result.CgroupExists {
					t.Error("expected CgroupExists to be true")
				}
				if result.Stack.Metadata.Name != "test-stack" {
					t.Errorf("expected stack name 'test-stack', got %q", result.Stack.Metadata.Name)
				}
			},
			wantErr: false,
		},
		{
			name:      "stack exists with no resources",
			stackName: "test-stack",
			realmName: "test-realm",
			spaceName: "test-space",
			setupRunner: func(f *fakeRunner) {
				stack := buildTestStack("test-stack", "test-realm", "test-space")
				f.GetStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return stack, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return false, nil
				}
			},
			wantResult: func(t *testing.T, result controller.GetStackResult) {
				if !result.MetadataExists {
					t.Error("expected MetadataExists to be true")
				}
				if result.CgroupExists {
					t.Error("expected CgroupExists to be false")
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
			stack := intmodel.Stack{
				Metadata: intmodel.StackMetadata{
					Name: tt.stackName,
				},
				Spec: intmodel.StackSpec{
					RealmName: tt.realmName,
					SpaceName: tt.spaceName,
				},
			}

			result, err := ctrl.GetStack(stack)

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

// TestGetStack_ValidationErrors tests validation errors for stack name, realm name, and space name.
func TestGetStack_ValidationErrors(t *testing.T) {
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

			_, err := ctrl.GetStack(stack)

			if err == nil {
				t.Fatal("expected error but got none")
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("expected error %v, got %v", tt.wantErr, err)
			}
		})
	}
}

// TestGetStack_NotFound tests stack not found scenario.
func TestGetStack_NotFound(t *testing.T) {
	tests := []struct {
		name        string
		stackName   string
		realmName   string
		spaceName   string
		setupRunner func(*fakeRunner)
		wantResult  func(t *testing.T, result controller.GetStackResult)
		wantErr     bool
	}{
		{
			name:      "stack not found - ErrStackNotFound",
			stackName: "test-stack",
			realmName: "test-realm",
			spaceName: "test-space",
			setupRunner: func(f *fakeRunner) {
				f.GetStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return intmodel.Stack{}, errdefs.ErrStackNotFound
				}
			},
			wantResult: func(t *testing.T, result controller.GetStackResult) {
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
			stack := intmodel.Stack{
				Metadata: intmodel.StackMetadata{
					Name: tt.stackName,
				},
				Spec: intmodel.StackSpec{
					RealmName: tt.realmName,
					SpaceName: tt.spaceName,
				},
			}

			result, err := ctrl.GetStack(stack)

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

// TestGetStack_RunnerErrors tests error propagation from runner methods.
func TestGetStack_RunnerErrors(t *testing.T) {
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
			name:      "GetStack error (non-NotFound) wrapped with ErrGetStack",
			stackName: "test-stack",
			realmName: "test-realm",
			spaceName: "test-space",
			setupRunner: func(f *fakeRunner) {
				f.GetStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return intmodel.Stack{}, errors.New("runner error")
				}
			},
			wantErr: errdefs.ErrGetStack,
		},
		{
			name:      "ExistsCgroup error wrapped with descriptive message",
			stackName: "test-stack",
			realmName: "test-realm",
			spaceName: "test-space",
			setupRunner: func(f *fakeRunner) {
				stack := buildTestStack("test-stack", "test-realm", "test-space")
				f.GetStackFn = func(_ intmodel.Stack) (intmodel.Stack, error) {
					return stack, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return false, errors.New("cgroup check failed")
				}
			},
			wantErr:     nil,
			errContains: "failed to check if stack cgroup exists",
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

			_, err := ctrl.GetStack(stack)

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

// TestGetStack_NameTrimming tests name trimming.
func TestGetStack_NameTrimming(t *testing.T) {
	tests := []struct {
		name        string
		stackName   string
		realmName   string
		spaceName   string
		setupRunner func(*fakeRunner, string, string, string)
		wantResult  func(t *testing.T, result controller.GetStackResult)
		wantErr     bool
	}{
		{
			name:      "stack name, realm name, and space name trimmed",
			stackName: "  test-stack  ",
			realmName: "  test-realm  ",
			spaceName: "  test-space  ",
			setupRunner: func(f *fakeRunner, expectedStackName, expectedRealmName, expectedSpaceName string) {
				stack := buildTestStack(expectedStackName, expectedRealmName, expectedSpaceName)
				f.GetStackFn = func(received intmodel.Stack) (intmodel.Stack, error) {
					if received.Metadata.Name != expectedStackName {
						t.Errorf("expected trimmed stack name %q, got %q", expectedStackName, received.Metadata.Name)
					}
					if received.Spec.RealmName != expectedRealmName {
						t.Errorf("expected trimmed realm name %q, got %q", expectedRealmName, received.Spec.RealmName)
					}
					if received.Spec.SpaceName != expectedSpaceName {
						t.Errorf("expected trimmed space name %q, got %q", expectedSpaceName, received.Spec.SpaceName)
					}
					return stack, nil
				}
				f.ExistsCgroupFn = func(_ any) (bool, error) {
					return true, nil
				}
			},
			wantResult: func(t *testing.T, result controller.GetStackResult) {
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
				tt.setupRunner(mockRunner, "test-stack", "test-realm", "test-space")
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

			result, err := ctrl.GetStack(stack)

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

// TestListStacks_SuccessfulRetrieval tests successful retrieval of stacks list.
func TestListStacks_SuccessfulRetrieval(t *testing.T) {
	tests := []struct {
		name        string
		realmName   string
		spaceName   string
		setupRunner func(*fakeRunner, string, string)
		wantStacks  []string
		wantErr     bool
	}{
		{
			name:      "returns list of stacks from runner",
			realmName: "test-realm",
			spaceName: "test-space",
			setupRunner: func(f *fakeRunner, realmName, spaceName string) {
				f.ListStacksFn = func(filterRealmName, filterSpaceName string) ([]intmodel.Stack, error) {
					if filterRealmName != realmName {
						t.Errorf("expected realm filter %q, got %q", realmName, filterRealmName)
					}
					if filterSpaceName != spaceName {
						t.Errorf("expected space filter %q, got %q", spaceName, filterSpaceName)
					}
					return []intmodel.Stack{
						buildTestStack("stack1", realmName, spaceName),
						buildTestStack("stack2", realmName, spaceName),
					}, nil
				}
			},
			wantStacks: []string{"stack1", "stack2"},
			wantErr:    false,
		},
		{
			name:      "empty list handled correctly",
			realmName: "test-realm",
			spaceName: "test-space",
			setupRunner: func(f *fakeRunner, _ string, _ string) {
				f.ListStacksFn = func(_ string, _ string) ([]intmodel.Stack, error) {
					return []intmodel.Stack{}, nil
				}
			},
			wantStacks: []string{},
			wantErr:    false,
		},
		{
			name:      "works with empty filters",
			realmName: "",
			spaceName: "",
			setupRunner: func(f *fakeRunner, _ string, _ string) {
				f.ListStacksFn = func(_ string, _ string) ([]intmodel.Stack, error) {
					return []intmodel.Stack{
						buildTestStack("stack1", "realm1", "space1"),
						buildTestStack("stack2", "realm2", "space2"),
					}, nil
				}
			},
			wantStacks: []string{"stack1", "stack2"},
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &fakeRunner{}
			if tt.setupRunner != nil {
				tt.setupRunner(mockRunner, tt.realmName, tt.spaceName)
			}

			ctrl := setupTestController(t, mockRunner)

			stacks, err := ctrl.ListStacks(tt.realmName, tt.spaceName)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(stacks) != len(tt.wantStacks) {
				t.Errorf("expected %d stacks, got %d", len(tt.wantStacks), len(stacks))
			}
			for i, wantName := range tt.wantStacks {
				if i < len(stacks) && stacks[i].Metadata.Name != wantName {
					t.Errorf("expected stack[%d].Name = %q, got %q", i, wantName, stacks[i].Metadata.Name)
				}
			}
		})
	}
}

// TestListStacks_RunnerError tests error propagation from runner.
func TestListStacks_RunnerError(t *testing.T) {
	mockRunner := &fakeRunner{}
	mockRunner.ListStacksFn = func(_ string, _ string) ([]intmodel.Stack, error) {
		return nil, errors.New("runner error")
	}

	ctrl := setupTestController(t, mockRunner)

	_, err := ctrl.ListStacks("test-realm", "test-space")

	if err == nil {
		t.Fatal("expected error but got none")
	}
}
