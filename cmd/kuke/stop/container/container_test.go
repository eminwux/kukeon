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
	container "github.com/eminwux/kukeon/cmd/kuke/stop/container"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var _ = container.NewContainerCmd // ensure container package is linked in

type docExpectation struct {
	name  string
	realm string
	space string
	stack string
	cell  string
}

func TestNewContainerCmdRunE(t *testing.T) {
	t.Cleanup(func() {
		viper.Reset()
	})

	tests := []struct {
		name         string
		args         []string
		setup        func(t *testing.T, cmd *cobra.Command)
		controllerFn func(doc *v1beta1.ContainerDoc) (controller.StopContainerResult, error)
		wantErr      string
		wantCallStop bool
		wantDoc      *docExpectation
		wantOutput   []string
	}{
		{
			name: "success: all flags provided",
			args: []string{"test-container"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "space", "space-a")
				setFlag(t, cmd, "stack", "stack-a")
				setFlag(t, cmd, "cell", "cell-a")
			},
			controllerFn: func(doc *v1beta1.ContainerDoc) (controller.StopContainerResult, error) {
				return buildStopResult(doc, true), nil
			},
			wantCallStop: true,
			wantDoc: &docExpectation{
				name:  "test-container",
				realm: "realm-a",
				space: "space-a",
				stack: "stack-a",
				cell:  "cell-a",
			},
			wantOutput: []string{`Stopped container "test-container" from cell "cell-a"`},
		},
		{
			name: "success: values from viper config",
			args: []string{"viper-container"},
			setup: func(_ *testing.T, _ *cobra.Command) {
				viper.Set(config.KUKE_STOP_CONTAINER_REALM.ViperKey, "realm-b")
				viper.Set(config.KUKE_STOP_CONTAINER_SPACE.ViperKey, "space-b")
				viper.Set(config.KUKE_STOP_CONTAINER_STACK.ViperKey, "stack-b")
				viper.Set(config.KUKE_STOP_CONTAINER_CELL.ViperKey, "cell-b")
			},
			controllerFn: func(doc *v1beta1.ContainerDoc) (controller.StopContainerResult, error) {
				return buildStopResult(doc, true), nil
			},
			wantCallStop: true,
			wantDoc: &docExpectation{
				name:  "viper-container",
				realm: "realm-b",
				space: "space-b",
				stack: "stack-b",
				cell:  "cell-b",
			},
			wantOutput: []string{`Stopped container "viper-container" from cell "cell-b"`},
		},
		{
			name: "success: whitespace trimming on args and flags",
			args: []string{"  test-container  "},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "  realm-a  ")
				setFlag(t, cmd, "space", "  space-a  ")
				setFlag(t, cmd, "stack", "  stack-a  ")
				setFlag(t, cmd, "cell", "  cell-a  ")
			},
			controllerFn: func(doc *v1beta1.ContainerDoc) (controller.StopContainerResult, error) {
				return buildStopResult(doc, true), nil
			},
			wantCallStop: true,
			wantDoc: &docExpectation{
				name:  "test-container",
				realm: "realm-a",
				space: "space-a",
				stack: "stack-a",
				cell:  "cell-a",
			},
			wantOutput: []string{`Stopped container "test-container" from cell "cell-a"`},
		},
		{
			name: "success: container already stopped",
			args: []string{"stopped-container"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "space", "space-a")
				setFlag(t, cmd, "stack", "stack-a")
				setFlag(t, cmd, "cell", "cell-a")
			},
			controllerFn: func(doc *v1beta1.ContainerDoc) (controller.StopContainerResult, error) {
				return buildStopResult(doc, false), nil
			},
			wantCallStop: true,
			wantDoc: &docExpectation{
				name:  "stopped-container",
				realm: "realm-a",
				space: "space-a",
				stack: "stack-a",
				cell:  "cell-a",
			},
			wantOutput: []string{`Container "stopped-container" was already stopped in cell "cell-a"`},
		},
		{
			name: "error: missing realm",
			args: []string{"test-container"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "space", "space-a")
				setFlag(t, cmd, "stack", "stack-a")
				setFlag(t, cmd, "cell", "cell-a")
			},
			wantErr:      "realm name is required",
			wantCallStop: false,
		},
		{
			name: "error: missing space",
			args: []string{"test-container"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "stack", "stack-a")
				setFlag(t, cmd, "cell", "cell-a")
			},
			wantErr:      "space name is required",
			wantCallStop: false,
		},
		{
			name: "error: missing stack",
			args: []string{"test-container"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "space", "space-a")
				setFlag(t, cmd, "cell", "cell-a")
			},
			wantErr:      "stack name is required",
			wantCallStop: false,
		},
		{
			name: "error: missing cell",
			args: []string{"test-container"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "space", "space-a")
				setFlag(t, cmd, "stack", "stack-a")
			},
			wantErr:      "cell name is required",
			wantCallStop: false,
		},
		{
			name: "error: empty realm after trimming whitespace",
			args: []string{"test-container"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "   ")
				setFlag(t, cmd, "space", "space-a")
				setFlag(t, cmd, "stack", "stack-a")
				setFlag(t, cmd, "cell", "cell-a")
			},
			wantErr:      "realm name is required",
			wantCallStop: false,
		},
		{
			name: "error: empty space after trimming whitespace",
			args: []string{"test-container"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "space", "   ")
				setFlag(t, cmd, "stack", "stack-a")
				setFlag(t, cmd, "cell", "cell-a")
			},
			wantErr:      "space name is required",
			wantCallStop: false,
		},
		{
			name: "error: empty stack after trimming whitespace",
			args: []string{"test-container"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "space", "space-a")
				setFlag(t, cmd, "stack", "   ")
				setFlag(t, cmd, "cell", "cell-a")
			},
			wantErr:      "stack name is required",
			wantCallStop: false,
		},
		{
			name: "error: empty cell after trimming whitespace",
			args: []string{"test-container"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "space", "space-a")
				setFlag(t, cmd, "stack", "stack-a")
				setFlag(t, cmd, "cell", "   ")
			},
			wantErr:      "cell name is required",
			wantCallStop: false,
		},
		{
			name: "error: logger not in context",
			args: []string{"test-container"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				cmd.SetContext(context.Background())
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "space", "space-a")
				setFlag(t, cmd, "stack", "stack-a")
				setFlag(t, cmd, "cell", "cell-a")
			},
			wantErr:      "logger not found",
			wantCallStop: false,
		},
		{
			name: "error: StopContainer fails with ErrCellNotFound",
			args: []string{"test-container"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "space", "space-a")
				setFlag(t, cmd, "stack", "stack-a")
				setFlag(t, cmd, "cell", "missing-cell")
			},
			controllerFn: func(_ *v1beta1.ContainerDoc) (controller.StopContainerResult, error) {
				return controller.StopContainerResult{}, errdefs.ErrCellNotFound
			},
			wantErr:      "cell not found",
			wantCallStop: true,
			wantDoc: &docExpectation{
				name:  "test-container",
				realm: "realm-a",
				space: "space-a",
				stack: "stack-a",
				cell:  "missing-cell",
			},
		},
		{
			name: "error: StopContainer fails with generic error",
			args: []string{"test-container"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "space", "space-a")
				setFlag(t, cmd, "stack", "stack-a")
				setFlag(t, cmd, "cell", "cell-a")
			},
			controllerFn: func(_ *v1beta1.ContainerDoc) (controller.StopContainerResult, error) {
				return controller.StopContainerResult{}, errors.New("failed to stop container")
			},
			wantErr:      "failed to stop container",
			wantCallStop: true,
			wantDoc: &docExpectation{
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

			var stopCalled bool
			var stopDoc docExpectation

			cmd := container.NewContainerCmd()
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
					fakeCtrl := &fakeController{
						stopContainerFn: func(doc *v1beta1.ContainerDoc) (controller.StopContainerResult, error) {
							stopCalled = true
							if doc != nil {
								stopDoc.name = strings.TrimSpace(doc.Metadata.Name)
								stopDoc.realm = strings.TrimSpace(doc.Spec.RealmID)
								stopDoc.space = strings.TrimSpace(doc.Spec.SpaceID)
								stopDoc.stack = strings.TrimSpace(doc.Spec.StackID)
								stopDoc.cell = strings.TrimSpace(doc.Spec.CellID)
							} else {
								stopDoc = docExpectation{}
							}
							return tt.controllerFn(doc)
						},
					}
					// Inject mock controller into context
					ctx = context.WithValue(ctx, container.MockControllerKey{}, fakeCtrl)
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

			if stopCalled != tt.wantCallStop {
				t.Errorf("StopContainer called=%v want=%v", stopCalled, tt.wantCallStop)
			}

			assertDocExpectation(t, stopDoc, tt.wantDoc)

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

func buildStopResult(doc *v1beta1.ContainerDoc, stopped bool) controller.StopContainerResult {
	if doc == nil {
		return controller.StopContainerResult{}
	}

	return controller.StopContainerResult{
		Stopped:      stopped,
		ContainerDoc: doc,
	}
}

func assertDocExpectation(t *testing.T, got docExpectation, want *docExpectation) {
	t.Helper()
	if want == nil {
		return
	}

	if got.name != want.name {
		t.Errorf("ContainerDoc metadata.name=%q want=%q", got.name, want.name)
	}
	if got.realm != want.realm {
		t.Errorf("ContainerDoc spec.realm=%q want=%q", got.realm, want.realm)
	}
	if got.space != want.space {
		t.Errorf("ContainerDoc spec.space=%q want=%q", got.space, want.space)
	}
	if got.stack != want.stack {
		t.Errorf("ContainerDoc spec.stack=%q want=%q", got.stack, want.stack)
	}
	if got.cell != want.cell {
		t.Errorf("ContainerDoc spec.cell=%q want=%q", got.cell, want.cell)
	}
}

type fakeController struct {
	stopContainerFn func(doc *v1beta1.ContainerDoc) (controller.StopContainerResult, error)
}

func (f *fakeController) StopContainer(doc *v1beta1.ContainerDoc) (controller.StopContainerResult, error) {
	if f.stopContainerFn == nil {
		return controller.StopContainerResult{}, errors.New("unexpected StopContainer call")
	}
	return f.stopContainerFn(doc)
}

func setFlag(t *testing.T, cmd *cobra.Command, name, value string) {
	t.Helper()
	if err := cmd.Flags().Set(name, value); err != nil {
		t.Fatalf("failed to set flag %s: %v", name, err)
	}
}
