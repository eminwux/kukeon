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
		name       string
		args       []string
		setup      func(t *testing.T, cmd *cobra.Command)
		fake       *fakeClient
		wantErr    string
		wantOutput string
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
		})
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
