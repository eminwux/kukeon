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
	cell "github.com/eminwux/kukeon/cmd/kuke/delete/cell"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var _ = cell.NewCellCmd // ensure cell package is linked in

func TestNewCellCmdRunE(t *testing.T) {
	t.Cleanup(func() {
		viper.Reset()
	})

	tests := []struct {
		name           string
		args           []string
		setup          func(t *testing.T, cmd *cobra.Command)
		controllerFn   func(name, realm, space, stack string) (*controller.DeleteCellResult, error)
		wantErr        string
		wantCallDelete bool
		wantOpts       *struct {
			name  string
			realm string
			space string
			stack string
		}
		wantOutput []string
	}{
		{
			name: "success: all flags provided",
			args: []string{"test-cell"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "space", "space-a")
				setFlag(t, cmd, "stack", "stack-a")
			},
			controllerFn: func(name, realm, space, stack string) (*controller.DeleteCellResult, error) {
				return &controller.DeleteCellResult{
					CellName:  name,
					RealmName: realm,
					SpaceName: space,
					StackName: stack,
					Deleted:   []string{"containers:2", "cgroup", "metadata"},
				}, nil
			},
			wantCallDelete: true,
			wantOpts: &struct {
				name  string
				realm string
				space string
				stack string
			}{
				name:  "test-cell",
				realm: "realm-a",
				space: "space-a",
				stack: "stack-a",
			},
			wantOutput: []string{`Deleted cell "test-cell" from stack "stack-a"`},
		},
		{
			name: "success: values from viper config",
			args: []string{"viper-cell"},
			setup: func(_ *testing.T, _ *cobra.Command) {
				viper.Set(config.KUKE_DELETE_CELL_REALM.ViperKey, "realm-b")
				viper.Set(config.KUKE_DELETE_CELL_SPACE.ViperKey, "space-b")
				viper.Set(config.KUKE_DELETE_CELL_STACK.ViperKey, "stack-b")
			},
			controllerFn: func(name, realm, space, stack string) (*controller.DeleteCellResult, error) {
				return &controller.DeleteCellResult{
					CellName:  name,
					RealmName: realm,
					SpaceName: space,
					StackName: stack,
					Deleted:   []string{"containers:0", "cgroup", "metadata"},
				}, nil
			},
			wantCallDelete: true,
			wantOpts: &struct {
				name  string
				realm string
				space string
				stack string
			}{
				name:  "viper-cell",
				realm: "realm-b",
				space: "space-b",
				stack: "stack-b",
			},
			wantOutput: []string{`Deleted cell "viper-cell" from stack "stack-b"`},
		},
		{
			name: "success: whitespace trimming on args and flags",
			args: []string{"  test-cell  "},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "  realm-a  ")
				setFlag(t, cmd, "space", "  space-a  ")
				setFlag(t, cmd, "stack", "  stack-a  ")
			},
			controllerFn: func(name, realm, space, stack string) (*controller.DeleteCellResult, error) {
				return &controller.DeleteCellResult{
					CellName:  name,
					RealmName: realm,
					SpaceName: space,
					StackName: stack,
					Deleted:   []string{"containers:1", "cgroup", "metadata"},
				}, nil
			},
			wantCallDelete: true,
			wantOpts: &struct {
				name  string
				realm string
				space string
				stack string
			}{
				name:  "test-cell",
				realm: "realm-a",
				space: "space-a",
				stack: "stack-a",
			},
			wantOutput: []string{`Deleted cell "test-cell" from stack "stack-a"`},
		},
		{
			name: "error: missing realm",
			args: []string{"test-cell"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "space", "space-a")
				setFlag(t, cmd, "stack", "stack-a")
			},
			wantErr:        "realm name is required",
			wantCallDelete: false,
		},
		{
			name: "error: missing space",
			args: []string{"test-cell"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "stack", "stack-a")
			},
			wantErr:        "space name is required",
			wantCallDelete: false,
		},
		{
			name: "error: missing stack",
			args: []string{"test-cell"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "space", "space-a")
			},
			wantErr:        "stack name is required",
			wantCallDelete: false,
		},
		{
			name: "error: empty realm after trimming whitespace",
			args: []string{"test-cell"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "   ")
				setFlag(t, cmd, "space", "space-a")
				setFlag(t, cmd, "stack", "stack-a")
			},
			wantErr:        "realm name is required",
			wantCallDelete: false,
		},
		{
			name: "error: empty space after trimming whitespace",
			args: []string{"test-cell"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "space", "   ")
				setFlag(t, cmd, "stack", "stack-a")
			},
			wantErr:        "space name is required",
			wantCallDelete: false,
		},
		{
			name: "error: empty stack after trimming whitespace",
			args: []string{"test-cell"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "space", "space-a")
				setFlag(t, cmd, "stack", "   ")
			},
			wantErr:        "stack name is required",
			wantCallDelete: false,
		},
		{
			name: "error: logger not in context",
			args: []string{"test-cell"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				cmd.SetContext(context.Background())
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "space", "space-a")
				setFlag(t, cmd, "stack", "stack-a")
			},
			wantErr:        "logger not found",
			wantCallDelete: false,
		},
		{
			name: "error: DeleteCell fails with ErrDeleteCell",
			args: []string{"test-cell"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "space", "space-a")
				setFlag(t, cmd, "stack", "stack-a")
			},
			controllerFn: func(_ string, _ string, _ string, _ string) (*controller.DeleteCellResult, error) {
				return nil, errdefs.ErrDeleteCell
			},
			wantErr:        "failed to delete cell",
			wantCallDelete: true,
			wantOpts: &struct {
				name  string
				realm string
				space string
				stack string
			}{
				name:  "test-cell",
				realm: "realm-a",
				space: "space-a",
				stack: "stack-a",
			},
		},
		{
			name: "error: DeleteCell fails with ErrCellNotFound",
			args: []string{"missing-cell"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "space", "space-a")
				setFlag(t, cmd, "stack", "stack-a")
			},
			controllerFn: func(_ string, _ string, _ string, _ string) (*controller.DeleteCellResult, error) {
				return nil, errdefs.ErrCellNotFound
			},
			wantErr:        "cell not found",
			wantCallDelete: true,
			wantOpts: &struct {
				name  string
				realm string
				space string
				stack string
			}{
				name:  "missing-cell",
				realm: "realm-a",
				space: "space-a",
				stack: "stack-a",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)

			var deleteCalled bool
			var deleteOpts struct {
				name  string
				realm string
				space string
				stack string
			}

			cmd := cell.NewCellCmd()
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
					fakeCtrl := &fakeControllerExec{
						deleteCellFn: func(name, realm, space, stack string) (*controller.DeleteCellResult, error) {
							deleteCalled = true
							deleteOpts.name = name
							deleteOpts.realm = realm
							deleteOpts.space = space
							deleteOpts.stack = stack
							return tt.controllerFn(name, realm, space, stack)
						},
					}
					// Inject mock controller into context
					ctx = context.WithValue(ctx, cell.MockControllerKey{}, fakeCtrl)
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

			if deleteCalled != tt.wantCallDelete {
				t.Errorf("DeleteCell called=%v want=%v", deleteCalled, tt.wantCallDelete)
			}

			if tt.wantOpts != nil {
				if deleteOpts.name != tt.wantOpts.name {
					t.Errorf("DeleteCell name=%q want=%q", deleteOpts.name, tt.wantOpts.name)
				}
				if deleteOpts.realm != tt.wantOpts.realm {
					t.Errorf("DeleteCell realm=%q want=%q", deleteOpts.realm, tt.wantOpts.realm)
				}
				if deleteOpts.space != tt.wantOpts.space {
					t.Errorf("DeleteCell space=%q want=%q", deleteOpts.space, tt.wantOpts.space)
				}
				if deleteOpts.stack != tt.wantOpts.stack {
					t.Errorf("DeleteCell stack=%q want=%q", deleteOpts.stack, tt.wantOpts.stack)
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

type fakeControllerExec struct {
	deleteCellFn func(name, realm, space, stack string) (*controller.DeleteCellResult, error)
}

func (f *fakeControllerExec) DeleteCell(name, realm, space, stack string) (*controller.DeleteCellResult, error) {
	if f.deleteCellFn == nil {
		return nil, errors.New("unexpected DeleteCell call")
	}
	return f.deleteCellFn(name, realm, space, stack)
}

func setFlag(t *testing.T, cmd *cobra.Command, name, value string) {
	t.Helper()
	if err := cmd.Flags().Set(name, value); err != nil {
		t.Fatalf("failed to set flag %s: %v", name, err)
	}
}
