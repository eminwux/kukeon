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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	configcmd "github.com/eminwux/kukeon/cmd/kuke/get/config"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
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

// TestNewConfigCmd_ListOutputShape pins the #1375 output contract: a list query
// always emits a top-level array, including the empty (nil-slice) case that
// Go's JSON encoder would otherwise render as null.
func TestNewConfigCmd_ListOutputShape(t *testing.T) {
	t.Cleanup(viper.Reset)

	run := func(t *testing.T, list func(_, _, _ string) ([]v1beta1.CellConfigDoc, error), args ...string) string {
		t.Helper()
		t.Cleanup(viper.Reset)
		cmd := configcmd.NewConfigCmd()
		buf := &bytes.Buffer{}
		cmd.SetOut(buf)
		cmd.SetErr(buf)
		fake := &fakeClient{listConfigsFn: list}
		ctx := context.WithValue(context.Background(), configcmd.MockControllerKey{}, kukeonv1.Client(fake))
		cmd.SetContext(ctx)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return buf.String()
	}
	empty := func(_, _, _ string) ([]v1beta1.CellConfigDoc, error) { return nil, nil }

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
		populated := func(_, _, _ string) ([]v1beta1.CellConfigDoc, error) {
			return []v1beta1.CellConfigDoc{
				{Metadata: v1beta1.CellConfigMetadata{Name: "a", Realm: "r1"}},
				{Metadata: v1beta1.CellConfigMetadata{Name: "b", Realm: "r1"}},
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
	})
}

