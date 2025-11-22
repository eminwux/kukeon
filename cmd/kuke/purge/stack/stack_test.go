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
	stack "github.com/eminwux/kukeon/cmd/kuke/purge/stack"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestNewStackCmd(t *testing.T) {
	t.Cleanup(viper.Reset)

	cmd := stack.NewStackCmd()

	if cmd.Use != "stack [name]" {
		t.Errorf("Use mismatch: got %q, want %q", cmd.Use, "stack [name]")
	}

	if cmd.Short != "Purge a stack with comprehensive cleanup" {
		t.Errorf("Short mismatch: got %q, want %q", cmd.Short, "Purge a stack with comprehensive cleanup")
	}

	if !cmd.SilenceUsage {
		t.Error("SilenceUsage should be true")
	}

	if cmd.SilenceErrors {
		t.Error("SilenceErrors should be false")
	}

	// Test flags exist
	flags := []struct {
		name     string
		required bool
	}{
		{"realm", true},
		{"space", true},
	}

	for _, flag := range flags {
		f := cmd.Flags().Lookup(flag.name)
		if f == nil {
			t.Errorf("flag %q not found", flag.name)
			continue
		}
	}

	// Test viper binding
	testCases := []struct {
		name     string
		viperKey string
		value    string
	}{
		{"realm", config.KUKE_PURGE_STACK_REALM.ViperKey, "test-realm"},
		{"space", config.KUKE_PURGE_STACK_SPACE.ViperKey, "test-space"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			viper.Reset()
			// Create a new command for each test to ensure clean state
			testCmd := stack.NewStackCmd()
			if err := testCmd.Flags().Set(tc.name, tc.value); err != nil {
				t.Fatalf("failed to set flag: %v", err)
			}
			got := viper.GetString(tc.viperKey)
			if got != tc.value {
				t.Errorf("viper binding mismatch: got %q, want %q", got, tc.value)
			}
		})
	}
}

