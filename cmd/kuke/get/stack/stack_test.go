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

package stack_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	stack "github.com/eminwux/kukeon/cmd/kuke/get/stack"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestNewStackCmd(t *testing.T) {
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
			name: "get single stack",
			args: []string{"st1"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				_ = cmd.Flags().Set("realm", "r1")
				_ = cmd.Flags().Set("space", "s1")
			},
			fake: &fakeClient{
				getStackFn: func(_ v1beta1.StackDoc) (kukeonv1.GetStackResult, error) {
					return kukeonv1.GetStackResult{
						Stack: v1beta1.StackDoc{
							Metadata: v1beta1.StackMetadata{Name: "st1"},
							Spec:     v1beta1.StackSpec{RealmID: "r1", SpaceID: "s1"},
						},
						MetadataExists: true,
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
			},
			fake: &fakeClient{
				getStackFn: func(_ v1beta1.StackDoc) (kukeonv1.GetStackResult, error) {
					return kukeonv1.GetStackResult{}, errdefs.ErrStackNotFound
				},
			},
			wantErr: `stack "missing" not found`,
		},
		{
			name: "list stacks",
			fake: &fakeClient{
				listStacksFn: func(_, _ string) ([]v1beta1.StackDoc, error) {
					return []v1beta1.StackDoc{{Metadata: v1beta1.StackMetadata{Name: "st1"}}}, nil
				},
			},
			wantOutput: "st1",
		},
		{
			name: "list empty",
			fake: &fakeClient{
				listStacksFn: func(_, _ string) ([]v1beta1.StackDoc, error) { return nil, nil },
			},
			wantOutput: "No stacks found.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)

			cmd := stack.NewStackCmd()
			buf := &bytes.Buffer{}
			cmd.SetOut(buf)
			cmd.SetErr(buf)

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
			ctx = context.WithValue(ctx, stack.MockControllerKey{}, kukeonv1.Client(tt.fake))
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

	getStackFn   func(doc v1beta1.StackDoc) (kukeonv1.GetStackResult, error)
	listStacksFn func(realm, space string) ([]v1beta1.StackDoc, error)
}

func (f *fakeClient) GetStack(_ context.Context, doc v1beta1.StackDoc) (kukeonv1.GetStackResult, error) {
	if f.getStackFn == nil {
		return kukeonv1.GetStackResult{}, errors.New("unexpected GetStack call")
	}
	return f.getStackFn(doc)
}

func (f *fakeClient) ListStacks(_ context.Context, realm, space string) ([]v1beta1.StackDoc, error) {
	if f.listStacksFn == nil {
		return nil, errors.New("unexpected ListStacks call")
	}
	return f.listStacksFn(realm, space)
}