// TestNewConfigCmd_ScheduleColumns pins the #1503 table contract: `kuke get
// configs` shows SCHEDULE and NEXT, filled for a scheduled Config and empty
// for an unscheduled one, and `-o yaml` of a single Config shows
// status.schedule.
func TestNewConfigCmd_ScheduleColumns(t *testing.T) {
	t.Cleanup(viper.Reset)

	next := time.Date(2026, 11, 17, 8, 40, 0, 0, time.UTC)
	last := time.Date(2026, 11, 17, 8, 30, 0, 0, time.UTC)
	scheduled := v1beta1.CellConfigDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellConfig,
		Metadata: v1beta1.CellConfigMetadata{
			Name: "disk-watch", Realm: "default", Space: "default", Stack: "default",
		},
		Spec: v1beta1.CellConfigSpec{
			Schedule: &v1beta1.CellConfigSchedule{Cron: "*/10 * * * *"},
		},
		Status: &v1beta1.CellConfigStatus{Schedule: &v1beta1.CellConfigScheduleStatus{
			LastScheduleTime: &last,
			NextScheduleTime: &next,
			LastCell:         "disk-watch-1794904200",
			ActiveCells:      []string{"disk-watch-1794904200"},
			SkippedFires:     2,
		}},
	}
	zoned := v1beta1.CellConfigDoc{
		Metadata: v1beta1.CellConfigMetadata{Name: "nightly", Realm: "default"},
		Spec: v1beta1.CellConfigSpec{
			Schedule: &v1beta1.CellConfigSchedule{Cron: "0 2 * * *", Timezone: "Europe/Madrid"},
		},
		// Applied a moment ago: kukeond has not computed the next fire yet.
	}
	suspended := v1beta1.CellConfigDoc{
		Metadata: v1beta1.CellConfigMetadata{Name: "paused", Realm: "default"},
		Spec: v1beta1.CellConfigSpec{
			Schedule: &v1beta1.CellConfigSchedule{Cron: "@hourly", Suspend: true},
		},
		Status: &v1beta1.CellConfigStatus{Schedule: &v1beta1.CellConfigScheduleStatus{}},
	}
	plain := v1beta1.CellConfigDoc{Metadata: v1beta1.CellConfigMetadata{Name: "plain", Realm: "default"}}

	fake := &fakeClient{
		listConfigsFn: func(_, _, _ string) ([]v1beta1.CellConfigDoc, error) {
			return []v1beta1.CellConfigDoc{scheduled, zoned, suspended, plain}, nil
		},
		getConfigFn: func(v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
			return kukeonv1.GetConfigResult{Config: scheduled, MetadataExists: true}, nil
		},
	}
	run := func(t *testing.T, args ...string) string {
		t.Helper()
		t.Cleanup(viper.Reset)
		cmd := configcmd.NewConfigCmd()
		buf := &bytes.Buffer{}
		cmd.SetOut(buf)
		cmd.SetErr(buf)
		cmd.SetContext(context.WithValue(context.Background(), configcmd.MockControllerKey{}, kukeonv1.Client(fake)))
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return buf.String()
	}
	// rowFor returns the whitespace-collapsed table row of the named config.
	rowFor := func(t *testing.T, out, name string) string {
		t.Helper()
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Fields(line)
			if len(fields) > 0 && fields[0] == name {
				return strings.Join(fields, " ")
			}
		}
		t.Fatalf("no row for %q in:\n%s", name, out)
		return ""
	}

	t.Run("list table", func(t *testing.T) {
		out := run(t)
		header := strings.Join(strings.Fields(strings.SplitN(out, "\n", 2)[0]), " ")
		if header != "NAME REALM SPACE STACK SCHEDULE NEXT" {
			t.Errorf("header = %q, want NAME REALM SPACE STACK SCHEDULE NEXT", header)
		}
		for name, want := range map[string]string{
			"disk-watch": "disk-watch default default default */10 * * * * 2026-11-17T08:40:00Z",
			// A time zone other than UTC is shown; NEXT is empty until
			// kukeond's next pass.
			"nightly": "nightly default - - 0 2 * * * (Europe/Madrid)",
			"paused":  "paused default - - @hourly suspended",
			// An unscheduled Config leaves both columns empty.
			"plain": "plain default - -",
		} {
			if got := rowFor(t, out, name); got != want {
				t.Errorf("row = %q, want %q", got, want)
			}
		}
	})

	t.Run("named table row carries the same columns", func(t *testing.T) {
		out := run(t, "disk-watch")
		if got, want := rowFor(t, out, "disk-watch"),
			"disk-watch default default default */10 * * * * 2026-11-17T08:40:00Z"; got != want {
			t.Errorf("row = %q, want %q", got, want)
		}
	})

	t.Run("-o yaml shows status.schedule", func(t *testing.T) {
		out := run(t, "disk-watch", "-o", "yaml")
		var doc v1beta1.CellConfigDoc
		if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatalf("-o yaml is not a CellConfig document: %v\n%s", err, out)
		}
		status := doc.Status
		if status == nil || status.Schedule == nil {
			t.Fatalf("-o yaml carries no status.schedule:\n%s", out)
		}
		got := status.Schedule
		if !got.NextScheduleTime.Equal(next) || !got.LastScheduleTime.Equal(last) ||
			got.LastCell != "disk-watch-1794904200" || got.SkippedFires != 2 ||
			len(got.ActiveCells) != 1 || got.ActiveCells[0] != "disk-watch-1794904200" {
			t.Errorf("status.schedule = %+v", got)
		}
		for _, key := range []string{
			"lastScheduleTime:", "nextScheduleTime:", "lastCell:", "activeCells:", "skippedFires:", "lastError:",
		} {
			if !strings.Contains(out, key) {
				t.Errorf("-o yaml is missing %q:\n%s", key, out)
			}
		}
	})
}

