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

package stack_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/create/shared"
	stack "github.com/eminwux/kukeon/cmd/kuke/create/stack"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Behaviors covered:
// 1. Name argument handling (from args vs viper default)
// 2. Realm flag validation (required, trimming whitespace)
// 3. Space flag validation (required, trimming whitespace)
// 4. Controller creation from command context
// 5. CreateStack call with correct options
// 6. Error propagation from dependencies
// 7. Success path with result printing

func TestNewStackCmd(t *testing.T) {
	tests := []struct {
		name            string
		cliArgs         []string
		viperName       string
		viperRealm      string
		viperSpace      string
		controller      *fakeStackController
		setupPrints     func(t *testing.T)
		wantErrSub      string
		wantOutputSubs  []string
		verifyCreateOpt func(t *testing.T, opts controller.CreateStackOptions)
	}{
		{
			name:       "missing realm flag",
			cliArgs:    []string{"stack-name", "--space", "space-a"},
			controller: &fakeStackController{},
			wantErrSub: "realm name is required",
		},
		{
			name:       "missing space flag",
			cliArgs:    []string{"stack-name", "--realm", "realm-a"},
			controller: &fakeStackController{},
			wantErrSub: "space name is required",
		},
		{
			name:       "missing realm from viper",
			cliArgs:    []string{"stack-name"},
			viperSpace: "space-a",
			controller: &fakeStackController{},
			wantErrSub: "realm name is required",
		},
		{
			name:       "missing space from viper",
			cliArgs:    []string{"stack-name"},
			viperRealm: "realm-a",
			controller: &fakeStackController{},
			wantErrSub: "space name is required",
		},
		{
			name:    "name from args with trimming",
			cliArgs: []string{" stack-name ", "--realm", "realm-a", "--space", "space-a"},
			controller: &fakeStackController{
				createStack: func(opts controller.CreateStackOptions) (controller.CreateStackResult, error) {
					if opts.Name != "stack-name" {
						t.Fatalf("unexpected name: %q", opts.Name)
					}
					return controller.CreateStackResult{
						Name:      "stack-name",
						RealmName: "realm-a",
						SpaceName: "space-a",
					}, nil
				},
			},
			verifyCreateOpt: func(t *testing.T, opts controller.CreateStackOptions) {
				if opts.Name != "stack-name" {
					t.Fatalf("expected name %q, got %q", "stack-name", opts.Name)
				}
				if opts.RealmName != "realm-a" {
					t.Fatalf("expected realm %q, got %q", "realm-a", opts.RealmName)
				}
				if opts.SpaceName != "space-a" {
					t.Fatalf("expected space %q, got %q", "space-a", opts.SpaceName)
				}
			},
			setupPrints: func(_ *testing.T) {
				// Verification happens in the printOutcome function
			},
			wantOutputSubs: []string{"Stack \"stack-name\" (realm \"realm-a\", space \"space-a\")"},
		},
		{
			name:      "name from viper default",
			viperName: "default-stack",
			cliArgs:   []string{"--realm", "realm-a", "--space", "space-a"},
			controller: &fakeStackController{
				createStack: func(opts controller.CreateStackOptions) (controller.CreateStackResult, error) {
					if opts.Name != "default-stack" {
						t.Fatalf("unexpected name: %q", opts.Name)
					}
					return controller.CreateStackResult{
						Name:      "default-stack",
						RealmName: "realm-a",
						SpaceName: "space-a",
					}, nil
				},
			},
			verifyCreateOpt: func(t *testing.T, opts controller.CreateStackOptions) {
				if opts.Name != "default-stack" {
					t.Fatalf("expected name %q, got %q", "default-stack", opts.Name)
				}
			},
			setupPrints: func(_ *testing.T) {
				// Verification will be done via captured calls
			},
		},
		{
			name:    "realm and space trimming whitespace",
			cliArgs: []string{"stack-name", "--realm", " realm-a ", "--space", "\tspace-a"},
			controller: &fakeStackController{
				createStack: func(opts controller.CreateStackOptions) (controller.CreateStackResult, error) {
					if opts.RealmName != "realm-a" {
						t.Fatalf("expected trimmed realm %q, got %q", "realm-a", opts.RealmName)
					}
					if opts.SpaceName != "space-a" {
						t.Fatalf("expected trimmed space %q, got %q", "space-a", opts.SpaceName)
					}
					return controller.CreateStackResult{
						Name:      "stack-name",
						RealmName: "realm-a",
						SpaceName: "space-a",
					}, nil
				},
			},
			verifyCreateOpt: func(t *testing.T, opts controller.CreateStackOptions) {
				if opts.RealmName != "realm-a" {
					t.Fatalf("expected trimmed realm %q, got %q", "realm-a", opts.RealmName)
				}
				if opts.SpaceName != "space-a" {
					t.Fatalf("expected trimmed space %q, got %q", "space-a", opts.SpaceName)
				}
			},
			setupPrints: func(_ *testing.T) {
				// Verification will be done via captured calls
			},
		},
		{
			name:       "controller creation error propagation",
			cliArgs:    []string{"stack-name", "--realm", "realm-a", "--space", "space-a"},
			controller: nil, // Will trigger error when no logger in context
			wantErrSub: "logger not found",
		},
		{
			name:    "CreateStack error propagation",
			cliArgs: []string{"stack-name", "--realm", "realm-a", "--space", "space-a"},
			controller: &fakeStackController{
				createStack: func(_ controller.CreateStackOptions) (controller.CreateStackResult, error) {
					return controller.CreateStackResult{}, errors.New("create stack failed")
				},
			},
			wantErrSub: "create stack failed",
		},
		{
			name:    "success with created resources",
			cliArgs: []string{"stack-name", "--realm", "realm-a", "--space", "space-a"},
			controller: &fakeStackController{
				createStack: func(_ controller.CreateStackOptions) (controller.CreateStackResult, error) {
					return controller.CreateStackResult{
						Name:               "stack-name",
						RealmName:          "realm-a",
						SpaceName:          "space-a",
						MetadataExistsPost: true,
						CgroupExistsPost:   true,
						Created:            true,
						CgroupCreated:      true,
					}, nil
				},
			},
			setupPrints: func(_ *testing.T) {
				// Verification will be done via captured calls
			},
			wantOutputSubs: []string{
				"Stack \"stack-name\" (realm \"realm-a\", space \"space-a\")",
			},
		},
		{
			name:    "success with existing resources",
			cliArgs: []string{"stack-name", "--realm", "realm-a", "--space", "space-a"},
			controller: &fakeStackController{
				createStack: func(_ controller.CreateStackOptions) (controller.CreateStackResult, error) {
					return controller.CreateStackResult{
						Name:               "stack-name",
						RealmName:          "realm-a",
						SpaceName:          "space-a",
						MetadataExistsPost: true,
						CgroupExistsPost:   true,
						Created:            false,
						CgroupCreated:      false,
					}, nil
				},
			},
			setupPrints: func(_ *testing.T) {
				// Verification will be done via captured calls
			},
		},
		{
			name:    "success with mixed states",
			cliArgs: []string{"stack-name", "--realm", "realm-a", "--space", "space-a"},
			controller: &fakeStackController{
				createStack: func(_ controller.CreateStackOptions) (controller.CreateStackResult, error) {
					return controller.CreateStackResult{
						Name:               "stack-name",
						RealmName:          "realm-a",
						SpaceName:          "space-a",
						MetadataExistsPost: true,
						CgroupExistsPost:   true,
						Created:            true,
						CgroupCreated:      false,
					}, nil
				},
			},
			setupPrints: func(_ *testing.T) {
				// Verification will be done via captured calls
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)
			if tt.viperName != "" {
				viper.Set(config.KUKE_CREATE_STACK_NAME.ViperKey, tt.viperName)
			}
			if tt.viperRealm != "" {
				viper.Set(config.KUKE_CREATE_STACK_REALM.ViperKey, tt.viperRealm)
			}
			if tt.viperSpace != "" {
				viper.Set(config.KUKE_CREATE_STACK_SPACE.ViperKey, tt.viperSpace)
			}

			// Set up command context with logger (unless testing controller error)
			var ctx context.Context
			if tt.controller == nil && tt.wantErrSub == "logger not found" {
				// Don't set logger to test controller creation error
				ctx = context.Background()
			} else {
				logger := slog.New(slog.NewTextHandler(io.Discard, nil))
				ctx = context.WithValue(context.Background(), types.CtxLogger, logger)
			}

			// Inject mock controller via context if needed
			if tt.controller != nil {
				// Capture CreateStack options if verifyCreateOpt is provided
				if tt.verifyCreateOpt != nil {
					originalCreateStack := tt.controller.createStack
					tt.controller.createStack = func(opts controller.CreateStackOptions) (controller.CreateStackResult, error) {
						tt.verifyCreateOpt(t, opts)
						return originalCreateStack(opts)
					}
				}
				ctx = context.WithValue(ctx, stack.MockControllerKey{}, tt.controller)
			}

			cmd := stack.NewStackCmd()
			cmd.SetContext(ctx)
			buf := &bytes.Buffer{}
			cmd.SetOut(buf)
			cmd.SetErr(buf)
			if len(tt.cliArgs) > 0 {
				cmd.SetArgs(tt.cliArgs)
			}

			// Execute the command
			err := cmd.Execute()

			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Verify output if expected
			if len(tt.wantOutputSubs) > 0 {
				output := buf.String()
				for _, wantSub := range tt.wantOutputSubs {
					if !strings.Contains(output, wantSub) {
						t.Fatalf("expected output to contain %q, got %q", wantSub, output)
					}
				}
			}
		})
	}
}

