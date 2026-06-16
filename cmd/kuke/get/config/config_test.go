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

package config_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	configcmd "github.com/eminwux/kukeon/cmd/kuke/get/config"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestNewConfigCmd(t *testing.T) {
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
			// Single-resource get renders YAML to os.Stdout (not the command
			// buffer), so this case only asserts the happy path returns no error.
			name: "get single config",
			args: []string{"web"},
			setup: func(_ *testing.T, cmd *cobra.Command) {
				_ = cmd.Flags().Set("realm", "r1")
				_ = cmd.Flags().Set("space", "s1")
			},
			fake: &fakeClient{
				getConfigFn: func(doc v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
					return kukeonv1.GetConfigResult{
						Config:         v1beta1.CellConfigDoc{Metadata: doc.Metadata},
						MetadataExists: true,
					}, nil
				},
			},
		},
		{
			// No --space/--stack: the single-get lookup must resolve to the
			// full default scope (realm/space/stack = "default") so a
			// full-scoped Config is found (issue #1156).
			name: "get single config defaults full scope when no flags",
			args: []string{"web"},
			fake: &fakeClient{
				getConfigFn: func(doc v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
					m := doc.Metadata
					if m.Realm != "default" || m.Space != "default" || m.Stack != "default" {
						return kukeonv1.GetConfigResult{}, fmt.Errorf(
							"lookup scope = realm=%q space=%q stack=%q, want all \"default\"",
							m.Realm, m.Space, m.Stack,
						)
					}
					return kukeonv1.GetConfigResult{
						Config:         v1beta1.CellConfigDoc{Metadata: doc.Metadata},
						MetadataExists: true,
					}, nil
				},
			},
		},
		{
			// An explicit empty --space/--stack stays empty so a realm-scoped
			// Config remains findable (the escape hatch).
			name: "get single config explicit empty space stays realm-scoped",
			args: []string{"web"},
			setup: func(_ *testing.T, cmd *cobra.Command) {
				_ = cmd.Flags().Set("space", "")
				_ = cmd.Flags().Set("stack", "")
			},
			fake: &fakeClient{
				getConfigFn: func(doc v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
					m := doc.Metadata
					if m.Realm != "default" || m.Space != "" || m.Stack != "" {
						return kukeonv1.GetConfigResult{}, fmt.Errorf(
							"lookup scope = realm=%q space=%q stack=%q, want realm=\"default\" space/stack empty",
							m.Realm, m.Space, m.Stack,
						)
					}
					return kukeonv1.GetConfigResult{
						Config:         v1beta1.CellConfigDoc{Metadata: doc.Metadata},
						MetadataExists: true,
					}, nil
				},
			},
		},
		{
			name: "get not found surfaces friendly error",
			args: []string{"missing"},
			setup: func(_ *testing.T, cmd *cobra.Command) {
				_ = cmd.Flags().Set("realm", "r1")
			},
			fake: &fakeClient{
				getConfigFn: func(_ v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
					return kukeonv1.GetConfigResult{}, errdefs.ErrConfigNotFound
				},
			},
			wantErr: `config "missing" not found`,
		},
		{
			// On a miss, the realm-wide probe hints the coordinate where a
			// Config of the same name does live (issue #1156).
			name: "get not found hints other-scope coordinate",
			args: []string{"web"},
			fake: &fakeClient{
				getConfigFn: func(_ v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
					return kukeonv1.GetConfigResult{MetadataExists: false}, nil
				},
				listConfigsFn: func(_, _, _ string) ([]v1beta1.CellConfigDoc, error) {
					return []v1beta1.CellConfigDoc{
						{Metadata: v1beta1.CellConfigMetadata{
							Name: "web", Realm: "default", Space: "team-a", Stack: "core",
						}},
					}, nil
				},
			},
			wantErr: `exists at realm="default" space="team-a" stack="core"`,
		},
		{
			name: "get not found via MetadataExists=false",
			args: []string{"missing"},
			setup: func(_ *testing.T, cmd *cobra.Command) {
				_ = cmd.Flags().Set("realm", "r1")
			},
			fake: &fakeClient{
				getConfigFn: func(_ v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
					return kukeonv1.GetConfigResult{MetadataExists: false}, nil
				},
			},
			wantErr: `config "missing" not found`,
		},
		{
			name: "list empty",
			fake: &fakeClient{
				listConfigsFn: func(_, _, _ string) ([]v1beta1.CellConfigDoc, error) {
					return nil, nil
				},
			},
			wantOutput: "No configs found.",
		},
		{
			name: "list table renders scope columns",
			fake: &fakeClient{
				listConfigsFn: func(_, _, _ string) ([]v1beta1.CellConfigDoc, error) {
					return []v1beta1.CellConfigDoc{
						{Metadata: v1beta1.CellConfigMetadata{Name: "realm-cfg", Realm: "default"}},
						{
							Metadata: v1beta1.CellConfigMetadata{
								Name:  "stack-cfg",
								Realm: "default",
								Space: "s",
								Stack: "st",
							},
						},
					}, nil
				},
			},
			wantOutput: "realm-cfg",
		},
		{
			name: "list passes filter scope through (no cell coordinate)",
			setup: func(_ *testing.T, cmd *cobra.Command) {
				_ = cmd.Flags().Set("realm", "default")
				_ = cmd.Flags().Set("space", "team-a")
			},
			fake: &fakeClient{
				listConfigsFn: func(realm, space, _ string) ([]v1beta1.CellConfigDoc, error) {
					if realm != "default" || space != "team-a" {
						return nil, errors.New("unexpected filter scope")
					}
					return []v1beta1.CellConfigDoc{
						{Metadata: v1beta1.CellConfigMetadata{Name: "space-cfg", Realm: realm, Space: space}},
					}, nil
				},
			},
			wantOutput: "space-cfg",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)

			cmd := configcmd.NewConfigCmd()
			buf := &bytes.Buffer{}
			cmd.SetOut(buf)
			cmd.SetErr(buf)

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
			ctx = context.WithValue(ctx, configcmd.MockControllerKey{}, kukeonv1.Client(tt.fake))
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

