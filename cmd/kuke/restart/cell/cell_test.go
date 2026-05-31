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
	cell "github.com/eminwux/kukeon/cmd/kuke/restart/cell"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/viper"
)

func TestRestartCell(t *testing.T) {
	t.Cleanup(viper.Reset)

	tests := []struct {
		name       string
		args       []string
		setup      func()
		fake       *fakeClient
		wantErr    string
		wantOutput string
		// validate is run after a successful command for additional
		// post-conditions the assertion knobs above don't cover.
		validate func(t *testing.T, f *fakeClient)
	}{
		{
			// Per #983, restart on a Ready cell is unconditionally stop+start.
			// The daemon's controller.StartCell handles the OutOfSync reapply
			// daemon-side, so the CLI no longer branches on OutOfSync — the
			// same stop+start sequence works whether the cell is Synced or
			// OutOfSync.
			name: "ready cell: stop then start",
			args: []string{"c1"},
			setup: func() {
				viper.Set(config.KUKE_RESTART_CELL_REALM.ViperKey, "r1")
				viper.Set(config.KUKE_RESTART_CELL_SPACE.ViperKey, "s1")
				viper.Set(config.KUKE_RESTART_CELL_STACK.ViperKey, "st1")
			},
			fake: &fakeClient{
				getCellFn: func(doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
					return kukeonv1.GetCellResult{
						MetadataExists: true,
						Cell: v1beta1.CellDoc{
							Metadata: v1beta1.CellMetadata{Name: doc.Metadata.Name},
							Spec: v1beta1.CellSpec{
								ID:      doc.Metadata.Name,
								RealmID: "r1",
								SpaceID: "s1",
								StackID: "st1",
							},
							Status: v1beta1.CellStatus{State: v1beta1.CellStateReady, OutOfSync: false},
						},
					}, nil
				},
				stopCellFn: func(_ v1beta1.CellDoc) (kukeonv1.StopCellResult, error) {
					return kukeonv1.StopCellResult{}, nil
				},
				startCellFn: func(doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
					return kukeonv1.StartCellResult{Cell: doc, Started: true}, nil
				},
			},
			wantOutput: `Restarted cell "c1" from stack "st1"`,
			validate: func(t *testing.T, f *fakeClient) {
				if f.stopCalls != 1 || f.startCalls != 1 {
					t.Fatalf("want exactly 1 StopCell + 1 StartCell, got stop=%d start=%d",
						f.stopCalls, f.startCalls)
				}
				if f.applyCalls != 0 {
					t.Fatalf("restart must not call ApplyDocuments, got %d calls", f.applyCalls)
				}
				if f.getConfigCnt != 0 || f.getBpCalls != 0 {
					t.Fatalf(
						"restart must not call GetConfig or GetBlueprint (daemon-side reapply owns it now), "+
							"got getConfig=%d getBlueprint=%d",
						f.getConfigCnt, f.getBpCalls,
					)
				}
			},
		},
		{
			// Sanity check: the OutOfSync flag is transparent to the CLI now —
			// stop+start runs unconditionally, and the daemon's StartCell is
			// what re-materialises from the lineage Config.
			name: "outofsync ready cell: stop then start (daemon-side reapply)",
			args: []string{"prod"},
			setup: func() {
				viper.Set(config.KUKE_RESTART_CELL_REALM.ViperKey, "r1")
				viper.Set(config.KUKE_RESTART_CELL_SPACE.ViperKey, "s1")
				viper.Set(config.KUKE_RESTART_CELL_STACK.ViperKey, "st1")
			},
			fake: &fakeClient{
				getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
					return kukeonv1.GetCellResult{
						MetadataExists: true,
						Cell: v1beta1.CellDoc{
							Metadata: v1beta1.CellMetadata{Name: "prod"},
							Spec:     v1beta1.CellSpec{ID: "prod", RealmID: "r1", SpaceID: "s1", StackID: "st1"},
							Status: v1beta1.CellStatus{
								State:           v1beta1.CellStateReady,
								OutOfSync:       true,
								OutOfSyncReason: "spec differs",
							},
						},
					}, nil
				},
				stopCellFn: func(_ v1beta1.CellDoc) (kukeonv1.StopCellResult, error) {
					return kukeonv1.StopCellResult{}, nil
				},
				startCellFn: func(doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
					return kukeonv1.StartCellResult{Cell: doc, Started: true}, nil
				},
			},
			wantOutput: `Restarted cell "prod" from stack "st1"`,
			validate: func(t *testing.T, f *fakeClient) {
				if f.stopCalls != 1 || f.startCalls != 1 {
					t.Fatalf("want exactly 1 StopCell + 1 StartCell, got stop=%d start=%d",
						f.stopCalls, f.startCalls)
				}
				if f.applyCalls != 0 || f.getConfigCnt != 0 || f.getBpCalls != 0 {
					t.Fatalf(
						"OutOfSync reapply lives daemon-side; restart CLI must not call ApplyDocuments/GetConfig/GetBlueprint, "+
							"got apply=%d getConfig=%d getBlueprint=%d",
						f.applyCalls, f.getConfigCnt, f.getBpCalls,
					)
				}
			},
		},
		{
			name: "stopped cell: equivalent to kuke start cell",
			args: []string{"c1"},
			setup: func() {
				viper.Set(config.KUKE_RESTART_CELL_REALM.ViperKey, "r1")
				viper.Set(config.KUKE_RESTART_CELL_SPACE.ViperKey, "s1")
				viper.Set(config.KUKE_RESTART_CELL_STACK.ViperKey, "st1")
			},
			fake: &fakeClient{
				getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
					return kukeonv1.GetCellResult{
						MetadataExists: true,
						Cell: v1beta1.CellDoc{
							Metadata: v1beta1.CellMetadata{Name: "c1"},
							Spec:     v1beta1.CellSpec{ID: "c1", RealmID: "r1", SpaceID: "s1", StackID: "st1"},
							Status:   v1beta1.CellStatus{State: v1beta1.CellStateStopped},
						},
					}, nil
				},
				startCellFn: func(doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
					return kukeonv1.StartCellResult{Cell: doc, Started: true}, nil
				},
			},
			wantOutput: `Started cell "c1" from stack "st1"`,
			validate: func(t *testing.T, f *fakeClient) {
				if f.stopCalls != 0 || f.startCalls != 1 {
					t.Fatalf("Stopped restart must call StartCell only, got stop=%d start=%d",
						f.stopCalls, f.startCalls)
				}
			},
		},
		{
			name: "failed cell: refused with kuke delete pointer",
			args: []string{"broken"},
			setup: func() {
				viper.Set(config.KUKE_RESTART_CELL_REALM.ViperKey, "r1")
				viper.Set(config.KUKE_RESTART_CELL_SPACE.ViperKey, "s1")
				viper.Set(config.KUKE_RESTART_CELL_STACK.ViperKey, "st1")
			},
			fake: &fakeClient{
				getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
					return kukeonv1.GetCellResult{
						MetadataExists: true,
						Cell: v1beta1.CellDoc{
							Metadata: v1beta1.CellMetadata{Name: "broken"},
							Status:   v1beta1.CellStatus{State: v1beta1.CellStateFailed},
						},
					}, nil
				},
			},
			wantErr: "kuke delete cell broken",
		},
		{
			name: "pending cell: refused with kuke delete pointer",
			args: []string{"halfborn"},
			setup: func() {
				viper.Set(config.KUKE_RESTART_CELL_REALM.ViperKey, "r1")
				viper.Set(config.KUKE_RESTART_CELL_SPACE.ViperKey, "s1")
				viper.Set(config.KUKE_RESTART_CELL_STACK.ViperKey, "st1")
			},
			fake: &fakeClient{
				getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
					return kukeonv1.GetCellResult{
						MetadataExists: true,
						Cell: v1beta1.CellDoc{
							Metadata: v1beta1.CellMetadata{Name: "halfborn"},
							Status:   v1beta1.CellStatus{State: v1beta1.CellStatePending},
						},
					}, nil
				},
			},
			wantErr: "kuke delete cell halfborn",
		},
		{
			name: "missing cell: returns ErrCellNotFound",
			args: []string{"nope"},
			setup: func() {
				viper.Set(config.KUKE_RESTART_CELL_REALM.ViperKey, "r1")
				viper.Set(config.KUKE_RESTART_CELL_SPACE.ViperKey, "s1")
				viper.Set(config.KUKE_RESTART_CELL_STACK.ViperKey, "st1")
			},
			fake: &fakeClient{
				getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
					return kukeonv1.GetCellResult{MetadataExists: false}, nil
				},
			},
			wantErr: "cell not found",
		},
		{
			name:    "missing realm",
			args:    []string{"c1"},
			wantErr: "realm name is required",
		},
		{
			name: "stop step errors: surfaces and skips start",
			args: []string{"c1"},
			setup: func() {
				viper.Set(config.KUKE_RESTART_CELL_REALM.ViperKey, "r1")
				viper.Set(config.KUKE_RESTART_CELL_SPACE.ViperKey, "s1")
				viper.Set(config.KUKE_RESTART_CELL_STACK.ViperKey, "st1")
			},
			fake: &fakeClient{
				getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
					return kukeonv1.GetCellResult{
						MetadataExists: true,
						Cell: v1beta1.CellDoc{
							Metadata: v1beta1.CellMetadata{Name: "c1"},
							Spec:     v1beta1.CellSpec{ID: "c1", RealmID: "r1", SpaceID: "s1", StackID: "st1"},
							Status:   v1beta1.CellStatus{State: v1beta1.CellStateReady},
						},
					}, nil
				},
				stopCellFn: func(_ v1beta1.CellDoc) (kukeonv1.StopCellResult, error) {
					return kukeonv1.StopCellResult{}, errors.New("boom")
				},
			},
			wantErr: "boom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)
			viper.Reset()
			if tt.setup != nil {
				tt.setup()
			}

			cmd := cell.NewCellCmd()
			outBuf := &bytes.Buffer{}
			errBuf := &bytes.Buffer{}
			cmd.SetOut(outBuf)
			cmd.SetErr(errBuf)
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
			if tt.fake != nil {
				ctx = context.WithValue(ctx, cell.MockControllerKey{}, kukeonv1.Client(tt.fake))
			}
			cmd.SetContext(ctx)
			cmd.SetArgs(tt.args)

			err := cmd.Execute()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("want err containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantOutput != "" && !strings.Contains(outBuf.String(), tt.wantOutput) {
				t.Errorf("stdout missing %q\nGot:\n%s", tt.wantOutput, outBuf.String())
			}
			if tt.validate != nil {
				tt.validate(t, tt.fake)
			}
		})
	}
}

