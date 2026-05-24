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

package apply_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	apply "github.com/eminwux/kukeon/cmd/kuke/apply"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

func writeTempYAML(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp yaml: %v", err)
	}
	return path
}

func TestApplyRunE(t *testing.T) {
	const validYAML = `apiVersion: v1beta1
kind: Realm
metadata:
  name: r1
spec:
  namespace: r1
`

	tests := []struct {
		name       string
		args       []string
		fake       *fakeClient
		wantErr    string
		wantOutput string
	}{
		{
			name:    "no file flag",
			args:    []string{},
			fake:    &fakeClient{},
			wantErr: "file flag is required",
		},
		{
			name: "success",
			args: []string{"-f", writeTempYAML(t, validYAML)},
			fake: &fakeClient{
				applyFn: func(_ []byte) (kukeonv1.ApplyDocumentsResult, error) {
					return kukeonv1.ApplyDocumentsResult{
						Resources: []kukeonv1.ApplyResourceResult{
							{Kind: "Realm", Name: "r1", Action: "created"},
						},
					}, nil
				},
			},
			wantOutput: `Realm "r1": created`,
		},
		{
			name: "client returns error",
			args: []string{"-f", writeTempYAML(t, validYAML)},
			fake: &fakeClient{
				applyFn: func(_ []byte) (kukeonv1.ApplyDocumentsResult, error) {
					return kukeonv1.ApplyDocumentsResult{}, errors.New("server exploded")
				},
			},
			wantErr: "server exploded",
		},
		{
			name: "failure recorded as failed action",
			args: []string{"-f", writeTempYAML(t, validYAML)},
			fake: &fakeClient{
				applyFn: func(_ []byte) (kukeonv1.ApplyDocumentsResult, error) {
					return kukeonv1.ApplyDocumentsResult{
						Resources: []kukeonv1.ApplyResourceResult{
							{Kind: "Realm", Name: "r1", Action: "failed", Error: "boom"},
						},
					}, nil
				},
			},
			wantErr: "some resources failed to apply",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := apply.NewApplyCmd()
			buf := &bytes.Buffer{}
			cmd.SetOut(buf)
			cmd.SetErr(buf)

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
			ctx = context.WithValue(ctx, apply.MockControllerKey{}, kukeonv1.Client(tt.fake))
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

// TestNewApplyCmd_RunE_JSONOutput locks the lowercase shape of
// `kuke apply -f -o json` so the keys can't drift back to Go's default
// uppercase marshaling. Mirrors TestNewDeleteCmd_RunE_JSONOutput on the
// sibling delete command.
func TestNewApplyCmd_RunE_JSONOutput(t *testing.T) {
	const validYAML = `apiVersion: v1beta1
kind: Realm
metadata:
  name: r1
spec:
  namespace: r1
`

	cmd := apply.NewApplyCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)

	fc := &fakeClient{
		applyFn: func(_ []byte) (kukeonv1.ApplyDocumentsResult, error) {
			return kukeonv1.ApplyDocumentsResult{
				Resources: []kukeonv1.ApplyResourceResult{
					{Index: 0, Kind: "Realm", Name: "r1", Action: "created"},
				},
			}, nil
		},
	}
	ctx = context.WithValue(ctx, apply.MockControllerKey{}, kukeonv1.Client(fc))
	cmd.SetContext(ctx)

	cmd.SetArgs([]string{"-f", writeTempYAML(t, validYAML), "--output", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	for _, want := range []string{`"index"`, `"kind"`, `"name"`, `"action"`, `"resources"`, "Realm", "r1", "created"} {
		if !strings.Contains(output, want) {
			t.Errorf("expected JSON output to contain %q, got: %q", want, output)
		}
	}
}

// TestApply_BlueprintFlag_Removed pins #823's removal of `-b/--blueprint` from
// `kuke apply`. The cobra-side response is the standard "unknown flag" error;
// no fall-through to a no-op success.
func TestApply_BlueprintFlag_Removed(t *testing.T) {
	for _, args := range [][]string{
		{"-b", "web"},
		{"--blueprint", "web"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			cmd := apply.NewApplyCmd()
			buf := &bytes.Buffer{}
			cmd.SetOut(buf)
			cmd.SetErr(buf)
			cmd.SetArgs(args)

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
			ctx = context.WithValue(ctx, apply.MockControllerKey{}, kukeonv1.Client(&fakeClient{}))
			cmd.SetContext(ctx)

			err := cmd.Execute()
			if err == nil {
				t.Fatalf("expected unknown-flag error, got nil")
			}
			if !strings.Contains(err.Error(), "unknown") {
				t.Errorf("err=%q want cobra's unknown-flag message", err.Error())
			}
		})
	}
}

// TestApply_ConfigFlag_Removed is the parallel guard for `-c/--config`.
func TestApply_ConfigFlag_Removed(t *testing.T) {
	for _, args := range [][]string{
		{"-c", "prod"},
		{"--config", "prod"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			cmd := apply.NewApplyCmd()
			buf := &bytes.Buffer{}
			cmd.SetOut(buf)
			cmd.SetErr(buf)
			cmd.SetArgs(args)

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
			ctx = context.WithValue(ctx, apply.MockControllerKey{}, kukeonv1.Client(&fakeClient{}))
			cmd.SetContext(ctx)

			err := cmd.Execute()
			if err == nil {
				t.Fatalf("expected unknown-flag error, got nil")
			}
			if !strings.Contains(err.Error(), "unknown") {
				t.Errorf("err=%q want cobra's unknown-flag message", err.Error())
			}
		})
	}
}

type fakeClient struct {
	kukeonv1.FakeClient

	applyFn func(raw []byte) (kukeonv1.ApplyDocumentsResult, error)

	applyCalls int
}

func (f *fakeClient) ApplyDocuments(_ context.Context, raw []byte) (kukeonv1.ApplyDocumentsResult, error) {
	f.applyCalls++
	if f.applyFn == nil {
		return kukeonv1.ApplyDocumentsResult{}, errors.New("unexpected ApplyDocuments call")
	}
	return f.applyFn(raw)
}
