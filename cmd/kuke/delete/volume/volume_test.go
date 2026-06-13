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

package volume_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	volume "github.com/eminwux/kukeon/cmd/kuke/delete/volume"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestDeleteVolume(t *testing.T) {
	t.Cleanup(viper.Reset)

	tests := []struct {
		name       string
		args       []string
		setup      func(t *testing.T, cmd *cobra.Command)
		fake       *fakeClient
		wantErr    string
		wantScope  v1beta1.VolumeMetadata
		wantOutput string
	}{
		{
			name: "success at realm scope",
			args: []string{"vol1"},
			setup: func(_ *testing.T, cmd *cobra.Command) {
				_ = cmd.Flags().Set("realm", "r1")
			},
			fake: &fakeClient{
				deleteVolumeFn: func(doc v1beta1.VolumeDoc) (kukeonv1.DeleteVolumeResult, error) {
					return kukeonv1.DeleteVolumeResult{Volume: doc, Deleted: true}, nil
				},
			},
			wantScope:  v1beta1.VolumeMetadata{Name: "vol1", Realm: "r1"},
			wantOutput: `Deleted volume "vol1"`,
		},
		{
			name: "success at stack scope passes full coordinates",
			args: []string{"stack-vol"},
			setup: func(_ *testing.T, cmd *cobra.Command) {
				_ = cmd.Flags().Set("realm", "r1")
				_ = cmd.Flags().Set("space", "s1")
				_ = cmd.Flags().Set("stack", "st1")
			},
			fake: &fakeClient{
				deleteVolumeFn: func(doc v1beta1.VolumeDoc) (kukeonv1.DeleteVolumeResult, error) {
					return kukeonv1.DeleteVolumeResult{Volume: doc, Deleted: true}, nil
				},
			},
			wantScope:  v1beta1.VolumeMetadata{Name: "stack-vol", Realm: "r1", Space: "s1", Stack: "st1"},
			wantOutput: `Deleted volume "stack-vol"`,
		},
		{
			name: "not found surfaces error",
			args: []string{"missing"},
			setup: func(_ *testing.T, cmd *cobra.Command) {
				_ = cmd.Flags().Set("realm", "r1")
			},
			fake: &fakeClient{
				deleteVolumeFn: func(_ v1beta1.VolumeDoc) (kukeonv1.DeleteVolumeResult, error) {
					return kukeonv1.DeleteVolumeResult{}, errdefs.ErrVolumeNotFound
				},
			},
			wantErr: "volume not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)

			cmd := volume.NewVolumeCmd()
			buf := &bytes.Buffer{}
			cmd.SetOut(buf)
			cmd.SetErr(buf)

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
			ctx = context.WithValue(ctx, volume.MockControllerKey{}, kukeonv1.Client(tt.fake))
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
			if tt.wantScope != (v1beta1.VolumeMetadata{}) && tt.fake.gotDoc.Metadata != tt.wantScope {
				t.Errorf("scope passed = %+v, want %+v", tt.fake.gotDoc.Metadata, tt.wantScope)
			}
			if tt.wantOutput != "" && !strings.Contains(buf.String(), tt.wantOutput) {
				t.Errorf("output missing %q\nGot:\n%s", tt.wantOutput, buf.String())
			}
		})
	}
}

// TestDeleteVolume_NoCellFlag confirms the verb does not expose a --cell flag:
// a Volume is never cell-scoped (issue #1018/#1236).
func TestDeleteVolume_NoCellFlag(t *testing.T) {
	cmd := volume.NewVolumeCmd()
	if cmd.Flags().Lookup("cell") != nil {
		t.Error("delete volume should not expose a --cell flag (volumes are never cell-scoped)")
	}
}

type fakeClient struct {
	kukeonv1.FakeClient

	deleteVolumeFn func(doc v1beta1.VolumeDoc) (kukeonv1.DeleteVolumeResult, error)
	gotDoc         v1beta1.VolumeDoc
}

func (f *fakeClient) DeleteVolume(_ context.Context, doc v1beta1.VolumeDoc) (kukeonv1.DeleteVolumeResult, error) {
	f.gotDoc = doc
	if f.deleteVolumeFn == nil {
		return kukeonv1.DeleteVolumeResult{}, errors.New("unexpected DeleteVolume call")
	}
	return f.deleteVolumeFn(doc)
}
