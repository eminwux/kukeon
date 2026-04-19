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

	stack "github.com/eminwux/kukeon/cmd/kuke/create/stack"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newStackDoc(name, realm, space string) v1beta1.StackDoc {
	return v1beta1.StackDoc{
		Metadata: v1beta1.StackMetadata{Name: name},
		Spec: v1beta1.StackSpec{
			ID:      name,
			RealmID: realm,
			SpaceID: space,
		},
	}
}

func TestPrintStackResult(t *testing.T) {
	tests := []struct {
		name           string
		result         kukeonv1.CreateStackResult
		expectedOutput []string
	}{
		{
			name: "all created",
			result: kukeonv1.CreateStackResult{
				Stack:              newStackDoc("st1", "r1", "s1"),
				Created:            true,
				MetadataExistsPost: true,
				CgroupCreated:      true,
				CgroupExistsPost:   true,
			},
			expectedOutput: []string{
				`Stack "st1" (realm "r1", space "s1")`,
				"  - metadata: created",
				"  - cgroup: created",
			},
		},
		{
			name: "all existed",
			result: kukeonv1.CreateStackResult{
				Stack:              newStackDoc("st2", "r2", "s2"),
				MetadataExistsPost: true,
				CgroupExistsPost:   true,
			},
			expectedOutput: []string{
				`Stack "st2" (realm "r2", space "s2")`,
				"  - metadata: already existed",
				"  - cgroup: already existed",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, buf := newTestCommand()
			stack.PrintStackResult(cmd, tt.result)
			out := buf.String()
			for _, want := range tt.expectedOutput {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q\nGot:\n%s", want, out)
				}
			}
		})
	}
}

func TestNewStackCmdRunE(t *testing.T) {
	t.Cleanup(viper.Reset)

	tests := []struct {
		name           string
		args           []string
		setup          func(t *testing.T, cmd *cobra.Command)
		clientFn       func(doc v1beta1.StackDoc) (kukeonv1.CreateStackResult, error)
		wantErr        string
		wantCallCreate bool
		wantDoc        v1beta1.StackDoc
		wantOutput     []string
	}{
		{
			name: "success with all flags",
			args: []string{"st1"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "r1")
				setFlag(t, cmd, "space", "s1")
			},
			clientFn: func(doc v1beta1.StackDoc) (kukeonv1.CreateStackResult, error) {
				return kukeonv1.CreateStackResult{Stack: doc, Created: true, MetadataExistsPost: true}, nil
			},
			wantCallCreate: true,
			wantDoc:        newStackDoc("st1", "r1", "s1"),
			wantOutput:     []string{`Stack "st1" (realm "r1", space "s1")`},
		},
		{
			name: "uses default realm+space",
			args: []string{"st1"},
			clientFn: func(doc v1beta1.StackDoc) (kukeonv1.CreateStackResult, error) {
				return kukeonv1.CreateStackResult{Stack: doc, Created: true, MetadataExistsPost: true}, nil
			},
			wantCallCreate: true,
			wantDoc:        newStackDoc("st1", "default", "default"),
		},
		{
			name:    "error missing name",
			wantErr: "stack name is required",
		},
		{
			name: "error client returns",
			args: []string{"st1"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "realm", "r1")
				setFlag(t, cmd, "space", "s1")
			},
			clientFn: func(_ v1beta1.StackDoc) (kukeonv1.CreateStackResult, error) {
				return kukeonv1.CreateStackResult{}, errdefs.ErrCreateStack
			},
			wantErr:        "failed to create stack",
			wantCallCreate: true,
			wantDoc:        newStackDoc("st1", "r1", "s1"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)

			var createCalled bool
			var createDoc v1beta1.StackDoc

			cmd := stack.NewStackCmd()
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			ctx := context.WithValue(context.Background(), types.CtxLogger, logger)

			if tt.clientFn != nil {
				fake := &fakeClient{
					createStackFn: func(doc v1beta1.StackDoc) (kukeonv1.CreateStackResult, error) {
						createCalled = true
						createDoc = doc
						return tt.clientFn(doc)
					},
				}
				ctx = context.WithValue(ctx, stack.MockControllerKey{}, kukeonv1.Client(fake))
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
				t.Errorf("CreateStack called=%v want=%v", createCalled, tt.wantCallCreate)
			}

			if tt.wantCallCreate {
				if createDoc.Metadata.Name != tt.wantDoc.Metadata.Name {
					t.Errorf("Name=%q want=%q", createDoc.Metadata.Name, tt.wantDoc.Metadata.Name)
				}
				if createDoc.Spec.RealmID != tt.wantDoc.Spec.RealmID {
					t.Errorf("RealmID=%q want=%q", createDoc.Spec.RealmID, tt.wantDoc.Spec.RealmID)
				}
				if createDoc.Spec.SpaceID != tt.wantDoc.Spec.SpaceID {
					t.Errorf("SpaceID=%q want=%q", createDoc.Spec.SpaceID, tt.wantDoc.Spec.SpaceID)
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

func TestNewStackCmd_Structure(t *testing.T) {
	cmd := stack.NewStackCmd()
	if cmd.Use != "stack [name]" {
		t.Errorf("Use=%q want stack [name]", cmd.Use)
	}
	for _, f := range []string{"realm", "space"} {
		if cmd.Flags().Lookup(f) == nil {
			t.Errorf("expected %q flag", f)
		}
	}
}

type fakeClient struct {
	kukeonv1.FakeClient

	createStackFn func(doc v1beta1.StackDoc) (kukeonv1.CreateStackResult, error)
}

func (f *fakeClient) CreateStack(_ context.Context, doc v1beta1.StackDoc) (kukeonv1.CreateStackResult, error) {
	if f.createStackFn == nil {
		return kukeonv1.CreateStackResult{}, errors.New("unexpected CreateStack call")
	}
	return f.createStackFn(doc)
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