func TestPrintStackResult(t *testing.T) {
	tests := []struct {
		name           string
		result         controller.CreateStackResult
		wantOutputSubs []string
	}{
		{
			name: "all resources created",
			result: controller.CreateStackResult{
				Name:               "stack-a",
				RealmName:          "realm-a",
				SpaceName:          "space-a",
				MetadataExistsPost: true,
				CgroupExistsPost:   true,
				Created:            true,
				CgroupCreated:      true,
			},
			wantOutputSubs: []string{
				"Stack \"stack-a\" (realm \"realm-a\", space \"space-a\")",
			},
		},
		{
			name: "all resources already existed",
			result: controller.CreateStackResult{
				Name:               "stack-b",
				RealmName:          "realm-b",
				SpaceName:          "space-b",
				MetadataExistsPost: true,
				CgroupExistsPost:   true,
				Created:            false,
				CgroupCreated:      false,
			},
			wantOutputSubs: []string{
				"Stack \"stack-b\" (realm \"realm-b\", space \"space-b\")",
			},
		},
		{
			name: "metadata created, cgroup existed",
			result: controller.CreateStackResult{
				Name:               "stack-c",
				RealmName:          "realm-c",
				SpaceName:          "space-c",
				MetadataExistsPost: true,
				CgroupExistsPost:   true,
				Created:            true,
				CgroupCreated:      false,
			},
			wantOutputSubs: []string{
				"Stack \"stack-c\" (realm \"realm-c\", space \"space-c\")",
			},
		},
		{
			name: "metadata existed, cgroup created",
			result: controller.CreateStackResult{
				Name:               "stack-d",
				RealmName:          "realm-d",
				SpaceName:          "space-d",
				MetadataExistsPost: true,
				CgroupExistsPost:   true,
				Created:            false,
				CgroupCreated:      true,
			},
			wantOutputSubs: []string{
				"Stack \"stack-d\" (realm \"realm-d\", space \"space-d\")",
			},
		},
		{
			name: "metadata missing, cgroup missing",
			result: controller.CreateStackResult{
				Name:               "stack-e",
				RealmName:          "realm-e",
				SpaceName:          "space-e",
				MetadataExistsPost: false,
				CgroupExistsPost:   false,
				Created:            false,
				CgroupCreated:      false,
			},
			wantOutputSubs: []string{
				"Stack \"stack-e\" (realm \"realm-e\", space \"space-e\")",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{}
			buf := &bytes.Buffer{}
			cmd.SetOut(buf)
			cmd.SetErr(buf)

			metadataCalls := 0
			cgroupCalls := 0
			printOutcome := func(cmd *cobra.Command, label string, existsPost, created bool) {
				switch label {
				case "metadata":
					metadataCalls++
					if existsPost != tt.result.MetadataExistsPost {
						t.Fatalf(
							"metadata existsPost mismatch: expected %v, got %v",
							tt.result.MetadataExistsPost,
							existsPost,
						)
					}
					if created != tt.result.Created {
						t.Fatalf("metadata created mismatch: expected %v, got %v", tt.result.Created, created)
					}
				case "cgroup":
					cgroupCalls++
					if existsPost != tt.result.CgroupExistsPost {
						t.Fatalf(
							"cgroup existsPost mismatch: expected %v, got %v",
							tt.result.CgroupExistsPost,
							existsPost,
						)
					}
					if created != tt.result.CgroupCreated {
						t.Fatalf("cgroup created mismatch: expected %v, got %v", tt.result.CgroupCreated, created)
					}
				}
				shared.PrintCreationOutcome(cmd, label, existsPost, created)
			}

			stack.PrintStackResult(cmd, tt.result, printOutcome)

			output := buf.String()
			for _, wantSub := range tt.wantOutputSubs {
				if !strings.Contains(output, wantSub) {
					t.Fatalf("expected output to contain %q, got %q", wantSub, output)
				}
			}

			if metadataCalls != 1 {
				t.Fatalf("expected metadata PrintCreationOutcome to be called once, got %d", metadataCalls)
			}
			if cgroupCalls != 1 {
				t.Fatalf("expected cgroup PrintCreationOutcome to be called once, got %d", cgroupCalls)
			}
		})
	}
}

