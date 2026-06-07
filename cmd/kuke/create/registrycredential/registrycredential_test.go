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

package registrycredential_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/kuke/create/registrycredential"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"gopkg.in/yaml.v3"
)

type fakeClient struct {
	kukeonv1.FakeClient
	getRealmFn func(doc v1beta1.RealmDoc) (kukeonv1.GetRealmResult, error)
	applyFn    func(rawYAML []byte) (kukeonv1.ApplyDocumentsResult, error)
}

func (f *fakeClient) GetRealm(_ context.Context, doc v1beta1.RealmDoc) (kukeonv1.GetRealmResult, error) {
	if f.getRealmFn == nil {
		return kukeonv1.GetRealmResult{}, errors.New("unexpected GetRealm call")
	}
	return f.getRealmFn(doc)
}

func (f *fakeClient) ApplyDocuments(_ context.Context, rawYAML []byte) (kukeonv1.ApplyDocumentsResult, error) {
	if f.applyFn == nil {
		return kukeonv1.ApplyDocumentsResult{}, errors.New("unexpected ApplyDocuments call")
	}
	return f.applyFn(rawYAML)
}

func (f *fakeClient) Close() error { return nil }

// existingRealm returns a GetRealm result for a realm that exists with the
// supplied namespace and credentials.
func existingRealm(namespace string, creds []v1beta1.RegistryCredentials) kukeonv1.GetRealmResult {
	return kukeonv1.GetRealmResult{
		MetadataExists: true,
		Realm: v1beta1.RealmDoc{
			Metadata: v1beta1.RealmMetadata{Name: "default"},
			Spec: v1beta1.RealmSpec{
				Namespace:           namespace,
				RegistryCredentials: creds,
			},
		},
	}
}

// applyOK echoes an "updated" Realm resource result and captures the applied
// YAML into *captured so the test can assert the desired-spec shape.
func applyOK(captured *[]byte) func(rawYAML []byte) (kukeonv1.ApplyDocumentsResult, error) {
	return func(rawYAML []byte) (kukeonv1.ApplyDocumentsResult, error) {
		*captured = append([]byte(nil), rawYAML...)
		return kukeonv1.ApplyDocumentsResult{
			Resources: []kukeonv1.ApplyResourceResult{
				{Kind: string(v1beta1.KindRealm), Name: "default", Action: "updated"},
			},
		}, nil
	}
}

func decodeRealm(t *testing.T, raw []byte) v1beta1.RealmDoc {
	t.Helper()
	var doc v1beta1.RealmDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal applied YAML: %v", err)
	}
	return doc
}

