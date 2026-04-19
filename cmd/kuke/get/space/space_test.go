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
