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

package realm_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	realm "github.com/eminwux/kukeon/cmd/kuke/get/realm"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/viper"
)

func TestNewRealmCmd(t *testing.T) {
	t.Cleanup(viper.Reset)

	tests := []struct {
		name       string
		args       []string
		fake       *fakeClient
		wantErr    string
		wantOutput string
	}{
		{
			name: "get single realm succeeds",
			args: []string{"r1"},
			fake: &fakeClient{
				getRealmFn: func(_ v1beta1.RealmDoc) (kukeonv1.GetRealmResult, error) {
					return kukeonv1.GetRealmResult{
						Realm: v1beta1.RealmDoc{
							APIVersion: v1beta1.APIVersionV1Beta1,
							Kind:       v1beta1.KindRealm,
							Metadata:   v1beta1.RealmMetadata{Name: "r1"},
							Spec:       v1beta1.RealmSpec{Namespace: "ns1"},
						},
						MetadataExists: true,
					}, nil
				},
			},
		},
		{
			name: "get missing realm returns not found",
			args: []string{"missing"},
			fake: &fakeClient{
				getRealmFn: func(_ v1beta1.RealmDoc) (kukeonv1.GetRealmResult, error) {
					return kukeonv1.GetRealmResult{}, errdefs.ErrRealmNotFound
				},
			},
			wantErr: `realm "missing" not found`,
		},
		{
			name: "get with metadataExists=false returns not found",
			args: []string{"ghost"},
			fake: &fakeClient{
				getRealmFn: func(_ v1beta1.RealmDoc) (kukeonv1.GetRealmResult, error) {
					return kukeonv1.GetRealmResult{}, nil
				},
			},
			wantErr: `realm "ghost" not found`,
		},
		{
			name: "list realms",
			fake: &fakeClient{
				listRealmsFn: func() ([]v1beta1.RealmDoc, error) {
					return []v1beta1.RealmDoc{
						{Metadata: v1beta1.RealmMetadata{Name: "r1"}, Spec: v1beta1.RealmSpec{Namespace: "ns1"}},
						{Metadata: v1beta1.RealmMetadata{Name: "r2"}, Spec: v1beta1.RealmSpec{Namespace: "ns2"}},
					}, nil
				},
			},
			wantOutput: "r1",
		},
		{
			name: "list empty",
			fake: &fakeClient{
				listRealmsFn: func() ([]v1beta1.RealmDoc, error) {
					return nil, nil
				},
			},
			wantOutput: "No realms found.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)

			cmd := realm.NewRealmCmd()
			buf := &bytes.Buffer{}
			cmd.SetOut(buf)
			cmd.SetErr(buf)

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
			ctx = context.WithValue(ctx, realm.MockControllerKey{}, kukeonv1.Client(tt.fake))
			cmd.SetContext(ctx)

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

func TestNewRealmCmd_Structure(t *testing.T) {
	cmd := realm.NewRealmCmd()
	if cmd.Use != "realm [name]" {
		t.Errorf("Use=%q want realm [name]", cmd.Use)
	}
	if cmd.Flags().Lookup("output") == nil {
		t.Fatal("expected output flag")
	}
}

type fakeClient struct {
	kukeonv1.FakeClient

	getRealmFn   func(doc v1beta1.RealmDoc) (kukeonv1.GetRealmResult, error)
	listRealmsFn func() ([]v1beta1.RealmDoc, error)
}

func (f *fakeClient) GetRealm(_ context.Context, doc v1beta1.RealmDoc) (kukeonv1.GetRealmResult, error) {
	if f.getRealmFn == nil {
		return kukeonv1.GetRealmResult{}, errors.New("unexpected GetRealm call")
	}
	return f.getRealmFn(doc)
}

func (f *fakeClient) ListRealms(_ context.Context) ([]v1beta1.RealmDoc, error) {
	if f.listRealmsFn == nil {
		return nil, errors.New("unexpected ListRealms call")
	}
	return f.listRealmsFn()
}
