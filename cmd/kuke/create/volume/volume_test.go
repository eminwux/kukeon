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

	"github.com/eminwux/kukeon/cmd/kuke/create/volume"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/connector"
	"github.com/spf13/viper"
)

type fakeClient struct {
	kukeonv1.FakeClient

	createVolumeFn func(doc v1beta1.VolumeDoc) (kukeonv1.CreateVolumeResult, error)
}

func (f *fakeClient) CreateVolume(_ context.Context, doc v1beta1.VolumeDoc) (kukeonv1.CreateVolumeResult, error) {
	if f.createVolumeFn == nil {
		return kukeonv1.CreateVolumeResult{}, errors.New("unexpected CreateVolume call")
	}
	return f.createVolumeFn(doc)
}

func TestNewVolumeCmdRunE(t *testing.T) {
	t.Cleanup(viper.Reset)

	tests := []struct {
		name     string
		args     []string
		clientFn func(doc v1beta1.VolumeDoc) (kukeonv1.CreateVolumeResult, error)
		wantErr  string
		wantOut  string
	}{
		{
			name: "happy path at default realm",
			args: []string{"myvol"},
			clientFn: func(doc v1beta1.VolumeDoc) (kukeonv1.CreateVolumeResult, error) {
				if doc.Metadata.Name != "myvol" {
					t.Errorf("unexpected name: %q", doc.Metadata.Name)
				}
				if doc.Metadata.Realm != "default" {
					t.Errorf("unexpected realm: %q", doc.Metadata.Realm)
				}
				if doc.Kind != v1beta1.KindVolume {
					t.Errorf("unexpected kind: %q", doc.Kind)
				}
				return kukeonv1.CreateVolumeResult{Volume: doc, Created: true}, nil
			},
			wantOut: `Volume "myvol" (realm "default", space "", stack "")`,
		},
		{
			name: "scope flags threaded through",
			args: []string{"scoped", "--realm=myrealm", "--space=myspace", "--stack=mystack"},
			clientFn: func(doc v1beta1.VolumeDoc) (kukeonv1.CreateVolumeResult, error) {
				if doc.Metadata.Realm != "myrealm" || doc.Metadata.Space != "myspace" ||
					doc.Metadata.Stack != "mystack" {
					t.Errorf("unexpected scope: %+v", doc.Metadata)
				}
				return kukeonv1.CreateVolumeResult{Volume: doc, Created: true}, nil
			},
			wantOut: `Volume "scoped" (realm "myrealm", space "myspace", stack "mystack")`,
		},
		{
			name: "re-create reports already existed",
			args: []string{"existing"},
			clientFn: func(doc v1beta1.VolumeDoc) (kukeonv1.CreateVolumeResult, error) {
				return kukeonv1.CreateVolumeResult{Volume: doc, Created: false}, nil
			},
			wantOut: "already existed",
		},
		{
			name:    "no name rejected",
			args:    []string{},
			wantErr: "volume name is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)

			cmd := volume.NewVolumeCmd()
			out := &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(out)

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			ctx := context.WithValue(context.Background(), types.CtxLogger, logger)

			if tt.clientFn != nil {
				fake := &fakeClient{createVolumeFn: tt.clientFn}
				ctx = context.WithValue(ctx, volume.MockControllerKey{}, kukeonv1.Client(fake))
			}
			cmd.SetContext(ctx)

			cmd.SetArgs(tt.args)
			err := cmd.Execute()

			if tt.wantErr != "" {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantOut != "" && !strings.Contains(out.String(), tt.wantOut) {
				t.Errorf("expected output to contain %q, got %q", tt.wantOut, out.String())
			}
		})
	}
}

func TestNewVolumeCmd_FlagRegistration(t *testing.T) {
	cmd := volume.NewVolumeCmd()
	for _, name := range []string{"realm", "space", "stack"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("expected %q flag to exist", name)
		}
	}
	// A Volume is never cell-scoped; there must be no --cell flag.
	if cmd.Flags().Lookup("cell") != nil {
		t.Error("create volume should not expose a --cell flag (volumes are never cell-scoped)")
	}
}

// loginConnectorName is a shipped connector with a login method, read from the
// catalog so the test names no connector itself.
func loginConnectorName(t *testing.T) string {
	t.Helper()
	catalog, err := connector.Default()
	if err != nil {
		t.Fatal(err)
	}
	for i := range catalog.Connectors {
		if _, ok := catalog.Connectors[i].LoginMethod(""); ok {
			return catalog.Connectors[i].Metadata.Name
		}
	}
	t.Fatal("the catalog has no connector with a login method")
	return ""
}

// TestNewVolumeCmd_Connector: `--connector` creates a login identity — the
// Volume tagged with the connector and its login method, carrying the login
// marker, and retained (#1685). An unknown connector, and `--method` alone,
// are refused before the daemon is reached.
func TestNewVolumeCmd_Connector(t *testing.T) {
	t.Cleanup(viper.Reset)
	name := loginConnectorName(t)

	run := func(t *testing.T, fake *fakeClient, args ...string) error {
		t.Helper()
		t.Cleanup(viper.Reset)
		cmd := volume.NewVolumeCmd()
		out := &bytes.Buffer{}
		cmd.SetOut(out)
		cmd.SetErr(out)
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
		cmd.SetContext(context.WithValue(ctx, volume.MockControllerKey{}, kukeonv1.Client(fake)))
		cmd.SetArgs(args)
		return cmd.Execute()
	}

	var got v1beta1.VolumeDoc
	fake := &fakeClient{createVolumeFn: func(doc v1beta1.VolumeDoc) (kukeonv1.CreateVolumeResult, error) {
		got = doc
		return kukeonv1.CreateVolumeResult{Volume: doc, Created: true}, nil
	}}
	if err := run(t, fake, "personal-max", "--connector", name); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Spec.Connector != name || got.Spec.Method == "" || got.Spec.LoginMarker == "" ||
		got.Spec.ReclaimPolicy != v1beta1.ReclaimRetain {
		t.Errorf("spec = %+v, want the connector tag, its login method and marker, and Retain", got.Spec)
	}
	if got.Metadata.Name != "personal-max" || got.Metadata.Realm != "default" {
		t.Errorf("metadata = %+v", got.Metadata)
	}

	for label, tc := range map[string]struct {
		args []string
		want string
	}{
		"unknown connector":    {[]string{"v", "--connector", "nope"}, "unknown connector"},
		"method alone":         {[]string{"v", "--method", "login"}, "--method needs --connector"},
		"unknown login method": {[]string{"v", "--connector", name, "--method", "nope"}, "no login method"},
	} {
		t.Run(label, func(t *testing.T) {
			err := run(t, &fakeClient{}, tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}
