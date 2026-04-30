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

	start "github.com/eminwux/kukeon/cmd/kuke/daemon/start"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func TestDaemonStart(t *testing.T) {
	tests := []struct {
		name       string
		fake       *fakeClient
		wantErr    string
		wantOutput string
	}{
		{
			name: "stopped cell is started",
			fake: &fakeClient{
				getCellFn: func(doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
					assertKukeondTarget(t, doc)
					return kukeonv1.GetCellResult{
						Cell: v1beta1.CellDoc{
							Status: v1beta1.CellStatus{State: v1beta1.CellStateStopped},
						},
						MetadataExists: true,
					}, nil
				},
				startCellFn: func(doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
					assertKukeondTarget(t, doc)
					return kukeonv1.StartCellResult{Cell: doc, Started: true}, nil
				},
			},
			wantOutput: `kukeond started (cell "kukeond" in realm "kuke-system")`,
		},
		{
			name: "already-running cell is a no-op",
			fake: &fakeClient{
				getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
					return kukeonv1.GetCellResult{
						Cell: v1beta1.CellDoc{
							Status: v1beta1.CellStatus{
								State: v1beta1.CellStateReady,
								Containers: []v1beta1.ContainerStatus{
									{State: v1beta1.ContainerStateReady},
								},
							},
						},
						MetadataExists: true,
					}, nil
				},
				startCellFn: func(_ v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
					t.Fatalf("StartCell must not be called when daemon is already running")
					return kukeonv1.StartCellResult{}, nil
				},
			},
			wantOutput: `kukeond is already running (cell "kukeond" in realm "kuke-system")`,
		},
		{
			name: "running by container state alone (stale cell metadata)",
			fake: &fakeClient{
				getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
					return kukeonv1.GetCellResult{
						Cell: v1beta1.CellDoc{
							Status: v1beta1.CellStatus{
								State: v1beta1.CellStateStopped,
								Containers: []v1beta1.ContainerStatus{
									{State: v1beta1.ContainerStateReady},
								},
							},
						},
						MetadataExists: true,
					}, nil
				},
				startCellFn: func(_ v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
					t.Fatalf("StartCell must not be called when a container is running")
					return kukeonv1.StartCellResult{}, nil
				},
			},
			wantOutput: "kukeond is already running",
		},
		{
			name: "host not initialized",
			fake: &fakeClient{
				getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
					return kukeonv1.GetCellResult{MetadataExists: false}, nil
				},
			},
			wantErr: "kukeon host is not initialized",
		},
		{
			name: "GetCell error is wrapped",
			fake: &fakeClient{
				getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
					return kukeonv1.GetCellResult{}, errors.New("io: read failed")
				},
			},
			wantErr: "inspect kukeond cell:",
		},
		{
			name: "StartCell error is wrapped",
			fake: &fakeClient{
				getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
					return kukeonv1.GetCellResult{
						Cell: v1beta1.CellDoc{
							Status: v1beta1.CellStatus{State: v1beta1.CellStateStopped},
						},
						MetadataExists: true,
					}, nil
				},
				startCellFn: func(_ v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
					return kukeonv1.StartCellResult{}, errors.New("runner blew up")
				},
			},
			wantErr: "start kukeond cell:",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := start.NewStartCmd()
			buf := &bytes.Buffer{}
			cmd.SetOut(buf)
			cmd.SetErr(buf)
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
			ctx = context.WithValue(ctx, start.MockClientKey{}, kukeonv1.Client(tt.fake))
			cmd.SetContext(ctx)
			cmd.SetArgs(nil)

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
			if tt.wantOutput != "" && !strings.Contains(buf.String(), tt.wantOutput) {
				t.Errorf("output missing %q\nGot:\n%s", tt.wantOutput, buf.String())
			}
		})
	}
}

func TestDaemonStart_LoggerMissingFromContext(t *testing.T) {
	cmd := start.NewStartCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetContext(context.Background())
	cmd.SetArgs(nil)

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "logger not found") {
		t.Fatalf("expected logger-missing error, got %v", err)
	}
}

func assertKukeondTarget(t *testing.T, doc v1beta1.CellDoc) {
	t.Helper()
	if doc.Metadata.Name != consts.KukeSystemCellName {
		t.Errorf("cell name: want %q, got %q", consts.KukeSystemCellName, doc.Metadata.Name)
	}
	if doc.Spec.RealmID != consts.KukeSystemRealmName {
		t.Errorf("realm: want %q, got %q", consts.KukeSystemRealmName, doc.Spec.RealmID)
	}
	if doc.Spec.SpaceID != consts.KukeSystemSpaceName {
		t.Errorf("space: want %q, got %q", consts.KukeSystemSpaceName, doc.Spec.SpaceID)
	}
	if doc.Spec.StackID != consts.KukeSystemStackName {
		t.Errorf("stack: want %q, got %q", consts.KukeSystemStackName, doc.Spec.StackID)
	}
}

type fakeClient struct {
	kukeonv1.FakeClient

	getCellFn   func(doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error)
	startCellFn func(doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error)
}

func (f *fakeClient) GetCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
	if f.getCellFn == nil {
		return kukeonv1.GetCellResult{}, errors.New("unexpected GetCell call")
	}
	return f.getCellFn(doc)
}

func (f *fakeClient) StartCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
	if f.startCellFn == nil {
		return kukeonv1.StartCellResult{}, errors.New("unexpected StartCell call")
	}
	return f.startCellFn(doc)
}
