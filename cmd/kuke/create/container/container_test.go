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

	container "github.com/eminwux/kukeon/cmd/kuke/create/container"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newContainerDoc(name, realm, space, stack, cell, image string) v1beta1.ContainerDoc {
	return v1beta1.ContainerDoc{
		Metadata: v1beta1.ContainerMetadata{Name: name},
		Spec: v1beta1.ContainerSpec{
			ID:      name,
			RealmID: realm,
			SpaceID: space,
			StackID: stack,
			CellID:  cell,
			Image:   image,
		},
	}
}

func TestPrintContainerResult(t *testing.T) {
	tests := []struct {
		name           string
		result         kukeonv1.CreateContainerResult
		expectedOutput []string
	}{
		{
			name: "container created and started",
			result: kukeonv1.CreateContainerResult{
				Container:           newContainerDoc("c1", "r1", "s1", "st1", "cell1", "img"),
				ContainerCreated:    true,
				ContainerExistsPost: true,
				Started:             true,
			},
			expectedOutput: []string{
				`Container "c1" (ID: "c1") in cell "cell1" (realm "r1", space "s1", stack "st1")`,
				"  - container: created",
				"  - container: started",
			},
		},
		{
			name: "container existed not started",
			result: kukeonv1.CreateContainerResult{
				Container:           newContainerDoc("c2", "r2", "s2", "st2", "cell2", "img"),
				ContainerExistsPost: true,
			},
			expectedOutput: []string{
				`Container "c2"`,
				"  - container: already existed",
				"  - container: not started",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, buf := newTestCommand()
			container.PrintContainerResult(cmd, tt.result)
			out := buf.String()
			for _, want := range tt.expectedOutput {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q\nGot:\n%s", want, out)
				}
			}
		})
	}
}

func TestNewContainerCmdRunE(t *testing.T) {
	t.Cleanup(viper.Reset)

	tests := []struct {
		name           string
		args           []string
		setup          func(t *testing.T, cmd *cobra.Command)
		clientFn       func(doc v1beta1.ContainerDoc) (kukeonv1.CreateContainerResult, error)
		wantErr        string
		wantCallCreate bool
		wantName       string
		wantCell       string
		wantImage      string
	}{
		{
			name: "success with required flags",
			args: []string{"c1"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "cell", "cell1")
				setFlag(t, cmd, "realm", "r1")
				setFlag(t, cmd, "space", "s1")
				setFlag(t, cmd, "stack", "st1")
				setFlag(t, cmd, "image", "my/image:tag")
			},
			clientFn: func(doc v1beta1.ContainerDoc) (kukeonv1.CreateContainerResult, error) {
				return kukeonv1.CreateContainerResult{
					Container:           doc,
					ContainerCreated:    true,
					ContainerExistsPost: true,
				}, nil
			},
			wantCallCreate: true,
			wantName:       "c1",
			wantCell:       "cell1",
			wantImage:      "docker.io/my/image:tag",
		},
		{
			name:    "error missing name",
			wantErr: "container name is required",
		},
		{
			name:    "error missing cell",
			args:    []string{"c1"},
			wantErr: "cell name is required",
		},
		{
			name: "error client returns",
			args: []string{"c1"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "cell", "cell1")
			},
			clientFn: func(_ v1beta1.ContainerDoc) (kukeonv1.CreateContainerResult, error) {
				return kukeonv1.CreateContainerResult{}, errdefs.ErrCreateCell
			},
			wantErr:        "failed to create cell",
			wantCallCreate: true,
			wantName:       "c1",
			wantCell:       "cell1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)

			var createCalled bool
			var createDoc v1beta1.ContainerDoc

			cmd := container.NewContainerCmd()
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			ctx := context.WithValue(context.Background(), types.CtxLogger, logger)

			if tt.clientFn != nil {
				fake := &fakeClient{
					createContainerFn: func(doc v1beta1.ContainerDoc) (kukeonv1.CreateContainerResult, error) {
						createCalled = true
						createDoc = doc
						return tt.clientFn(doc)
					},
				}
				ctx = context.WithValue(ctx, container.MockControllerKey{}, kukeonv1.Client(fake))
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
				t.Errorf("CreateContainer called=%v want=%v", createCalled, tt.wantCallCreate)
			}

			if tt.wantCallCreate {
				if createDoc.Metadata.Name != tt.wantName {
					t.Errorf("Name=%q want=%q", createDoc.Metadata.Name, tt.wantName)
				}
				if createDoc.Spec.CellID != tt.wantCell {
					t.Errorf("CellID=%q want=%q", createDoc.Spec.CellID, tt.wantCell)
				}
				if tt.wantImage != "" && createDoc.Spec.Image != tt.wantImage {
					t.Errorf("Image=%q want=%q", createDoc.Spec.Image, tt.wantImage)
				}
			}
		})
	}
}

func TestNewContainerCmd_Structure(t *testing.T) {
	cmd := container.NewContainerCmd()
	if cmd.Use != "container [name]" {
		t.Errorf("Use=%q want container [name]", cmd.Use)
	}
	for _, f := range []string{"realm", "space", "stack", "cell", "image", "env", "port"} {
		if cmd.Flags().Lookup(f) == nil {
			t.Errorf("expected %q flag", f)
		}
	}
}

type fakeClient struct {
	kukeonv1.FakeClient

	createContainerFn func(doc v1beta1.ContainerDoc) (kukeonv1.CreateContainerResult, error)
}

func (f *fakeClient) CreateContainer(
	_ context.Context,
	doc v1beta1.ContainerDoc,
) (kukeonv1.CreateContainerResult, error) {
	if f.createContainerFn == nil {
		return kukeonv1.CreateContainerResult{}, errors.New("unexpected CreateContainer call")
	}
	return f.createContainerFn(doc)
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