// TestRestartCell_CmdSurface pins the user-facing CLI shape: positional arg,
// scope flags, completion. Catches accidental flag renames or arg-count drift.
func TestRestartCell_CmdSurface(t *testing.T) {
	cmd := cell.NewCellCmd()

	if !cmd.HasAlias("ce") {
		t.Errorf("expected alias %q", "ce")
	}
	for _, f := range []string{"realm", "space", "stack"} {
		if cmd.Flag(f) == nil {
			t.Errorf("expected flag --%s to be registered", f)
		}
	}
	if cmd.ValidArgsFunction == nil {
		t.Error("expected ValidArgsFunction to be set for cell-name completion")
	}
	// The help text must still describe the OutOfSync reconcile contract so
	// operators know `kuke restart` doubles as a reconcile on OutOfSync cells
	// (now via the daemon-side reapply in controller.StartCell — #983).
	if !strings.Contains(cmd.Long, "reconcile") || !strings.Contains(cmd.Long, "OutOfSync") {
		t.Errorf("expected Long help to describe reconcile-on-OutOfSync, got: %s", cmd.Long)
	}
}

// fakeClient is a per-test stub kukeonv1.Client. Each RPC is dispatched
// through an optional Fn field; nil means "fail the test if called". Call
// counts let tests assert "restart did not invoke ApplyDocuments / GetConfig
// / GetBlueprint" — the OutOfSync reapply lives daemon-side now (#983), so
// none of those RPCs should fire from the restart CLI.
type fakeClient struct {
	kukeonv1.FakeClient

	getCellFn      func(doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error)
	getConfigFn    func(doc v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error)
	getBlueprintFn func(doc v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error)
	stopCellFn     func(doc v1beta1.CellDoc) (kukeonv1.StopCellResult, error)
	startCellFn    func(doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error)
	applyDocsFn    func(rawYAML []byte) (kukeonv1.ApplyDocumentsResult, error)

	getCellCalls int
	stopCalls    int
	startCalls   int
	getConfigCnt int
	getBpCalls   int
	applyCalls   int
}

