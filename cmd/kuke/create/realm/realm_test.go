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

package realm_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/config"
	realm "github.com/eminwux/kukeon/cmd/kuke/create/realm"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newRealmDoc(name, namespace string) v1beta1.RealmDoc {
	return v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{Name: name},
		Spec:     v1beta1.RealmSpec{Namespace: namespace},
	}
}

func TestPrintRealmResult(t *testing.T) {
	tests := []struct {
		name           string
		result         kukeonv1.CreateRealmResult
		expectedOutput []string
	}{
		{
			name: "all created",
			result: kukeonv1.CreateRealmResult{
				Realm:                         newRealmDoc("r1", "ns1"),
				Created:                       true,
				MetadataExistsPost:            true,
				ContainerdNamespaceCreated:    true,
				ContainerdNamespaceExistsPost: true,
				CgroupCreated:                 true,
				CgroupExistsPost:              true,
			},
			expectedOutput: []string{
				`Realm "r1" (namespace "ns1")`,
				"  - metadata: created",
				"  - containerd namespace: created",
				"  - cgroup: created",
			},
		},
		{
			name: "all existed",
			result: kukeonv1.CreateRealmResult{
				Realm:                         newRealmDoc("r2", "ns2"),
				MetadataExistsPost:            true,
				ContainerdNamespaceExistsPost: true,
				CgroupExistsPost:              true,
			},
			expectedOutput: []string{
				`Realm "r2" (namespace "ns2")`,
				"  - metadata: already existed",
				"  - containerd namespace: already existed",
				"  - cgroup: already existed",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, buf := newTestCommand()
			realm.PrintRealmResult(cmd, tt.result)
			out := buf.String()
			for _, want := range tt.expectedOutput {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q\nGot:\n%s", want, out)
				}
			}
		})
	}
}

func TestNewRealmCmdRunE(t *testing.T) {
	t.Cleanup(viper.Reset)

	tests := []struct {
		name           string
		args           []string
		setup          func(t *testing.T, cmd *cobra.Command)
		clientFn       func(doc v1beta1.RealmDoc) (kukeonv1.CreateRealmResult, error)
		wantErr        string
		wantCallCreate bool
		wantDoc        v1beta1.RealmDoc
		wantOutput     []string
	}{
		{
			name: "success with name arg",
			args: []string{"r1"},
			clientFn: func(doc v1beta1.RealmDoc) (kukeonv1.CreateRealmResult, error) {
				return kukeonv1.CreateRealmResult{Realm: doc, Created: true, MetadataExistsPost: true}, nil
			},
			wantCallCreate: true,
			wantDoc:        newRealmDoc("r1", ""),
			wantOutput:     []string{`Realm "r1"`},
		},
		{
			name: "success with namespace flag",
			args: []string{"r1", "--namespace", "custom-ns"},
			clientFn: func(doc v1beta1.RealmDoc) (kukeonv1.CreateRealmResult, error) {
				return kukeonv1.CreateRealmResult{Realm: doc, Created: true, MetadataExistsPost: true}, nil
			},
			wantCallCreate: true,
			wantDoc:        newRealmDoc("r1", "custom-ns"),
			wantOutput:     []string{`namespace "custom-ns"`},
		},
		{
			name: "success with name from viper",
			setup: func(_ *testing.T, _ *cobra.Command) {
				viper.Set(config.KUKE_CREATE_REALM_NAME.ViperKey, "viper-realm")
			},
			clientFn: func(doc v1beta1.RealmDoc) (kukeonv1.CreateRealmResult, error) {
				return kukeonv1.CreateRealmResult{Realm: doc, Created: true, MetadataExistsPost: true}, nil
			},
			wantCallCreate: true,
			wantDoc:        newRealmDoc("viper-realm", ""),
			wantOutput:     []string{`Realm "viper-realm"`},
		},
		{
			name:    "error missing name",
			wantErr: "realm name is required",
		},
		{
			name:    "error empty name after trim",
			args:    []string{"   "},
			wantErr: "realm name is required",
		},
		{
			name: "error client returns",
			args: []string{"r1"},
			clientFn: func(_ v1beta1.RealmDoc) (kukeonv1.CreateRealmResult, error) {
				return kukeonv1.CreateRealmResult{}, errdefs.ErrCreateRealm
			},
			wantErr:        "failed to create realm",
			wantCallCreate: true,
			wantDoc:        newRealmDoc("r1", ""),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)

			var createCalled bool
			var createDoc v1beta1.RealmDoc

			cmd := realm.NewRealmCmd()
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			ctx := context.WithValue(context.Background(), types.CtxLogger, logger)

			if tt.clientFn != nil {
				fake := &fakeClient{
					createRealmFn: func(doc v1beta1.RealmDoc) (kukeonv1.CreateRealmResult, error) {
						createCalled = true
						createDoc = doc
						return tt.clientFn(doc)
					},
				}
				ctx = context.WithValue(ctx, realm.MockControllerKey{}, kukeonv1.Client(fake))
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
				t.Errorf("CreateRealm called=%v want=%v", createCalled, tt.wantCallCreate)
			}

			if tt.wantCallCreate {
				if createDoc.Metadata.Name != tt.wantDoc.Metadata.Name {
					t.Errorf("CreateRealm Name=%q want=%q", createDoc.Metadata.Name, tt.wantDoc.Metadata.Name)
				}
				if createDoc.Spec.Namespace != tt.wantDoc.Spec.Namespace {
					t.Errorf("CreateRealm Namespace=%q want=%q", createDoc.Spec.Namespace, tt.wantDoc.Spec.Namespace)
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

func TestNewRealmCmd_Structure(t *testing.T) {
	cmd := realm.NewRealmCmd()
	if cmd.Use != "realm [name]" {
		t.Errorf("Use=%q want realm [name]", cmd.Use)
	}
	if !cmd.SilenceUsage {
		t.Error("expected SilenceUsage=true")
	}
	if f := cmd.Flags().Lookup("namespace"); f == nil {
		t.Fatal("expected namespace flag")
	}
}

type fakeClient struct {
	kukeonv1.FakeClient

	createRealmFn func(doc v1beta1.RealmDoc) (kukeonv1.CreateRealmResult, error)
}

func (f *fakeClient) CreateRealm(_ context.Context, doc v1beta1.RealmDoc) (kukeonv1.CreateRealmResult, error) {
	if f.createRealmFn == nil {
		return kukeonv1.CreateRealmResult{}, errors.New("unexpected CreateRealm call")
	}
	return f.createRealmFn(doc)
}

func newTestCommand() (*cobra.Command, *bytes.Buffer) {
	cmd := &cobra.Command{Use: "test"}
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	return cmd, buf
}
