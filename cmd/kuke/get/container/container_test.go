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

package container_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	container "github.com/eminwux/kukeon/cmd/kuke/get/container"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestNewContainerCmd(t *testing.T) {
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
			name: "get single container",
			args: []string{"co1"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				_ = cmd.Flags().Set("realm", "r1")
				_ = cmd.Flags().Set("space", "s1")
				_ = cmd.Flags().Set("stack", "st1")
				_ = cmd.Flags().Set("cell", "ce1")
			},
			fake: &fakeClient{
				getContainerFn: func(_ v1beta1.ContainerDoc) (kukeonv1.GetContainerResult, error) {
					return kukeonv1.GetContainerResult{
						Container: v1beta1.ContainerDoc{
							Metadata: v1beta1.ContainerMetadata{Name: "co1"},
							Spec:     v1beta1.ContainerSpec{ID: "co1", RealmID: "r1"},
						},
						ContainerExists: true,
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
				_ = cmd.Flags().Set("cell", "ce1")
			},
			fake: &fakeClient{
				getContainerFn: func(_ v1beta1.ContainerDoc) (kukeonv1.GetContainerResult, error) {
					return kukeonv1.GetContainerResult{}, errdefs.ErrContainerNotFound
				},
			},
			wantErr: `container "missing" not found`,
		},
		{
			name: "get missing cell flag",
			args: []string{"co1"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				_ = cmd.Flags().Set("realm", "r1")
				_ = cmd.Flags().Set("space", "s1")
				_ = cmd.Flags().Set("stack", "st1")
			},
			fake:    &fakeClient{},
			wantErr: "cell name is required",
		},
		{
			name: "list empty",
			fake: &fakeClient{
				listContainersFn: func(_, _, _, _ string) ([]v1beta1.ContainerSpec, error) {
					return nil, nil
				},
			},
			wantOutput: "No containers found.",
		},
		{
			// Issue #472, Option B. Cell filter returns zero in the current
			// scope, but the named cell exists in a different (realm,space,
			// stack). Table output appends a Hint: line naming where to look.
			name: "list empty with cell-only filter hints at other scope",
			setup: func(_ *testing.T, cmd *cobra.Command) {
				_ = cmd.Flags().Set("cell", "kukeond")
			},
			fake: &fakeClient{
				listContainersFn: func(_, _, _, cell string) ([]v1beta1.ContainerSpec, error) {
					if cell == "kukeond" {
						return nil, nil
					}
					return []v1beta1.ContainerSpec{
						{
							ID:      "kukeond",
							RealmID: "kuke-system",
							SpaceID: "kukeon",
							StackID: "kukeon",
							CellID:  "kukeond",
						},
					}, nil
				},
			},
			wantOutput: `No containers found for cell "kukeond".` + "\n" +
				`Hint: cell "kukeond" exists in realm "kuke-system" space "kukeon" stack "kukeon" — pass --realm/--space/--stack to filter there.`,
		},
		{
			// Cell filter empty *and* no cross-scope match — no hint, plain
			// scoped message so the operator knows the filter fired and just
			// matched nothing.
			name: "list empty with cell filter no match anywhere",
			setup: func(_ *testing.T, cmd *cobra.Command) {
				_ = cmd.Flags().Set("cell", "ghost")
			},
			fake: &fakeClient{
				listContainersFn: func(_, _, _, cell string) ([]v1beta1.ContainerSpec, error) {
					if cell == "ghost" {
						return nil, nil
					}
					return []v1beta1.ContainerSpec{
						{ID: "x", RealmID: "r1", SpaceID: "s1", StackID: "st1", CellID: "other"},
					}, nil
				},
			},
			wantOutput: `No containers found for cell "ghost".`,
		},
		{
			// AC4: --stack follows the same convention. Empty result with
			// --stack <name> set hints at the realm/space where it exists.
			name: "list empty with stack-only filter hints at other scope",
			setup: func(_ *testing.T, cmd *cobra.Command) {
				_ = cmd.Flags().Set("stack", "kukeon")
			},
			fake: &fakeClient{
				listContainersFn: func(_, _, stack, _ string) ([]v1beta1.ContainerSpec, error) {
					if stack == "kukeon" {
						return nil, nil
					}
					return []v1beta1.ContainerSpec{
						{
							ID:      "kukeond",
							RealmID: "kuke-system",
							SpaceID: "kukeon",
							StackID: "kukeon",
							CellID:  "kukeond",
						},
					}, nil
				},
			},
			wantOutput: `Hint: stack "kukeon" exists in realm "kuke-system" space "kukeon" stack "kukeon"`,
		},
		{
			// AC4: --space follows the same convention.
			name: "list empty with space-only filter hints at other scope",
			setup: func(_ *testing.T, cmd *cobra.Command) {
				_ = cmd.Flags().Set("space", "kukeon")
			},
			fake: &fakeClient{
				listContainersFn: func(_, space, _, _ string) ([]v1beta1.ContainerSpec, error) {
					if space == "kukeon" {
						return nil, nil
					}
					return []v1beta1.ContainerSpec{
						{
							ID:      "kukeond",
							RealmID: "kuke-system",
							SpaceID: "kukeon",
							StackID: "kukeon",
							CellID:  "kukeond",
						},
					}, nil
				},
			},
			wantOutput: `Hint: space "kukeon" exists in realm "kuke-system" space "kukeon" stack "kukeon"`,
		},
		{
			// realm-only filter does not trigger a hint: realm is the
			// top-level scope; if the user asked for a realm by name and got
			// nothing, "look in another realm" is not actionable advice.
			name: "list empty with realm-only filter does not hint",
			setup: func(_ *testing.T, cmd *cobra.Command) {
				_ = cmd.Flags().Set("realm", "default")
			},
			fake: &fakeClient{
				listContainersFn: func(realm, _, _, _ string) ([]v1beta1.ContainerSpec, error) {
					if realm == "default" {
						return nil, nil
					}
					return []v1beta1.ContainerSpec{
						{
							ID:      "kukeond",
							RealmID: "kuke-system",
							SpaceID: "kukeon",
							StackID: "kukeon",
							CellID:  "kukeond",
						},
					}, nil
				},
			},
			wantOutput:      `No containers found for realm "default".`,
			wantNotInOutput: []string{"Hint:"},
		},
		{
			// -o yaml on empty result emits an empty list and no hint —
			// the hint is a table-only UX aid; structured output stays
			// machine-readable.
			name: "list empty yaml output suppresses hint",
			setup: func(_ *testing.T, cmd *cobra.Command) {
				_ = cmd.Flags().Set("cell", "kukeond")
				_ = cmd.Flags().Set("output", "yaml")
			},
			fake: &fakeClient{
				listContainersFn: func(_, _, _, _ string) ([]v1beta1.ContainerSpec, error) {
					return nil, nil
				},
			},
			wantNotInOutput: []string{"Hint:", "No containers found"},
		},
		{
			name: "list one and probe state",
			fake: &fakeClient{
				listContainersFn: func(_, _, _, _ string) ([]v1beta1.ContainerSpec, error) {
					return []v1beta1.ContainerSpec{
						{ID: "co1", RealmID: "r1", SpaceID: "s1", StackID: "st1", CellID: "ce1", Image: "img"},
					}, nil
				},
				getContainerFn: func(_ v1beta1.ContainerDoc) (kukeonv1.GetContainerResult, error) {
					return kukeonv1.GetContainerResult{
						Container: v1beta1.ContainerDoc{
							Status: v1beta1.ContainerStatus{State: v1beta1.ContainerStateReady},
						},
						ContainerExists: true,
					}, nil
				},
			},
			wantOutput: "co1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)

			cmd := container.NewContainerCmd()
			buf := &bytes.Buffer{}
			cmd.SetOut(buf)
			cmd.SetErr(buf)

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
			ctx = context.WithValue(ctx, container.MockControllerKey{}, kukeonv1.Client(tt.fake))
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

// TestGetContainer_NotFound_SurfacesSentinel locks in the
// ErrContainerNotFound branch's %w wrap: when GetContainer's RPC reports
// the named container doesn't exist, `kuke get container <name>` must
// propagate the sentinel so upstream callers can still errors.Is it.
func TestGetContainer_NotFound_SurfacesSentinel(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		getContainerFn: func(_ v1beta1.ContainerDoc) (kukeonv1.GetContainerResult, error) {
			return kukeonv1.GetContainerResult{}, errdefs.ErrContainerNotFound
		},
	}
	cmd := container.NewContainerCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	ctx = context.WithValue(ctx, container.MockControllerKey{}, kukeonv1.Client(fc))
	cmd.SetContext(ctx)
	_ = cmd.Flags().Set("realm", "r1")
	_ = cmd.Flags().Set("space", "s1")
	_ = cmd.Flags().Set("stack", "st1")
	_ = cmd.Flags().Set("cell", "ce1")
	cmd.SetArgs([]string{"missing"})

	err := cmd.Execute()
	if !errors.Is(err, errdefs.ErrContainerNotFound) {
		t.Fatalf("error %v does not unwrap to ErrContainerNotFound", err)
	}
}

// TestNewContainerCmd_DefaultColumns pins the default `kuke get container`
// column set after the epic:get redefinition (issue #605): NAME REALM SPACE
// STACK CELL STATE RESTARTS AGE — eight columns. ROOT (as a column), IMAGE
// (as a default column), and CGROUP must NOT appear.
func TestNewContainerCmd_DefaultColumns(t *testing.T) {
	t.Cleanup(viper.Reset)

	listFn := func(_, _, _, _ string) ([]v1beta1.ContainerSpec, error) {
		return []v1beta1.ContainerSpec{
			{ID: "co1", RealmID: "r1", SpaceID: "s1", StackID: "st1", CellID: "ce1", Image: "img"},
		}, nil
	}
	getFn := func(_ v1beta1.ContainerDoc) (kukeonv1.GetContainerResult, error) {
		return kukeonv1.GetContainerResult{
			Container: v1beta1.ContainerDoc{
				Status: v1beta1.ContainerStatus{
					State:        v1beta1.ContainerStateReady,
					RestartCount: 3,
					CreatedAt:    time.Now().Add(-2 * time.Hour),
				},
			},
			ContainerExists: true,
		}, nil
	}

	buf := &bytes.Buffer{}
	cmd := container.NewContainerCmd()
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	ctx = context.WithValue(ctx, container.MockControllerKey{},
		kukeonv1.Client(&fakeClient{listContainersFn: listFn, getContainerFn: getFn}))
	cmd.SetContext(ctx)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	for _, h := range []string{"NAME", "REALM", "SPACE", "STACK", "CELL", "STATE", "RESTARTS", "AGE"} {
		if !strings.Contains(out, h) {
			t.Errorf("default table missing header %q\nGot:\n%s", h, out)
		}
	}
	for _, denied := range []string{"ROOT", "IMAGE", "EXIT", "CGROUP"} {
		if strings.Contains(out, denied) {
			t.Errorf("default table must NOT contain %q; got:\n%s", denied, out)
		}
	}
	// RESTARTS column carries the integer literally.
	if !strings.Contains(out, " 3 ") {
		t.Errorf("default row missing RESTARTS=3\nGot:\n%s", out)
	}
}

// TestNewContainerCmd_WideColumns pins the `-o wide` column set after the
// epic:get redefinition: NAME REALM SPACE STACK CELL STATE RESTARTS AGE
// IMAGE EXIT — ten columns. CGROUP / ROOT must NOT appear.
func TestNewContainerCmd_WideColumns(t *testing.T) {
	t.Cleanup(viper.Reset)

	listFn := func(_, _, _, _ string) ([]v1beta1.ContainerSpec, error) {
		return []v1beta1.ContainerSpec{
			{ID: "co1", RealmID: "r1", SpaceID: "s1", StackID: "st1", CellID: "ce1", Image: "nginx:alpine"},
		}, nil
	}
	getFn := func(_ v1beta1.ContainerDoc) (kukeonv1.GetContainerResult, error) {
		return kukeonv1.GetContainerResult{
			Container: v1beta1.ContainerDoc{
				Status: v1beta1.ContainerStatus{
					State:        v1beta1.ContainerStateStopped,
					RestartCount: 1,
					CreatedAt:    time.Now().Add(-30 * time.Minute),
					ExitCode:     137,
					ExitSignal:   "SIGKILL",
				},
			},
			ContainerExists: true,
		}, nil
	}

	buf := &bytes.Buffer{}
	cmd := container.NewContainerCmd()
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	ctx = context.WithValue(ctx, container.MockControllerKey{},
		kukeonv1.Client(&fakeClient{listContainersFn: listFn, getContainerFn: getFn}))
	cmd.SetContext(ctx)
	cmd.SetArgs([]string{"-o", "wide"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	for _, h := range []string{"NAME", "REALM", "SPACE", "STACK", "CELL", "STATE", "RESTARTS", "AGE", "IMAGE", "EXIT"} {
		if !strings.Contains(out, h) {
			t.Errorf("-o wide table missing header %q\nGot:\n%s", h, out)
		}
	}
	for _, denied := range []string{"ROOT", "CGROUP"} {
		if strings.Contains(out, denied) {
			t.Errorf("-o wide table must NOT contain %q; got:\n%s", denied, out)
		}
	}
	for _, sub := range []string{"co1", "nginx:alpine", "137/SIGKILL"} {
		if !strings.Contains(out, sub) {
			t.Errorf("-o wide row missing %q\nGot:\n%s", sub, out)
		}
	}
}

// TestNewContainerCmd_ExitRendering pins the EXIT column edge cases from
// #605's AC: `<code>/<signal>` when either field is non-zero/non-empty;
// "-" when both are zero. Cases: both zero -> "-"; code only -> "139/";
// signal only -> "0/SIGTERM"; both -> "137/SIGKILL". Most meaningful on
// Stopped/Failed.
func TestNewContainerCmd_ExitRendering(t *testing.T) {
	t.Cleanup(viper.Reset)

	tests := []struct {
		name       string
		exitCode   int
		exitSignal string
		wantSub    string
	}{
		{"both zero renders dash", 0, "", " - "},
		{"code only", 139, "", "139/"},
		{"signal only", 0, "SIGTERM", "0/SIGTERM"},
		{"both set", 137, "SIGKILL", "137/SIGKILL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)

			listFn := func(_, _, _, _ string) ([]v1beta1.ContainerSpec, error) {
				return []v1beta1.ContainerSpec{
					{ID: "co1", RealmID: "r1", SpaceID: "s1", StackID: "st1", CellID: "ce1", Image: "img"},
				}, nil
			}
			getFn := func(_ v1beta1.ContainerDoc) (kukeonv1.GetContainerResult, error) {
				return kukeonv1.GetContainerResult{
					Container: v1beta1.ContainerDoc{
						Status: v1beta1.ContainerStatus{
							State:      v1beta1.ContainerStateStopped,
							ExitCode:   tt.exitCode,
							ExitSignal: tt.exitSignal,
						},
					},
					ContainerExists: true,
				}, nil
			}

			buf := &bytes.Buffer{}
			cmd := container.NewContainerCmd()
			cmd.SetOut(buf)
			cmd.SetErr(buf)
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
			ctx = context.WithValue(ctx, container.MockControllerKey{},
				kukeonv1.Client(&fakeClient{listContainersFn: listFn, getContainerFn: getFn}))
			cmd.SetContext(ctx)
			cmd.SetArgs([]string{"-o", "wide"})
			if err := cmd.Execute(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(buf.String(), tt.wantSub) {
				t.Errorf("EXIT cell missing %q\nGot:\n%s", tt.wantSub, buf.String())
			}
		})
	}
}

// TestNewContainerCmd_NameRendersRootForRootContainer pins the existing
// containerDisplayName behavior: a container with Spec.Root == true renders
// as "root" in the NAME column rather than its ID. Carried into #605's new
// column set unchanged.
func TestNewContainerCmd_NameRendersRootForRootContainer(t *testing.T) {
	t.Cleanup(viper.Reset)

	listFn := func(_, _, _, _ string) ([]v1beta1.ContainerSpec, error) {
		return []v1beta1.ContainerSpec{
			{ID: "rootco", RealmID: "r1", SpaceID: "s1", StackID: "st1", CellID: "ce1", Root: true, Image: "img"},
		}, nil
	}
	getFn := func(_ v1beta1.ContainerDoc) (kukeonv1.GetContainerResult, error) {
		return kukeonv1.GetContainerResult{
			Container:       v1beta1.ContainerDoc{Status: v1beta1.ContainerStatus{State: v1beta1.ContainerStateReady}},
			ContainerExists: true,
		}, nil
	}

	buf := &bytes.Buffer{}
	cmd := container.NewContainerCmd()
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	ctx = context.WithValue(ctx, container.MockControllerKey{},
		kukeonv1.Client(&fakeClient{listContainersFn: listFn, getContainerFn: getFn}))
	cmd.SetContext(ctx)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "root ") {
		t.Errorf("expected NAME to render 'root' for Spec.Root==true; got:\n%s", out)
	}
	if strings.Contains(out, "rootco ") {
		t.Errorf("NAME column should hide ID 'rootco' under Root override; got:\n%s", out)
	}
}

// TestNewContainerCmd_AgeZeroRendersDash pins that a container with a zero
// CreatedAt (not yet observed by the controller, or pre-#605 persisted
// state) renders AGE as "-" via shared.RenderAge rather than a stale value.
func TestNewContainerCmd_AgeZeroRendersDash(t *testing.T) {
	t.Cleanup(viper.Reset)

	listFn := func(_, _, _, _ string) ([]v1beta1.ContainerSpec, error) {
		return []v1beta1.ContainerSpec{
			{ID: "co1", RealmID: "r1", SpaceID: "s1", StackID: "st1", CellID: "ce1", Image: "img"},
		}, nil
	}
	getFn := func(_ v1beta1.ContainerDoc) (kukeonv1.GetContainerResult, error) {
		return kukeonv1.GetContainerResult{
			Container: v1beta1.ContainerDoc{
				Status: v1beta1.ContainerStatus{State: v1beta1.ContainerStateReady},
			},
			ContainerExists: true,
		}, nil
	}

	buf := &bytes.Buffer{}
	cmd := container.NewContainerCmd()
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	ctx = context.WithValue(ctx, container.MockControllerKey{},
		kukeonv1.Client(&fakeClient{listContainersFn: listFn, getContainerFn: getFn}))
	cmd.SetContext(ctx)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// AGE column should render "-" — a single dash bracketed by spaces.
	if !strings.Contains(buf.String(), " - ") {
		t.Errorf("expected zero CreatedAt to render AGE as '-'; got:\n%s", buf.String())
	}
}

// TestNewContainerCmd_Selector verifies the `-l`/`--selector` filter
// wiring on `kuke get container` (issue #614). ListContainers returns
// ContainerSpec (no labels), so the per-container Metadata.Labels arrives
// via the per-spec GetContainer probe that already populates state;
// filtering happens after that loop. Grammar coverage lives in the
// shared selector_test.go; this test pins the per-verb wiring.
func TestNewContainerCmd_Selector(t *testing.T) {
	t.Cleanup(viper.Reset)

	listFn := func(_, _, _, _ string) ([]v1beta1.ContainerSpec, error) {
		return []v1beta1.ContainerSpec{
			{ID: "alpha", RealmID: "r1", SpaceID: "s1", StackID: "st1", CellID: "c1"},
			{ID: "beta", RealmID: "r1", SpaceID: "s1", StackID: "st1", CellID: "c1"},
			{ID: "gamma", RealmID: "r1", SpaceID: "s1", StackID: "st1", CellID: "c1"},
		}, nil
	}
	// Labels live on ContainerDoc.Metadata, which arrives from GetContainer.
	labelsByID := map[string]map[string]string{
		"alpha": {"app": "web"},
		"beta":  {"app": "db"},
		"gamma": {"app": "web", "edge": "true"},
	}
	getFn := func(doc v1beta1.ContainerDoc) (kukeonv1.GetContainerResult, error) {
		labels, ok := labelsByID[doc.Metadata.Name]
		if !ok {
			return kukeonv1.GetContainerResult{}, errdefs.ErrContainerNotFound
		}
		return kukeonv1.GetContainerResult{
			Container: v1beta1.ContainerDoc{
				Metadata: v1beta1.ContainerMetadata{
					Name:   doc.Metadata.Name,
					Labels: labels,
				},
				Spec: doc.Spec,
				Status: v1beta1.ContainerStatus{
					State: v1beta1.ContainerStateReady,
				},
			},
			ContainerExists: true,
		}, nil
	}

	t.Run("equality filters by label", func(t *testing.T) {
		t.Cleanup(viper.Reset)
		cmd := container.NewContainerCmd()
		buf := &bytes.Buffer{}
		cmd.SetOut(buf)
		cmd.SetErr(buf)
		ctx := context.WithValue(context.Background(), container.MockControllerKey{},
			kukeonv1.Client(&fakeClient{listContainersFn: listFn, getContainerFn: getFn}))
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{"-l", "app=web"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		out := buf.String()
		for _, want := range []string{"alpha", "gamma"} {
			if !strings.Contains(out, want) {
				t.Errorf("expected %q in output, got:\n%s", want, out)
			}
		}
		if strings.Contains(out, "beta") {
			t.Errorf("expected 'beta' filtered out, got:\n%s", out)
		}
	})

	t.Run("AND-combination filters further", func(t *testing.T) {
		t.Cleanup(viper.Reset)
		cmd := container.NewContainerCmd()
		buf := &bytes.Buffer{}
		cmd.SetOut(buf)
		cmd.SetErr(buf)
		ctx := context.WithValue(context.Background(), container.MockControllerKey{},
			kukeonv1.Client(&fakeClient{listContainersFn: listFn, getContainerFn: getFn}))
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{"-l", "app=web,edge"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, "gamma") {
			t.Errorf("expected 'gamma' in output, got:\n%s", out)
		}
		for _, deny := range []string{"alpha", "beta"} {
			if strings.Contains(out, deny) {
				t.Errorf("expected %q filtered out, got:\n%s", deny, out)
			}
		}
	})

	t.Run("selector + name is rejected", func(t *testing.T) {
		t.Cleanup(viper.Reset)
		cmd := container.NewContainerCmd()
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		ctx := context.WithValue(context.Background(), container.MockControllerKey{},
			kukeonv1.Client(&fakeClient{}))
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{"alpha", "-l", "app=web"})
		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "--selector cannot be combined") {
			t.Fatalf("expected --selector + name rejection, got: %v", err)
		}
	})
}

type fakeClient struct {
	kukeonv1.FakeClient

	getContainerFn   func(doc v1beta1.ContainerDoc) (kukeonv1.GetContainerResult, error)
	listContainersFn func(realm, space, stack, cell string) ([]v1beta1.ContainerSpec, error)
}

func (f *fakeClient) GetContainer(_ context.Context, doc v1beta1.ContainerDoc) (kukeonv1.GetContainerResult, error) {
	if f.getContainerFn == nil {
		return kukeonv1.GetContainerResult{}, errors.New("unexpected GetContainer call")
	}
	return f.getContainerFn(doc)
}

func (f *fakeClient) ListContainers(
	_ context.Context,
	realm, space, stack, cell string,
) ([]v1beta1.ContainerSpec, error) {
	if f.listContainersFn == nil {
		return nil, errors.New("unexpected ListContainers call")
	}
	return f.listContainersFn(realm, space, stack, cell)
}
