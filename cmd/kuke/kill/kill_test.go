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

package kill_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/config"
	kill "github.com/eminwux/kukeon/cmd/kuke/kill"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestNewKillCmd(t *testing.T) {
	t.Cleanup(viper.Reset)

	cmd := kill.NewKillCmd()

	if cmd.Use != "kill --realm <realm> --space <space> --stack <stack> [--cell <cell>] <resource-type> <resource-name>" {
		t.Errorf(
			"Use mismatch: got %q, want %q",
			cmd.Use,
			"kill --realm <realm> --space <space> --stack <stack> [--cell <cell>] <resource-type> <resource-name>",
		)
	}

	if cmd.Short != "Kill Kukeon resources (cell, container)" {
		t.Errorf("Short mismatch: got %q, want %q", cmd.Short, "Kill Kukeon resources (cell, container)")
	}

	if cmd.Long != "Kill a cell or container. For cells, kills all containers in the cell. For containers, requires --cell flag." {
		t.Errorf(
			"Long mismatch: got %q, want %q",
			cmd.Long,
			"Kill a cell or container. For cells, kills all containers in the cell. For containers, requires --cell flag.",
		)
	}

	if !cmd.SilenceUsage {
		t.Error("SilenceUsage should be true")
	}

	if cmd.SilenceErrors {
		t.Error("SilenceErrors should be false")
	}

	// Test flags exist
	flags := []struct {
		name string
	}{
		{"realm"},
		{"space"},
		{"stack"},
		{"cell"},
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
		{"realm", config.KUKE_KILL_REALM.ViperKey, "test-realm"},
		{"space", config.KUKE_KILL_SPACE.ViperKey, "test-space"},
		{"stack", config.KUKE_KILL_STACK.ViperKey, "test-stack"},
		{"cell", config.KUKE_KILL_CELL.ViperKey, "test-cell"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			viper.Reset()
			// Create a new command for each test to ensure clean state
			testCmd := kill.NewKillCmd()
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

func TestNewKillCmd_AutocompleteRegistration(t *testing.T) {
	cmd := kill.NewKillCmd()

	// Test that ValidArgsFunction is set for positional arguments
	if cmd.ValidArgsFunction == nil {
		t.Fatal("expected ValidArgsFunction to be set for positional arguments")
	}

	// Test that all flags exist and have correct usage
	flags := []struct {
		name  string
		usage string
	}{
		{"realm", "Realm that owns the resource"},
		{"space", "Space that owns the resource"},
		{"stack", "Stack that owns the resource"},
		{"cell", "Cell that owns the container (required for container resource type)"},
	}

	for _, flag := range flags {
		flagObj := cmd.Flags().Lookup(flag.name)
		if flagObj == nil {
			t.Errorf("expected %q flag to exist", flag.name)
			continue
		}
		if flagObj.Usage != flag.usage {
			t.Errorf("unexpected %q flag usage: got %q, want %q", flag.name, flagObj.Usage, flag.usage)
		}
	}

	// Note: Completion function registration is verified by Cobra internally.
	// ValidArgsFunction is set and flags exist confirms the structure is correct.
}

func TestNewKillCmdRunE(t *testing.T) {
	t.Cleanup(viper.Reset)

	tests := []struct {
		name        string
		args        []string
		flags       map[string]string
		viperConfig map[string]string
		setupCtx    func(*cobra.Command)
		controller  *fakeKillController
		wantErr     string
		wantOutput  []string
	}{
		{
			name: "missing realm error",
			args: []string{"cell", "my-cell"},
			flags: map[string]string{
				"space": "my-space",
				"stack": "my-stack",
			},
			wantErr: "realm name is required",
		},
		{
			name: "missing space error",
			args: []string{"cell", "my-cell"},
			flags: map[string]string{
				"realm": "my-realm",
				"stack": "my-stack",
			},
			wantErr: "space name is required",
		},
		{
			name: "missing stack error",
			args: []string{"cell", "my-cell"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
			},
			wantErr: "stack name is required",
		},
		{
			name: "missing cell error for container",
			args: []string{"container", "my-container"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
				"stack": "my-stack",
			},
			wantErr: "cell name is required",
		},
		{
			name: "empty realm after trimming",
			args: []string{"cell", "my-cell"},
			flags: map[string]string{
				"realm": "   ",
				"space": "my-space",
				"stack": "my-stack",
			},
			wantErr: "realm name is required",
		},
		{
			name: "empty space after trimming",
			args: []string{"cell", "my-cell"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "   ",
				"stack": "my-stack",
			},
			wantErr: "space name is required",
		},
		{
			name: "empty stack after trimming",
			args: []string{"cell", "my-cell"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
				"stack": "   ",
			},
			wantErr: "stack name is required",
		},
		{
			name: "invalid resource type",
			args: []string{"invalid", "my-resource"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
				"stack": "my-stack",
			},
			wantErr: "invalid resource type",
		},
		{
			name: "controller creation error - missing logger",
			args: []string{"cell", "my-cell"},
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
			name: "successful cell kill",
			args: []string{"cell", "my-cell"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
				"stack": "my-stack",
			},
			controller: &fakeKillController{
				killCellFn: func(_ string, _ string, _ string, _ string) (*controller.KillCellResult, error) {
					return &controller.KillCellResult{
						CellName:  "my-cell",
						RealmName: "my-realm",
						SpaceName: "my-space",
						StackName: "my-stack",
						Killed:    true,
					}, nil
				},
			},
			wantOutput: []string{
				"Killed cell \"my-cell\" from stack \"my-stack\"",
			},
		},
		{
			name: "cell kill with controller error",
			args: []string{"cell", "my-cell"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
				"stack": "my-stack",
			},
			controller: &fakeKillController{
				killCellFn: func(_ string, _ string, _ string, _ string) (*controller.KillCellResult, error) {
					return nil, errors.New("failed to kill cell")
				},
			},
			wantErr: "failed to kill cell",
		},
		{
			name: "successful cell kill with trimmed whitespace",
			args: []string{"  cell  ", "  my-cell  "},
			flags: map[string]string{
				"realm": "  my-realm  ",
				"space": "  my-space  ",
				"stack": "  my-stack  ",
			},
			controller: &fakeKillController{
				killCellFn: func(name, realm, space, stack string) (*controller.KillCellResult, error) {
					// Verify that trimming happened
					if name != "my-cell" || realm != "my-realm" || space != "my-space" || stack != "my-stack" {
						return nil, errors.New("unexpected trimmed args")
					}
					return &controller.KillCellResult{
						CellName:  "my-cell",
						RealmName: "my-realm",
						SpaceName: "my-space",
						StackName: "my-stack",
						Killed:    true,
					}, nil
				},
			},
			wantOutput: []string{
				"Killed cell \"my-cell\" from stack \"my-stack\"",
			},
		},
		{
			name: "cell kill with values from viper config",
			args: []string{"cell", "my-cell"},
			viperConfig: map[string]string{
				config.KUKE_KILL_REALM.ViperKey: "viper-realm",
				config.KUKE_KILL_SPACE.ViperKey: "viper-space",
				config.KUKE_KILL_STACK.ViperKey: "viper-stack",
			},
			controller: &fakeKillController{
				killCellFn: func(name, realm, space, stack string) (*controller.KillCellResult, error) {
					if name != "my-cell" || realm != "viper-realm" || space != "viper-space" || stack != "viper-stack" {
						return nil, errors.New("unexpected args from viper")
					}
					return &controller.KillCellResult{
						CellName:  "my-cell",
						RealmName: "viper-realm",
						SpaceName: "viper-space",
						StackName: "viper-stack",
						Killed:    true,
					}, nil
				},
			},
			wantOutput: []string{
				"Killed cell \"my-cell\" from stack \"viper-stack\"",
			},
		},
		{
			name: "successful container kill",
			args: []string{"container", "my-container"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
				"stack": "my-stack",
				"cell":  "my-cell",
			},
			controller: &fakeKillController{
				killContainerFn: func(_ string, _ string, _ string, _ string, _ string) (*controller.KillContainerResult, error) {
					return &controller.KillContainerResult{
						ContainerName: "my-container",
						RealmName:     "my-realm",
						SpaceName:     "my-space",
						StackName:     "my-stack",
						CellName:      "my-cell",
						Killed:        true,
					}, nil
				},
			},
			wantOutput: []string{
				"Killed container \"my-container\" from cell \"my-cell\"",
			},
		},
		{
			name: "container kill with controller error",
			args: []string{"container", "my-container"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
				"stack": "my-stack",
				"cell":  "my-cell",
			},
			controller: &fakeKillController{
				killContainerFn: func(_ string, _ string, _ string, _ string, _ string) (*controller.KillContainerResult, error) {
					return nil, errors.New("failed to kill container")
				},
			},
			wantErr: "failed to kill container",
		},
		{
			name: "successful container kill with trimmed whitespace",
			args: []string{"  container  ", "  my-container  "},
			flags: map[string]string{
				"realm": "  my-realm  ",
				"space": "  my-space  ",
				"stack": "  my-stack  ",
				"cell":  "  my-cell  ",
			},
			controller: &fakeKillController{
				killContainerFn: func(name, realm, space, stack, cell string) (*controller.KillContainerResult, error) {
					// Verify that trimming happened
					if name != "my-container" || realm != "my-realm" || space != "my-space" || stack != "my-stack" ||
						cell != "my-cell" {
						return nil, errors.New("unexpected trimmed args")
					}
					return &controller.KillContainerResult{
						ContainerName: "my-container",
						RealmName:     "my-realm",
						SpaceName:     "my-space",
						StackName:     "my-stack",
						CellName:      "my-cell",
						Killed:        true,
					}, nil
				},
			},
			wantOutput: []string{
				"Killed container \"my-container\" from cell \"my-cell\"",
			},
		},
		{
			name: "container kill with values from viper config",
			args: []string{"container", "my-container"},
			viperConfig: map[string]string{
				config.KUKE_KILL_REALM.ViperKey: "viper-realm",
				config.KUKE_KILL_SPACE.ViperKey: "viper-space",
				config.KUKE_KILL_STACK.ViperKey: "viper-stack",
				config.KUKE_KILL_CELL.ViperKey:  "viper-cell",
			},
			controller: &fakeKillController{
				killContainerFn: func(name, realm, space, stack, cell string) (*controller.KillContainerResult, error) {
					if name != "my-container" || realm != "viper-realm" || space != "viper-space" ||
						stack != "viper-stack" ||
						cell != "viper-cell" {
						return nil, errors.New("unexpected args from viper")
					}
					return &controller.KillContainerResult{
						ContainerName: "my-container",
						RealmName:     "viper-realm",
						SpaceName:     "viper-space",
						StackName:     "viper-stack",
						CellName:      "viper-cell",
						Killed:        true,
					}, nil
				},
			},
			wantOutput: []string{
				"Killed container \"my-container\" from cell \"viper-cell\"",
			},
		},
		{
			name: "case insensitive resource type - CELL",
			args: []string{"CELL", "my-cell"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
				"stack": "my-stack",
			},
			controller: &fakeKillController{
				killCellFn: func(_ string, _ string, _ string, _ string) (*controller.KillCellResult, error) {
					return &controller.KillCellResult{
						CellName:  "my-cell",
						RealmName: "my-realm",
						SpaceName: "my-space",
						StackName: "my-stack",
						Killed:    true,
					}, nil
				},
			},
			wantOutput: []string{
				"Killed cell \"my-cell\" from stack \"my-stack\"",
			},
		},
		{
			name: "case insensitive resource type - CONTAINER",
			args: []string{"CONTAINER", "my-container"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
				"stack": "my-stack",
				"cell":  "my-cell",
			},
			controller: &fakeKillController{
				killContainerFn: func(_ string, _ string, _ string, _ string, _ string) (*controller.KillContainerResult, error) {
					return &controller.KillContainerResult{
						ContainerName: "my-container",
						RealmName:     "my-realm",
						SpaceName:     "my-space",
						StackName:     "my-stack",
						CellName:      "my-cell",
						Killed:        true,
					}, nil
				},
			},
			wantOutput: []string{
				"Killed container \"my-container\" from cell \"my-cell\"",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			viper.Reset()
			cmd := kill.NewKillCmd()
			var outBuf bytes.Buffer
			cmd.SetOut(&outBuf)
			cmd.SetErr(&outBuf)

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
				ctx = context.WithValue(ctx, kill.MockControllerKey{}, tt.controller)
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

			cmd.SetArgs(tt.args)
			err := cmd.Execute()

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

// Test helpers

// fakeKillController provides a mock implementation for testing KillCell and KillContainer.
type fakeKillController struct {
	killCellFn      func(name, realm, space, stack string) (*controller.KillCellResult, error)
	killContainerFn func(name, realm, space, stack, cell string) (*controller.KillContainerResult, error)
}

func (f *fakeKillController) KillCell(
	name, realmName, spaceName, stackName string,
) (*controller.KillCellResult, error) {
	if f.killCellFn == nil {
		return nil, errors.New("unexpected KillCell call")
	}
	return f.killCellFn(name, realmName, spaceName, stackName)
}

func (f *fakeKillController) KillContainer(
	name, realmName, spaceName, stackName, cellName string,
) (*controller.KillContainerResult, error) {
	if f.killContainerFn == nil {
		return nil, errors.New("unexpected KillContainer call")
	}
	return f.killContainerFn(name, realmName, spaceName, stackName, cellName)
}