func TestRegistryCredentialUpsert(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		stdin    string
		getRealm func(doc v1beta1.RealmDoc) (kukeonv1.GetRealmResult, error)
		// assertApplied inspects the desired realm doc sent to ApplyDocuments.
		assertApplied func(t *testing.T, doc v1beta1.RealmDoc)
		wantErr       string
		wantOut       string
		wantNoApply   bool
	}{
		{
			name:  "stdin happy path appends to empty list",
			args:  []string{"default", "--server", "ghcr.io", "--username", "alice", "--password-stdin"},
			stdin: "s3cr3t\n",
			getRealm: func(_ v1beta1.RealmDoc) (kukeonv1.GetRealmResult, error) {
				return existingRealm("default.kukeon.io", nil), nil
			},
			assertApplied: func(t *testing.T, doc v1beta1.RealmDoc) {
				if doc.Kind != v1beta1.KindRealm || doc.APIVersion != v1beta1.APIVersionV1Beta1 {
					t.Errorf("apiVersion/kind not set: %q/%q", doc.APIVersion, doc.Kind)
				}
				if doc.Spec.Namespace != "default.kukeon.io" {
					t.Errorf("namespace not preserved: %q", doc.Spec.Namespace)
				}
				if len(doc.Spec.RegistryCredentials) != 1 {
					t.Fatalf("want 1 credential, got %d", len(doc.Spec.RegistryCredentials))
				}
				c := doc.Spec.RegistryCredentials[0]
				if c.ServerAddress != "ghcr.io" || c.Username != "alice" || c.Password != "s3cr3t" {
					t.Errorf("unexpected credential: %+v", c)
				}
			},
			wantOut: `applied to realm "default"`,
		},
		{
			name:  "upsert replaces matching server in place",
			args:  []string{"default", "--server", "ghcr.io", "--username", "bob", "--password-stdin"},
			stdin: "newtoken",
			getRealm: func(_ v1beta1.RealmDoc) (kukeonv1.GetRealmResult, error) {
				return existingRealm("default.kukeon.io", []v1beta1.RegistryCredentials{
					{ServerAddress: "ghcr.io", Username: "alice", Password: "old"},
					{ServerAddress: "docker.io", Username: "carol", Password: "keep"},
				}), nil
			},
			assertApplied: func(t *testing.T, doc v1beta1.RealmDoc) {
				if len(doc.Spec.RegistryCredentials) != 2 {
					t.Fatalf("want 2 credentials (no duplicate), got %d", len(doc.Spec.RegistryCredentials))
				}
				var ghcr, docker v1beta1.RegistryCredentials
				for _, c := range doc.Spec.RegistryCredentials {
					switch c.ServerAddress {
					case "ghcr.io":
						ghcr = c
					case "docker.io":
						docker = c
					}
				}
				if ghcr.Username != "bob" || ghcr.Password != "newtoken" {
					t.Errorf("ghcr.io not updated in place: %+v", ghcr)
				}
				if docker.Username != "carol" || docker.Password != "keep" {
					t.Errorf("docker.io entry not preserved: %+v", docker)
				}
			},
			wantOut: "updated",
		},
		{
			name:  "appends when server differs",
			args:  []string{"default", "--server", "ghcr.io", "--username", "bob", "--password-stdin"},
			stdin: "tok",
			getRealm: func(_ v1beta1.RealmDoc) (kukeonv1.GetRealmResult, error) {
				return existingRealm("default.kukeon.io", []v1beta1.RegistryCredentials{
					{ServerAddress: "docker.io", Username: "carol", Password: "keep"},
				}), nil
			},
			assertApplied: func(t *testing.T, doc v1beta1.RealmDoc) {
				if len(doc.Spec.RegistryCredentials) != 2 {
					t.Fatalf("want 2 credentials, got %d", len(doc.Spec.RegistryCredentials))
				}
			},
			wantOut: "applied",
		},
		{
			name:  "empty server upserts against itself",
			args:  []string{"default", "--username", "bob", "--password-stdin"},
			stdin: "tok2",
			getRealm: func(_ v1beta1.RealmDoc) (kukeonv1.GetRealmResult, error) {
				return existingRealm("default.kukeon.io", []v1beta1.RegistryCredentials{
					{ServerAddress: "", Username: "old", Password: "old"},
				}), nil
			},
			assertApplied: func(t *testing.T, doc v1beta1.RealmDoc) {
				if len(doc.Spec.RegistryCredentials) != 1 {
					t.Fatalf("want 1 credential (in-place), got %d", len(doc.Spec.RegistryCredentials))
				}
				if doc.Spec.RegistryCredentials[0].Username != "bob" {
					t.Errorf("empty-server entry not updated: %+v", doc.Spec.RegistryCredentials[0])
				}
			},
			wantOut: "default: image reference",
		},
		{
			name:  "unknown realm errors",
			args:  []string{"ghost", "--server", "ghcr.io", "--username", "bob", "--password-stdin"},
			stdin: "tok",
			getRealm: func(_ v1beta1.RealmDoc) (kukeonv1.GetRealmResult, error) {
				return kukeonv1.GetRealmResult{MetadataExists: false}, nil
			},
			wantErr:     `realm "ghost" not found`,
			wantNoApply: true,
		},
		{
			name:        "both token sources rejected",
			args:        []string{"default", "--username", "bob", "--password-stdin", "--from-file", "x"},
			stdin:       "tok",
			wantErr:     "only one of --password-stdin or --from-file may be specified",
			wantNoApply: true,
		},
		{
			name:        "no token source rejected",
			args:        []string{"default", "--username", "bob"},
			wantErr:     "exactly one of --password-stdin or --from-file is required",
			wantNoApply: true,
		},
		{
			name:        "missing username rejected",
			args:        []string{"default", "--password-stdin"},
			stdin:       "tok",
			wantErr:     "--username is required",
			wantNoApply: true,
		},
		{
			name:        "empty token rejected",
			args:        []string{"default", "--username", "bob", "--password-stdin"},
			stdin:       "\n",
			wantErr:     "registry token is empty",
			wantNoApply: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := registrycredential.NewRegistryCredentialCmd()
			out := &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(out)
			cmd.SetIn(strings.NewReader(tt.stdin))

			var captured []byte
			applied := false
			fake := &fakeClient{
				getRealmFn: tt.getRealm,
				applyFn: func(rawYAML []byte) (kukeonv1.ApplyDocumentsResult, error) {
					applied = true
					return applyOK(&captured)(rawYAML)
				},
			}

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
			ctx = context.WithValue(ctx, registrycredential.MockControllerKey{}, kukeonv1.Client(fake))
			cmd.SetContext(ctx)

			cmd.SetArgs(tt.args)
			err := cmd.Execute()

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantNoApply && applied {
				t.Error("ApplyDocuments should not have been called")
			}

			if tt.assertApplied != nil {
				if !applied {
					t.Fatal("ApplyDocuments was not called")
				}
				tt.assertApplied(t, decodeRealm(t, captured))
			}

			if tt.wantOut != "" && !strings.Contains(out.String(), tt.wantOut) {
				t.Errorf("expected output to contain %q, got %q", tt.wantOut, out.String())
			}
		})
	}
}

func TestRegistryCredentialFromFile(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token.txt")
	if err := os.WriteFile(tokenPath, []byte("file-token\n"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}

	cmd := registrycredential.NewRegistryCredentialCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)

	var captured []byte
	fake := &fakeClient{
		getRealmFn: func(_ v1beta1.RealmDoc) (kukeonv1.GetRealmResult, error) {
			return existingRealm("default.kukeon.io", nil), nil
		},
		applyFn: applyOK(&captured),
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	ctx = context.WithValue(ctx, registrycredential.MockControllerKey{}, kukeonv1.Client(fake))
	cmd.SetContext(ctx)

	cmd.SetArgs([]string{"default", "--server", "ghcr.io", "--username", "alice", "--from-file", tokenPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	doc := decodeRealm(t, captured)
	if len(doc.Spec.RegistryCredentials) != 1 {
		t.Fatalf("want 1 credential, got %d", len(doc.Spec.RegistryCredentials))
	}
	if got := doc.Spec.RegistryCredentials[0].Password; got != "file-token" {
		t.Errorf("token not read from file (trailing newline stripped): %q", got)
	}
}

func TestRegistryCredentialNoPasswordFlag(t *testing.T) {
	// The token must never be settable via an argv flag.
	cmd := registrycredential.NewRegistryCredentialCmd()
	if cmd.Flags().Lookup("password") != nil {
		t.Error("a plaintext --password flag must not exist")
	}
	for _, name := range []string{"server", "username", "password-stdin", "from-file"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("expected %q flag to exist", name)
		}
	}
}
