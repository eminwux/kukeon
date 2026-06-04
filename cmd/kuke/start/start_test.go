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

package start_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/config"
	startpkg "github.com/eminwux/kukeon/cmd/kuke/start"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/viper"
)

func TestNewStartCmdMetadata(t *testing.T) {
	cmd := startpkg.NewStartCmd()

	if cmd.Use != "start <name>" {
		t.Errorf("Use mismatch: got %q want %q", cmd.Use, "start <name>")
	}
	if cmd.Short != "Start a cell" {
		t.Errorf("Short mismatch: got %q", cmd.Short)
	}
	if !cmd.HasAlias("sta") {
		t.Errorf("expected alias %q to be registered", "sta")
	}
	if cmd.Args == nil {
		t.Errorf("expected Args validator on positional leaf, got nil")
	}
	for _, f := range []string{"realm", "space", "stack"} {
		if cmd.Flag(f) == nil {
			t.Errorf("expected flag --%s to be registered", f)
		}
	}
	if cmd.ValidArgsFunction == nil {
		t.Error("expected ValidArgsFunction to be set for cell-name completion")
	}
	if len(cmd.Commands()) != 0 {
		t.Errorf("expected no subcommands on collapsed leaf, got %d", len(cmd.Commands()))
	}
}

func TestStartCmd(t *testing.T) {
	t.Cleanup(viper.Reset)

	tests := []struct {
		name       string
		args       []string
		setup      func()
		fake       *fakeClient
		wantErr    string
		wantOutput string
	}{
		{
			name: "success",
			args: []string{"c1"},
			setup: func() {
				viper.Set(config.KUKE_START_CELL_REALM.ViperKey, "r1")
				viper.Set(config.KUKE_START_CELL_SPACE.ViperKey, "s1")
				viper.Set(config.KUKE_START_CELL_STACK.ViperKey, "st1")
			},
			fake: &fakeClient{
				startCellFn: func(doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
					return kukeonv1.StartCellResult{Cell: doc, Started: true}, nil
				},
			},
			wantOutput: `Started cell "c1" from stack "st1"`,
		},
		{
			name:    "missing realm",
			args:    []string{"c1"},
			wantErr: "realm name is required",
		},
		{
			name: "client returns error",
			args: []string{"c1"},
			setup: func() {
				viper.Set(config.KUKE_START_CELL_REALM.ViperKey, "r1")
				viper.Set(config.KUKE_START_CELL_SPACE.ViperKey, "s1")
				viper.Set(config.KUKE_START_CELL_STACK.ViperKey, "st1")
			},
			fake: &fakeClient{
				startCellFn: func(_ v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
					return kukeonv1.StartCellResult{}, errdefs.ErrCellNotFound
				},
			},
			wantErr: "cell not found",
		},
		{
			name:    "missing positional",
			args:    []string{},
			wantErr: "accepts 1 arg",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)
			viper.Reset()
			if tt.setup != nil {
				tt.setup()
			}

			cmd := startpkg.NewStartCmd()
			buf := &bytes.Buffer{}
			cmd.SetOut(buf)
			cmd.SetErr(buf)
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
			if tt.fake != nil {
				ctx = context.WithValue(ctx, startpkg.MockControllerKey{}, kukeonv1.Client(tt.fake))
			}
			cmd.SetContext(ctx)
			cmd.SetArgs(tt.args)

			err := cmd.Execute()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("want err %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantOutput != "" && !strings.Contains(buf.String(), tt.wantOutput) {
				t.Errorf("output missing %q\nGot:\n%s", tt.wantOutput, buf.String())
			}
		})
	}
}

// TestStartCmd_RejectsCellSubcommand pins the hard CLI break: the `start cell <name>` subcommand form
// must fail with cobra's Args-validation error (`accepts 1 arg(s), received 2`)
// after the collapse — the verb is now a leaf with `cobra.ExactArgs(1)` and no
// subcommand list, so cobra's unknown-command path no longer fires.
func TestStartCmd_RejectsCellSubcommand(t *testing.T) {
	cmd := startpkg.NewStartCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"cell", "c1"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error invoking removed `start cell` subcommand, got nil")
	}
}

type fakeClient struct {
	kukeonv1.FakeClient

	startCellFn func(doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error)
}

func (f *fakeClient) StartCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
	if f.startCellFn == nil {
		return kukeonv1.StartCellResult{}, errors.New("unexpected StartCell call")
	}
	return f.startCellFn(doc)
}
