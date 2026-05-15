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
