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
	cellcmd "github.com/eminwux/kukeon/cmd/kuke/kill/cell"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestNewCellCmdRunE(t *testing.T) {
	t.Cleanup(viper.Reset)

	tests := []struct {
		name       string
		args       []string
		setup      func(t *testing.T, cmd *cobra.Command)
		controller *fakeCellController
		wantErr    string
		wantOutput []string
		wantReqDoc *v1beta1.CellDoc
		skipLogger bool
	}{
		{
			name: "success with flags",
			args: []string{"test-cell"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "space", "space-a")
				setFlag(t, cmd, "stack", "stack-a")
			},
			controller: &fakeCellController{
				killCellFn: func(doc *v1beta1.CellDoc) (controller.KillCellResult, error) {
					return controller.KillCellResult{
						CellDoc: doc,
						Killed:  true,
					}, nil
				},
			},
			wantReqDoc: newCellDoc("test-cell", "realm-a", "space-a", "stack-a"),
			wantOutput: []string{`Killed cell "test-cell" from stack "stack-a"`},
		},
		{
			name: "success with viper config",
			args: []string{"cell-b"},
			setup: func(_ *testing.T, _ *cobra.Command) {
				viper.Set(config.KUKE_KILL_CELL_REALM.ViperKey, "realm-b")
				viper.Set(config.KUKE_KILL_CELL_SPACE.ViperKey, "space-b")
				viper.Set(config.KUKE_KILL_CELL_STACK.ViperKey, "stack-b")
			},
			controller: &fakeCellController{
				killCellFn: func(doc *v1beta1.CellDoc) (controller.KillCellResult, error) {
					return controller.KillCellResult{
						CellDoc: doc,
						Killed:  true,
					}, nil
				},
			},
			wantReqDoc: newCellDoc("cell-b", "realm-b", "space-b", "stack-b"),
			wantOutput: []string{`Killed cell "cell-b" from stack "stack-b"`},
		},
		{
			name: "trims whitespace",
			args: []string{"  trimmed  "},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "  realm-c  ")
				setFlag(t, cmd, "space", "  space-c  ")
				setFlag(t, cmd, "stack", "  stack-c  ")
			},
			controller: &fakeCellController{
				killCellFn: func(doc *v1beta1.CellDoc) (controller.KillCellResult, error) {
					return controller.KillCellResult{
						CellDoc: doc,
						Killed:  true,
					}, nil
				},
			},
			wantReqDoc: newCellDoc("trimmed", "realm-c", "space-c", "stack-c"),
			wantOutput: []string{`Killed cell "trimmed" from stack "stack-c"`},
		},
		{
			name: "missing realm",
			args: []string{"test-cell"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "space", "space-a")
				setFlag(t, cmd, "stack", "stack-a")
			},
			wantErr: "realm name is required",
		},
		{
			name: "missing space",
			args: []string{"test-cell"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "stack", "stack-a")
			},
			wantErr: "space name is required",
		},
		{
			name: "missing stack",
			args: []string{"test-cell"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "space", "space-a")
			},
			wantErr: "stack name is required",
		},
		{
			name: "logger missing in context",
			args: []string{"test-cell"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				cmd.SetContext(context.Background())
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "space", "space-a")
				setFlag(t, cmd, "stack", "stack-a")
			},
			wantErr:    errdefs.ErrLoggerNotFound.Error(),
			skipLogger: true,
		},
		{
			name: "controller returns error",
			args: []string{"test-cell"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "realm-a")
				setFlag(t, cmd, "space", "space-a")
				setFlag(t, cmd, "stack", "stack-a")
			},
			controller: &fakeCellController{
				killCellFn: func(_ *v1beta1.CellDoc) (controller.KillCellResult, error) {
					return controller.KillCellResult{}, errors.New("kill failed")
				},
			},
			wantErr:    "kill failed",
			wantReqDoc: newCellDoc("test-cell", "realm-a", "space-a", "stack-a"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)

			cmd := cellcmd.NewCellCmd()
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
				ctx = context.WithValue(ctx, cellcmd.MockControllerKey{}, tt.controller)
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

			if tt.wantReqDoc != nil {
				got := tt.controller.capturedDoc
				if got == nil {
					t.Fatalf("expected KillCell to be called, but captured doc is nil")
				}
				if got.Metadata.Name != tt.wantReqDoc.Metadata.Name ||
					got.Spec.RealmID != tt.wantReqDoc.Spec.RealmID ||
					got.Spec.SpaceID != tt.wantReqDoc.Spec.SpaceID ||
					got.Spec.StackID != tt.wantReqDoc.Spec.StackID {
					t.Errorf("KillCell called with %+v, want %+v", got, tt.wantReqDoc)
				}
			}
		})
	}
}

func TestNewCellCmd_AutocompleteRegistration(t *testing.T) {
	cmd := cellcmd.NewCellCmd()

	if cmd.ValidArgsFunction == nil {
		t.Fatal("expected ValidArgsFunction to be configured")
	}

	flags := []struct {
		name  string
		usage string
	}{
		{"realm", "Realm that owns the cell"},
		{"space", "Space that owns the cell"},
		{"stack", "Stack that owns the cell"},
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

type fakeCellController struct {
	killCellFn  func(doc *v1beta1.CellDoc) (controller.KillCellResult, error)
	capturedDoc *v1beta1.CellDoc
}

func (f *fakeCellController) KillCell(doc *v1beta1.CellDoc) (controller.KillCellResult, error) {
	if f.killCellFn == nil {
		return controller.KillCellResult{}, errors.New("unexpected KillCell call")
	}

	f.capturedDoc = doc
	return f.killCellFn(doc)
}

func newCellDoc(name, realm, space, stack string) *v1beta1.CellDoc {
	return &v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{
			Name: name,
		},
		Spec: v1beta1.CellSpec{
			RealmID: realm,
			SpaceID: space,
			StackID: stack,
		},
	}
}

func setFlag(t *testing.T, cmd *cobra.Command, name, value string) {
	t.Helper()
	if err := cmd.Flags().Set(name, value); err != nil {
		t.Fatalf("failed to set flag %q: %v", name, err)
	}
}
