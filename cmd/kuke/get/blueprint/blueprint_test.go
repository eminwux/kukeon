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

package blueprint_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	blueprint "github.com/eminwux/kukeon/cmd/kuke/get/blueprint"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestNewBlueprintCmd(t *testing.T) {
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
			name: "get single blueprint",
			args: []string{"web"},
			setup: func(_ *testing.T, cmd *cobra.Command) {
				_ = cmd.Flags().Set("realm", "r1")
				_ = cmd.Flags().Set("space", "s1")
			},
			fake: &fakeClient{
				getBlueprintFn: func(doc v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
					return kukeonv1.GetBlueprintResult{
						Blueprint:      v1beta1.CellBlueprintDoc{Metadata: doc.Metadata},
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
				getBlueprintFn: func(_ v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
					return kukeonv1.GetBlueprintResult{}, errdefs.ErrBlueprintNotFound
				},
			},
			wantErr: `blueprint "missing" not found`,
		},
		{
			name: "get not found via MetadataExists=false",
			args: []string{"missing"},
			setup: func(_ *testing.T, cmd *cobra.Command) {
				_ = cmd.Flags().Set("realm", "r1")
			},
			fake: &fakeClient{
				getBlueprintFn: func(_ v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
					return kukeonv1.GetBlueprintResult{MetadataExists: false}, nil
				},
			},
			wantErr: `blueprint "missing" not found`,
		},
		{
			name: "list empty",
			fake: &fakeClient{
				listBlueprintsFn: func(_, _, _ string) ([]v1beta1.CellBlueprintDoc, error) {
					return nil, nil
				},
			},
			wantOutput: "No blueprints found.",
		},
		{
			name: "list table renders scope columns",
			fake: &fakeClient{
				listBlueprintsFn: func(_, _, _ string) ([]v1beta1.CellBlueprintDoc, error) {
					return []v1beta1.CellBlueprintDoc{
						{Metadata: v1beta1.CellBlueprintMetadata{Name: "realm-bp", Realm: "default"}},
						{
							Metadata: v1beta1.CellBlueprintMetadata{
								Name:  "stack-bp",
								Realm: "default",
								Space: "s",
								Stack: "st",
							},
						},
					}, nil
				},
			},
			wantOutput: "realm-bp",
		},
		{
			name: "list passes filter scope through (no cell coordinate)",
			setup: func(_ *testing.T, cmd *cobra.Command) {
				_ = cmd.Flags().Set("realm", "default")
				_ = cmd.Flags().Set("space", "team-a")
			},
			fake: &fakeClient{
				listBlueprintsFn: func(realm, space, _ string) ([]v1beta1.CellBlueprintDoc, error) {
					if realm != "default" || space != "team-a" {
						return nil, errors.New("unexpected filter scope")
					}
					return []v1beta1.CellBlueprintDoc{
						{Metadata: v1beta1.CellBlueprintMetadata{Name: "space-bp", Realm: realm, Space: space}},
					}, nil
				},
			},
			wantOutput: "space-bp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)

			cmd := blueprint.NewBlueprintCmd()
			buf := &bytes.Buffer{}
			cmd.SetOut(buf)
			cmd.SetErr(buf)

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
			ctx = context.WithValue(ctx, blueprint.MockControllerKey{}, kukeonv1.Client(tt.fake))
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

type fakeClient struct {
	kukeonv1.FakeClient

	getBlueprintFn   func(doc v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error)
	listBlueprintsFn func(realm, space, stack string) ([]v1beta1.CellBlueprintDoc, error)
}

func (f *fakeClient) GetBlueprint(
	_ context.Context, doc v1beta1.CellBlueprintDoc,
) (kukeonv1.GetBlueprintResult, error) {
	if f.getBlueprintFn == nil {
		return kukeonv1.GetBlueprintResult{}, errors.New("unexpected GetBlueprint call")
	}
	return f.getBlueprintFn(doc)
}

func (f *fakeClient) ListBlueprints(
	_ context.Context,
	realm, space, stack string,
) ([]v1beta1.CellBlueprintDoc, error) {
	if f.listBlueprintsFn == nil {
		return nil, errors.New("unexpected ListBlueprints call")
	}
	return f.listBlueprintsFn(realm, space, stack)
}
