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

package cell_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/config"
	cell "github.com/eminwux/kukeon/cmd/kuke/purge/cell"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestNewCellCmd(t *testing.T) {
	t.Cleanup(viper.Reset)

	cmd := cell.NewCellCmd()

	if cmd.Use != "cell [name]" {
		t.Errorf("Use mismatch: got %q, want %q", cmd.Use, "cell [name]")
	}

	if cmd.Short != "Purge a cell with comprehensive cleanup" {
		t.Errorf("Short mismatch: got %q, want %q", cmd.Short, "Purge a cell with comprehensive cleanup")
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
		{"stack", true},
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
		{"realm", config.KUKE_PURGE_CELL_REALM.ViperKey, "test-realm"},
		{"space", config.KUKE_PURGE_CELL_SPACE.ViperKey, "test-space"},
		{"stack", config.KUKE_PURGE_CELL_STACK.ViperKey, "test-stack"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			viper.Reset()
			// Create a new command for each test to ensure clean state
			testCmd := cell.NewCellCmd()
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

func TestNewCellCmdRunE(t *testing.T) {
	t.Cleanup(viper.Reset)

	tests := []struct {
		name        string
		args        []string
		flags       map[string]string
		viperConfig map[string]string
		setupCtx    func(*cobra.Command)
		controller  *fakePurgeController
		forceFlag   bool
		cascadeFlag bool
		wantErr     string
		wantOutput  []string
	}{
		{
			name: "missing realm error",
			args: []string{"my-cell"},
			flags: map[string]string{
				"space": "my-space",
				"stack": "my-stack",
			},
			wantErr: "realm name is required",
		},
		{
			name: "missing space error",
			args: []string{"my-cell"},
			flags: map[string]string{
				"realm": "my-realm",
				"stack": "my-stack",
			},
			wantErr: "space name is required",
		},
		{
			name: "missing stack error",
			args: []string{"my-cell"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
			},
			wantErr: "stack name is required",
		},
		{
			name: "empty realm after trimming",
			args: []string{"my-cell"},
			flags: map[string]string{
				"realm": "   ",
				"space": "my-space",
				"stack": "my-stack",
			},
			wantErr: "realm name is required",
		},
		{
			name: "empty space after trimming",
			args: []string{"my-cell"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "   ",
				"stack": "my-stack",
			},
			wantErr: "space name is required",
		},
		{
			name: "empty stack after trimming",
			args: []string{"my-cell"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
				"stack": "   ",
			},
			wantErr: "stack name is required",
		},
		{
			name: "empty cell name after trimming",
			args: []string{"   "},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
				"stack": "my-stack",
			},
			controller: &fakePurgeController{
				purgeCellFn: func(name, _ string, _ string, _ string, _ bool, _ bool) (*controller.PurgeCellResult, error) {
					// Should not reach here due to validation, but if it does, expect empty name
					if name == "" {
						return nil, errdefs.ErrCellNameRequired
					}
					return nil, errors.New("unexpected call")
				},
			},
			wantErr: "cell name is required",
		},
		{
			name: "controller creation error - missing logger",
			args: []string{"my-cell"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
				"stack": "my-stack",
			},
			setupCtx: func(cmd *cobra.Command) {
				// Don't set logger in context
				cmd.SetContext(context.Background())
			},
			wantErr: "logger not found",
		},
		{
			name: "controller PurgeCell returns error",
			args: []string{"my-cell"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
				"stack": "my-stack",
			},
			controller: &fakePurgeController{
				purgeCellFn: func(name, realm, space, stack string, _ bool, _ bool) (*controller.PurgeCellResult, error) {
					if name != "my-cell" || realm != "my-realm" || space != "my-space" || stack != "my-stack" {
						return nil, errors.New("unexpected args")
					}
					return nil, errors.New("cell not found")
				},
			},
			wantErr: "cell not found",
		},
		{
			name: "successful cell purge",
			args: []string{"my-cell"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
				"stack": "my-stack",
			},
			controller: &fakePurgeController{
				purgeCellFn: func(name, realm, space, stack string, _ bool, _ bool) (*controller.PurgeCellResult, error) {
					if name != "my-cell" || realm != "my-realm" || space != "my-space" || stack != "my-stack" {
						return nil, errors.New("unexpected args")
					}
					return &controller.PurgeCellResult{
						CellName:  "my-cell",
						RealmName: "my-realm",
						SpaceName: "my-space",
						StackName: "my-stack",
						Purged:    []string{"cni-resources", "orphaned-containers"},
					}, nil
				},
			},
			wantOutput: []string{
				"Purged cell \"my-cell\" from stack \"my-stack\"",
				"Additional resources purged: [cni-resources orphaned-containers]",
			},
		},
		{
			name: "successful purge with no additional resources",
			args: []string{"my-cell"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
				"stack": "my-stack",
			},
			controller: &fakePurgeController{
				purgeCellFn: func(_ string, _ string, _ string, _ string, _ bool, _ bool) (*controller.PurgeCellResult, error) {
					return &controller.PurgeCellResult{
						CellName:  "my-cell",
						StackName: "my-stack",
						Purged:    []string{},
					}, nil
				},
			},
			wantOutput: []string{
				"Purged cell \"my-cell\" from stack \"my-stack\"",
			},
		},
		{
			name: "successful deletion with trimmed whitespace in args and flags",
			args: []string{"  my-cell  "},
			flags: map[string]string{
				"realm": "  my-realm  ",
				"space": "  my-space  ",
				"stack": "  my-stack  ",
			},
			controller: &fakePurgeController{
				purgeCellFn: func(name, realm, space, stack string, _, _ bool) (*controller.PurgeCellResult, error) {
					// Verify that trimming happened
					if name != "my-cell" || realm != "my-realm" || space != "my-space" || stack != "my-stack" {
						return nil, errors.New("unexpected trimmed args")
					}
					return &controller.PurgeCellResult{
						CellName:  "my-cell",
						StackName: "my-stack",
						Purged:    []string{"cni-resources"},
					}, nil
				},
			},
			wantOutput: []string{
				"Purged cell \"my-cell\" from stack \"my-stack\"",
			},
		},
		{
			name: "values from viper config",
			args: []string{"my-cell"},
			viperConfig: map[string]string{
				config.KUKE_PURGE_CELL_REALM.ViperKey: "viper-realm",
				config.KUKE_PURGE_CELL_SPACE.ViperKey: "viper-space",
				config.KUKE_PURGE_CELL_STACK.ViperKey: "viper-stack",
			},
			controller: &fakePurgeController{
				purgeCellFn: func(name, realm, space, stack string, _, _ bool) (*controller.PurgeCellResult, error) {
					if name != "my-cell" || realm != "viper-realm" || space != "viper-space" || stack != "viper-stack" {
						return nil, errors.New("unexpected args from viper")
					}
					return &controller.PurgeCellResult{
						CellName:  "my-cell",
						StackName: "viper-stack",
						Purged:    []string{},
					}, nil
				},
			},
			wantOutput: []string{
				"Purged cell \"my-cell\" from stack \"viper-stack\"",
			},
		},
		{
			name: "force flag passed to controller",
			args: []string{"my-cell"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
				"stack": "my-stack",
			},
			forceFlag: true,
			controller: &fakePurgeController{
				purgeCellFn: func(_, _, _, _ string, force, _ bool) (*controller.PurgeCellResult, error) {
					if !force {
						return nil, errors.New("force flag not passed")
					}
					return &controller.PurgeCellResult{
						CellName:  "my-cell",
						StackName: "my-stack",
						Purged:    []string{},
					}, nil
				},
			},
			wantOutput: []string{
				"Purged cell \"my-cell\" from stack \"my-stack\"",
			},
		},
		{
			name: "cascade flag passed to controller",
			args: []string{"my-cell"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
				"stack": "my-stack",
			},
			cascadeFlag: true,
			controller: &fakePurgeController{
				purgeCellFn: func(_, _, _, _ string, _, cascade bool) (*controller.PurgeCellResult, error) {
					if !cascade {
						return nil, errors.New("cascade flag not passed")
					}
					return &controller.PurgeCellResult{
						CellName:  "my-cell",
						StackName: "my-stack",
						Purged:    []string{},
					}, nil
				},
			},
			wantOutput: []string{
				"Purged cell \"my-cell\" from stack \"my-stack\"",
			},
		},
		{
			name: "both force and cascade flags passed",
			args: []string{"my-cell"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
				"stack": "my-stack",
			},
			forceFlag:   true,
			cascadeFlag: true,
			controller: &fakePurgeController{
				purgeCellFn: func(_, _, _, _ string, force, cascade bool) (*controller.PurgeCellResult, error) {
					if !force || !cascade {
						return nil, errors.New("flags not passed correctly")
					}
					return &controller.PurgeCellResult{
						CellName:  "my-cell",
						StackName: "my-stack",
						Purged:    []string{"cni-resources"},
					}, nil
				},
			},
			wantOutput: []string{
				"Purged cell \"my-cell\" from stack \"my-stack\"",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			viper.Reset()
			cmd := cell.NewCellCmd()
			var outBuf bytes.Buffer
			cmd.SetOut(&outBuf)
			cmd.SetErr(&outBuf)

			// Create parent command with persistent flags for force and cascade
			parentCmd := &cobra.Command{Use: "purge"}
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
				ctx = context.WithValue(ctx, cell.MockControllerKey{}, tt.controller)
			}

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

			// Build args including persistent flags
			args := append([]string{"cell"}, tt.args...)
			for name, value := range tt.flags {
				args = append(args, "--"+name, value)
			}
			if tt.forceFlag {
				args = append(args, "--force")
			}
			if tt.cascadeFlag {
				args = append(args, "--cascade")
			}

			// Set args on parent command
			parentCmd.SetArgs(args)
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

// fakePurgeController provides a mock implementation for testing PurgeCell.
type fakePurgeController struct {
	purgeCellFn func(name, realm, space, stack string, force, cascade bool) (*controller.PurgeCellResult, error)
}

func (f *fakePurgeController) PurgeCell(
	name, realm, space, stack string,
	force, cascade bool,
) (*controller.PurgeCellResult, error) {
	if f.purgeCellFn == nil {
		return nil, errors.New("unexpected PurgeCell call")
	}
	return f.purgeCellFn(name, realm, space, stack, force, cascade)
}

func TestNewCellCmd_AutocompleteRegistration(t *testing.T) {
	cmd := cell.NewCellCmd()

	// Test that ValidArgsFunction is set to CompleteCellNames
	if cmd.ValidArgsFunction == nil {
		t.Fatal("expected ValidArgsFunction to be set")
	}

	// Test that realm flag exists
	realmFlag := cmd.Flags().Lookup("realm")
	if realmFlag == nil {
		t.Fatal("expected 'realm' flag to exist")
	}
	if realmFlag.Usage != "Realm that owns the cell" {
		t.Errorf("unexpected realm flag usage: %q", realmFlag.Usage)
	}

	// Test that space flag exists
	spaceFlag := cmd.Flags().Lookup("space")
	if spaceFlag == nil {
		t.Fatal("expected 'space' flag to exist")
	}
	if spaceFlag.Usage != "Space that owns the cell" {
		t.Errorf("unexpected space flag usage: %q", spaceFlag.Usage)
	}

	// Test that stack flag exists
	stackFlag := cmd.Flags().Lookup("stack")
	if stackFlag == nil {
		t.Fatal("expected 'stack' flag to exist")
	}
	if stackFlag.Usage != "Stack that owns the cell" {
		t.Errorf("unexpected stack flag usage: %q", stackFlag.Usage)
	}
}
