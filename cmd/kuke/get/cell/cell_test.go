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

	cell "github.com/eminwux/kukeon/cmd/kuke/get/cell"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/cellconfig"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestNewCellCmd(t *testing.T) {
	t.Cleanup(viper.Reset)

	tests := []struct {
		name       string
		args       []string
		setup      func(t *testing.T, cmd *cobra.Command)
		fake       *fakeClient
		wantErr    string
		wantOutput string
	}{
		{
			name: "get single cell",
			args: []string{"ce1"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				_ = cmd.Flags().Set("realm", "r1")
				_ = cmd.Flags().Set("space", "s1")
				_ = cmd.Flags().Set("stack", "st1")
			},
			fake: &fakeClient{
				getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
					return kukeonv1.GetCellResult{
						Cell: v1beta1.CellDoc{
							Metadata: v1beta1.CellMetadata{Name: "ce1"},
							Spec:     v1beta1.CellSpec{RealmID: "r1", SpaceID: "s1", StackID: "st1"},
						},
						MetadataExists: true,
					}, nil
				},
			},
		},
		{
			name: "get not found",
			args: []string{"missing"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				_ = cmd.Flags().Set("realm", "r1")
				_ = cmd.Flags().Set("space", "s1")
				_ = cmd.Flags().Set("stack", "st1")
			},
			fake: &fakeClient{
				getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
					return kukeonv1.GetCellResult{}, errdefs.ErrCellNotFound
				},
			},
			wantErr: `cell "missing" not found`,
		},
		{
			name: "list cells",
			fake: &fakeClient{
				listCellsFn: func(_, _, _ string) ([]v1beta1.CellDoc, error) {
					return []v1beta1.CellDoc{{Metadata: v1beta1.CellMetadata{Name: "ce1"}}}, nil
				},
			},
			wantOutput: "ce1",
		},
		{
			name: "list empty",
			fake: &fakeClient{
				listCellsFn: func(_, _, _ string) ([]v1beta1.CellDoc, error) { return nil, nil },
			},
			wantOutput: "No cells found.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)

			cmd := cell.NewCellCmd()
			buf := &bytes.Buffer{}
			cmd.SetOut(buf)
			cmd.SetErr(buf)

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
			ctx = context.WithValue(ctx, cell.MockControllerKey{}, kukeonv1.Client(tt.fake))
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

// TestNewCellCmd_SyncColumn covers the SYNC column the `kuke get cell`
// default table acquired alongside #830's OutOfSync detection (issue #822).
// Each subtest pins a single mock cell and asserts the rendered SYNC value
// and (in -o wide) the DIVERGENCE value, plus that the SYNC header is
// always present in the default table. The fourth case asserts the full
// status fields land in -o yaml without an extra projection step — the
// strict-omitempty serialization in pkg/api/model/v1beta1/cell.go means
// non-default values surface automatically.
func TestNewCellCmd_SyncColumn(t *testing.T) {
	t.Cleanup(viper.Reset)

	configLineageLabels := map[string]string{cellconfig.LabelConfig: "prod"}

	tests := []struct {
		name        string
		cells       []v1beta1.CellDoc
		extraFlags  map[string]string
		wantHeaders []string
		wantRow     []string // substrings that must all appear on the cell's row
		wantStatus  []string // substrings that must all appear in yaml output
	}{
		{
			name: "synced cell renders Synced in default table",
			cells: []v1beta1.CellDoc{{
				Metadata: v1beta1.CellMetadata{Name: "ce1", Labels: configLineageLabels},
				Spec:     v1beta1.CellSpec{RealmID: "r1", SpaceID: "s1", StackID: "st1"},
				Status:   v1beta1.CellStatus{State: v1beta1.CellStateReady},
			}},
			wantHeaders: []string{"SYNC"},
			wantRow:     []string{"ce1", "Synced"},
		},
		{
			name: "out-of-sync cell renders OutOfSync in default table",
			cells: []v1beta1.CellDoc{{
				Metadata: v1beta1.CellMetadata{Name: "ce2", Labels: configLineageLabels},
				Spec:     v1beta1.CellSpec{RealmID: "r1", SpaceID: "s1", StackID: "st1"},
				Status: v1beta1.CellStatus{
					State:           v1beta1.CellStateReady,
					OutOfSync:       true,
					OutOfSyncReason: "spec differs: image",
				},
			}},
			wantHeaders: []string{"SYNC"},
			wantRow:     []string{"ce2", "OutOfSync"},
		},
		{
			name: "no-lineage cell renders dash in SYNC column",
			cells: []v1beta1.CellDoc{{
				Metadata: v1beta1.CellMetadata{Name: "ce3"},
				Spec:     v1beta1.CellSpec{RealmID: "r1", SpaceID: "s1", StackID: "st1"},
				Status:   v1beta1.CellStatus{State: v1beta1.CellStateReady},
			}},
			wantHeaders: []string{"SYNC"},
			// CGROUP retired in #827, so SYNC is now the last default column;
			// the no-lineage row ends in "-" preceded by the col separator,
			// which PrintTable's width-padded last column still surrounds
			// with spaces — " - " stays a stable substring.
			wantRow: []string{"ce3", " - "},
		},
		{
			name: "-o wide adds DIVERGENCE column with reason for out-of-sync cells",
			cells: []v1beta1.CellDoc{{
				Metadata: v1beta1.CellMetadata{Name: "ce4", Labels: configLineageLabels},
				Spec:     v1beta1.CellSpec{RealmID: "r1", SpaceID: "s1", StackID: "st1"},
				Status: v1beta1.CellStatus{
					State:           v1beta1.CellStateReady,
					OutOfSync:       true,
					OutOfSyncReason: "spec differs: image, env",
				},
			}},
			extraFlags:  map[string]string{"output": "wide"},
			wantHeaders: []string{"SYNC", "DIVERGENCE"},
			wantRow:     []string{"ce4", "OutOfSync", "spec differs: image, env"},
		},
		{
			name: "-o wide surfaces OutOfSyncError as error: <msg>",
			cells: []v1beta1.CellDoc{{
				Metadata: v1beta1.CellMetadata{Name: "ce5", Labels: configLineageLabels},
				Spec:     v1beta1.CellSpec{RealmID: "r1", SpaceID: "s1", StackID: "st1"},
				Status: v1beta1.CellStatus{
					State:          v1beta1.CellStateReady,
					OutOfSyncError: `blueprint "web" not found`,
				},
			}},
			extraFlags:  map[string]string{"output": "wide"},
			wantHeaders: []string{"SYNC", "DIVERGENCE"},
			// OutOfSyncError folds into the OutOfSync SYNC verdict; the
			// distinct surface lives in DIVERGENCE so the operator sees
			// the actionable signal in the column they're filtering on.
			wantRow: []string{"ce5", "OutOfSync", `error: blueprint "web" not found`},
		},
		{
			name: "-o yaml passes through outOfSync and outOfSyncReason fields",
			cells: []v1beta1.CellDoc{{
				Metadata: v1beta1.CellMetadata{Name: "ce6", Labels: configLineageLabels},
				Spec:     v1beta1.CellSpec{RealmID: "r1", SpaceID: "s1", StackID: "st1"},
				Status: v1beta1.CellStatus{
					State:           v1beta1.CellStateReady,
					OutOfSync:       true,
					OutOfSyncReason: "lineage Config deleted",
				},
			}},
			extraFlags: map[string]string{"output": "yaml"},
			wantStatus: []string{"outOfSync: true", "outOfSyncReason: lineage Config deleted"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)

			cmd := cell.NewCellCmd()
			buf := &bytes.Buffer{}
			cmd.SetOut(buf)
			cmd.SetErr(buf)

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
			fake := &fakeClient{
				listCellsFn: func(_, _, _ string) ([]v1beta1.CellDoc, error) {
					return tt.cells, nil
				},
			}
			ctx = context.WithValue(ctx, cell.MockControllerKey{}, kukeonv1.Client(fake))
			cmd.SetContext(ctx)

			for flag, val := range tt.extraFlags {
				if err := cmd.Flags().Set(flag, val); err != nil {
					t.Fatalf("set flag %s=%s: %v", flag, val, err)
				}
			}

			if err := cmd.Execute(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			out := buf.String()
			for _, h := range tt.wantHeaders {
				if !strings.Contains(out, h) {
					t.Errorf("table missing header %q\nGot:\n%s", h, out)
				}
			}
			for _, sub := range tt.wantRow {
				if !strings.Contains(out, sub) {
					t.Errorf("row missing %q\nGot:\n%s", sub, out)
				}
			}
			for _, sub := range tt.wantStatus {
				if !strings.Contains(out, sub) {
					t.Errorf("yaml output missing %q\nGot:\n%s", sub, out)
				}
			}
		})
	}
}

// TestNewCellCmd_NoCgroupOrControllers pins the epic:get step-1
// cross-cutting cleanup for cells: the CGROUP column and the
// --show-controllers flag must be gone in both default and -o wide
// output (the SYNC and DIVERGENCE columns added by earlier work are
// unaffected; -o wide still appends DIVERGENCE).
func TestNewCellCmd_NoCgroupOrControllers(t *testing.T) {
	t.Cleanup(viper.Reset)

	if cell.NewCellCmd().Flags().Lookup("show-controllers") != nil {
		t.Error("show-controllers flag must be removed (issue #827)")
	}

	listFn := func(_, _, _ string) ([]v1beta1.CellDoc, error) {
		return []v1beta1.CellDoc{{
			Metadata: v1beta1.CellMetadata{Name: "ce1"},
			Spec:     v1beta1.CellSpec{RealmID: "r1", SpaceID: "s1", StackID: "st1"},
			Status: v1beta1.CellStatus{
				State:              v1beta1.CellStateReady,
				CgroupPath:         "/kukeon/r1/s1/st1/ce1",
				SubtreeControllers: []string{"cpu", "memory"},
			},
		}}, nil
	}

	for _, args := range [][]string{nil, {"-o", "wide"}} {
		buf := &bytes.Buffer{}
		cmd := cell.NewCellCmd()
		cmd.SetOut(buf)
		cmd.SetErr(buf)
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
		ctx = context.WithValue(ctx, cell.MockControllerKey{},
			kukeonv1.Client(&fakeClient{listCellsFn: listFn}))
		cmd.SetContext(ctx)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("args=%v: unexpected error: %v", args, err)
		}
		out := buf.String()
		for _, denied := range []string{"CGROUP", "CONTROLLERS"} {
			if strings.Contains(out, denied) {
				t.Errorf("args=%v: output must NOT contain %q; got:\n%s", args, denied, out)
			}
		}
	}
}

type fakeClient struct {
	kukeonv1.FakeClient

	getCellFn   func(doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error)
	listCellsFn func(realm, space, stack string) ([]v1beta1.CellDoc, error)
}

func (f *fakeClient) GetCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
	if f.getCellFn == nil {
		return kukeonv1.GetCellResult{}, errors.New("unexpected GetCell call")
	}
	return f.getCellFn(doc)
}

func (f *fakeClient) ListCells(_ context.Context, realm, space, stack string) ([]v1beta1.CellDoc, error) {
	if f.listCellsFn == nil {
		return nil, errors.New("unexpected ListCells call")
	}
	return f.listCellsFn(realm, space, stack)
}