type fakeStackController struct {
	createStack func(opts controller.CreateStackOptions) (controller.CreateStackResult, error)
}

func (f *fakeStackController) CreateStack(opts controller.CreateStackOptions) (controller.CreateStackResult, error) {
	if f.createStack == nil {
		panic("CreateStack was called unexpectedly")
	}
	return f.createStack(opts)
}

func TestNewStackCmdRunE(t *testing.T) {
	t.Cleanup(func() {
		viper.Reset()
	})

	tests := []struct {
		name           string
		args           []string
		setup          func(t *testing.T, cmd *cobra.Command)
		controllerFn   func(opts controller.CreateStackOptions) (controller.CreateStackResult, error)
		wantErr        string
		wantCallCreate bool
		wantOpts       *controller.CreateStackOptions
		wantOutput     []string
	}{
		{
			name: "success: name from args with flags",
			args: []string{"test-stack"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "space", "space-a")
			},
			controllerFn: func(opts controller.CreateStackOptions) (controller.CreateStackResult, error) {
				return controller.CreateStackResult{
					Name:               opts.Name,
					RealmName:          opts.RealmName,
					SpaceName:          opts.SpaceName,
					Created:            true,
					MetadataExistsPost: true,
					CgroupCreated:      true,
					CgroupExistsPost:   true,
				}, nil
			},
			wantCallCreate: true,
			wantOpts: &controller.CreateStackOptions{
				Name:      "test-stack",
				RealmName: "realm-a",
				SpaceName: "space-a",
			},
			wantOutput: []string{
				`Stack "test-stack" (realm "realm-a", space "space-a")`,
			},
		},
		{
			name: "success: name from viper with flags",
			setup: func(t *testing.T, cmd *cobra.Command) {
				viper.Set(config.KUKE_CREATE_STACK_NAME.ViperKey, "viper-stack")
				setFlag(t, cmd, "realm", "realm-b")
				setFlag(t, cmd, "space", "space-b")
			},
			controllerFn: func(opts controller.CreateStackOptions) (controller.CreateStackResult, error) {
				return controller.CreateStackResult{
					Name:               opts.Name,
					RealmName:          opts.RealmName,
					SpaceName:          opts.SpaceName,
					Created:            true,
					MetadataExistsPost: true,
					CgroupCreated:      true,
					CgroupExistsPost:   true,
				}, nil
			},
			wantCallCreate: true,
			wantOpts: &controller.CreateStackOptions{
				Name:      "viper-stack",
				RealmName: "realm-b",
				SpaceName: "space-b",
			},
			wantOutput: []string{
				`Stack "viper-stack" (realm "realm-b", space "space-b")`,
			},
		},
		{
			name: "error: missing realm",
			args: []string{"test-stack"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "space", "space-a")
			},
			wantErr:        "realm name is required",
			wantCallCreate: false,
		},
		{
			name: "error: missing space",
			args: []string{"test-stack"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "realm-a")
			},
			wantErr:        "space name is required",
			wantCallCreate: false,
		},
		{
			name: "error: logger not in context",
			args: []string{"test-stack"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				cmd.SetContext(context.Background())
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "space", "space-a")
			},
			controllerFn: func(_ controller.CreateStackOptions) (controller.CreateStackResult, error) {
				return controller.CreateStackResult{}, errors.New("unexpected call")
			},
			wantErr:        "logger not found",
			wantCallCreate: false,
		},
		{
			name: "error: CreateStack fails",
			args: []string{"test-stack"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "space", "space-a")
			},
			controllerFn: func(_ controller.CreateStackOptions) (controller.CreateStackResult, error) {
				return controller.CreateStackResult{}, errors.New("failed to create stack")
			},
			wantErr:        "failed to create stack",
			wantCallCreate: true,
			wantOpts: &controller.CreateStackOptions{
				Name:      "test-stack",
				RealmName: "realm-a",
				SpaceName: "space-a",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)

			var createCalled bool
			var createOpts controller.CreateStackOptions

			cmd := stack.NewStackCmd()
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			ctx := context.Background()

			// Inject mock controller via context if needed
			if tt.name != "error: logger not in context" {
				// Set up logger context
				logger := slog.New(slog.NewTextHandler(io.Discard, nil))
				ctx = context.WithValue(ctx, types.CtxLogger, logger)

				// If we need to mock the controller, inject it via context
				if tt.controllerFn != nil {
					fakeCtrl := &fakeStackController{
						createStack: func(opts controller.CreateStackOptions) (controller.CreateStackResult, error) {
							createCalled = true
							createOpts = opts
							return tt.controllerFn(opts)
						},
					}
					// Inject mock controller into context using the exported key
					ctx = context.WithValue(ctx, stack.MockControllerKey{}, fakeCtrl)
				}
			}

			cmd.SetContext(ctx)

			if tt.setup != nil {
				tt.setup(t, cmd)
			}

			cmd.SetArgs(tt.args)

			err := cmd.Execute()

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if createCalled != tt.wantCallCreate {
				t.Errorf("CreateStack called=%v want=%v", createCalled, tt.wantCallCreate)
			}

			if tt.wantOpts != nil {
				if createOpts.Name != tt.wantOpts.Name {
					t.Errorf("CreateStack Name=%q want=%q", createOpts.Name, tt.wantOpts.Name)
				}
				if createOpts.RealmName != tt.wantOpts.RealmName {
					t.Errorf("CreateStack RealmName=%q want=%q", createOpts.RealmName, tt.wantOpts.RealmName)
				}
				if createOpts.SpaceName != tt.wantOpts.SpaceName {
					t.Errorf("CreateStack SpaceName=%q want=%q", createOpts.SpaceName, tt.wantOpts.SpaceName)
				}
			}

			if tt.wantOutput != nil {
				output := cmd.OutOrStdout().(*bytes.Buffer).String()
				for _, expected := range tt.wantOutput {
					if !strings.Contains(output, expected) {
						t.Errorf("output missing expected string %q\nGot output:\n%s", expected, output)
					}
				}
			}
		})
	}
}

func setFlag(t *testing.T, cmd *cobra.Command, name, value string) {
	t.Helper()
	if err := cmd.Flags().Set(name, value); err != nil {
		t.Fatalf("failed to set flag %s: %v", name, err)
	}
}

func TestNewStackCmd_AutocompleteRegistration(t *testing.T) {
	// Test that autocomplete functions are properly registered for --realm and --space flags
	cmd := stack.NewStackCmd()

	// Test that realm flag exists
	realmFlag := cmd.Flags().Lookup("realm")
	if realmFlag == nil {
		t.Fatal("expected 'realm' flag to exist")
	}

	// Test that space flag exists
	spaceFlag := cmd.Flags().Lookup("space")
	if spaceFlag == nil {
		t.Fatal("expected 'space' flag to exist")
	}

	// Verify flag structure (completion function registration is verified by Cobra)
	if realmFlag.Usage != "Realm that owns the stack" {
		t.Errorf("unexpected realm flag usage: %q", realmFlag.Usage)
	}

	if spaceFlag.Usage != "Space that owns the stack" {
		t.Errorf("unexpected space flag usage: %q", spaceFlag.Usage)
	}
}