func TestNewStackCmdRunE(t *testing.T) {
	t.Cleanup(viper.Reset)

	tests := []struct {
		name        string
		args        []string
		flags       map[string]string
		viperConfig map[string]string
		setupCtx    func(*cobra.Command)
		controller  *fakeStackController
		forceFlag   bool
		cascadeFlag bool
		wantErr     string
		wantOutput  []string
	}{
		{
			name: "missing realm error",
			args: []string{"my-stack"},
			flags: map[string]string{
				"space": "my-space",
			},
			wantErr: "realm name is required",
		},
		{
			name: "missing space error",
			args: []string{"my-stack"},
			flags: map[string]string{
				"realm": "my-realm",
			},
			wantErr: "space name is required",
		},
		{
			name: "empty realm after trimming",
			args: []string{"my-stack"},
			flags: map[string]string{
				"realm": "   ",
				"space": "my-space",
			},
			wantErr: "realm name is required",
		},
		{
			name: "empty space after trimming",
			args: []string{"my-stack"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "   ",
			},
			wantErr: "space name is required",
		},
		{
			name: "empty stack name after trimming",
			args: []string{"   "},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
			},
			controller: &fakeStackController{
				purgeStackFn: func(name, _, _ string, _, _ bool) (*controller.PurgeStackResult, error) {
					// Should not reach here due to validation, but if it does, expect empty name
					if name == "" {
						return nil, errdefs.ErrStackNameRequired
					}
					return nil, errors.New("unexpected call")
				},
			},
			wantErr: "stack name is required",
		},
		{
			name: "controller creation error - missing logger",
			args: []string{"my-stack"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
			},
			setupCtx: func(cmd *cobra.Command) {
				// Don't set logger in context
				cmd.SetContext(context.Background())
			},
			wantErr: "logger not found",
		},
		{
			name: "controller PurgeStack returns error",
			args: []string{"my-stack"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
			},
			controller: &fakeStackController{
				purgeStackFn: func(name, realm, space string, _, _ bool) (*controller.PurgeStackResult, error) {
					if name != "my-stack" || realm != "my-realm" || space != "my-space" {
						return nil, errors.New("unexpected args")
					}
					return nil, errors.New("stack not found")
				},
			},
			wantErr: "stack not found",
		},
		{
			name: "controller PurgeStack returns stack not found error",
			args: []string{"nonexistent-stack"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
			},
			controller: &fakeStackController{
				purgeStackFn: func(_, _, _ string, _, _ bool) (*controller.PurgeStackResult, error) {
					return nil, errdefs.ErrStackNotFound
				},
			},
			wantErr: "stack not found",
		},
		{
			name: "successful stack purge",
			args: []string{"my-stack"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
			},
			controller: &fakeStackController{
				purgeStackFn: func(name, realm, space string, force, cascade bool) (*controller.PurgeStackResult, error) {
					if name != "my-stack" || realm != "my-realm" || space != "my-space" {
						return nil, errors.New("unexpected args")
					}
					if force {
						return nil, errors.New("unexpected force flag")
					}
					if cascade {
						return nil, errors.New("unexpected cascade flag")
					}
					return &controller.PurgeStackResult{
						StackName: "my-stack",
						RealmName: "my-realm",
						SpaceName: "my-space",
						Purged:    []string{"cni-resources", "orphaned-containers"},
					}, nil
				},
			},
			wantOutput: []string{
				"Purged stack \"my-stack\" from space \"my-space\"",
				"Additional resources purged: [cni-resources orphaned-containers]",
			},
		},
		{
			name: "successful purge with no additional resources",
			args: []string{"my-stack"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
			},
			controller: &fakeStackController{
				purgeStackFn: func(_, _, _ string, _, _ bool) (*controller.PurgeStackResult, error) {
					return &controller.PurgeStackResult{
						StackName: "my-stack",
						RealmName: "my-realm",
						SpaceName: "my-space",
						Purged:    []string{},
					}, nil
				},
			},
			wantOutput: []string{
				"Purged stack \"my-stack\" from space \"my-space\"",
			},
		},
		{
			name: "successful deletion with trimmed whitespace in args and flags",
			args: []string{"  my-stack  "},
			flags: map[string]string{
				"realm": "  my-realm  ",
				"space": "  my-space  ",
			},
			controller: &fakeStackController{
				purgeStackFn: func(name, realm, space string, _, _ bool) (*controller.PurgeStackResult, error) {
					// Verify that trimming happened
					if name != "my-stack" || realm != "my-realm" || space != "my-space" {
						return nil, errors.New("unexpected trimmed args")
					}
					return &controller.PurgeStackResult{
						StackName: "my-stack",
						RealmName: "my-realm",
						SpaceName: "my-space",
						Purged:    []string{"cni-resources"},
					}, nil
				},
			},
			wantOutput: []string{
				"Purged stack \"my-stack\" from space \"my-space\"",
			},
		},
		{
			name: "values from viper config",
			args: []string{"my-stack"},
			viperConfig: map[string]string{
				config.KUKE_PURGE_STACK_REALM.ViperKey: "viper-realm",
				config.KUKE_PURGE_STACK_SPACE.ViperKey: "viper-space",
			},
			controller: &fakeStackController{
				purgeStackFn: func(name, realm, space string, _, _ bool) (*controller.PurgeStackResult, error) {
					if name != "my-stack" || realm != "viper-realm" || space != "viper-space" {
						return nil, errors.New("unexpected args from viper")
					}
					return &controller.PurgeStackResult{
						StackName: "my-stack",
						RealmName: "viper-realm",
						SpaceName: "viper-space",
						Purged:    []string{},
					}, nil
				},
			},
			wantOutput: []string{
				"Purged stack \"my-stack\" from space \"viper-space\"",
			},
		},
		{
			name: "success with --force flag",
			args: []string{"my-stack"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
			},
			forceFlag: true,
			controller: &fakeStackController{
				purgeStackFn: func(_, _, _ string, force, cascade bool) (*controller.PurgeStackResult, error) {
					if !force {
						return nil, errors.New("expected force to be true")
					}
					if cascade {
						return nil, errors.New("unexpected cascade flag")
					}
					return &controller.PurgeStackResult{
						StackName: "my-stack",
						RealmName: "my-realm",
						SpaceName: "my-space",
						Purged:    []string{},
					}, nil
				},
			},
			wantOutput: []string{
				"Purged stack \"my-stack\" from space \"my-space\"",
			},
		},
		{
			name: "success with --cascade flag",
			args: []string{"my-stack"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
			},
			cascadeFlag: true,
			controller: &fakeStackController{
				purgeStackFn: func(_, _, _ string, force, cascade bool) (*controller.PurgeStackResult, error) {
					if force {
						return nil, errors.New("unexpected force flag")
					}
					if !cascade {
						return nil, errors.New("expected cascade to be true")
					}
					return &controller.PurgeStackResult{
						StackName: "my-stack",
						RealmName: "my-realm",
						SpaceName: "my-space",
						Purged:    []string{},
					}, nil
				},
			},
			wantOutput: []string{
				"Purged stack \"my-stack\" from space \"my-space\"",
			},
		},
		{
			name: "success with both --force and --cascade flags",
			args: []string{"my-stack"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
			},
			forceFlag:   true,
			cascadeFlag: true,
			controller: &fakeStackController{
				purgeStackFn: func(_, _, _ string, force, cascade bool) (*controller.PurgeStackResult, error) {
					if !force {
						return nil, errors.New("expected force to be true")
					}
					if !cascade {
						return nil, errors.New("expected cascade to be true")
					}
					return &controller.PurgeStackResult{
						StackName: "my-stack",
						RealmName: "my-realm",
						SpaceName: "my-space",
						Purged:    []string{},
					}, nil
				},
			},
			wantOutput: []string{
				"Purged stack \"my-stack\" from space \"my-space\"",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			viper.Reset()
			cmd := stack.NewStackCmd()
			var outBuf bytes.Buffer
			cmd.SetOut(&outBuf)
			cmd.SetErr(&outBuf)

			// Set up parent command for persistent flags
			parentCmd := &cobra.Command{
				Use: "purge",
			}
			parentCmd.PersistentFlags().Bool("force", false, "")
			parentCmd.PersistentFlags().Bool("cascade", false, "")
			_ = viper.BindPFlag(config.KUKE_PURGE_FORCE.ViperKey, parentCmd.PersistentFlags().Lookup("force"))
			_ = viper.BindPFlag(config.KUKE_PURGE_CASCADE.ViperKey, parentCmd.PersistentFlags().Lookup("cascade"))
			parentCmd.AddCommand(cmd)
			parentCmd.SetOut(&outBuf)
			parentCmd.SetErr(&outBuf)

			// Set up context with logger (unless overridden)
			var ctx context.Context
			if tt.setupCtx != nil {
				tt.setupCtx(cmd)
				ctx = cmd.Context()
			} else {
				logger := slog.New(slog.NewTextHandler(io.Discard, nil))
				ctx = context.WithValue(context.Background(), types.CtxLogger, logger)
			}

			// Inject mock controller via context if needed
			if tt.controller != nil {
				ctx = context.WithValue(ctx, stack.MockControllerKey{}, tt.controller)
			}

			parentCmd.SetContext(ctx)
			cmd.SetContext(ctx)

			// Set viper config
			for k, v := range tt.viperConfig {
				viper.Set(k, v)
			}

			// Set flags
			for name, value := range tt.flags {
				if err := cmd.Flags().Set(name, value); err != nil {
					t.Fatalf("failed to set flag %q: %v", name, err)
				}
			}

			// Build args with flags
			args := tt.args
			if tt.forceFlag {
				args = append(args, "--force")
			}
			if tt.cascadeFlag {
				args = append(args, "--cascade")
			}

			// Set args on parent command
			allArgs := append([]string{"stack"}, args...)
			parentCmd.SetArgs(allArgs)

			err := parentCmd.Execute()

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(tt.wantOutput) > 0 {
				output := outBuf.String()
				for _, want := range tt.wantOutput {
					if !strings.Contains(output, want) {
						t.Errorf("output missing expected string %q. Got output: %q", want, output)
					}
				}
			}
		})
	}
}

