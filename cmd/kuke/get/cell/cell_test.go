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

// TestNewCellCmd_DefaultColumns pins the default `kuke get cell` column set
// after the epic:get redefinition (issue #604): NAME REALM SPACE STACK STATE
// AGE — six columns, no SYNC / CGROUP / CONTAINERS / BRIDGE / DIVERGENCE.
func TestNewCellCmd_DefaultColumns(t *testing.T) {
	t.Cleanup(viper.Reset)

	listFn := func(_, _, _ string) ([]v1beta1.CellDoc, error) {
		return []v1beta1.CellDoc{{
			Metadata: v1beta1.CellMetadata{Name: "ce1"},
			Spec:     v1beta1.CellSpec{RealmID: "r1", SpaceID: "s1", StackID: "st1"},
			Status: v1beta1.CellStatus{
				State:      v1beta1.CellStateReady,
				CgroupPath: "/kukeon/r1/s1/st1/ce1",
				Network:    v1beta1.CellNetworkStatus{BridgeName: "k-1a2b3c4d"},
				Containers: []v1beta1.ContainerStatus{
					{Name: "root", State: v1beta1.ContainerStateReady},
				},
			},
		}}, nil
	}

	buf := &bytes.Buffer{}
	cmd := cell.NewCellCmd()
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	ctx = context.WithValue(ctx, cell.MockControllerKey{},
		kukeonv1.Client(&fakeClient{listCellsFn: listFn}))
	cmd.SetContext(ctx)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	for _, h := range []string{"NAME", "REALM", "SPACE", "STACK", "STATE", "AGE"} {
		if !strings.Contains(out, h) {
			t.Errorf("default table missing header %q\nGot:\n%s", h, out)
		}
	}
	for _, denied := range []string{"CGROUP", "CONTROLLERS", "SYNC", "DIVERGENCE", "CONTAINERS", "BRIDGE"} {
		if strings.Contains(out, denied) {
			t.Errorf("default table must NOT contain %q; got:\n%s", denied, out)
		}
	}
}

