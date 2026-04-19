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

	"github.com/eminwux/kukeon/cmd/config"
	container "github.com/eminwux/kukeon/cmd/kuke/delete/container"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/viper"
)

func TestDeleteContainer(t *testing.T) {
	t.Cleanup(viper.Reset)

	tests := []struct {
		name       string
		args       []string
		setup      func()
		fake       *fakeClient
		wantErr    string
		wantOutput string
	}{
		{
			name: "success",
			args: []string{"co1"},
			setup: func() {
				viper.Set(config.KUKE_DELETE_CONTAINER_REALM.ViperKey, "r1")
				viper.Set(config.KUKE_DELETE_CONTAINER_SPACE.ViperKey, "s1")
				viper.Set(config.KUKE_DELETE_CONTAINER_STACK.ViperKey, "st1")
				viper.Set(config.KUKE_DELETE_CONTAINER_CELL.ViperKey, "ce1")
			},
			fake: &fakeClient{
				deleteContainerFn: func(doc v1beta1.ContainerDoc) (kukeonv1.DeleteContainerResult, error) {
					return kukeonv1.DeleteContainerResult{Container: doc, ContainerExists: true}, nil
				},
			},
			wantOutput: `Deleted container "co1" from cell "ce1"`,
		},
		{
			name: "missing cell",
			args: []string{"co1"},
			setup: func() {
				viper.Set(config.KUKE_DELETE_CONTAINER_REALM.ViperKey, "r1")
				viper.Set(config.KUKE_DELETE_CONTAINER_SPACE.ViperKey, "s1")
				viper.Set(config.KUKE_DELETE_CONTAINER_STACK.ViperKey, "st1")
			},
			fake:    &fakeClient{},
			wantErr: "cell name is required",
		},
		{
			name: "not found",
			args: []string{"missing"},
			setup: func() {
				viper.Set(config.KUKE_DELETE_CONTAINER_REALM.ViperKey, "r1")
				viper.Set(config.KUKE_DELETE_CONTAINER_SPACE.ViperKey, "s1")
				viper.Set(config.KUKE_DELETE_CONTAINER_STACK.ViperKey, "st1")
				viper.Set(config.KUKE_DELETE_CONTAINER_CELL.ViperKey, "ce1")
			},
			fake: &fakeClient{
				deleteContainerFn: func(_ v1beta1.ContainerDoc) (kukeonv1.DeleteContainerResult, error) {
					return kukeonv1.DeleteContainerResult{}, errdefs.ErrContainerNotFound
				},
			},
			wantErr: "container not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)
			viper.Reset()
			if tt.setup != nil {
				tt.setup()
			}
			cmd := container.NewContainerCmd()
			buf := &bytes.Buffer{}
			cmd.SetOut(buf)
			cmd.SetErr(buf)
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
			ctx = context.WithValue(ctx, container.MockControllerKey{}, kukeonv1.Client(tt.fake))
			cmd.SetContext(ctx)
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
			if tt.wantOutput != "" && !strings.Contains(buf.String(), tt.wantOutput) {
				t.Errorf("output missing %q\nGot:\n%s", tt.wantOutput, buf.String())
			}
		})
	}
}

type fakeClient struct {
	kukeonv1.FakeClient

	deleteContainerFn func(doc v1beta1.ContainerDoc) (kukeonv1.DeleteContainerResult, error)
}

func (f *fakeClient) DeleteContainer(
	_ context.Context,
	doc v1beta1.ContainerDoc,
) (kukeonv1.DeleteContainerResult, error) {
	if f.deleteContainerFn == nil {
		return kukeonv1.DeleteContainerResult{}, errors.New("unexpected DeleteContainer call")
	}
	return f.deleteContainerFn(doc)
}
