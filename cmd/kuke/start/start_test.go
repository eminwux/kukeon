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

	if cmd.Use != "start [name]" {
		t.Errorf("Use mismatch: got %q want %q", cmd.Use, "start [name]")
	}
	if cmd.Short != "Start a cell (or a fleet via -l <selector>)" {
		t.Errorf("Short mismatch: got %q", cmd.Short)
	}
	if !cmd.HasAlias("sta") {
		t.Errorf("expected alias %q to be registered", "sta")
	}
	if cmd.Args == nil {
		t.Errorf("expected Args validator on positional leaf, got nil")
	}
	for _, f := range []string{"realm", "space", "stack", "selector"} {
		if cmd.Flag(f) == nil {
			t.Errorf("expected flag --%s to be registered", f)
		}
	}
	if cmd.Flag("selector").Shorthand != "l" {
		t.Errorf("expected --selector shorthand -l, got %q", cmd.Flag("selector").Shorthand)
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
			name:    "no name and no selector",
			args:    []string{},
			wantErr: "cell name is required",
		},
		{
			name:    "name and selector are mutually exclusive",
			args:    []string{"c1", "-l", "env=prod"},
			wantErr: "--selector cannot be combined with a resource name",
		},
		{
			name: "selector fan-out starts every matched cell",
			args: []string{"-l", "env=prod"},
			fake: &fakeClient{
				listCellsFn: func() ([]v1beta1.CellDoc, error) {
					return []v1beta1.CellDoc{
						cellWithLabels("c1", "r1", "s1", "st1", map[string]string{"env": "prod"}),
						cellWithLabels("c2", "r1", "s1", "st1", map[string]string{"env": "dev"}),
						cellWithLabels("c3", "r2", "s2", "st2", map[string]string{"env": "prod"}),
					}, nil
				},
				startCellFn: func(doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
					return kukeonv1.StartCellResult{Cell: doc, Started: true}, nil
				},
			},
			wantOutput: `Started cell "c1" from stack "st1"`,
		},
		{
			name: "selector matching nothing reports no match",
			args: []string{"-l", "env=staging"},
			fake: &fakeClient{
				listCellsFn: func() ([]v1beta1.CellDoc, error) {
					return []v1beta1.CellDoc{
						cellWithLabels("c1", "r1", "s1", "st1", map[string]string{"env": "prod"}),
					}, nil
				},
			},
			wantOutput: "No cells matched the selector.",
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
// must fail with cobra's Args-validation error (`accepts at most 1 arg(s), received 2`)
// after the collapse — the verb is now a leaf with `cobra.MaximumNArgs(1)` (a bare
// name or a bare `-l` selector, never two positionals) and no subcommand list, so
// cobra's unknown-command path no longer fires.
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
	listCellsFn func() ([]v1beta1.CellDoc, error)
	started     []string
}

func (f *fakeClient) StartCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
	if f.startCellFn == nil {
		return kukeonv1.StartCellResult{}, errors.New("unexpected StartCell call")
	}
	f.started = append(f.started, doc.Metadata.Name)
	return f.startCellFn(doc)
}

func (f *fakeClient) ListCells(_ context.Context, _, _, _ string) ([]v1beta1.CellDoc, error) {
	if f.listCellsFn == nil {
		return nil, errors.New("unexpected ListCells call")
	}
	return f.listCellsFn()
}

// cellWithLabels builds a minimal CellDoc carrying the given scope and labels
// for selector fan-out tests.
func cellWithLabels(name, realm, space, stack string, labels map[string]string) v1beta1.CellDoc {
	return v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{Name: name, Labels: labels},
		Spec: v1beta1.CellSpec{
			ID:      name,
			RealmID: realm,
			SpaceID: space,
			StackID: stack,
		},
	}
}

// TestStartCmd_SelectorFanOutIndividual pins AC #1: `-l` re-resolves every
// matched cell individually and leaves unmatched cells untouched.
func TestStartCmd_SelectorFanOutIndividual(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()

	fake := &fakeClient{
		listCellsFn: func() ([]v1beta1.CellDoc, error) {
			return []v1beta1.CellDoc{
				cellWithLabels("c1", "r1", "s1", "st1", map[string]string{"env": "prod"}),
				cellWithLabels("c2", "r1", "s1", "st1", map[string]string{"env": "dev"}),
				cellWithLabels("c3", "r2", "s2", "st2", map[string]string{"env": "prod"}),
			}, nil
		},
		startCellFn: func(doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
			return kukeonv1.StartCellResult{Cell: doc, Started: true}, nil
		},
	}

	cmd := startpkg.NewStartCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	ctx = context.WithValue(ctx, startpkg.MockControllerKey{}, kukeonv1.Client(fake))
	cmd.SetContext(ctx)
	cmd.SetArgs([]string{"-l", "env=prod"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{"c1", "c3"}
	if len(fake.started) != len(want) {
		t.Fatalf("started %v, want %v", fake.started, want)
	}
	for i, name := range want {
		if fake.started[i] != name {
			t.Errorf("started[%d] = %q, want %q (started=%v)", i, fake.started[i], name, fake.started)
		}
	}
	for _, name := range fake.started {
		if name == "c2" {
			t.Errorf("unmatched cell c2 was started; fan-out must leave it untouched")
		}
	}
}
