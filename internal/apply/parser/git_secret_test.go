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

package parser_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/apply/parser"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

const parserTestPrivateKey = "-----BEGIN OPENSSH PRIVATE" + " KEY-----\nYm9keQ==\n-----END OPENSSH PRIVATE" + " KEY-----\n"

// TestParseDocument_TypedSecret confirms the typed blocks of a Secret parse
// from YAML into the SecretDoc (issue #1472, step 1).
func TestParseDocument_TypedSecret(t *testing.T) {
	yaml := `
apiVersion: v1beta1
kind: Secret
metadata:
  name: deploy-key
  realm: default
spec:
  type: ssh-key
  sshKey:
    privateKey: |
      -----BEGIN OPENSSH PRIVATE` + ` KEY-----
      Ym9keQ==
      -----END OPENSSH PRIVATE` + ` KEY-----
    passphrase: hunter2
    publicKey: ssh-ed25519 AAAA deploy
    knownHosts: |
      git.example.com ssh-ed25519 AAAA
`
	doc, err := parser.ParseDocument(0, []byte(yaml))
	if err != nil {
		t.Fatalf("ParseDocument failed: %v", err)
	}
	spec := doc.SecretDoc.Spec
	if spec.Type != v1beta1.SecretTypeSSHKey || spec.SSHKey == nil {
		t.Fatalf("spec = %+v, want an ssh-key secret with its block", spec)
	}
	if spec.SSHKey.Passphrase != "hunter2" || spec.SSHKey.PublicKey != "ssh-ed25519 AAAA deploy" ||
		!strings.Contains(spec.SSHKey.PrivateKey, "BEGIN OPENSSH PRIVATE"+" KEY") ||
		!strings.Contains(spec.SSHKey.KnownHosts, "git.example.com") {
		t.Errorf("sshKey fields not carried: %+v", spec.SSHKey)
	}
	if verr := parser.ValidateDocument(doc); verr != nil {
		t.Fatalf("ValidateDocument: %v", verr)
	}

	basic, err := parser.ParseDocument(0, []byte(`
apiVersion: v1beta1
kind: Secret
metadata:
  name: forge-token
  realm: default
spec:
  type: basic-auth
  basicAuth:
    username: x-access-token
    token: tok-123
`))
	if err != nil {
		t.Fatalf("ParseDocument failed: %v", err)
	}
	ba := basic.SecretDoc.Spec.BasicAuth
	if ba == nil || ba.Username != "x-access-token" || ba.Token != "tok-123" {
		t.Fatalf("basicAuth = %+v, want username + token", ba)
	}
	if verr := parser.ValidateDocument(basic); verr != nil {
		t.Fatalf("ValidateDocument: %v", verr)
	}
}

