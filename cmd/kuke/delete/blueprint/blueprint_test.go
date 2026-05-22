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

	blueprint "github.com/eminwux/kukeon/cmd/kuke/delete/blueprint"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestDeleteBlueprint(t *testing.T) {
	t.Cleanup(viper.Reset)

	tests := []struct {
		name       string
		args       []string
		setup      func(t *testing.T, cmd *cobra.Command)
		fake       *fakeClient
		wantErr    string
		wantScope  v1beta1.CellBlueprintMetadata
		wantOutput string
	}{
		{
			name: "success at realm scope",
			args: []string{"web"},
			setup: func(_ *testing.T, cmd *cobra.Command) {
				_ = cmd.Flags().Set("realm", "r1")
			},
			fake: &fakeClient{
				deleteBlueprintFn: func(doc v1beta1.CellBlueprintDoc) (kukeonv1.DeleteBlueprintResult, error) {
					return kukeonv1.DeleteBlueprintResult{Blueprint: doc, Deleted: true}, nil
				},
			},
			wantScope:  v1beta1.CellBlueprintMetadata{Name: "web", Realm: "r1"},
			wantOutput: `Deleted blueprint "web"`,
		},
		{
			name: "success at stack scope passes full coordinates",
			args: []string{"stack-bp"},
			setup: func(_ *testing.T, cmd *cobra.Command) {
				_ = cmd.Flags().Set("realm", "r1")
				_ = cmd.Flags().Set("space", "s1")
				_ = cmd.Flags().Set("stack", "st1")
			},
			fake: &fakeClient{
				deleteBlueprintFn: func(doc v1beta1.CellBlueprintDoc) (kukeonv1.DeleteBlueprintResult, error) {
					return kukeonv1.DeleteBlueprintResult{Blueprint: doc, Deleted: true}, nil
				},
			},
			wantScope:  v1beta1.CellBlueprintMetadata{Name: "stack-bp", Realm: "r1", Space: "s1", Stack: "st1"},
			wantOutput: `Deleted blueprint "stack-bp"`,
		},
		{
			name: "not found surfaces error",
			args: []string{"missing"},
			setup: func(_ *testing.T, cmd *cobra.Command) {
				_ = cmd.Flags().Set("realm", "r1")
			},
			fake: &fakeClient{
				deleteBlueprintFn: func(_ v1beta1.CellBlueprintDoc) (kukeonv1.DeleteBlueprintResult, error) {
					return kukeonv1.DeleteBlueprintResult{}, errdefs.ErrBlueprintNotFound
				},
			},
			wantErr: "blueprint not found",
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
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("want err %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := tt.fake.gotDoc.Metadata; got.Name != tt.wantScope.Name ||
				got.Realm != tt.wantScope.Realm ||
				got.Space != tt.wantScope.Space ||
				got.Stack != tt.wantScope.Stack {
				t.Errorf("scope passed = %+v, want %+v", got, tt.wantScope)
			}
			if tt.wantOutput != "" && !strings.Contains(buf.String(), tt.wantOutput) {
				t.Errorf("output missing %q\nGot:\n%s", tt.wantOutput, buf.String())
			}
		})
	}
}

type fakeClient struct {
	kukeonv1.FakeClient

	deleteBlueprintFn func(doc v1beta1.CellBlueprintDoc) (kukeonv1.DeleteBlueprintResult, error)
	gotDoc            v1beta1.CellBlueprintDoc
}

func (f *fakeClient) DeleteBlueprint(
	_ context.Context, doc v1beta1.CellBlueprintDoc,
) (kukeonv1.DeleteBlueprintResult, error) {
	f.gotDoc = doc
	if f.deleteBlueprintFn == nil {
		return kukeonv1.DeleteBlueprintResult{}, errors.New("unexpected DeleteBlueprint call")
	}
	return f.deleteBlueprintFn(doc)
}