// TestNewCellCmd_WideColumns pins the `-o wide` column set after the
// epic:get redefinition: NAME REALM SPACE STACK STATE AGE CONTAINERS BRIDGE
// (8 cols). CGROUP, SYNC, DIVERGENCE must not appear.
func TestNewCellCmd_WideColumns(t *testing.T) {
	t.Cleanup(viper.Reset)

	listFn := func(_, _, _ string) ([]v1beta1.CellDoc, error) {
		return []v1beta1.CellDoc{{
			Metadata: v1beta1.CellMetadata{Name: "ce1"},
			Spec:     v1beta1.CellSpec{RealmID: "r1", SpaceID: "s1", StackID: "st1"},
			Status: v1beta1.CellStatus{
				State:   v1beta1.CellStateReady,
				Network: v1beta1.CellNetworkStatus{BridgeName: "k-1a2b3c4d"},
				Containers: []v1beta1.ContainerStatus{
					{Name: "root", State: v1beta1.ContainerStateReady},
					{Name: "side", State: v1beta1.ContainerStatePending},
				},
			},
		}}, nil
	}

	buf := &bytes.Buffer{}
	cmd := cell.NewCellCmd()
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	ctx = context.WithValue(ctx, cell.MockControllerKey{},
		kukeonv1.Client(&fakeClient{listCellsFn: listFn}))
	cmd.SetContext(ctx)
	cmd.SetArgs([]string{"-o", "wide"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	for _, h := range []string{"NAME", "REALM", "SPACE", "STACK", "STATE", "AGE", "CONTAINERS", "BRIDGE"} {
		if !strings.Contains(out, h) {
			t.Errorf("-o wide table missing header %q\nGot:\n%s", h, out)
		}
	}
	for _, denied := range []string{"CGROUP", "CONTROLLERS", "SYNC", "DIVERGENCE"} {
		if strings.Contains(out, denied) {
			t.Errorf("-o wide table must NOT contain %q; got:\n%s", denied, out)
		}
	}
	for _, sub := range []string{"ce1", "1/2", "k-1a2b3c4d"} {
		if !strings.Contains(out, sub) {
			t.Errorf("-o wide row missing %q\nGot:\n%s", sub, out)
		}
	}
}

// TestNewCellCmd_ContainersReadyTotal pins the ready/total rendering edge
// cases enumerated in #604's AC: 0/0 (no containers), 0/1 (one pending),
// 1/1 (one ready), and 2/3 (two of three ready). BRIDGE empty renders as
// "-" — checked under the 0/0 case so a single subtest covers both
// no-runtime fallbacks.
func TestNewCellCmd_ContainersReadyTotal(t *testing.T) {
	t.Cleanup(viper.Reset)

	tests := []struct {
		name       string
		containers []v1beta1.ContainerStatus
		bridge     string
		wantRow    []string
	}{
		{
			name:       "empty list renders 0/0 and bridge dash",
			containers: nil,
			bridge:     "",
			wantRow:    []string{"0/0", " - "},
		},
		{
			name:       "single pending renders 0/1",
			containers: []v1beta1.ContainerStatus{{Name: "root", State: v1beta1.ContainerStatePending}},
			bridge:     "k-aaaaaaaa",
			wantRow:    []string{"0/1", "k-aaaaaaaa"},
		},
		{
			name:       "single ready renders 1/1",
			containers: []v1beta1.ContainerStatus{{Name: "root", State: v1beta1.ContainerStateReady}},
			bridge:     "k-bbbbbbbb",
			wantRow:    []string{"1/1", "k-bbbbbbbb"},
		},
		{
			name: "two of three ready renders 2/3",
			containers: []v1beta1.ContainerStatus{
				{Name: "root", State: v1beta1.ContainerStateReady},
				{Name: "side", State: v1beta1.ContainerStateReady},
				{Name: "boot", State: v1beta1.ContainerStateFailed},
			},
			bridge:  "k-cccccccc",
			wantRow: []string{"2/3", "k-cccccccc"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)

			listFn := func(_, _, _ string) ([]v1beta1.CellDoc, error) {
				return []v1beta1.CellDoc{{
					Metadata: v1beta1.CellMetadata{Name: "ce1"},
					Spec:     v1beta1.CellSpec{RealmID: "r1", SpaceID: "s1", StackID: "st1"},
					Status: v1beta1.CellStatus{
						State:      v1beta1.CellStateReady,
						Network:    v1beta1.CellNetworkStatus{BridgeName: tt.bridge},
						Containers: tt.containers,
					},
				}}, nil
			}

			buf := &bytes.Buffer{}
			cmd := cell.NewCellCmd()
			cmd.SetOut(buf)
			cmd.SetErr(buf)
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
			ctx = context.WithValue(ctx, cell.MockControllerKey{},
				kukeonv1.Client(&fakeClient{listCellsFn: listFn}))
			cmd.SetContext(ctx)
			cmd.SetArgs([]string{"-o", "wide"})
			if err := cmd.Execute(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			out := buf.String()
			for _, sub := range tt.wantRow {
				if !strings.Contains(out, sub) {
					t.Errorf("-o wide row missing %q\nGot:\n%s", sub, out)
				}
			}
		})
	}
}

// TestNewCellCmd_NoShowControllersFlag pins the epic:get step-1 retirement
// of `--show-controllers`: the flag must not be registered on the cell
// command after #881 (issue #827).
func TestNewCellCmd_NoShowControllersFlag(t *testing.T) {
	if cell.NewCellCmd().Flags().Lookup("show-controllers") != nil {
		t.Error("show-controllers flag must be removed (issue #827)")
	}
}

// TestNewCellCmd_YamlSurfacesStatus pins the contract that the cell table
// no longer surfaces sync state or cgroup info, but `-o yaml` still does —
// operators who need those fields read them from the structured output.
func TestNewCellCmd_YamlSurfacesStatus(t *testing.T) {
	t.Cleanup(viper.Reset)

	listFn := func(_, _, _ string) ([]v1beta1.CellDoc, error) {
		return []v1beta1.CellDoc{{
			Metadata: v1beta1.CellMetadata{Name: "ce1"},
			Spec:     v1beta1.CellSpec{RealmID: "r1", SpaceID: "s1", StackID: "st1"},
			Status: v1beta1.CellStatus{
				State:           v1beta1.CellStateReady,
				CgroupPath:      "/kukeon/r1/s1/st1/ce1",
				OutOfSync:       true,
				OutOfSyncReason: "lineage Config deleted",
			},
		}}, nil
	}

	buf := &bytes.Buffer{}
	cmd := cell.NewCellCmd()
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	ctx = context.WithValue(ctx, cell.MockControllerKey{},
		kukeonv1.Client(&fakeClient{listCellsFn: listFn}))
	cmd.SetContext(ctx)
	if err := cmd.Flags().Set("output", "yaml"); err != nil {
		t.Fatalf("set output=yaml: %v", err)
	}
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	for _, sub := range []string{
		"cgroupPath: /kukeon/r1/s1/st1/ce1",
		"outOfSync: true",
		"outOfSyncReason: lineage Config deleted",
	} {
		if !strings.Contains(out, sub) {
			t.Errorf("yaml output missing %q\nGot:\n%s", sub, out)
		}
	}
}

// TestNewCellCmd_Selector verifies the `-l`/`--selector` filter wiring on
// `kuke get cell` (issue #614). Grammar coverage lives in the shared
// selector_test.go; this test pins the per-verb wiring.
func TestNewCellCmd_Selector(t *testing.T) {
	t.Cleanup(viper.Reset)

	listFn := func(_, _, _ string) ([]v1beta1.CellDoc, error) {
		return []v1beta1.CellDoc{
			{
				Metadata: v1beta1.CellMetadata{
					Name:   "prod-web",
					Labels: map[string]string{"env": "prod", "role": "web"},
				},
				Spec: v1beta1.CellSpec{RealmID: "r1", SpaceID: "s1", StackID: "st1"},
			},
			{
				Metadata: v1beta1.CellMetadata{
					Name:   "prod-db",
					Labels: map[string]string{"env": "prod", "role": "db"},
				},
				Spec: v1beta1.CellSpec{RealmID: "r1", SpaceID: "s1", StackID: "st1"},
			},
			{
				Metadata: v1beta1.CellMetadata{
					Name:   "dev-web",
					Labels: map[string]string{"env": "dev", "role": "web"},
				},
				Spec: v1beta1.CellSpec{RealmID: "r1", SpaceID: "s1", StackID: "st1"},
			},
		}, nil
	}

	t.Run("AND-combination filters by both labels", func(t *testing.T) {
		t.Cleanup(viper.Reset)
		cmd := cell.NewCellCmd()
		buf := &bytes.Buffer{}
		cmd.SetOut(buf)
		cmd.SetErr(buf)
		ctx := context.WithValue(context.Background(), cell.MockControllerKey{},
			kukeonv1.Client(&fakeClient{listCellsFn: listFn}))
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{"-l", "env=prod,role=web"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, "prod-web") {
			t.Errorf("expected 'prod-web' in output, got:\n%s", out)
		}
		for _, deny := range []string{"prod-db", "dev-web"} {
			if strings.Contains(out, deny) {
				t.Errorf("expected %q filtered out, got:\n%s", deny, out)
			}
		}
	})

	t.Run("malformed selector fails before listing", func(t *testing.T) {
		t.Cleanup(viper.Reset)
		cmd := cell.NewCellCmd()
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		// No ListCells expected — selector parse fails first.
		ctx := context.WithValue(context.Background(), cell.MockControllerKey{},
			kukeonv1.Client(&fakeClient{}))
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{"-l", "env="})
		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "empty value") {
			t.Fatalf("expected malformed-selector error, got: %v", err)
		}
	})

	t.Run("selector + name is rejected", func(t *testing.T) {
		t.Cleanup(viper.Reset)
		cmd := cell.NewCellCmd()
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		ctx := context.WithValue(context.Background(), cell.MockControllerKey{},
			kukeonv1.Client(&fakeClient{}))
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{"prod-web", "-l", "env=prod"})
		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "--selector cannot be combined") {
			t.Fatalf("expected --selector + name rejection, got: %v", err)
		}
	})
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