// TestValidateDocument_TypedSecret exercises the per-type apply-time gates:
// each type accepts only its own block, an unknown type is refused, and the
// name of the daemon's type sidecar directory is reserved.
func TestValidateDocument_TypedSecret(t *testing.T) {
	basicAuth := func(u, p, tok string) v1beta1.SecretSpec {
		return v1beta1.SecretSpec{
			Type:      v1beta1.SecretTypeBasicAuth,
			BasicAuth: &v1beta1.SecretBasicAuth{Username: u, Password: p, Token: tok},
		}
	}
	sshKey := func(key *v1beta1.SecretSSHKey) v1beta1.SecretSpec {
		return v1beta1.SecretSpec{Type: v1beta1.SecretTypeSSHKey, SSHKey: key}
	}
	tests := []struct {
		name    string
		secret  string // metadata.name; "" means "tok"
		spec    v1beta1.SecretSpec
		wantErr error
	}{
		{name: "opaque explicit type", spec: v1beta1.SecretSpec{Type: v1beta1.SecretTypeOpaque, Data: "x"}},
		{name: "basic-auth password", spec: basicAuth("u", "p", "")},
		{name: "basic-auth token", spec: basicAuth("u", "", "t")},
		{name: "ssh-key", spec: sshKey(&v1beta1.SecretSSHKey{PrivateKey: parserTestPrivateKey})},
		{
			name: "ssh-key with passphrase and pinned hosts",
			spec: sshKey(&v1beta1.SecretSSHKey{
				PrivateKey: parserTestPrivateKey, Passphrase: "pp", KnownHosts: "h ssh-ed25519 A\nh2 ssh-ed25519 B\n",
			}),
		},
		{name: "basic-auth without username", spec: basicAuth("", "p", ""), wantErr: errdefs.ErrSecretTypeFields},
		{name: "basic-auth without credential", spec: basicAuth("u", "", ""), wantErr: errdefs.ErrSecretTypeFields},
		{name: "basic-auth password and token", spec: basicAuth("u", "p", "t"), wantErr: errdefs.ErrSecretTypeFields},
		{
			name:    "basic-auth without block",
			spec:    v1beta1.SecretSpec{Type: v1beta1.SecretTypeBasicAuth},
			wantErr: errdefs.ErrSecretTypeFields,
		},
		{
			name: "basic-auth with data",
			spec: v1beta1.SecretSpec{
				Type: v1beta1.SecretTypeBasicAuth, Data: "x",
				BasicAuth: &v1beta1.SecretBasicAuth{Username: "u", Password: "p"},
			},
			wantErr: errdefs.ErrSecretTypeFields,
		},
		{
			name:    "ssh-key without block",
			spec:    v1beta1.SecretSpec{Type: v1beta1.SecretTypeSSHKey},
			wantErr: errdefs.ErrSecretTypeFields,
		},
		{
			name:    "ssh-key given a public key",
			spec:    sshKey(&v1beta1.SecretSSHKey{PrivateKey: "ssh-ed25519 AAAA"}),
			wantErr: errdefs.ErrSecretTypeFields,
		},
		{
			name: "opaque with typed block",
			spec: v1beta1.SecretSpec{
				Data: "x", BasicAuth: &v1beta1.SecretBasicAuth{Username: "u", Password: "p"},
			},
			wantErr: errdefs.ErrSecretTypeFields,
		},
		{
			name:    "typed block without type still needs data",
			spec:    v1beta1.SecretSpec{SSHKey: &v1beta1.SecretSSHKey{PrivateKey: parserTestPrivateKey}},
			wantErr: errdefs.ErrSecretTypeFields,
		},
		{
			name:    "unknown type",
			spec:    v1beta1.SecretSpec{Type: "oauth", Data: "x"},
			wantErr: errdefs.ErrSecretTypeUnknown,
		},
		{
			name:    "reserved name",
			secret:  ".types",
			spec:    v1beta1.SecretSpec{Data: "x"},
			wantErr: errdefs.ErrSecretReservedName,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := tt.secret
			if name == "" {
				name = "tok"
			}
			err := parser.ValidateDocument(&parser.Document{
				APIVersion: v1beta1.APIVersionV1Beta1,
				Kind:       v1beta1.KindSecret,
				SecretDoc: &v1beta1.SecretDoc{
					APIVersion: v1beta1.APIVersionV1Beta1,
					Kind:       v1beta1.KindSecret,
					Metadata:   v1beta1.SecretMetadata{Name: name, Realm: "default"},
					Spec:       tt.spec,
				},
			})
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected validation error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected %v, got nil", tt.wantErr)
			}
			if !errors.Is(err.Err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

const gitBlueprintHead = `apiVersion: v1beta1
kind: CellBlueprint
metadata:
  name: web
  realm: default
spec:
  cell:
    containers:
      - id: main
        image: registry.example.com/web:latest
        attachable: true
`

// TestValidateDocument_Blueprint_Git covers the git blocks a blueprint's
// template containers and their repos may carry (#1472): stored credentials
// and signing are accepted, and a malformed block is rejected naming the
// container (and repo) it sits on.
func TestValidateDocument_Blueprint_Git(t *testing.T) {
	tests := []struct {
		name    string
		tail    string
		wantErr string
	}{
		{
			name: "container git with stored credentials and signing",
			tail: `        git:
          author: {name: Dev, email: dev@example.com}
          sign: [commits, tags]
          signing:
            secretRef: {name: signing-key, realm: default}
          credentials:
            - host: github.com
              secretRef: {name: gh-key, realm: default}
              hostKeyPolicy: strict
            - host: https://gitlab.example.com
              secretRef: {name: gl-token, realm: default}
`,
		},
		{
			name: "repo git with own identity and credential",
			tail: `        repos:
          - name: src
            target: /workspace/src
            url: git@github.com:o/r.git
            git:
              author: {name: Repo Dev, email: repo@example.com}
              credential:
                secretRef: {name: repo-key, realm: default}
`,
		},
		{
			name: "container credential without secretRef",
			tail: `        git:
          credentials:
            - host: github.com
`,
			wantErr: `container "main": git.credentials[0].secretRef requires name and realm`,
		},
		{
			name: "container credential bad hostKeyPolicy",
			tail: `        git:
          credentials:
            - host: github.com
              secretRef: {name: k, realm: default}
              hostKeyPolicy: insecure
`,
			wantErr: `container "main": git.credentials[0].hostKeyPolicy "insecure"`,
		},
		{
			name: "container duplicate credential host",
			tail: `        git:
          credentials:
            - host: github.com
              secretRef: {name: a, realm: default}
            - host: github.com
              secretRef: {name: b, realm: default}
`,
			wantErr: "duplicate credential host",
		},
		{
			// A blueprint may ask for signing and leave the key to the Config
			// or a scope default; the merged block is checked at render.
			name: "container sign without key is left to the merged block",
			tail: `        git:
          sign: [commits]
`,
		},
		{
			name: "repo credential names a host",
			tail: `        repos:
          - name: src
            target: /workspace/src
            url: git@github.com:o/r.git
            git:
              credential:
                host: github.com
                secretRef: {name: repo-key, realm: default}
`,
			wantErr: `container "main" repo "src": git.credential.host must be empty`,
		},
		{
			name: "repo identity half set",
			tail: `        repos:
          - name: src
            target: /workspace/src
            git:
              author: {name: Repo Dev}
`,
			wantErr: `repo "src": git.author requires both name and email`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := parser.ValidateDocument(parseBlueprint(t, gitBlueprintHead+tt.tail))
			requireGitValidation(t, err, tt.wantErr)
		})
	}
}

const gitConfigHead = `apiVersion: v1beta1
kind: CellConfig
metadata:
  name: kukeon-dev
  realm: default
spec:
  blueprint:
    name: dev
    realm: default
`

// TestValidateDocument_Config_Git covers a CellConfig's git override and each
// repo fill's own identity (#1472, steps 6-7).
func TestValidateDocument_Config_Git(t *testing.T) {
	tests := []struct {
		name    string
		tail    string
		wantErr string
	}{
		{
			name: "config git and repo fill git",
			tail: `  git:
    author: {name: Dev, email: dev@example.com}
    credentials:
      - host: github.com
        secretRef: {name: gh-key, realm: default}
  repos:
    project:
      url: git@github.com:o/r.git
      git:
        committer: {name: Bot, email: bot@example.com}
        signing:
          secretRef: {name: signing-key, realm: default}
        sign: [commits]
`,
		},
		{
			name: "config git credential missing host",
			tail: `  git:
    credentials:
      - secretRef: {name: gh-key, realm: default}
`,
			wantErr: "git.credentials[0].host is required",
		},
		{
			name: "config git signing ref without realm",
			tail: `  git:
    signing:
      secretRef: {name: signing-key}
`,
			wantErr: "git.signing.secretRef requires name and realm",
		},
		{
			name: "repo fill git unknown sign target",
			tail: `  repos:
    project:
      url: git@github.com:o/r.git
      git:
        sign: [branches]
`,
			wantErr: `repos["project"]: git.sign "branches"`,
		},
		{
			name: "repo fill credential port out of range",
			tail: `  repos:
    project:
      url: ssh://git@git.example.com/o/r.git
      git:
        credential:
          secretRef: {name: k, realm: default}
          port: 99999
`,
			wantErr: `repos["project"]: git.credential.port 99999`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := parser.ValidateDocument(parseConfig(t, gitConfigHead+tt.tail))
			requireGitValidation(t, err, tt.wantErr)
		})
	}
}

func requireGitValidation(t *testing.T, err *parser.ValidationError, want string) {
	t.Helper()
	if want == "" {
		if err != nil {
			t.Fatalf("unexpected validation error: %v", err)
		}
		return
	}
	if err == nil {
		t.Fatalf("expected validation error containing %q, got nil", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want substring %q", err, want)
	}
}
