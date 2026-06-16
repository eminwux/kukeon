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

package stack_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	stack "github.com/eminwux/kukeon/cmd/kuke/get/stack"
	"github.com/eminwux/kukeon/cmd/kuke/get/testutil"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestNewStackCmd(t *testing.T) {
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
			name: "get single stack",
			args: []string{"st1"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				_ = cmd.Flags().Set("realm", "r1")
				_ = cmd.Flags().Set("space", "s1")
			},
			fake: &fakeClient{
				getStackFn: func(_ v1beta1.StackDoc) (kukeonv1.GetStackResult, error) {
					return kukeonv1.GetStackResult{
						Stack: v1beta1.StackDoc{
							Metadata: v1beta1.StackMetadata{Name: "st1"},
							Spec:     v1beta1.StackSpec{RealmID: "r1", SpaceID: "s1"},
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
			},
			fake: &fakeClient{
				getStackFn: func(_ v1beta1.StackDoc) (kukeonv1.GetStackResult, error) {
					return kukeonv1.GetStackResult{}, errdefs.ErrStackNotFound
				},
			},
			wantErr: `stack "missing" not found`,
		},
		{
			name: "list stacks",
			fake: &fakeClient{
				listStacksFn: func(_, _ string) ([]v1beta1.StackDoc, error) {
					return []v1beta1.StackDoc{{Metadata: v1beta1.StackMetadata{Name: "st1"}}}, nil
				},
			},
			wantOutput: "st1",
		},
		{
			name: "list empty",
			fake: &fakeClient{
				listStacksFn: func(_, _ string) ([]v1beta1.StackDoc, error) { return nil, nil },
			},
			wantOutput: "No stacks found.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)

			cmd := stack.NewStackCmd()
			buf := &bytes.Buffer{}
			cmd.SetOut(buf)
			cmd.SetErr(buf)

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
			ctx = context.WithValue(ctx, stack.MockControllerKey{}, kukeonv1.Client(tt.fake))
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

// TestNewStackCmd_Columns pins the epic:get step-3 (#603) column
// contract for `kuke get stack`: default and `-o wide` both emit
// `NAME REALM SPACE STATE AGE` — stack carries no per-entity wide
// columns beyond hierarchy pointers, so wide deliberately renders
// the same shape as default. Also re-pins the cross-cutting epic
// invariants from #827: the `CGROUP`/`CONTROLLERS` columns and the
// `--show-controllers` flag stay gone.
// TestNewStackCmd_NamedSingleRow pins the #1323 kubectl-parity flip: a named
// `kuke get stack <name>` renders a single table row by default, while
// `-o yaml` / `-o json` still emit the full document. Stack has no per-entity
// `-o wide` columns, so wide renders the same shape as default (#603).
func TestNewStackCmd_NamedSingleRow(t *testing.T) {
	t.Cleanup(viper.Reset)

	fake := &fakeClient{
		getStackFn: func(_ v1beta1.StackDoc) (kukeonv1.GetStackResult, error) {
			return kukeonv1.GetStackResult{
				Stack: v1beta1.StackDoc{
					Metadata: v1beta1.StackMetadata{Name: "st1"},
					Spec:     v1beta1.StackSpec{RealmID: "r1", SpaceID: "s1"},
					Status:   v1beta1.StackStatus{State: v1beta1.StackStateReady},
				},
				MetadataExists: true,
			}, nil
		},
	}

	run := func(t *testing.T, args ...string) string {
		t.Helper()
		t.Cleanup(viper.Reset)
		cmd := stack.NewStackCmd()
		buf := &bytes.Buffer{}
		cmd.SetOut(buf)
		cmd.SetErr(buf)
		ctx := context.WithValue(context.Background(), stack.MockControllerKey{}, kukeonv1.Client(fake))
		cmd.SetContext(ctx)
		cmd.SetArgs(append([]string{"st1", "--realm", "r1", "--space", "s1"}, args...))
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return buf.String()
	}

	t.Run("default renders single table row", func(t *testing.T) {
		out := run(t)
		header := testutil.FirstLine(out)
		for _, col := range []string{"NAME", "REALM", "SPACE", "STATE", "AGE"} {
			if !strings.Contains(header, col) {
				t.Errorf("named default header missing %q; got: %q", col, header)
			}
		}
		if !strings.Contains(out, "st1") {
			t.Errorf("expected single row carrying name; got:\n%s", out)
		}
		if strings.Contains(out, "metadata:") {
			t.Errorf("named default must not emit the full document; got:\n%s", out)
		}
	})

	t.Run("-o wide renders single row", func(t *testing.T) {
		out := run(t, "-o", "wide")
		header := testutil.FirstLine(out)
		for _, col := range []string{"NAME", "REALM", "SPACE", "STATE", "AGE"} {
			if !strings.Contains(header, col) {
				t.Errorf("named wide header missing %q; got: %q", col, header)
			}
		}
		if strings.Contains(out, "metadata:") {
			t.Errorf("named wide must not emit the full document; got:\n%s", out)
		}
	})

	t.Run("-o yaml emits the full document", func(t *testing.T) {
		out := run(t, "-o", "yaml")
		if !strings.Contains(out, "metadata:") {
			t.Errorf("-o yaml should emit the document; got:\n%s", out)
		}
	})

	t.Run("-o json emits the full document", func(t *testing.T) {
		out := run(t, "-o", "json")
		if !strings.Contains(out, "\"metadata\"") {
			t.Errorf("-o json should emit the document; got:\n%s", out)
		}
	})
}

func TestNewStackCmd_Columns(t *testing.T) {
	t.Cleanup(viper.Reset)

	if stack.NewStackCmd().Flags().Lookup("show-controllers") != nil {
		t.Error("show-controllers flag must be removed (issue #827)")
	}

	created := time.Now().Add(-2 * time.Hour)
	listFn := func(_, _ string) ([]v1beta1.StackDoc, error) {
		return []v1beta1.StackDoc{{
			Metadata: v1beta1.StackMetadata{Name: "st1"},
			Spec:     v1beta1.StackSpec{RealmID: "r1", SpaceID: "s1"},
			Status: v1beta1.StackStatus{
				State:              v1beta1.StackStateReady,
				CgroupPath:         "/kukeon/r1/s1/st1",
				SubtreeControllers: []string{"cpu", "memory"},
				CreatedAt:          created,
			},
		}}, nil
	}

	for _, args := range [][]string{nil, {"-o", "wide"}} {
		buf := &bytes.Buffer{}
		cmd := stack.NewStackCmd()
		cmd.SetOut(buf)
		cmd.SetErr(buf)
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
		ctx = context.WithValue(ctx, stack.MockControllerKey{},
			kukeonv1.Client(&fakeClient{listStacksFn: listFn}))
		cmd.SetContext(ctx)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("args=%v: unexpected error: %v", args, err)
		}
		out := buf.String()
		header := testutil.FirstLine(out)
		for _, want := range []string{"NAME", "REALM", "SPACE", "STATE", "AGE"} {
			if !strings.Contains(header, want) {
				t.Errorf("args=%v: header missing %q; got: %q", args, want, header)
			}
		}
		for _, denied := range []string{"CGROUP", "CONTROLLERS"} {
			if strings.Contains(out, denied) {
				t.Errorf("args=%v: output must NOT contain %q; got:\n%s", args, denied, out)
			}
		}
		// AGE value rendered against a 2h-old timestamp must surface
		// the kubectl-style coarse duration (RenderAge floors to the
		// largest unit), not the raw RFC3339 timestamp.
		if !strings.Contains(out, "2h") {
			t.Errorf("args=%v: expected AGE column to render \"2h\" for a 2h-old stack; got:\n%s", args, out)
		}
	}
}

// TestNewStackCmd_Selector verifies the `-l`/`--selector` filter wiring on
// `kuke get stack` (issue #614). Grammar coverage lives in the shared
// selector_test.go; this test pins the per-verb wiring.
func TestNewStackCmd_Selector(t *testing.T) {
	t.Cleanup(viper.Reset)

	listFn := func(_, _ string) ([]v1beta1.StackDoc, error) {
		return []v1beta1.StackDoc{
			{
				Metadata: v1beta1.StackMetadata{
					Name:   "frontend",
					Labels: map[string]string{"app": "frontend"},
				},
				Spec: v1beta1.StackSpec{RealmID: "r1", SpaceID: "s1"},
			},
			{
				Metadata: v1beta1.StackMetadata{
					Name:   "backend",
					Labels: map[string]string{"app": "backend"},
				},
				Spec: v1beta1.StackSpec{RealmID: "r1", SpaceID: "s1"},
			},
		}, nil
	}

	t.Run("equality filters by label", func(t *testing.T) {
		t.Cleanup(viper.Reset)
		cmd := stack.NewStackCmd()
		buf := &bytes.Buffer{}
		cmd.SetOut(buf)
		cmd.SetErr(buf)
		ctx := context.WithValue(context.Background(), stack.MockControllerKey{},
			kukeonv1.Client(&fakeClient{listStacksFn: listFn}))
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{"-l", "app=frontend"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, "frontend") {
			t.Errorf("expected 'frontend' in output, got:\n%s", out)
		}
		if strings.Contains(out, "backend") {
			t.Errorf("expected 'backend' filtered out, got:\n%s", out)
		}
	})

	t.Run("selector + name is rejected", func(t *testing.T) {
		t.Cleanup(viper.Reset)
		cmd := stack.NewStackCmd()
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		ctx := context.WithValue(context.Background(), stack.MockControllerKey{},
			kukeonv1.Client(&fakeClient{}))
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{"backend", "-l", "app=frontend"})
		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "--selector cannot be combined") {
			t.Fatalf("expected --selector + name rejection, got: %v", err)
		}
	})
}

type fakeClient struct {
	kukeonv1.FakeClient

	getStackFn   func(doc v1beta1.StackDoc) (kukeonv1.GetStackResult, error)
	listStacksFn func(realm, space string) ([]v1beta1.StackDoc, error)
}

func (f *fakeClient) GetStack(_ context.Context, doc v1beta1.StackDoc) (kukeonv1.GetStackResult, error) {
	if f.getStackFn == nil {
		return kukeonv1.GetStackResult{}, errors.New("unexpected GetStack call")
	}
	return f.getStackFn(doc)
}

func (f *fakeClient) ListStacks(_ context.Context, realm, space string) ([]v1beta1.StackDoc, error) {
	if f.listStacksFn == nil {
		return nil, errors.New("unexpected ListStacks call")
	}
	return f.listStacksFn(realm, space)
}
