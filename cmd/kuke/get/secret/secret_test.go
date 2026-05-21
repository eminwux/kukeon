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
