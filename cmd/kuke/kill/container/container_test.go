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
	containercmd "github.com/eminwux/kukeon/cmd/kuke/kill/container"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestNewContainerCmdRunE(t *testing.T) {
	t.Cleanup(viper.Reset)

	tests := []struct {
		name          string
		args          []string
		setup         func(t *testing.T, cmd *cobra.Command)
		controller    *fakeContainerController
		wantErr       string
		wantOutput    []string
		wantDocFields *struct {
			name  string
			realm string
			space string
			stack string
			cell  string
		}
		skipLogger bool
	}{
		{
			name: "success with flags",
			args: []string{"test-container"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "space", "space-a")
				setFlag(t, cmd, "stack", "stack-a")
				setFlag(t, cmd, "cell", "cell-a")
			},
			controller: &fakeContainerController{
				killContainerFn: func(container intmodel.Container) (controller.KillContainerResult, error) {
					return controller.KillContainerResult{
						Container: container,
						Killed:    true,
					}, nil
				},
			},
			wantDocFields: &struct {
				name  string
				realm string
				space string
				stack string
				cell  string
			}{
				name:  "test-container",
				realm: "realm-a",
				space: "space-a",
				stack: "stack-a",
				cell:  "cell-a",
			},
			wantOutput: []string{`Killed container "test-container" from cell "cell-a"`},
		},
		{
			name: "success with viper config",
			args: []string{"viper-container"},
			setup: func(_ *testing.T, _ *cobra.Command) {
				viper.Set(config.KUKE_KILL_CONTAINER_REALM.ViperKey, "realm-b")
				viper.Set(config.KUKE_KILL_CONTAINER_SPACE.ViperKey, "space-b")
				viper.Set(config.KUKE_KILL_CONTAINER_STACK.ViperKey, "stack-b")
				viper.Set(config.KUKE_KILL_CONTAINER_CELL.ViperKey, "cell-b")
			},
			controller: &fakeContainerController{
				killContainerFn: func(container intmodel.Container) (controller.KillContainerResult, error) {
					return controller.KillContainerResult{
						Container: container,
						Killed:    true,
					}, nil
				},
			},
			wantDocFields: &struct {
				name  string
				realm string
				space string
				stack string
				cell  string
			}{
				name:  "viper-container",
				realm: "realm-b",
				space: "space-b",
				stack: "stack-b",
				cell:  "cell-b",
			},
			wantOutput: []string{`Killed container "viper-container" from cell "cell-b"`},
		},
		{
			name: "trims whitespace",
			args: []string{"  trim-me  "},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "  realm-c  ")
				setFlag(t, cmd, "space", "  space-c  ")
				setFlag(t, cmd, "stack", "  stack-c  ")
				setFlag(t, cmd, "cell", "  cell-c  ")
			},
			controller: &fakeContainerController{
				killContainerFn: func(container intmodel.Container) (controller.KillContainerResult, error) {
					return controller.KillContainerResult{
						Container: container,
						Killed:    true,
					}, nil
				},
			},
			wantDocFields: &struct {
				name  string
				realm string
				space string
				stack string
				cell  string
			}{
				name:  "trim-me",
				realm: "realm-c",
				space: "space-c",
				stack: "stack-c",
				cell:  "cell-c",
			},
			wantOutput: []string{`Killed container "trim-me" from cell "cell-c"`},
		},
		{
			name: "missing realm",
			args: []string{"test-container"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "space", "space-a")
				setFlag(t, cmd, "stack", "stack-a")
				setFlag(t, cmd, "cell", "cell-a")
			},
			wantErr: "realm name is required",
		},
		{
			name: "missing space",
			args: []string{"test-container"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "stack", "stack-a")
				setFlag(t, cmd, "cell", "cell-a")
			},
			wantErr: "space name is required",
		},
		{
			name: "missing stack",
			args: []string{"test-container"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "space", "space-a")
				setFlag(t, cmd, "cell", "cell-a")
			},
			wantErr: "stack name is required",
		},
		{
			name: "missing cell",
			args: []string{"test-container"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "space", "space-a")
				setFlag(t, cmd, "stack", "stack-a")
			},
			wantErr: "cell name is required",
		},
		{
			name: "logger missing in context",
			args: []string{"test-container"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				cmd.SetContext(context.Background())
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "space", "space-a")
				setFlag(t, cmd, "stack", "stack-a")
				setFlag(t, cmd, "cell", "cell-a")
			},
			wantErr:    errdefs.ErrLoggerNotFound.Error(),
			skipLogger: true,
		},
		{
			name: "controller returns error",
			args: []string{"test-container"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "space", "space-a")
				setFlag(t, cmd, "stack", "stack-a")
				setFlag(t, cmd, "cell", "cell-a")
			},
			controller: &fakeContainerController{
				killContainerFn: func(_ intmodel.Container) (controller.KillContainerResult, error) {
					return controller.KillContainerResult{}, errors.New("kill failed")
				},
			},
			wantErr: "kill failed",
			wantDocFields: &struct {
				name  string
				realm string
				space string
				stack string
				cell  string
			}{
				name:  "test-container",
				realm: "realm-a",
				space: "space-a",
				stack: "stack-a",
				cell:  "cell-a",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)

			cmd := containercmd.NewContainerCmd()
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			if tt.setup != nil {
				tt.setup(t, cmd)
			}

			ctx := context.Background()
			if !tt.skipLogger {
				logger := slog.New(slog.NewTextHandler(io.Discard, nil))
				ctx = context.WithValue(ctx, types.CtxLogger, logger)
			}
			if tt.controller != nil {
				ctx = context.WithValue(ctx, containercmd.MockControllerKey{}, tt.controller)
			}
			cmd.SetContext(ctx)

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

			if tt.wantOutput != nil {
				output := cmd.OutOrStdout().(*bytes.Buffer).String()
				for _, want := range tt.wantOutput {
					if !strings.Contains(output, want) {
						t.Errorf("output missing %q\nGot:\n%s", want, output)
					}
				}
			}

			if tt.wantDocFields != nil {
				gotContainer := tt.controller.capturedContainer
				if gotContainer.Metadata.Name == "" && gotContainer.Spec.ID == "" {
					t.Fatal("expected captured container, got empty")
				}

				gotName := strings.TrimSpace(gotContainer.Metadata.Name)
				if gotName == "" {
					gotName = strings.TrimSpace(gotContainer.Spec.ID)
				}

				if gotName != tt.wantDocFields.name ||
					strings.TrimSpace(gotContainer.Spec.RealmName) != tt.wantDocFields.realm ||
					strings.TrimSpace(gotContainer.Spec.SpaceName) != tt.wantDocFields.space ||
					strings.TrimSpace(gotContainer.Spec.StackName) != tt.wantDocFields.stack ||
					strings.TrimSpace(gotContainer.Spec.CellName) != tt.wantDocFields.cell {
					t.Errorf("KillContainer called with name=%q realm=%q space=%q stack=%q cell=%q, want name=%q realm=%q space=%q stack=%q cell=%q",
						gotName, gotContainer.Spec.RealmName, gotContainer.Spec.SpaceName, gotContainer.Spec.StackName, gotContainer.Spec.CellName,
						tt.wantDocFields.name, tt.wantDocFields.realm, tt.wantDocFields.space, tt.wantDocFields.stack, tt.wantDocFields.cell)
				}
			}
		})
	}
}

func TestNewContainerCmd_AutocompleteRegistration(t *testing.T) {
	cmd := containercmd.NewContainerCmd()

	if cmd.ValidArgsFunction == nil {
		t.Fatal("expected ValidArgsFunction to be configured")
	}

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
			t.Fatalf("expected flag %q to exist", flag.name)
		}
		if flagObj.Usage != flag.usage {
			t.Errorf("unexpected usage for %q: got %q, want %q", flag.name, flagObj.Usage, flag.usage)
		}
	}
}

type fakeContainerController struct {
	killContainerFn   func(container intmodel.Container) (controller.KillContainerResult, error)
	capturedContainer intmodel.Container
}

func (f *fakeContainerController) KillContainer(
	container intmodel.Container,
) (controller.KillContainerResult, error) {
	if f.killContainerFn == nil {
		return controller.KillContainerResult{}, errors.New("unexpected KillContainer call")
	}

	f.capturedContainer = container

	return f.killContainerFn(container)
}

func setFlag(t *testing.T, cmd *cobra.Command, name, value string) {
	t.Helper()
	if err := cmd.Flags().Set(name, value); err != nil {
		t.Fatalf("failed to set flag %q: %v", name, err)
	}
}
