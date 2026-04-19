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
		stdin      string
		fake       *fakeClient
		wantErr    string
		wantOutput string
	}{
		{
			name:    "missing file flag",
			args:    []string{},
			fake:    &fakeClient{},
			wantErr: "required flag",
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

type fakeClient struct {
	kukeonv1.FakeClient

	applyFn func(raw []byte) (kukeonv1.ApplyDocumentsResult, error)
}

func (f *fakeClient) ApplyDocuments(_ context.Context, raw []byte) (kukeonv1.ApplyDocumentsResult, error) {
	if f.applyFn == nil {
		return kukeonv1.ApplyDocumentsResult{}, errors.New("unexpected ApplyDocuments call")
	}
	return f.applyFn(raw)
}
