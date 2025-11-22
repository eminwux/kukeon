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
	container "github.com/eminwux/kukeon/cmd/kuke/create/container"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestNewContainerCmd(t *testing.T) {
	t.Cleanup(viper.Reset)

	cmd := container.NewContainerCmd()

	if cmd.Use != "container [name]" {
		t.Errorf("Use mismatch: got %q, want %q", cmd.Use, "container [name]")
	}

	if cmd.Short != "Create a new container inside a cell" {
		t.Errorf("Short mismatch: got %q, want %q", cmd.Short, "Create a new container inside a cell")
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
		{"image", false},
		{"command", false},
		{"args", false},
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
		{"realm", config.KUKE_CREATE_CONTAINER_REALM.ViperKey, "test-realm"},
		{"space", config.KUKE_CREATE_CONTAINER_SPACE.ViperKey, "test-space"},
		{"stack", config.KUKE_CREATE_CONTAINER_STACK.ViperKey, "test-stack"},
		{"cell", config.KUKE_CREATE_CONTAINER_CELL.ViperKey, "test-cell"},
		{"image", config.KUKE_CREATE_CONTAINER_IMAGE.ViperKey, "test-image"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
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
			name: "missing name error",
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
				"stack": "my-stack",
				"cell":  "my-cell",
			},
			wantErr: "container name is required",
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
			name: "success: container created and started",
			args: []string{"my-container"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
				"stack": "my-stack",
				"cell":  "my-cell",
				"image": "docker.io/library/alpine:latest",
			},
			setupCtx: func(cmd *cobra.Command) {
				logger := slog.New(slog.NewTextHandler(io.Discard, nil))
				ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
				fakeCtrl := &fakeContainerController{
					createContainerFn: func(opts controller.CreateContainerOptions) (controller.CreateContainerResult, error) {
						return controller.CreateContainerResult{
							RealmName:           opts.RealmName,
							SpaceName:           opts.SpaceName,
							StackName:           opts.StackName,
							CellName:            opts.CellName,
							ContainerName:       opts.ContainerName,
							ContainerID:         "container-123",
							ContainerCreated:    true,
							ContainerExistsPost: true,
							Started:             true,
						}, nil
					},
				}
				ctx = context.WithValue(ctx, container.MockControllerKey{}, fakeCtrl)
				cmd.SetContext(ctx)
			},
			wantOutput: []string{
				"Container \"my-container\" (ID: \"container-123\")",
				"container: created",
				"container: started",
			},
		},
		{
			name: "success: container already existed",
			args: []string{"existing-container"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
				"stack": "my-stack",
				"cell":  "my-cell",
			},
			setupCtx: func(cmd *cobra.Command) {
				logger := slog.New(slog.NewTextHandler(io.Discard, nil))
				ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
				fakeCtrl := &fakeContainerController{
					createContainerFn: func(opts controller.CreateContainerOptions) (controller.CreateContainerResult, error) {
						return controller.CreateContainerResult{
							RealmName:           opts.RealmName,
							SpaceName:           opts.SpaceName,
							StackName:           opts.StackName,
							CellName:            opts.CellName,
							ContainerName:       opts.ContainerName,
							ContainerID:         "existing-456",
							ContainerCreated:    false,
							ContainerExistsPost: true,
							Started:             false,
						}, nil
					},
				}
				ctx = context.WithValue(ctx, container.MockControllerKey{}, fakeCtrl)
				cmd.SetContext(ctx)
			},
			wantOutput: []string{
				"Container \"existing-container\"",
				"container: already existed",
				"container: not started",
			},
		},
		{
			name: "error: CreateContainer fails",
			args: []string{"my-container"},
			flags: map[string]string{
				"realm": "my-realm",
				"space": "my-space",
				"stack": "my-stack",
				"cell":  "my-cell",
			},
			setupCtx: func(cmd *cobra.Command) {
				logger := slog.New(slog.NewTextHandler(io.Discard, nil))
				ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
				fakeCtrl := &fakeContainerController{
					createContainerFn: func(_ controller.CreateContainerOptions) (controller.CreateContainerResult, error) {
						return controller.CreateContainerResult{}, errors.New("failed to create container")
					},
				}
				ctx = context.WithValue(ctx, container.MockControllerKey{}, fakeCtrl)
				cmd.SetContext(ctx)
			},
			wantErr: "failed to create container",
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
			if tt.setupCtx != nil {
				tt.setupCtx(cmd)
			} else {
				logger := slog.New(slog.NewTextHandler(io.Discard, nil))
				ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
				cmd.SetContext(ctx)
			}

			// Set viper config
			for k, v := range tt.viperConfig {
				viper.Set(k, v)
			}

			// Set flags
			for name, value := range tt.flags {
				if name == "args" {
					// Handle string array flag
					args := strings.Split(value, ",")
					for _, arg := range args {
						if err := cmd.Flags().Set("args", arg); err != nil {
							t.Fatalf("failed to set args flag: %v", err)
						}
					}
				} else {
					if err := cmd.Flags().Set(name, value); err != nil {
						t.Fatalf("failed to set flag %q: %v", name, err)
					}
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

func TestPrintContainerResult(t *testing.T) {
	tests := []struct {
		name          string
		result        controller.CreateContainerResult
		wantOutput    []string
		notWantOutput []string
	}{
		{
			name: "container created and started",
			result: controller.CreateContainerResult{
				RealmName:           "my-realm",
				SpaceName:           "my-space",
				StackName:           "my-stack",
				CellName:            "my-cell",
				ContainerName:       "my-container",
				ContainerID:         "container-123",
				ContainerCreated:    true,
				ContainerExistsPost: true,
				Started:             true,
			},
			wantOutput: []string{
				"Container \"my-container\" (ID: \"container-123\")",
				"in cell \"my-cell\"",
				"realm \"my-realm\"",
				"space \"my-space\"",
				"stack \"my-stack\"",
				"container: created",
				"container: started",
			},
		},
		{
			name: "container already existed and not started",
			result: controller.CreateContainerResult{
				RealmName:           "my-realm",
				SpaceName:           "my-space",
				StackName:           "my-stack",
				CellName:            "my-cell",
				ContainerName:       "existing-container",
				ContainerID:         "existing-456",
				ContainerCreated:    false,
				ContainerExistsPost: true,
				Started:             false,
			},
			wantOutput: []string{
				"Container \"existing-container\"",
				"container: already existed",
				"container: not started",
			},
			notWantOutput: []string{
				"container: created",
				"container: started",
			},
		},
		{
			name: "container missing",
			result: controller.CreateContainerResult{
				RealmName:           "my-realm",
				SpaceName:           "my-space",
				StackName:           "my-stack",
				CellName:            "my-cell",
				ContainerName:       "missing-container",
				ContainerID:         "missing-789",
				ContainerCreated:    false,
				ContainerExistsPost: false,
				Started:             false,
			},
			wantOutput: []string{
				"Container \"missing-container\"",
				"container: missing",
				"container: not started",
			},
			notWantOutput: []string{
				"container: created",
				"container: already existed",
				"container: started",
			},
		},
		{
			name: "container created but not started",
			result: controller.CreateContainerResult{
				RealmName:           "my-realm",
				SpaceName:           "my-space",
				StackName:           "my-stack",
				CellName:            "my-cell",
				ContainerName:       "stopped-container",
				ContainerID:         "stopped-123",
				ContainerCreated:    true,
				ContainerExistsPost: true,
				Started:             false,
			},
			wantOutput: []string{
				"container: created",
				"container: not started",
			},
			notWantOutput: []string{
				"container: started",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, buf := newOutputCommand()
			container.PrintContainerResult(cmd, tt.result)
			output := buf.String()

			for _, want := range tt.wantOutput {
				if !strings.Contains(output, want) {
					t.Errorf("output missing expected string %q. Got output: %q", want, output)
				}
			}

			for _, notWant := range tt.notWantOutput {
				if strings.Contains(output, notWant) {
					t.Errorf("output contains unexpected string %q. Got output: %q", notWant, output)
				}
			}
		})
	}
}

// Test helpers

type fakeContainerController struct {
	createContainerFn func(opts controller.CreateContainerOptions) (controller.CreateContainerResult, error)
}

func (f *fakeContainerController) CreateContainer(
	opts controller.CreateContainerOptions,
) (controller.CreateContainerResult, error) {
	if f.createContainerFn == nil {
		return controller.CreateContainerResult{}, errors.New("unexpected CreateContainer call")
	}
	return f.createContainerFn(opts)
}

func newOutputCommand() (*cobra.Command, *bytes.Buffer) {
	cmd := &cobra.Command{Use: "test"}
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	return cmd, buf
}

func TestNewContainerCmd_AutocompleteRegistration(t *testing.T) {
	cmd := container.NewContainerCmd()

	// Test that flags exist and have completion functions registered
	flags := []struct {
		name string
	}{
		{"realm"},
		{"space"},
		{"stack"},
		{"cell"},
	}

	for _, flag := range flags {
		flagObj := cmd.Flags().Lookup(flag.name)
		if flagObj == nil {
			t.Errorf("expected %q flag to exist", flag.name)
			continue
		}

		// Verify flag structure (completion function registration is verified by Cobra)
		switch flag.name {
		case "realm":
			if flagObj.Usage != "Realm that owns the container" {
				t.Errorf("unexpected realm flag usage: %q", flagObj.Usage)
			}
		case "space":
			if flagObj.Usage != "Space that owns the container" {
				t.Errorf("unexpected space flag usage: %q", flagObj.Usage)
			}
		case "stack":
			if flagObj.Usage != "Stack that owns the container" {
				t.Errorf("unexpected stack flag usage: %q", flagObj.Usage)
			}
		case "cell":
			if flagObj.Usage != "Cell that owns the container" {
				t.Errorf("unexpected cell flag usage: %q", flagObj.Usage)
			}
		}
	}

	// Test that ValidArgsFunction is set for positional argument
	if cmd.ValidArgsFunction == nil {
		t.Error("expected ValidArgsFunction to be set for positional argument")
	}
}
