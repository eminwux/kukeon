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

	space "github.com/eminwux/kukeon/cmd/kuke/create/space"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newSpaceDoc(name, realm string) v1beta1.SpaceDoc {
	return v1beta1.SpaceDoc{
		Metadata: v1beta1.SpaceMetadata{Name: name},
		Spec:     v1beta1.SpaceSpec{RealmID: realm},
	}
}

func TestPrintSpaceResult(t *testing.T) {
	tests := []struct {
		name           string
		result         kukeonv1.CreateSpaceResult
		expectedOutput []string
	}{
		{
			name: "all created",
			result: kukeonv1.CreateSpaceResult{
				Space:                newSpaceDoc("s1", "r1"),
				Created:              true,
				MetadataExistsPost:   true,
				CNINetworkCreated:    true,
				CNINetworkExistsPost: true,
				CgroupCreated:        true,
				CgroupExistsPost:     true,
			},
			expectedOutput: []string{
				`Space "s1" (realm "r1"`,
				"  - metadata: created",
				"  - network: created",
				"  - cgroup: created",
			},
		},
		{
			name: "all existed",
			result: kukeonv1.CreateSpaceResult{
				Space:                newSpaceDoc("s2", "r2"),
				MetadataExistsPost:   true,
				CNINetworkExistsPost: true,
				CgroupExistsPost:     true,
			},
			expectedOutput: []string{
				`Space "s2" (realm "r2"`,
				"  - metadata: already existed",
				"  - network: already existed",
				"  - cgroup: already existed",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, buf := newTestCommand()
			space.PrintSpaceResult(cmd, tt.result)
			out := buf.String()
			for _, want := range tt.expectedOutput {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q\nGot:\n%s", want, out)
				}
			}
		})
	}
}

func TestNewSpaceCmdRunE(t *testing.T) {
	t.Cleanup(viper.Reset)

	tests := []struct {
		name           string
		args           []string
		setup          func(t *testing.T, cmd *cobra.Command)
		clientFn       func(doc v1beta1.SpaceDoc) (kukeonv1.CreateSpaceResult, error)
		wantErr        string
		wantCallCreate bool
		wantDoc        v1beta1.SpaceDoc
		wantOutput     []string
	}{
		{
			name: "success with args and realm flag",
			args: []string{"s1"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "r1")
			},
			clientFn: func(doc v1beta1.SpaceDoc) (kukeonv1.CreateSpaceResult, error) {
				return kukeonv1.CreateSpaceResult{Space: doc, Created: true, MetadataExistsPost: true}, nil
			},
			wantCallCreate: true,
			wantDoc:        newSpaceDoc("s1", "r1"),
			wantOutput:     []string{`Space "s1"`, `realm "r1"`},
		},
		{
			name: "uses default realm when flag not set",
			args: []string{"s1"},
			clientFn: func(doc v1beta1.SpaceDoc) (kukeonv1.CreateSpaceResult, error) {
				return kukeonv1.CreateSpaceResult{Space: doc, Created: true, MetadataExistsPost: true}, nil
			},
			wantCallCreate: true,
			wantDoc:        newSpaceDoc("s1", "default"),
		},
		{
			name: "trims whitespace in realm",
			args: []string{"s1"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "  r1  ")
			},
			clientFn: func(doc v1beta1.SpaceDoc) (kukeonv1.CreateSpaceResult, error) {
				return kukeonv1.CreateSpaceResult{Space: doc, Created: true, MetadataExistsPost: true}, nil
			},
			wantCallCreate: true,
			wantDoc:        newSpaceDoc("s1", "r1"),
		},
		{
			name:    "error missing name",
			wantErr: "space name is required",
		},
		{
			name: "error client returns",
			args: []string{"s1"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "r1")
			},
			clientFn: func(_ v1beta1.SpaceDoc) (kukeonv1.CreateSpaceResult, error) {
				return kukeonv1.CreateSpaceResult{}, errdefs.ErrCreateSpace
			},
			wantErr:        "failed to create space",
			wantCallCreate: true,
			wantDoc:        newSpaceDoc("s1", "r1"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)

			var createCalled bool
			var createDoc v1beta1.SpaceDoc

			cmd := space.NewSpaceCmd()
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			ctx := context.WithValue(context.Background(), types.CtxLogger, logger)

			if tt.clientFn != nil {
				fake := &fakeClient{
					createSpaceFn: func(doc v1beta1.SpaceDoc) (kukeonv1.CreateSpaceResult, error) {
						createCalled = true
						createDoc = doc
						return tt.clientFn(doc)
					},
				}
				ctx = context.WithValue(ctx, space.MockControllerKey{}, kukeonv1.Client(fake))
			}

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
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if createCalled != tt.wantCallCreate {
				t.Errorf("CreateSpace called=%v want=%v", createCalled, tt.wantCallCreate)
			}

			if tt.wantCallCreate {
				if createDoc.Metadata.Name != tt.wantDoc.Metadata.Name {
					t.Errorf("Name=%q want=%q", createDoc.Metadata.Name, tt.wantDoc.Metadata.Name)
				}
				if createDoc.Spec.RealmID != tt.wantDoc.Spec.RealmID {
					t.Errorf("RealmID=%q want=%q", createDoc.Spec.RealmID, tt.wantDoc.Spec.RealmID)
				}
			}

			if tt.wantOutput != nil {
				out := cmd.OutOrStdout().(*bytes.Buffer).String()
				for _, want := range tt.wantOutput {
					if !strings.Contains(out, want) {
						t.Errorf("output missing %q\nGot:\n%s", want, out)
					}
				}
			}
		})
	}
}

func TestNewSpaceCmd_Structure(t *testing.T) {
	cmd := space.NewSpaceCmd()
	if cmd.Use != "space [name]" {
		t.Errorf("Use=%q want space [name]", cmd.Use)
	}
	if f := cmd.Flags().Lookup("realm"); f == nil {
		t.Fatal("expected realm flag")
	}
}

type fakeClient struct {
	kukeonv1.FakeClient

	createSpaceFn func(doc v1beta1.SpaceDoc) (kukeonv1.CreateSpaceResult, error)
}

func (f *fakeClient) CreateSpace(_ context.Context, doc v1beta1.SpaceDoc) (kukeonv1.CreateSpaceResult, error) {
	if f.createSpaceFn == nil {
		return kukeonv1.CreateSpaceResult{}, errors.New("unexpected CreateSpace call")
	}
	return f.createSpaceFn(doc)
}

func newTestCommand() (*cobra.Command, *bytes.Buffer) {
	cmd := &cobra.Command{Use: "test"}
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	return cmd, buf
}

func setFlag(t *testing.T, cmd *cobra.Command, name, value string) {
	t.Helper()
	if err := cmd.Flags().Set(name, value); err != nil {
		t.Fatalf("failed to set flag %s: %v", name, err)
	}
}