// TestNewConfigCmd_NamedSingleRow pins the #1323 kubectl-parity flip: a named
// `kuke get config <name>` renders a single table row by default, while
// `-o yaml` / `-o json` still emit the full document.
func TestNewConfigCmd_NamedSingleRow(t *testing.T) {
	t.Cleanup(viper.Reset)

	fake := &fakeClient{
		getConfigFn: func(doc v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
			return kukeonv1.GetConfigResult{
				Config:         v1beta1.CellConfigDoc{Metadata: doc.Metadata},
				MetadataExists: true,
			}, nil
		},
	}

	run := func(t *testing.T, args ...string) string {
		t.Helper()
		t.Cleanup(viper.Reset)
		cmd := configcmd.NewConfigCmd()
		buf := &bytes.Buffer{}
		cmd.SetOut(buf)
		cmd.SetErr(buf)
		ctx := context.WithValue(context.Background(), configcmd.MockControllerKey{}, kukeonv1.Client(fake))
		cmd.SetContext(ctx)
		cmd.SetArgs(append([]string{"web"}, args...))
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return buf.String()
	}

	t.Run("default renders single table row", func(t *testing.T) {
		out := run(t)
		for _, col := range []string{"NAME", "REALM", "SPACE", "STACK"} {
			if !strings.Contains(out, col) {
				t.Errorf("named default missing column %q; got:\n%s", col, out)
			}
		}
		if !strings.Contains(out, "web") {
			t.Errorf("expected single row carrying name; got:\n%s", out)
		}
		if strings.Contains(out, "metadata:") {
			t.Errorf("named default must not emit the full document; got:\n%s", out)
		}
	})

	t.Run("-o wide renders a single row", func(t *testing.T) {
		out := run(t, "-o", "wide")
		if !strings.Contains(out, "NAME") || !strings.Contains(out, "web") {
			t.Errorf("named wide should render the single row; got:\n%s", out)
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

type fakeClient struct {
	kukeonv1.FakeClient

	getConfigFn   func(doc v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error)
	listConfigsFn func(realm, space, stack string) ([]v1beta1.CellConfigDoc, error)
}

func (f *fakeClient) GetConfig(
	_ context.Context, doc v1beta1.CellConfigDoc,
) (kukeonv1.GetConfigResult, error) {
	if f.getConfigFn == nil {
		return kukeonv1.GetConfigResult{}, errors.New("unexpected GetConfig call")
	}
	return f.getConfigFn(doc)
}

func (f *fakeClient) ListConfigs(
	_ context.Context,
	realm, space, stack string,
) ([]v1beta1.CellConfigDoc, error) {
	if f.listConfigsFn == nil {
		return nil, errors.New("unexpected ListConfigs call")
	}
	return f.listConfigsFn(realm, space, stack)
}