func (f *fakeClient) GetCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
	f.getCellCalls++
	if f.getCellFn == nil {
		return kukeonv1.GetCellResult{}, errors.New("unexpected GetCell call")
	}
	return f.getCellFn(doc)
}

func (f *fakeClient) GetConfig(_ context.Context, doc v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
	f.getConfigCnt++
	if f.getConfigFn == nil {
		return kukeonv1.GetConfigResult{}, errors.New("unexpected GetConfig call")
	}
	return f.getConfigFn(doc)
}

func (f *fakeClient) GetBlueprint(
	_ context.Context,
	doc v1beta1.CellBlueprintDoc,
) (kukeonv1.GetBlueprintResult, error) {
	f.getBpCalls++
	if f.getBlueprintFn == nil {
		return kukeonv1.GetBlueprintResult{}, errors.New("unexpected GetBlueprint call")
	}
	return f.getBlueprintFn(doc)
}

func (f *fakeClient) StopCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.StopCellResult, error) {
	f.stopCalls++
	if f.stopCellFn == nil {
		return kukeonv1.StopCellResult{}, errors.New("unexpected StopCell call")
	}
	return f.stopCellFn(doc)
}

func (f *fakeClient) StartCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
	f.startCalls++
	if f.startCellFn == nil {
		return kukeonv1.StartCellResult{}, errors.New("unexpected StartCell call")
	}
	return f.startCellFn(doc)
}

func (f *fakeClient) ApplyDocuments(_ context.Context, rawYAML []byte) (kukeonv1.ApplyDocumentsResult, error) {
	f.applyCalls++
	if f.applyDocsFn == nil {
		return kukeonv1.ApplyDocumentsResult{}, errors.New("unexpected ApplyDocuments call")
	}
	return f.applyDocsFn(rawYAML)
}

// Sanity guard that the sentinel error in errdefs is still the one we
// reference — if it gets renamed, this fails at compile time rather than
// silently slipping into a string-match assertion.
var _ = errdefs.ErrCellNotFound
