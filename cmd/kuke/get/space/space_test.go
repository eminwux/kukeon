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

package space_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	space "github.com/eminwux/kukeon/cmd/kuke/get/space"
	"github.com/eminwux/kukeon/cmd/kuke/get/testutil"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestNewSpaceCmd(t *testing.T) {
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
			name: "get single space with realm flag",
			args: []string{"s1"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				if err := cmd.Flags().Set("realm", "r1"); err != nil {
					t.Fatal(err)
				}
			},
			fake: &fakeClient{
				getSpaceFn: func(_ v1beta1.SpaceDoc) (kukeonv1.GetSpaceResult, error) {
					return kukeonv1.GetSpaceResult{
						Space: v1beta1.SpaceDoc{
							Metadata: v1beta1.SpaceMetadata{Name: "s1"},
							Spec:     v1beta1.SpaceSpec{RealmID: "r1"},
						},
						MetadataExists: true,
					}, nil
				},
			},
		},
		{
			name: "get without realm fails",
			args: []string{"s1"},
			// No realm flag; viper isn't preset — falls back to default "default" on the
			// current env.ValueOrDefault semantics, so this tests the path through to the fake.
			fake: &fakeClient{
				getSpaceFn: func(_ v1beta1.SpaceDoc) (kukeonv1.GetSpaceResult, error) {
					return kukeonv1.GetSpaceResult{}, errdefs.ErrSpaceNotFound
				},
			},
			wantErr: `space "s1" not found`,
		},
		{
			name: "list spaces",
			fake: &fakeClient{
				listSpacesFn: func(_ string) ([]v1beta1.SpaceDoc, error) {
					return []v1beta1.SpaceDoc{
						{Metadata: v1beta1.SpaceMetadata{Name: "s1"}, Spec: v1beta1.SpaceSpec{RealmID: "r1"}},
					}, nil
				},
			},
			wantOutput: "s1",
		},
		{
			name: "list empty",
			fake: &fakeClient{
				listSpacesFn: func(_ string) ([]v1beta1.SpaceDoc, error) { return nil, nil },
			},
			wantOutput: "No spaces found.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)

			cmd := space.NewSpaceCmd()
			buf := &bytes.Buffer{}
			cmd.SetOut(buf)
			cmd.SetErr(buf)

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
			ctx = context.WithValue(ctx, space.MockControllerKey{}, kukeonv1.Client(tt.fake))
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

// TestNewSpaceCmd_Columns pins the epic:get step-2 column contract for
// `kuke get space` (issue #602): default emits `NAME REALM STATE AGE`
// (4 cols); `-o wide` appends `EGRESS NET-DEFAULTS` (6 cols). EGRESS
// renders `allow`/`deny`/`-` across the nil-cases for Spec.Network and
// Spec.Network.Egress; NET-DEFAULTS renders `yes`/`-` boolean of whether
// Spec.Defaults != nil. CGROUP/CONTROLLERS never appear in any table.
// TestNewSpaceCmd_NamedSingleRow pins the #1323 kubectl-parity flip: a named
// `kuke get space <name>` renders a single table row by default (and the wide
// row with `-o wide`), while `-o yaml` / `-o json` still emit the full
// document.
func TestNewSpaceCmd_NamedSingleRow(t *testing.T) {
	t.Cleanup(viper.Reset)

	fake := &fakeClient{
		getSpaceFn: func(_ v1beta1.SpaceDoc) (kukeonv1.GetSpaceResult, error) {
			return kukeonv1.GetSpaceResult{
				Space: v1beta1.SpaceDoc{
					Metadata: v1beta1.SpaceMetadata{Name: "s1"},
					Spec:     v1beta1.SpaceSpec{RealmID: "r1"},
					Status:   v1beta1.SpaceStatus{State: v1beta1.SpaceStateReady},
				},
				MetadataExists: true,
			}, nil
		},
	}

	run := func(t *testing.T, args ...string) string {
		t.Helper()
		t.Cleanup(viper.Reset)
		cmd := space.NewSpaceCmd()
		buf := &bytes.Buffer{}
		cmd.SetOut(buf)
		cmd.SetErr(buf)
		ctx := context.WithValue(context.Background(), space.MockControllerKey{}, kukeonv1.Client(fake))
		cmd.SetContext(ctx)
		cmd.SetArgs(append([]string{"s1", "--realm", "r1"}, args...))
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return buf.String()
	}

	t.Run("default renders single table row", func(t *testing.T) {
		out := run(t)
		header := testutil.FirstLine(out)
		for _, col := range []string{"NAME", "REALM", "STATE", "AGE"} {
			if !strings.Contains(header, col) {
				t.Errorf("named default header missing %q; got: %q", col, header)
			}
		}
		if !strings.Contains(out, "s1") {
			t.Errorf("expected single row carrying name; got:\n%s", out)
		}
		if strings.Contains(out, "metadata:") || strings.Contains(out, "EGRESS") {
			t.Errorf("named default must not emit document/wide columns; got:\n%s", out)
		}
	})

	t.Run("-o wide renders single wide row", func(t *testing.T) {
		out := run(t, "-o", "wide")
		header := testutil.FirstLine(out)
		for _, col := range []string{"NAME", "REALM", "EGRESS", "NET-DEFAULTS"} {
			if !strings.Contains(header, col) {
				t.Errorf("named wide header missing %q; got: %q", col, header)
			}
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

func TestNewSpaceCmd_Columns(t *testing.T) {
	t.Cleanup(viper.Reset)

	listFn := func(_ string) ([]v1beta1.SpaceDoc, error) {
		return []v1beta1.SpaceDoc{
			{
				// No network policy, no defaults — both wide cols render "-".
				Metadata: v1beta1.SpaceMetadata{Name: "plain"},
				Spec:     v1beta1.SpaceSpec{RealmID: "r1"},
			},
			{
				// Explicit allow + defaults set.
				Metadata: v1beta1.SpaceMetadata{Name: "open"},
				Spec: v1beta1.SpaceSpec{
					RealmID: "r1",
					Network: &v1beta1.SpaceNetwork{
						Egress: &v1beta1.EgressPolicy{Default: v1beta1.EgressDefaultAllow},
					},
					Defaults: &v1beta1.SpaceDefaults{},
				},
			},
			{
				// Explicit deny, no defaults.
				Metadata: v1beta1.SpaceMetadata{Name: "locked"},
				Spec: v1beta1.SpaceSpec{
					RealmID: "r1",
					Network: &v1beta1.SpaceNetwork{
						Egress: &v1beta1.EgressPolicy{Default: v1beta1.EgressDefaultDeny},
					},
				},
			},
			{
				// Spec.Network set but Egress nil — EGRESS renders "-".
				Metadata: v1beta1.SpaceMetadata{Name: "netonly"},
				Spec: v1beta1.SpaceSpec{
					RealmID: "r1",
					Network: &v1beta1.SpaceNetwork{},
				},
			},
		}, nil
	}

	t.Run("default table is NAME REALM STATE AGE", func(t *testing.T) {
		t.Cleanup(viper.Reset)

		cmd := space.NewSpaceCmd()
		buf := &bytes.Buffer{}
		cmd.SetOut(buf)
		cmd.SetErr(buf)
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
		ctx = context.WithValue(ctx, space.MockControllerKey{},
			kukeonv1.Client(&fakeClient{listSpacesFn: listFn}))
		cmd.SetContext(ctx)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		out := buf.String()
		header := testutil.FirstLine(out)
		for _, want := range []string{"NAME", "REALM", "STATE", "AGE"} {
			if !strings.Contains(header, want) {
				t.Errorf("default header missing %q; got: %q", want, header)
			}
		}
		for _, denied := range []string{"EGRESS", "NET-DEFAULTS", "CGROUP", "CONTROLLERS"} {
			if strings.Contains(out, denied) {
				t.Errorf("default output must NOT contain %q; got:\n%s", denied, out)
			}
		}
	})

	t.Run("-o wide appends EGRESS NET-DEFAULTS", func(t *testing.T) {
		t.Cleanup(viper.Reset)

		cmd := space.NewSpaceCmd()
		buf := &bytes.Buffer{}
		cmd.SetOut(buf)
		cmd.SetErr(buf)
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
		ctx = context.WithValue(ctx, space.MockControllerKey{},
			kukeonv1.Client(&fakeClient{listSpacesFn: listFn}))
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{"-o", "wide"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		out := buf.String()
		header := testutil.FirstLine(out)
		for _, want := range []string{"NAME", "REALM", "STATE", "AGE", "EGRESS", "NET-DEFAULTS"} {
			if !strings.Contains(header, want) {
				t.Errorf("wide header missing %q; got: %q", want, header)
			}
		}
		for _, denied := range []string{"CGROUP", "CONTROLLERS"} {
			if strings.Contains(out, denied) {
				t.Errorf("-o wide output must NOT contain %q; got:\n%s", denied, out)
			}
		}

		// Per-row EGRESS / NET-DEFAULTS rendering. Each row is identified
		// by its NAME prefix; the assertion checks the row's cell values.
		rows := dataRows(out)
		cases := map[string]struct {
			egress   string
			defaults string
		}{
			"plain":   {egress: "-", defaults: "-"},
			"open":    {egress: "allow", defaults: "yes"},
			"locked":  {egress: "deny", defaults: "-"},
			"netonly": {egress: "-", defaults: "-"},
		}
		for name, want := range cases {
			row := findRow(rows, name)
			if row == "" {
				t.Errorf("row for %q not found in:\n%s", name, out)
				continue
			}
			fields := strings.Fields(row)
			// Columns: NAME REALM STATE AGE EGRESS NET-DEFAULTS = 6 fields.
			if len(fields) != 6 {
				t.Errorf("row %q: expected 6 fields, got %d (%q)", name, len(fields), row)
				continue
			}
			if fields[4] != want.egress {
				t.Errorf("row %q: EGRESS = %q, want %q", name, fields[4], want.egress)
			}
			if fields[5] != want.defaults {
				t.Errorf("row %q: NET-DEFAULTS = %q, want %q", name, fields[5], want.defaults)
			}
		}
	})
}

// dataRows returns the non-header rows of a PrintTable output. Lines 1
// (header) and 2 (separator "---") are skipped; the remainder are data.
func dataRows(s string) []string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) < 3 {
		return nil
	}
	return lines[2:]
}

// findRow returns the first row whose first whitespace-delimited field
// equals name, or "" when no row matches.
func findRow(rows []string, name string) string {
	for _, r := range rows {
		fields := strings.Fields(r)
		if len(fields) > 0 && fields[0] == name {
			return r
		}
	}
	return ""
}

// TestNewSpaceCmd_NoCgroupOrControllers pins the epic:get step-1
// cross-cutting cleanup for spaces: the CGROUP column and the
// --show-controllers flag must be gone, and -o wide must not resurrect
// either (sibling #602 owns the per-entity wide columns).
func TestNewSpaceCmd_NoCgroupOrControllers(t *testing.T) {
	t.Cleanup(viper.Reset)

	if space.NewSpaceCmd().Flags().Lookup("show-controllers") != nil {
		t.Error("show-controllers flag must be removed (issue #827)")
	}

	listFn := func(_ string) ([]v1beta1.SpaceDoc, error) {
		return []v1beta1.SpaceDoc{
			{
				Metadata: v1beta1.SpaceMetadata{Name: "s1"},
				Spec:     v1beta1.SpaceSpec{RealmID: "r1"},
				Status: v1beta1.SpaceStatus{
					CgroupPath:         "/kukeon/r1/s1",
					SubtreeControllers: []string{"cpu", "memory"},
				},
			},
		}, nil
	}

	for _, args := range [][]string{nil, {"-o", "wide"}} {
		buf := &bytes.Buffer{}
		cmd := space.NewSpaceCmd()
		cmd.SetOut(buf)
		cmd.SetErr(buf)
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
		ctx = context.WithValue(ctx, space.MockControllerKey{},
			kukeonv1.Client(&fakeClient{listSpacesFn: listFn}))
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

// TestNewSpaceCmd_Selector verifies the `-l`/`--selector` filter wiring on
// `kuke get space` (issue #614). Grammar coverage lives in the shared
// selector_test.go; this test pins the per-verb wiring: the flag exists,
// is parsed, applied against Metadata.Labels post-list, and combining it
// with a positional name fails fast.
func TestNewSpaceCmd_Selector(t *testing.T) {
	t.Cleanup(viper.Reset)

	listFn := func(_ string) ([]v1beta1.SpaceDoc, error) {
		return []v1beta1.SpaceDoc{
			{
				Metadata: v1beta1.SpaceMetadata{
					Name:   "web",
					Labels: map[string]string{"tier": "web"},
				},
				Spec: v1beta1.SpaceSpec{RealmID: "r1"},
			},
			{
				Metadata: v1beta1.SpaceMetadata{
					Name:   "db",
					Labels: map[string]string{"tier": "db"},
				},
				Spec: v1beta1.SpaceSpec{RealmID: "r1"},
			},
		}, nil
	}

	t.Run("equality filters by label", func(t *testing.T) {
		t.Cleanup(viper.Reset)
		cmd := space.NewSpaceCmd()
		buf := &bytes.Buffer{}
		cmd.SetOut(buf)
		cmd.SetErr(buf)
		ctx := context.WithValue(context.Background(), space.MockControllerKey{},
			kukeonv1.Client(&fakeClient{listSpacesFn: listFn}))
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{"-l", "tier=web"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, "web") {
			t.Errorf("expected 'web' in output, got:\n%s", out)
		}
		if strings.Contains(out, "db") {
			t.Errorf("expected 'db' filtered out, got:\n%s", out)
		}
	})

	t.Run("selector + name is rejected", func(t *testing.T) {
		t.Cleanup(viper.Reset)
		cmd := space.NewSpaceCmd()
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		ctx := context.WithValue(context.Background(), space.MockControllerKey{},
			kukeonv1.Client(&fakeClient{}))
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{"web", "-l", "tier=web"})
		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "--selector cannot be combined") {
			t.Fatalf("expected --selector + name rejection, got: %v", err)
		}
	})
}

type fakeClient struct {
	kukeonv1.FakeClient

	getSpaceFn   func(doc v1beta1.SpaceDoc) (kukeonv1.GetSpaceResult, error)
	listSpacesFn func(realm string) ([]v1beta1.SpaceDoc, error)
}

func (f *fakeClient) GetSpace(_ context.Context, doc v1beta1.SpaceDoc) (kukeonv1.GetSpaceResult, error) {
	if f.getSpaceFn == nil {
		return kukeonv1.GetSpaceResult{}, errors.New("unexpected GetSpace call")
	}
	return f.getSpaceFn(doc)
}

func (f *fakeClient) ListSpaces(_ context.Context, realm string) ([]v1beta1.SpaceDoc, error) {
	if f.listSpacesFn == nil {
		return nil, errors.New("unexpected ListSpaces call")
	}
	return f.listSpacesFn(realm)
}
