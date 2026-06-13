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

	volume "github.com/eminwux/kukeon/cmd/kuke/get/volume"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestNewVolumeCmd(t *testing.T) {
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
			// Single-resource get renders YAML to os.Stdout (not the command
			// buffer), so this case only asserts the happy path returns no error.
			name: "get single volume metadata",
			args: []string{"vol1"},
			setup: func(_ *testing.T, cmd *cobra.Command) {
				_ = cmd.Flags().Set("realm", "r1")
				_ = cmd.Flags().Set("space", "s1")
			},
			fake: &fakeClient{
				getVolumeFn: func(doc v1beta1.VolumeDoc) (kukeonv1.GetVolumeResult, error) {
					return kukeonv1.GetVolumeResult{
						Volume:         v1beta1.VolumeDoc{Metadata: doc.Metadata},
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
				getVolumeFn: func(_ v1beta1.VolumeDoc) (kukeonv1.GetVolumeResult, error) {
					return kukeonv1.GetVolumeResult{}, errdefs.ErrVolumeNotFound
				},
			},
			wantErr: `volume "missing" not found`,
		},
		{
			name: "get not found via MetadataExists=false",
			args: []string{"missing"},
			setup: func(_ *testing.T, cmd *cobra.Command) {
				_ = cmd.Flags().Set("realm", "r1")
			},
			fake: &fakeClient{
				getVolumeFn: func(_ v1beta1.VolumeDoc) (kukeonv1.GetVolumeResult, error) {
					return kukeonv1.GetVolumeResult{MetadataExists: false}, nil
				},
			},
			wantErr: `volume "missing" not found`,
		},
		{
			name: "list empty prints no resources found",
			fake: &fakeClient{
				listVolumesFn: func(_, _, _ string) ([]v1beta1.VolumeDoc, error) {
					return nil, nil
				},
			},
			wantOutput: "No volumes found.",
		},
		{
			name: "list table renders scope columns",
			fake: &fakeClient{
				listVolumesFn: func(_, _, _ string) ([]v1beta1.VolumeDoc, error) {
					return []v1beta1.VolumeDoc{
						{Metadata: v1beta1.VolumeMetadata{Name: "realm-vol", Realm: "default"}},
						{
							Metadata: v1beta1.VolumeMetadata{
								Name:  "stack-vol",
								Realm: "default",
								Space: "s",
								Stack: "st",
							},
						},
					}, nil
				},
			},
			wantOutput: "realm-vol",
		},
		{
			name: "list passes filter scope through",
			setup: func(_ *testing.T, cmd *cobra.Command) {
				_ = cmd.Flags().Set("realm", "default")
				_ = cmd.Flags().Set("space", "team-a")
			},
			fake: &fakeClient{
				listVolumesFn: func(realm, space, _ string) ([]v1beta1.VolumeDoc, error) {
					if realm != "default" || space != "team-a" {
						return nil, errors.New("unexpected filter scope")
					}
					return []v1beta1.VolumeDoc{
						{Metadata: v1beta1.VolumeMetadata{Name: "space-vol", Realm: realm, Space: space}},
					}, nil
				},
			},
			wantOutput: "space-vol",
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

	getVolumeFn   func(doc v1beta1.VolumeDoc) (kukeonv1.GetVolumeResult, error)
	listVolumesFn func(realm, space, stack string) ([]v1beta1.VolumeDoc, error)
}

func (f *fakeClient) GetVolume(_ context.Context, doc v1beta1.VolumeDoc) (kukeonv1.GetVolumeResult, error) {
	if f.getVolumeFn == nil {
		return kukeonv1.GetVolumeResult{}, errors.New("unexpected GetVolume call")
	}
	return f.getVolumeFn(doc)
}

func (f *fakeClient) ListVolumes(
	_ context.Context,
	realm, space, stack string,
) ([]v1beta1.VolumeDoc, error) {
	if f.listVolumesFn == nil {
		return nil, errors.New("unexpected ListVolumes call")
	}
	return f.listVolumesFn(realm, space, stack)
}