func TestForceAndCascadeFlags(t *testing.T) {
	tests := []struct {
		name          string
		cliArgs       []string
		controller    *fakeStackController
		verifyForce   bool
		verifyCascade bool
	}{
		{
			name:    "force flag parsing",
			cliArgs: []string{"stack-name", "--realm", "realm-a", "--space", "space-a", "--force"},
			controller: &fakeStackController{
				purgeStackFn: func(_, _, _ string, force, cascade bool) (*controller.PurgeStackResult, error) {
					if !force {
						t.Errorf("expected force to be true, got false")
					}
					if cascade {
						t.Errorf("expected cascade to be false, got true")
					}
					return &controller.PurgeStackResult{
						StackName: "stack-name",
						SpaceName: "space-a",
					}, nil
				},
			},
			verifyForce:   true,
			verifyCascade: false,
		},
		{
			name:    "cascade flag parsing",
			cliArgs: []string{"stack-name", "--realm", "realm-a", "--space", "space-a", "--cascade"},
			controller: &fakeStackController{
				purgeStackFn: func(_, _, _ string, force, cascade bool) (*controller.PurgeStackResult, error) {
					if force {
						t.Errorf("expected force to be false, got true")
					}
					if !cascade {
						t.Errorf("expected cascade to be true, got false")
					}
					return &controller.PurgeStackResult{
						StackName: "stack-name",
						SpaceName: "space-a",
					}, nil
				},
			},
			verifyForce:   false,
			verifyCascade: true,
		},
		{
			name:    "both flags parsing",
			cliArgs: []string{"stack-name", "--realm", "realm-a", "--space", "space-a", "--force", "--cascade"},
			controller: &fakeStackController{
				purgeStackFn: func(_, _, _ string, force, cascade bool) (*controller.PurgeStackResult, error) {
					if !force {
						t.Errorf("expected force to be true, got false")
					}
					if !cascade {
						t.Errorf("expected cascade to be true, got false")
					}
					return &controller.PurgeStackResult{
						StackName: "stack-name",
						SpaceName: "space-a",
					}, nil
				},
			},
			verifyForce:   true,
			verifyCascade: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)

			// Set up command
			cmd := stack.NewStackCmd()
			buf := &bytes.Buffer{}
			cmd.SetOut(buf)
			cmd.SetErr(buf)

			// Set up parent command for persistent flags
			parentCmd := &cobra.Command{
				Use: "purge",
			}
			parentCmd.PersistentFlags().Bool("force", false, "")
			parentCmd.PersistentFlags().Bool("cascade", false, "")
			_ = viper.BindPFlag(config.KUKE_PURGE_FORCE.ViperKey, parentCmd.PersistentFlags().Lookup("force"))
			_ = viper.BindPFlag(config.KUKE_PURGE_CASCADE.ViperKey, parentCmd.PersistentFlags().Lookup("cascade"))
			parentCmd.AddCommand(cmd)
			parentCmd.SetOut(buf)
			parentCmd.SetErr(buf)

			// Set up context with logger
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			ctx := context.WithValue(context.Background(), types.CtxLogger, logger)

			// Inject mock controller via context
			if tt.controller != nil {
				ctx = context.WithValue(ctx, stack.MockControllerKey{}, tt.controller)
			}

			parentCmd.SetContext(ctx)
			cmd.SetContext(ctx)

			// Set args on parent command
			if len(tt.cliArgs) > 0 {
				allArgs := append([]string{"stack"}, tt.cliArgs...)
				parentCmd.SetArgs(allArgs)
			}

			// Execute command - this will parse flags and call the mock controller
			err := parentCmd.Execute()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// fakeStackController provides a mock implementation for testing PurgeStack.
type fakeStackController struct {
	purgeStackFn func(name, realm, space string, force, cascade bool) (*controller.PurgeStackResult, error)
}

func (f *fakeStackController) PurgeStack(
	name, realm, space string,
	force, cascade bool,
) (*controller.PurgeStackResult, error) {
	if f.purgeStackFn == nil {
		return nil, errors.New("unexpected PurgeStack call")
	}
	return f.purgeStackFn(name, realm, space, force, cascade)
}
