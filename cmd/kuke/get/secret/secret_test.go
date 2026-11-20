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

package secret_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	secret "github.com/eminwux/kukeon/cmd/kuke/get/secret"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestNewSecretCmd(t *testing.T) {
	t.Cleanup(viper.Reset)

	tests := []struct {
		name            string
		args            []string
		setup           func(t *testing.T, cmd *cobra.Command)
		fake            *fakeClient
		wantErr         string
		wantOutput      string
		wantNotInOutput []string
	}{
		{
			// Single-resource get renders YAML to os.Stdout (not the command
			// buffer), so this case only asserts the happy path returns no
			// error; the metadata-only / never-echo guarantee is pinned at the
			// controller and runner layers.
			name: "get single secret metadata",
			args: []string{"tok"},
			setup: func(_ *testing.T, cmd *cobra.Command) {
				_ = cmd.Flags().Set("realm", "r1")
				_ = cmd.Flags().Set("space", "s1")
			},
			fake: &fakeClient{
				getSecretFn: func(doc v1beta1.SecretDoc) (kukeonv1.GetSecretResult, error) {
					return kukeonv1.GetSecretResult{
						Secret:         v1beta1.SecretDoc{Metadata: doc.Metadata},
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
				getSecretFn: func(_ v1beta1.SecretDoc) (kukeonv1.GetSecretResult, error) {
					return kukeonv1.GetSecretResult{}, errdefs.ErrSecretNotFound
				},
			},
			wantErr: `secret "missing" not found`,
		},
		{
			name: "get not found via MetadataExists=false",
			args: []string{"missing"},
			setup: func(_ *testing.T, cmd *cobra.Command) {
				_ = cmd.Flags().Set("realm", "r1")
			},
			fake: &fakeClient{
				getSecretFn: func(_ v1beta1.SecretDoc) (kukeonv1.GetSecretResult, error) {
					return kukeonv1.GetSecretResult{MetadataExists: false}, nil
				},
			},
			wantErr: `secret "missing" not found`,
		},
		{
			name: "list empty",
			fake: &fakeClient{
				listSecretsFn: func(_, _, _, _ string) ([]v1beta1.SecretDoc, error) {
					return nil, nil
				},
			},
			wantOutput: "No secrets found.",
		},
		{
			name: "list table renders scope columns",
			fake: &fakeClient{
				listSecretsFn: func(_, _, _, _ string) ([]v1beta1.SecretDoc, error) {
					return []v1beta1.SecretDoc{
						{Metadata: v1beta1.SecretMetadata{Name: "realm-tok", Realm: "default"}},
						{
							Metadata: v1beta1.SecretMetadata{
								Name:  "cell-tok",
								Realm: "default",
								Space: "s",
								Stack: "st",
								Cell:  "c",
							},
						},
					}, nil
				},
			},
			wantOutput:      "realm-tok",
			wantNotInOutput: []string{"data:"},
		},
		{
			name: "list passes filter scope through",
			setup: func(_ *testing.T, cmd *cobra.Command) {
				_ = cmd.Flags().Set("realm", "default")
				_ = cmd.Flags().Set("space", "team-a")
			},
			fake: &fakeClient{
				listSecretsFn: func(realm, space, _, _ string) ([]v1beta1.SecretDoc, error) {
					if realm != "default" || space != "team-a" {
						return nil, errors.New("unexpected filter scope")
					}
					return []v1beta1.SecretDoc{
						{Metadata: v1beta1.SecretMetadata{Name: "space-tok", Realm: realm, Space: space}},
					}, nil
				},
			},
			wantOutput: "space-tok",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)

			cmd := secret.NewSecretCmd()
			buf := &bytes.Buffer{}
			cmd.SetOut(buf)
			cmd.SetErr(buf)

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
			ctx = context.WithValue(ctx, secret.MockControllerKey{}, kukeonv1.Client(tt.fake))
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
			for _, banned := range tt.wantNotInOutput {
				if strings.Contains(buf.String(), banned) {
					t.Errorf("output unexpectedly contains %q\nGot:\n%s", banned, buf.String())
				}
			}
		})
	}
}

