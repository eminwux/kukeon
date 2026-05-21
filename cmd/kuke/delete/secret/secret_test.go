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

	secret "github.com/eminwux/kukeon/cmd/kuke/delete/secret"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestDeleteSecret(t *testing.T) {
	t.Cleanup(viper.Reset)

	tests := []struct {
		name       string
		args       []string
		setup      func(t *testing.T, cmd *cobra.Command)
		fake       *fakeClient
		wantErr    string
		wantScope  v1beta1.SecretMetadata
		wantOutput string
	}{
		{
			name: "success at realm scope",
			args: []string{"tok"},
			setup: func(_ *testing.T, cmd *cobra.Command) {
				_ = cmd.Flags().Set("realm", "r1")
			},
			fake: &fakeClient{
				deleteSecretFn: func(doc v1beta1.SecretDoc) (kukeonv1.DeleteSecretResult, error) {
					return kukeonv1.DeleteSecretResult{Secret: doc, Deleted: true}, nil
				},
			},
			wantScope:  v1beta1.SecretMetadata{Name: "tok", Realm: "r1"},
			wantOutput: `Deleted secret "tok"`,
		},
		{
			name: "success at cell scope passes full coordinates",
			args: []string{"cell-tok"},
			setup: func(_ *testing.T, cmd *cobra.Command) {
				_ = cmd.Flags().Set("realm", "r1")
				_ = cmd.Flags().Set("space", "s1")
				_ = cmd.Flags().Set("stack", "st1")
				_ = cmd.Flags().Set("cell", "ce1")
			},
			fake: &fakeClient{
				deleteSecretFn: func(doc v1beta1.SecretDoc) (kukeonv1.DeleteSecretResult, error) {
					return kukeonv1.DeleteSecretResult{Secret: doc, Deleted: true}, nil
				},
			},
			wantScope:  v1beta1.SecretMetadata{Name: "cell-tok", Realm: "r1", Space: "s1", Stack: "st1", Cell: "ce1"},
			wantOutput: `Deleted secret "cell-tok"`,
		},
		{
			name: "not found surfaces error",
			args: []string{"missing"},
			setup: func(_ *testing.T, cmd *cobra.Command) {
				_ = cmd.Flags().Set("realm", "r1")
			},
			fake: &fakeClient{
				deleteSecretFn: func(_ v1beta1.SecretDoc) (kukeonv1.DeleteSecretResult, error) {
					return kukeonv1.DeleteSecretResult{}, errdefs.ErrSecretNotFound
				},
			},
			wantErr: "secret not found",
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
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("want err %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantScope != (v1beta1.SecretMetadata{}) && tt.fake.gotDoc.Metadata != tt.wantScope {
				t.Errorf("scope passed = %+v, want %+v", tt.fake.gotDoc.Metadata, tt.wantScope)
			}
			if tt.wantOutput != "" && !strings.Contains(buf.String(), tt.wantOutput) {
				t.Errorf("output missing %q\nGot:\n%s", tt.wantOutput, buf.String())
			}
		})
	}
}

// TestDeleteSecret_HelpDocumentsUnsafeWindow confirms the verb's long help
// names the temporary unconditional-delete window the AC of issue #622 calls
// out, so the operator sees the phase-3c caveat.
func TestDeleteSecret_HelpDocumentsUnsafeWindow(t *testing.T) {
	cmd := secret.NewSecretCmd()
	if !strings.Contains(cmd.Long, "phase 3c") {
		t.Errorf("delete secret long help does not document the phase-3c unsafe-delete window\nGot:\n%s", cmd.Long)
	}
}

type fakeClient struct {
	kukeonv1.FakeClient

	deleteSecretFn func(doc v1beta1.SecretDoc) (kukeonv1.DeleteSecretResult, error)
	gotDoc         v1beta1.SecretDoc
}

func (f *fakeClient) DeleteSecret(_ context.Context, doc v1beta1.SecretDoc) (kukeonv1.DeleteSecretResult, error) {
	f.gotDoc = doc
	if f.deleteSecretFn == nil {
		return kukeonv1.DeleteSecretResult{}, errors.New("unexpected DeleteSecret call")
	}
	return f.deleteSecretFn(doc)
}
