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

	realm "github.com/eminwux/kukeon/cmd/kuke/get/realm"
	"github.com/eminwux/kukeon/cmd/kuke/get/testutil"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/viper"
)

func TestNewRealmCmd(t *testing.T) {
	t.Cleanup(viper.Reset)

	tests := []struct {
		name       string
		args       []string
		fake       *fakeClient
		wantErr    string
		wantOutput string
	}{
		{
			name: "get single realm succeeds",
			args: []string{"r1"},
			fake: &fakeClient{
				getRealmFn: func(_ v1beta1.RealmDoc) (kukeonv1.GetRealmResult, error) {
					return kukeonv1.GetRealmResult{
						Realm: v1beta1.RealmDoc{
							APIVersion: v1beta1.APIVersionV1Beta1,
							Kind:       v1beta1.KindRealm,
							Metadata:   v1beta1.RealmMetadata{Name: "r1"},
							Spec:       v1beta1.RealmSpec{Namespace: "ns1"},
						},
						MetadataExists: true,
					}, nil
				},
			},
		},
		{
			name: "get missing realm returns not found",
			args: []string{"missing"},
			fake: &fakeClient{
				getRealmFn: func(_ v1beta1.RealmDoc) (kukeonv1.GetRealmResult, error) {
					return kukeonv1.GetRealmResult{}, errdefs.ErrRealmNotFound
				},
			},
			wantErr: `realm "missing" not found`,
		},
		{
			name: "get with metadataExists=false returns not found",
			args: []string{"ghost"},
			fake: &fakeClient{
				getRealmFn: func(_ v1beta1.RealmDoc) (kukeonv1.GetRealmResult, error) {
					return kukeonv1.GetRealmResult{}, nil
				},
			},
			wantErr: `realm "ghost" not found`,
		},
		{
			name: "list realms",
			fake: &fakeClient{
				listRealmsFn: func() ([]v1beta1.RealmDoc, error) {
					return []v1beta1.RealmDoc{
						{Metadata: v1beta1.RealmMetadata{Name: "r1"}, Spec: v1beta1.RealmSpec{Namespace: "ns1"}},
						{Metadata: v1beta1.RealmMetadata{Name: "r2"}, Spec: v1beta1.RealmSpec{Namespace: "ns2"}},
					}, nil
				},
			},
			wantOutput: "r1",
		},
		{
			name: "list empty",
			fake: &fakeClient{
				listRealmsFn: func() ([]v1beta1.RealmDoc, error) {
					return nil, nil
				},
			},
			wantOutput: "No realms found.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)

			cmd := realm.NewRealmCmd()
			buf := &bytes.Buffer{}
			cmd.SetOut(buf)
			cmd.SetErr(buf)

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
			ctx = context.WithValue(ctx, realm.MockControllerKey{}, kukeonv1.Client(tt.fake))
			cmd.SetContext(ctx)

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

func TestNewRealmCmd_Structure(t *testing.T) {
	cmd := realm.NewRealmCmd()
	if cmd.Use != "realm [name]" {
		t.Errorf("Use=%q want realm [name]", cmd.Use)
	}
	if cmd.Flags().Lookup("output") == nil {
		t.Fatal("expected output flag")
	}
	if cmd.Flags().Lookup("show-controllers") != nil {
		t.Error("show-controllers flag must be removed (issue #827)")
	}
}

// TestNewRealmCmd_Columns pins the epic:get step-1 column contract for
// `kuke get realm`: default emits `NAME STATE AGE`, `-o wide` appends
// `NAMESPACE`, and neither `CGROUP` nor `CONTROLLERS` ever appear in
// any table output (they live in `-o yaml`/`-o json` only).
func TestNewRealmCmd_Columns(t *testing.T) {
	t.Cleanup(viper.Reset)

	listFn := func() ([]v1beta1.RealmDoc, error) {
		return []v1beta1.RealmDoc{
			{
				Metadata: v1beta1.RealmMetadata{Name: "default"},
				Spec:     v1beta1.RealmSpec{Namespace: "default.kukeon.io"},
				Status: v1beta1.RealmStatus{
					State:              v1beta1.RealmStateReady,
					CgroupPath:         "/kukeon/default",
					SubtreeControllers: []string{"cpu", "memory", "io", "pids"},
				},
			},
			{
				Metadata: v1beta1.RealmMetadata{Name: "kuke-system"},
				Spec:     v1beta1.RealmSpec{Namespace: "kuke-system.kukeon.io"},
				Status: v1beta1.RealmStatus{
					State:      v1beta1.RealmStateReady,
					CgroupPath: "/kukeon/kuke-system",
				},
			},
		}, nil
	}

	t.Run("default table is NAME STATE AGE", func(t *testing.T) {
		t.Cleanup(viper.Reset)

		cmd := realm.NewRealmCmd()
		buf := &bytes.Buffer{}
		cmd.SetOut(buf)
		cmd.SetErr(buf)
		ctx := context.WithValue(context.Background(),
			realm.MockControllerKey{},
			kukeonv1.Client(&fakeClient{listRealmsFn: listFn}))
		cmd.SetContext(ctx)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		out := buf.String()
		header := testutil.FirstLine(out)
		// The header is rendered before any data row; check it directly so
		// row data can't accidentally satisfy the column-presence assertion.
		for _, want := range []string{"NAME", "STATE", "AGE"} {
			if !strings.Contains(header, want) {
				t.Errorf("default header missing %q; got: %q", want, header)
			}
		}
		for _, denied := range []string{"NAMESPACE", "CGROUP", "CONTROLLERS"} {
			if strings.Contains(out, denied) {
				t.Errorf("default output must NOT contain %q; got:\n%s", denied, out)
			}
		}
	})

	t.Run("-o wide appends NAMESPACE without resurrecting CGROUP/CONTROLLERS", func(t *testing.T) {
		t.Cleanup(viper.Reset)

		cmd := realm.NewRealmCmd()
		buf := &bytes.Buffer{}
		cmd.SetOut(buf)
		cmd.SetErr(buf)
		ctx := context.WithValue(context.Background(),
			realm.MockControllerKey{},
			kukeonv1.Client(&fakeClient{listRealmsFn: listFn}))
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{"-o", "wide"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		out := buf.String()
		header := testutil.FirstLine(out)
		for _, want := range []string{"NAME", "STATE", "AGE", "NAMESPACE"} {
			if !strings.Contains(header, want) {
				t.Errorf("wide header missing %q; got: %q", want, header)
			}
		}
		for _, denied := range []string{"CGROUP", "CONTROLLERS"} {
			if strings.Contains(out, denied) {
				t.Errorf("-o wide output must NOT contain %q; got:\n%s", denied, out)
			}
		}
		if !strings.Contains(out, "default.kukeon.io") {
			t.Errorf("expected NAMESPACE value in -o wide rows; got:\n%s", out)
		}
	})

	t.Run("-o yaml surfaces cgroupPath", func(t *testing.T) {
		t.Cleanup(viper.Reset)

		cmd := realm.NewRealmCmd()
		buf := &bytes.Buffer{}
		cmd.SetOut(buf)
		cmd.SetErr(buf)
		ctx := context.WithValue(context.Background(),
			realm.MockControllerKey{},
			kukeonv1.Client(&fakeClient{listRealmsFn: listFn}))
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{"-o", "yaml"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, "cgroupPath: /kukeon/default") {
			t.Errorf("-o yaml should still surface cgroupPath; got:\n%s", out)
		}
	})
}

// TestNewRealmCmd_Selector pins the issue #614 contract for `kuke get
// realm`: `-l <selector>` filters the listed realms against
// Metadata.Labels using the kubectl-style grammar, malformed selectors
// fail before any controller call, and combining `-l` with a positional
// name argument is rejected up front.
func TestNewRealmCmd_Selector(t *testing.T) {
	t.Cleanup(viper.Reset)

	listFn := func() ([]v1beta1.RealmDoc, error) {
		return []v1beta1.RealmDoc{
			{
				Metadata: v1beta1.RealmMetadata{
					Name:   "prod",
					Labels: map[string]string{"env": "prod", "tier": "web"},
				},
			},
			{
				Metadata: v1beta1.RealmMetadata{
					Name:   "staging",
					Labels: map[string]string{"env": "staging", "tier": "web"},
				},
			},
			{
				Metadata: v1beta1.RealmMetadata{
					Name:   "legacy",
					Labels: map[string]string{"env": "prod", "debug": "yes"},
				},
			},
		}, nil
	}

	type sub struct {
		name        string
		args        []string
		wantInclude []string
		wantExclude []string
		wantErr     string
	}
	subs := []sub{
		{
			name:        "equality filters by label value",
			args:        []string{"-l", "env=prod"},
			wantInclude: []string{"prod", "legacy"},
			wantExclude: []string{"staging"},
		},
		{
			name:        "inequality matches absent and differing keys",
			args:        []string{"-l", "env!=prod"},
			wantInclude: []string{"staging"},
			wantExclude: []string{"prod", "legacy"},
		},
		{
			name:        "existence matches key presence",
			args:        []string{"-l", "debug"},
			wantInclude: []string{"legacy"},
			wantExclude: []string{"prod", "staging"},
		},
		{
			name:        "absence matches when key missing",
			args:        []string{"-l", "!debug"},
			wantInclude: []string{"prod", "staging"},
			wantExclude: []string{"legacy"},
		},
		{
			name:        "comma is logical AND",
			args:        []string{"-l", "env=prod,tier=web"},
			wantInclude: []string{"prod"},
			wantExclude: []string{"staging", "legacy"},
		},
		{
			name:        "no matches still exits 0 with header",
			args:        []string{"-l", "env=nowhere"},
			wantInclude: []string{"No realms found."},
		},
		{
			name:    "malformed selector fails fast",
			args:    []string{"-l", "env="},
			wantErr: "empty value",
		},
		{
			name:    "selector with positional name is rejected",
			args:    []string{"prod", "-l", "env=prod"},
			wantErr: "--selector cannot be combined with a resource name",
		},
	}

	for _, s := range subs {
		t.Run(s.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)

			cmd := realm.NewRealmCmd()
			buf := &bytes.Buffer{}
			cmd.SetOut(buf)
			cmd.SetErr(buf)
			ctx := context.WithValue(context.Background(),
				realm.MockControllerKey{},
				kukeonv1.Client(&fakeClient{listRealmsFn: listFn}))
			cmd.SetContext(ctx)
			cmd.SetArgs(s.args)

			err := cmd.Execute()
			if s.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", s.wantErr)
				}
				if !strings.Contains(err.Error(), s.wantErr) {
					t.Fatalf("expected error containing %q, got %v", s.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			out := buf.String()
			for _, want := range s.wantInclude {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q\nGot:\n%s", want, out)
				}
			}
			for _, deny := range s.wantExclude {
				if strings.Contains(out, deny) {
					t.Errorf("output unexpectedly contains %q\nGot:\n%s", deny, out)
				}
			}
		})
	}
}

type fakeClient struct {
	kukeonv1.FakeClient

	getRealmFn   func(doc v1beta1.RealmDoc) (kukeonv1.GetRealmResult, error)
	listRealmsFn func() ([]v1beta1.RealmDoc, error)
}

func (f *fakeClient) GetRealm(_ context.Context, doc v1beta1.RealmDoc) (kukeonv1.GetRealmResult, error) {
	if f.getRealmFn == nil {
		return kukeonv1.GetRealmResult{}, errors.New("unexpected GetRealm call")
	}
	return f.getRealmFn(doc)
}

func (f *fakeClient) ListRealms(_ context.Context) ([]v1beta1.RealmDoc, error) {
	if f.listRealmsFn == nil {
		return nil, errors.New("unexpected ListRealms call")
	}
	return f.listRealmsFn()
}
