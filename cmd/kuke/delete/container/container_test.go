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

package container_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/config"
	container "github.com/eminwux/kukeon/cmd/kuke/delete/container"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestNewContainerCmd(t *testing.T) {
	t.Cleanup(viper.Reset)

	cmd := container.NewContainerCmd()

	if cmd.Use != "container [name]" {
		t.Errorf("Use mismatch: got %q, want %q", cmd.Use, "container [name]")
	}

	if cmd.Short != "Delete a container" {
		t.Errorf("Short mismatch: got %q, want %q", cmd.Short, "Delete a container")
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
		{"cell", true},
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
		{"realm", config.KUKE_DELETE_CONTAINER_REALM.ViperKey, "test-realm"},
		{"space", config.KUKE_DELETE_CONTAINER_SPACE.ViperKey, "test-space"},
		{"stack", config.KUKE_DELETE_CONTAINER_STACK.ViperKey, "test-stack"},
		{"cell", config.KUKE_DELETE_CONTAINER_CELL.ViperKey, "test-cell"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			viper.Reset()
			// Create a new command for each test to ensure clean state
			testCmd := container.NewContainerCmd()
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

func TestNewContainerCmdRunE(t *testing.T) {
	t.Cleanup(viper.Reset)

	tests := []struct {
		name        string
		args        []string
		flags       map[string]string
		viperConfig map[string]string
		setupCtx    func(*cobra.Command)
		controller  *fakeDeleteController
		wantErr     string
		wantOutput  []string
	}{
		{
			name: "missing realm error",
			args: []string{"my-container"},
			flags: map[string]string{
				"space": "my-space",
				"stack": "my-stack",
				"cell":  "my-cell",
			},
			wantErr: "realm name is required",
		},
		{
			name: "missing space error",
			args: []string{"my-container"},
			flags: map[string]string{
				"realm": "my-realm",
				"stack": "my-stack",
				"cell":  "my-cell",
			},
			wantErr: "space name is required",
		},
		{
			name: "missing stack error",
			args: []string{"my-container"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
				"cell":  "my-cell",
			},
			wantErr: "stack name is required",
		},
		{
			name: "missing cell error",
			args: []string{"my-container"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
				"stack": "my-stack",
			},
			wantErr: "cell name is required",
		},
		{
			name: "empty realm after trimming",
			args: []string{"my-container"},
			flags: map[string]string{
				"realm": "   ",
				"space": "my-space",
				"stack": "my-stack",
				"cell":  "my-cell",
			},
			wantErr: "realm name is required",
		},
		{
			name: "empty space after trimming",
			args: []string{"my-container"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "   ",
				"stack": "my-stack",
				"cell":  "my-cell",
			},
			wantErr: "space name is required",
		},
		{
			name: "empty stack after trimming",
			args: []string{"my-container"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
				"stack": "   ",
				"cell":  "my-cell",
			},
			wantErr: "stack name is required",
		},
		{
			name: "empty cell after trimming",
			args: []string{"my-container"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
				"stack": "my-stack",
				"cell":  "   ",
			},
			wantErr: "cell name is required",
		},
		{
			name: "empty container name after trimming",
			args: []string{"   "},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
				"stack": "my-stack",
				"cell":  "my-cell",
			},
			wantErr: "container name is required",
		},
		{
			name: "controller creation error - missing logger",
			args: []string{"my-container"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
				"stack": "my-stack",
				"cell":  "my-cell",
			},
			setupCtx: func(cmd *cobra.Command) {
				// Don't set logger in context
				cmd.SetContext(context.Background())
			},
			wantErr: "logger not found",
		},
		{
			name: "controller DeleteContainer returns error",
			args: []string{"my-container"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
				"stack": "my-stack",
				"cell":  "my-cell",
			},
			controller: &fakeDeleteController{
				deleteContainerFn: func(container intmodel.Container) (controller.DeleteContainerResult, error) {
					if container.Metadata.Name != "my-container" ||
						container.Spec.RealmName != "my-realm" ||
						container.Spec.SpaceName != "my-space" ||
						container.Spec.StackName != "my-stack" ||
						container.Spec.CellName != "my-cell" {
						return controller.DeleteContainerResult{}, errors.New("unexpected args")
					}
					return controller.DeleteContainerResult{}, errors.New("container not found")
				},
			},
			wantErr: "container not found",
		},
		{
			name: "controller DeleteContainer returns container not found error",
			args: []string{"nonexistent-container"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
				"stack": "my-stack",
				"cell":  "my-cell",
			},
			controller: &fakeDeleteController{
				deleteContainerFn: func(_ intmodel.Container) (controller.DeleteContainerResult, error) {
					return controller.DeleteContainerResult{}, errdefs.ErrDeleteContainer
				},
			},
			wantErr: "failed to delete container",
		},
		{
			name: "successful container deletion",
			args: []string{"my-container"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
				"stack": "my-stack",
				"cell":  "my-cell",
			},
			controller: &fakeDeleteController{
				deleteContainerFn: func(container intmodel.Container) (controller.DeleteContainerResult, error) {
					if container.Metadata.Name != "my-container" || container.Spec.RealmName != "my-realm" ||
						container.Spec.SpaceName != "my-space" ||
						container.Spec.StackName != "my-stack" ||
						container.Spec.CellName != "my-cell" {
						return controller.DeleteContainerResult{}, errors.New("unexpected args")
					}
					return controller.DeleteContainerResult{
						Container:          container,
						CellMetadataExists: true,
						ContainerExists:    true,
						Deleted:            []string{"container", "task"},
					}, nil
				},
			},
			wantOutput: []string{
				"Deleted container \"my-container\" from cell \"my-cell\"",
			},
		},
		{
			name: "successful deletion with trimmed whitespace in args and flags",
			args: []string{"  my-container  "},
			flags: map[string]string{
				"realm": "  my-realm  ",
				"space": "  my-space  ",
				"stack": "  my-stack  ",
				"cell":  "  my-cell  ",
			},
			controller: &fakeDeleteController{
				deleteContainerFn: func(container intmodel.Container) (controller.DeleteContainerResult, error) {
					// Verify that trimming happened
					if container.Metadata.Name != "my-container" || container.Spec.RealmName != "my-realm" ||
						container.Spec.SpaceName != "my-space" ||
						container.Spec.StackName != "my-stack" ||
						container.Spec.CellName != "my-cell" {
						return controller.DeleteContainerResult{}, errors.New("unexpected trimmed args")
					}
					return controller.DeleteContainerResult{
						Container:          container,
						CellMetadataExists: true,
						ContainerExists:    true,
						Deleted:            []string{"container"},
					}, nil
				},
			},
			wantOutput: []string{
				"Deleted container \"my-container\" from cell \"my-cell\"",
			},
		},
		{
			name: "values from viper config",
			args: []string{"my-container"},
			viperConfig: map[string]string{
				config.KUKE_DELETE_CONTAINER_REALM.ViperKey: "viper-realm",
				config.KUKE_DELETE_CONTAINER_SPACE.ViperKey: "viper-space",
				config.KUKE_DELETE_CONTAINER_STACK.ViperKey: "viper-stack",
				config.KUKE_DELETE_CONTAINER_CELL.ViperKey:  "viper-cell",
			},
			controller: &fakeDeleteController{
				deleteContainerFn: func(container intmodel.Container) (controller.DeleteContainerResult, error) {
					if container.Metadata.Name != "my-container" || container.Spec.RealmName != "viper-realm" ||
						container.Spec.SpaceName != "viper-space" ||
						container.Spec.StackName != "viper-stack" ||
						container.Spec.CellName != "viper-cell" {
						return controller.DeleteContainerResult{}, errors.New("unexpected args from viper")
					}
					return controller.DeleteContainerResult{
						Container:          container,
						CellMetadataExists: true,
						ContainerExists:    true,
						Deleted:            []string{"container"},
					}, nil
				},
			},
			wantOutput: []string{
				"Deleted container \"my-container\" from cell \"viper-cell\"",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			viper.Reset()
			cmd := container.NewContainerCmd()
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
				ctx = context.WithValue(ctx, container.MockControllerKey{}, tt.controller)
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

func TestNewContainerCmd_AutocompleteRegistration(t *testing.T) {
	cmd := container.NewContainerCmd()

	// Test that ValidArgsFunction is set for positional argument
	if cmd.ValidArgsFunction == nil {
		t.Fatal("expected ValidArgsFunction to be set for positional argument")
	}

	// Test that all flags exist and have correct usage
	flags := []struct {
		name  string
		usage string
	}{
		{"realm", "Realm that owns the container"},
		{"space", "Space that owns the container"},
		{"stack", "Stack that owns the container"},
		{"cell", "Cell that owns the container"},
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

// fakeDeleteController provides a mock implementation for testing DeleteContainer.
type fakeDeleteController struct {
	deleteContainerFn func(container intmodel.Container) (controller.DeleteContainerResult, error)
}

func (f *fakeDeleteController) DeleteContainer(
	container intmodel.Container,
) (controller.DeleteContainerResult, error) {
	if f.deleteContainerFn == nil {
		return controller.DeleteContainerResult{}, errors.New("unexpected DeleteContainer call")
	}
	return f.deleteContainerFn(container)
}
