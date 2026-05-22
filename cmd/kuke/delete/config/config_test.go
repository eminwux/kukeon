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
	"io"
	"log/slog"
	"strings"
	"testing"

	configcmd "github.com/eminwux/kukeon/cmd/kuke/delete/config"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestDeleteConfig(t *testing.T) {
	t.Cleanup(viper.Reset)

	tests := []struct {
		name        string
		args        []string
		setup       func(t *testing.T, cmd *cobra.Command)
		fake        *fakeClient
		wantErr     string
		wantScope   v1beta1.CellConfigMetadata
		wantOutputs []string
	}{
		{
			name: "success at realm scope",
			args: []string{"web"},
			setup: func(_ *testing.T, cmd *cobra.Command) {
				_ = cmd.Flags().Set("realm", "r1")
			},
			fake: &fakeClient{
				deleteConfigFn: func(doc v1beta1.CellConfigDoc) (kukeonv1.DeleteConfigResult, error) {
					return kukeonv1.DeleteConfigResult{Config: doc, Deleted: true}, nil
				},
			},
			wantScope:   v1beta1.CellConfigMetadata{Name: "web", Realm: "r1"},
			wantOutputs: []string{`Deleted config "web"`},
		},
		{
			name: "success at stack scope passes full coordinates",
			args: []string{"stack-cfg"},
			setup: func(_ *testing.T, cmd *cobra.Command) {
				_ = cmd.Flags().Set("realm", "r1")
				_ = cmd.Flags().Set("space", "s1")
				_ = cmd.Flags().Set("stack", "st1")
			},
			fake: &fakeClient{
				deleteConfigFn: func(doc v1beta1.CellConfigDoc) (kukeonv1.DeleteConfigResult, error) {
					return kukeonv1.DeleteConfigResult{Config: doc, Deleted: true}, nil
				},
			},
			wantScope:   v1beta1.CellConfigMetadata{Name: "stack-cfg", Realm: "r1", Space: "s1", Stack: "st1"},
			wantOutputs: []string{`Deleted config "stack-cfg"`},
		},
		{
			name: "back-ref cell emits notice not refusal",
			args: []string{"web"},
			setup: func(_ *testing.T, cmd *cobra.Command) {
				_ = cmd.Flags().Set("realm", "r1")
			},
			fake: &fakeClient{
				deleteConfigFn: func(doc v1beta1.CellConfigDoc) (kukeonv1.DeleteConfigResult, error) {
					return kukeonv1.DeleteConfigResult{
						Config:       doc,
						Deleted:      true,
						BackRefCells: []string{"r1/s1/st1/web"},
					}, nil
				},
			},
			wantScope: v1beta1.CellConfigMetadata{Name: "web", Realm: "r1"},
			wantOutputs: []string{
				`Deleted config "web"`,
				`r1/s1/st1/web`,
				`kuke delete cell web`,
			},
		},
		{
			name: "not found surfaces error",
			args: []string{"missing"},
			setup: func(_ *testing.T, cmd *cobra.Command) {
				_ = cmd.Flags().Set("realm", "r1")
			},
			fake: &fakeClient{
				deleteConfigFn: func(_ v1beta1.CellConfigDoc) (kukeonv1.DeleteConfigResult, error) {
					return kukeonv1.DeleteConfigResult{}, errdefs.ErrConfigNotFound
				},
			},
			wantErr: "config not found",
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
			for _, want := range tt.wantOutputs {
				if !strings.Contains(buf.String(), want) {
					t.Errorf("output missing %q\nGot:\n%s", want, buf.String())
				}
			}
		})
	}
}

type fakeClient struct {
	kukeonv1.FakeClient

	deleteConfigFn func(doc v1beta1.CellConfigDoc) (kukeonv1.DeleteConfigResult, error)
	gotDoc         v1beta1.CellConfigDoc
}

func (f *fakeClient) DeleteConfig(
	_ context.Context, doc v1beta1.CellConfigDoc,
) (kukeonv1.DeleteConfigResult, error) {
	f.gotDoc = doc
	if f.deleteConfigFn == nil {
		return kukeonv1.DeleteConfigResult{}, errors.New("unexpected DeleteConfig call")
	}
	return f.deleteConfigFn(doc)
}