// TestNewConfigCmd_ConnectorsColumn pins issue #1548: `-o wide` shows how each
// connector a Config requires resolves — the identity and the level it comes
// from, the method of a login, MISSING for a required connector nothing fills
// and `none` for one the cell starts without — and the structured formats
// carry the same as status.connectors.
func TestNewConfigCmd_ConnectorsColumn(t *testing.T) {
	t.Cleanup(viper.Reset)

	cfg := v1beta1.CellConfigDoc{
		Metadata: v1beta1.CellConfigMetadata{Name: "kukeon-dev", Realm: "default"},
		Status: &v1beta1.CellConfigStatus{Connectors: []v1beta1.ConnectorStatus{
			{
				Connector: "agent", Component: "agent-login", Method: "login",
				Delivery: v1beta1.ConnectorDeliveryStateVolume, State: v1beta1.ConnectorResolved,
			},
			{
				Connector: "forge", Component: "forge", Delivery: v1beta1.ConnectorDeliveryEnv,
				State: v1beta1.ConnectorResolved, Origin: v1beta1.RenderOriginRealm, Required: true,
				Identity: &v1beta1.ContainerSecretRef{Name: "forge-token", Realm: "default"},
			},
			{
				Connector: "cloud", Component: "cloud", Delivery: v1beta1.ConnectorDeliveryEnv,
				State: v1beta1.ConnectorUnresolved, Required: true,
			},
			{
				Connector: "scm", Component: "scm", Delivery: v1beta1.ConnectorDeliveryGit,
				State: v1beta1.ConnectorUnresolved,
			},
		}},
	}
	fake := &fakeClient{
		listConfigsFn: func(_, _, _ string) ([]v1beta1.CellConfigDoc, error) {
			return []v1beta1.CellConfigDoc{cfg}, nil
		},
		getConfigFn: func(v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
			return kukeonv1.GetConfigResult{Config: cfg, MetadataExists: true}, nil
		},
	}
	run := func(t *testing.T, args ...string) string {
		t.Helper()
		t.Cleanup(viper.Reset)
		cmd := configcmd.NewConfigCmd()
		buf := &bytes.Buffer{}
		cmd.SetOut(buf)
		cmd.SetErr(buf)
		cmd.SetContext(context.WithValue(context.Background(), configcmd.MockControllerKey{}, kukeonv1.Client(fake)))
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return buf.String()
	}

	wide := run(t, "--realm", "default", "-o", "wide")
	want := "agent=login, forge=forge-token (realm), cloud=MISSING, scm=none"
	if !strings.Contains(wide, "CONNECTORS") || !strings.Contains(wide, want) {
		t.Errorf("-o wide = \n%s\nwant a CONNECTORS column reading %q", wide, want)
	}
	if plain := run(t, "--realm", "default"); strings.Contains(plain, "CONNECTORS") {
		t.Errorf("the default table shows the CONNECTORS column:\n%s", plain)
	}
	for _, format := range []string{"yaml", "json"} {
		doc := run(t, "kukeon-dev", "--realm", "default", "-o", format)
		for _, field := range []string{"connectors", "forge-token", "unresolved", "agent-login"} {
			if !strings.Contains(doc, field) {
				t.Errorf("-o %s lost %q:\n%s", format, field, doc)
			}
		}
	}

	// A harness connector also says where the harness keeps its state: the
	// login identity with its login state, or a token method's state volume.
	cfg.Status.Connectors = []v1beta1.ConnectorStatus{
		{
			Connector: "agent", Method: "login", Delivery: v1beta1.ConnectorDeliveryStateVolume,
			State: v1beta1.ConnectorResolved, Volume: "dev-state", HarnessState: v1beta1.HarnessStatePersistent,
			Login: &v1beta1.LoginStatus{State: v1beta1.LoginStateLoggedIn},
		},
		{
			Connector: "other", Method: "token", Delivery: v1beta1.ConnectorDeliveryEnv,
			State: v1beta1.ConnectorResolved, Origin: v1beta1.RenderOriginRealm,
			Identity: &v1beta1.ContainerSecretRef{Name: "tok", Realm: "default"},
			Volume:   "other-state", HarnessState: v1beta1.HarnessStatePersistent,
		},
		{
			Connector: "third", Method: "token", Delivery: v1beta1.ConnectorDeliveryEnv,
			State:        v1beta1.ConnectorResolved,
			Identity:     &v1beta1.ContainerSecretRef{Name: "tok", Realm: "default"},
			HarnessState: v1beta1.HarnessStateEphemeral,
		},
	}
	wide = run(t, "--realm", "default", "-o", "wide")
	want = "agent=login [dev-state, logged in], other=tok (realm) [state other-state], third=tok [state ephemeral]"
	if !strings.Contains(wide, want) {
		t.Errorf("-o wide = \n%s\nwant the CONNECTORS column to read %q", wide, want)
	}
}