// TestNewSecretCmd_NamedSingleRow pins the #1323 kubectl-parity flip: a named
// `kuke get secret <name>` renders a single table row by default, while
// `-o yaml` / `-o json` still emit the full document.
func TestNewSecretCmd_NamedSingleRow(t *testing.T) {
	t.Cleanup(viper.Reset)

	fake := &fakeClient{
		getSecretFn: func(doc v1beta1.SecretDoc) (kukeonv1.GetSecretResult, error) {
			return kukeonv1.GetSecretResult{
				Secret:         v1beta1.SecretDoc{Metadata: doc.Metadata},
				MetadataExists: true,
			}, nil
		},
	}

	run := func(t *testing.T, args ...string) string {
		t.Helper()
		t.Cleanup(viper.Reset)
		cmd := secret.NewSecretCmd()
		buf := &bytes.Buffer{}
		cmd.SetOut(buf)
		cmd.SetErr(buf)
		ctx := context.WithValue(context.Background(), secret.MockControllerKey{}, kukeonv1.Client(fake))
		cmd.SetContext(ctx)
		cmd.SetArgs(append([]string{"tok", "--realm", "r1", "--space", "s1"}, args...))
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return buf.String()
	}

	t.Run("default renders single table row", func(t *testing.T) {
		out := run(t)
		for _, col := range []string{"NAME", "REALM", "SPACE", "STACK", "CELL"} {
			if !strings.Contains(out, col) {
				t.Errorf("named default missing column %q; got:\n%s", col, out)
			}
		}
		if !strings.Contains(out, "tok") {
			t.Errorf("expected single row carrying name; got:\n%s", out)
		}
		if strings.Contains(out, "metadata:") {
			t.Errorf("named default must not emit the full document; got:\n%s", out)
		}
	})

	t.Run("-o wide renders a single row", func(t *testing.T) {
		out := run(t, "-o", "wide")
		if !strings.Contains(out, "NAME") || !strings.Contains(out, "tok") {
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

	getSecretFn   func(doc v1beta1.SecretDoc) (kukeonv1.GetSecretResult, error)
	listSecretsFn func(realm, space, stack, cell string) ([]v1beta1.SecretDoc, error)
}

func (f *fakeClient) GetSecret(_ context.Context, doc v1beta1.SecretDoc) (kukeonv1.GetSecretResult, error) {
	if f.getSecretFn == nil {
		return kukeonv1.GetSecretResult{}, errors.New("unexpected GetSecret call")
	}
	return f.getSecretFn(doc)
}

func (f *fakeClient) ListSecrets(
	_ context.Context,
	realm, space, stack, cell string,
) ([]v1beta1.SecretDoc, error) {
	if f.listSecretsFn == nil {
		return nil, errors.New("unexpected ListSecrets call")
	}
	return f.listSecretsFn(realm, space, stack, cell)
}

// TestNewSecretCmd_ListOutputShape pins the #1375 output contract: a list query
// always emits a top-level array, including the empty (nil-slice) case that
// Go's JSON encoder would otherwise render as null.
func TestNewSecretCmd_ListOutputShape(t *testing.T) {
	t.Cleanup(viper.Reset)

	run := func(t *testing.T, list func(_, _, _, _ string) ([]v1beta1.SecretDoc, error), args ...string) string {
		t.Helper()
		t.Cleanup(viper.Reset)
		cmd := secret.NewSecretCmd()
		buf := &bytes.Buffer{}
		cmd.SetOut(buf)
		cmd.SetErr(buf)
		fake := &fakeClient{listSecretsFn: list}
		ctx := context.WithValue(context.Background(), secret.MockControllerKey{}, kukeonv1.Client(fake))
		cmd.SetContext(ctx)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return buf.String()
	}
	empty := func(_, _, _, _ string) ([]v1beta1.SecretDoc, error) { return nil, nil }

	t.Run("-o json empty list emits []", func(t *testing.T) {
		if out := strings.TrimSpace(run(t, empty, "-o", "json")); out != "[]" {
			t.Errorf("empty list JSON = %q, want []", out)
		}
	})

	t.Run("-o json empty filter result emits []", func(t *testing.T) {
		out := strings.TrimSpace(run(t, empty, "-o", "json", "--realm", "r1", "--space", "s1", "--stack", "st1"))
		if out != "[]" {
			t.Errorf("empty filtered JSON = %q, want []", out)
		}
	})

	t.Run("-o yaml empty list emits []", func(t *testing.T) {
		if out := strings.TrimSpace(run(t, empty, "-o", "yaml")); out != "[]" {
			t.Errorf("empty list YAML = %q, want []", out)
		}
	})

	t.Run("-o json populated list emits an array", func(t *testing.T) {
		populated := func(_, _, _, _ string) ([]v1beta1.SecretDoc, error) {
			return []v1beta1.SecretDoc{
				{Metadata: v1beta1.SecretMetadata{Name: "a", Realm: "r1"}},
				{Metadata: v1beta1.SecretMetadata{Name: "b", Realm: "r1"}},
			}, nil
		}
		out := run(t, populated, "-o", "json")
		var items []map[string]any
		if err := json.Unmarshal([]byte(out), &items); err != nil {
			t.Fatalf("populated list JSON is not an array: %v\n%s", err, out)
		}
		if len(items) != 2 {
			t.Errorf("populated list JSON has %d items, want 2; got:\n%s", len(items), out)
		}
		if strings.Contains(out, "\"data\"") {
			t.Errorf("list JSON must not carry secret data; got:\n%s", out)
		}
	})
}

// TestNewSecretCmd_TypeColumn pins the #1472 TYPE column: the table reports
// each secret's type (an unset type reads as opaque, so secrets written before
// typed secrets existed list correctly), on both the list and the named row,
// and `-o yaml` carries the type and nothing else of the spec.
func TestNewSecretCmd_TypeColumn(t *testing.T) {
	t.Cleanup(viper.Reset)

	secrets := map[string]string{
		"deploy-key":  v1beta1.SecretTypeSSHKey,
		"forge-token": v1beta1.SecretTypeBasicAuth,
		"legacy":      "",
	}
	fake := &fakeClient{
		getSecretFn: func(doc v1beta1.SecretDoc) (kukeonv1.GetSecretResult, error) {
			return kukeonv1.GetSecretResult{
				Secret: v1beta1.SecretDoc{
					Metadata: doc.Metadata,
					Spec:     v1beta1.SecretSpec{Type: secrets[doc.Metadata.Name]},
				},
				MetadataExists: true,
			}, nil
		},
		listSecretsFn: func(_, _, _, _ string) ([]v1beta1.SecretDoc, error) {
			out := make([]v1beta1.SecretDoc, 0, len(secrets))
			for _, name := range []string{"deploy-key", "forge-token", "legacy"} {
				out = append(out, v1beta1.SecretDoc{
					Metadata: v1beta1.SecretMetadata{Name: name, Realm: "r1"},
					Spec:     v1beta1.SecretSpec{Type: secrets[name]},
				})
			}
			return out, nil
		},
	}
	run := func(t *testing.T, args ...string) string {
		t.Helper()
		t.Cleanup(viper.Reset)
		cmd := secret.NewSecretCmd()
		buf := &bytes.Buffer{}
		cmd.SetOut(buf)
		cmd.SetErr(buf)
		cmd.SetContext(context.WithValue(context.Background(), secret.MockControllerKey{}, kukeonv1.Client(fake)))
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return buf.String()
	}
	// rowType returns the TYPE cell of the row whose NAME is name, located by
	// the header's column order rather than by offset.
	rowType := func(t *testing.T, out, name string) string {
		t.Helper()
		typeCol := -1
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			if fields[0] == "NAME" {
				for i, f := range fields {
					if f == "TYPE" {
						typeCol = i
					}
				}
				if typeCol != 1 {
					t.Fatalf("TYPE column index = %d, want 1 (right after NAME); header: %q", typeCol, line)
				}
				continue
			}
			if fields[0] == name {
				if typeCol < 0 || typeCol >= len(fields) {
					t.Fatalf("row %q has no TYPE cell: %q", name, line)
				}
				return fields[typeCol]
			}
		}
		t.Fatalf("no row for %q in:\n%s", name, out)
		return ""
	}

	want := map[string]string{
		"deploy-key":  v1beta1.SecretTypeSSHKey,
		"forge-token": v1beta1.SecretTypeBasicAuth,
		"legacy":      v1beta1.SecretTypeOpaque,
	}

	t.Run("list", func(t *testing.T) {
		out := run(t, "--realm", "r1")
		for name, typ := range want {
			if got := rowType(t, out, name); got != typ {
				t.Errorf("list TYPE for %q = %q, want %q; got:\n%s", name, got, typ, out)
			}
		}
	})

	t.Run("named row", func(t *testing.T) {
		for name, typ := range want {
			out := run(t, name, "--realm", "r1")
			if got := rowType(t, out, name); got != typ {
				t.Errorf("named TYPE for %q = %q, want %q; got:\n%s", name, got, typ, out)
			}
		}
	})

	t.Run("-o yaml carries the type only", func(t *testing.T) {
		out := run(t, "deploy-key", "--realm", "r1", "-o", "yaml")
		if !strings.Contains(out, "type: "+v1beta1.SecretTypeSSHKey) {
			t.Errorf("-o yaml should report the type; got:\n%s", out)
		}
		for _, line := range strings.Split(out, "\n") {
			key, _, _ := strings.Cut(strings.TrimSpace(line), ":")
			switch key {
			case "data", "sshKey", "basicAuth", "privateKey", "password", "token":
				t.Errorf("-o yaml emits material field %q; got:\n%s", key, out)
			}
		}
	})
}

// TestNewSecretCmd_ConnectorAndIdentity pins issue #1548: `kuke get secret`
// shows the connector a secret is an identity of — a CONNECTOR column, "-" for
// an untagged secret — `-o wide` adds its readable identity fields, and the
// structured formats carry both. Nothing of the material is ever reported.
func TestNewSecretCmd_ConnectorAndIdentity(t *testing.T) {
	t.Cleanup(viper.Reset)

	tagged := v1beta1.SecretDoc{
		Metadata: v1beta1.SecretMetadata{Name: "git-ssh", Realm: "r1"},
		Spec: v1beta1.SecretSpec{
			Type:      v1beta1.SecretTypeSSHKey,
			Connector: "git",
			Identity:  map[string]string{"hosts": "github.com", "author": "Ada <ada@example.com>"},
		},
	}
	plain := v1beta1.SecretDoc{Metadata: v1beta1.SecretMetadata{Name: "legacy", Realm: "r1"}}
	fake := &fakeClient{
		getSecretFn: func(v1beta1.SecretDoc) (kukeonv1.GetSecretResult, error) {
			return kukeonv1.GetSecretResult{Secret: tagged, MetadataExists: true}, nil
		},
		listSecretsFn: func(_, _, _, _ string) ([]v1beta1.SecretDoc, error) {
			return []v1beta1.SecretDoc{tagged, plain}, nil
		},
	}
	run := func(t *testing.T, args ...string) string {
		t.Helper()
		t.Cleanup(viper.Reset)
		cmd := secret.NewSecretCmd()
		buf := &bytes.Buffer{}
		cmd.SetOut(buf)
		cmd.SetErr(buf)
		cmd.SetContext(context.WithValue(context.Background(), secret.MockControllerKey{}, kukeonv1.Client(fake)))
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return buf.String()
	}
	row := func(t *testing.T, out, name string) []string {
		t.Helper()
		for _, line := range strings.Split(out, "\n") {
			if fields := strings.Fields(line); len(fields) > 0 && fields[0] == name {
				return fields
			}
		}
		t.Fatalf("no row for %q in:\n%s", name, out)
		return nil
	}

	out := run(t, "--realm", "r1")
	if header := row(t, out, "NAME"); len(header) < 3 || header[2] != "CONNECTOR" {
		t.Fatalf("header = %v, want CONNECTOR right after TYPE", header)
	}
	if got := row(t, out, "git-ssh")[2]; got != "git" {
		t.Errorf("CONNECTOR of git-ssh = %q, want git", got)
	}
	if got := row(t, out, "legacy")[2]; got != "-" {
		t.Errorf("CONNECTOR of an untagged secret = %q, want -", got)
	}
	if strings.Contains(out, "IDENTITY") || strings.Contains(out, "ada@example.com") {
		t.Errorf("the default table shows identity fields:\n%s", out)
	}

	wide := run(t, "--realm", "r1", "-o", "wide")
	if !strings.Contains(wide, "IDENTITY") ||
		!strings.Contains(wide, "author=Ada <ada@example.com>; hosts=github.com") {
		t.Errorf("-o wide does not list the identity fields:\n%s", wide)
	}

	for _, format := range []string{"yaml", "json"} {
		doc := run(t, "git-ssh", "--realm", "r1", "-o", format)
		for _, want := range []string{"connector", "git", "identity", "github.com", "ada@example.com"} {
			if !strings.Contains(doc, want) {
				t.Errorf("-o %s lost %q:\n%s", format, want, doc)
			}
		}
		for _, never := range []string{"privateKey", "PRIVATE KEY", "\n  data:", `"data"`, "basicAuth", "sshKey"} {
			if strings.Contains(doc, never) {
				t.Errorf("-o %s carries material (%q):\n%s", format, never, doc)
			}
		}
	}
}
