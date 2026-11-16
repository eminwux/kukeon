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

package secret_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/kuke/create/secret"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/viper"
)

const typedTestPrivateKey = "-----BEGIN OPENSSH PRIVATE" + " KEY-----\nYm9keQ==\n-----END OPENSSH PRIVATE" + " KEY-----\n"

// runTypedCreate executes `kuke create secret` with args and stdin against a
// fake daemon, returning the document the CLI sent (nil when it never called
// the daemon), the command output and the error.
func runTypedCreate(t *testing.T, stdin string, args ...string) (*v1beta1.SecretDoc, string, error) {
	t.Helper()
	t.Cleanup(viper.Reset)

	var sent *v1beta1.SecretDoc
	fake := &fakeClient{createSecretFn: func(doc v1beta1.SecretDoc) (kukeonv1.CreateSecretResult, error) {
		sent = &doc
		// The daemon's reply is metadata-only: the type, never the material.
		reply := doc
		reply.Spec = v1beta1.SecretSpec{Type: doc.Spec.Type}
		return kukeonv1.CreateSecretResult{Secret: reply, Created: true}, nil
	}}

	cmd := secret.NewSecretCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetIn(strings.NewReader(stdin))
	ctx := context.WithValue(
		context.Background(), types.CtxLogger, slog.New(slog.NewTextHandler(io.Discard, nil)))
	cmd.SetContext(context.WithValue(ctx, secret.MockControllerKey{}, kukeonv1.Client(fake)))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return sent, out.String(), err
}

func writeTypedFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// TestCreateSecret_BasicAuth covers each basic-auth value source: exactly one
// of password/token is filled, the trailing newline a pipe or editor adds is
// dropped, and the value never has to appear on the command line.
func TestCreateSecret_BasicAuth(t *testing.T) {
	tokenFile := writeTypedFile(t, "token", "tok-from-file\n")
	passwordFile := writeTypedFile(t, "password", "pw-from-file\r\n")

	tests := []struct {
		name         string
		stdin        string
		args         []string
		wantPassword string
		wantToken    string
	}{
		{
			name:  "password from stdin",
			stdin: "pw-from-stdin\n",
			args:  []string{"--password-stdin"}, wantPassword: "pw-from-stdin",
		},
		{
			name:  "token from stdin",
			stdin: "tok-from-stdin\n",
			args:  []string{"--token-stdin"}, wantToken: "tok-from-stdin",
		},
		{name: "token from file", args: []string{"--token-file", tokenFile}, wantToken: "tok-from-file"},
		{name: "password from file", args: []string{"--from-file", passwordFile}, wantPassword: "pw-from-file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{"forge", "--type", "basic-auth", "--username", " git "}, tt.args...)
			doc, out, err := runTypedCreate(t, tt.stdin, args...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if doc == nil {
				t.Fatal("CreateSecret was not called")
			}
			if doc.Spec.Type != v1beta1.SecretTypeBasicAuth || doc.Spec.Data != "" || doc.Spec.SSHKey != nil {
				t.Errorf("spec = %+v, want a basic-auth secret with only its block", doc.Spec)
			}
			ba := doc.Spec.BasicAuth
			if ba == nil || ba.Username != "git" || ba.Password != tt.wantPassword || ba.Token != tt.wantToken {
				t.Errorf("basicAuth = %+v, want user git password %q token %q", ba, tt.wantPassword, tt.wantToken)
			}
			if !strings.Contains(out, `Secret "forge"`) || !strings.Contains(out, "basic-auth") {
				t.Errorf("output %q should name the secret and its type", out)
			}
			for _, material := range []string{tt.wantPassword, tt.wantToken} {
				if material != "" && strings.Contains(out, material) {
					t.Errorf("output echoes secret material: %q", out)
				}
			}
		})
	}
}

// TestCreateSecret_SSHKey covers the ssh-key flags: the private key is kept
// byte-for-byte (OpenSSH rejects a key file missing its final newline), the
// one-line companions are trimmed, and known_hosts keeps its lines.
func TestCreateSecret_SSHKey(t *testing.T) {
	keyFile := writeTypedFile(t, "id_ed25519", typedTestPrivateKey)
	pubFile := writeTypedFile(t, "id_ed25519.pub", "ssh-ed25519 AAAA me@host\n")
	certFile := writeTypedFile(t, "id_ed25519-cert.pub", "ssh-ed25519-cert-v01@openssh.com AAAA\n")
	knownHosts := "git.example.com ssh-ed25519 AAAA\ngit2.example.com ssh-ed25519 BBBB\n"
	hostsFile := writeTypedFile(t, "known_hosts", knownHosts)
	passphraseFile := writeTypedFile(t, "passphrase", "pp-from-file\n")

	t.Run("key only", func(t *testing.T) {
		doc, out, err := runTypedCreate(t, "", "deploy", "--type", "ssh-key", "--from-file", keyFile)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		key := doc.Spec.SSHKey
		if doc.Spec.Type != v1beta1.SecretTypeSSHKey || key == nil || doc.Spec.Data != "" || doc.Spec.BasicAuth != nil {
			t.Fatalf("spec = %+v, want an ssh-key secret with only its block", doc.Spec)
		}
		if key.PrivateKey != typedTestPrivateKey {
			t.Errorf("private key was altered: %q", key.PrivateKey)
		}
		if key.Passphrase != "" || key.PublicKey != "" || key.Certificate != "" || key.KnownHosts != "" {
			t.Errorf("optional fields set without their flags: %+v", key)
		}
		if strings.Contains(out, "PRIVATE KEY") {
			t.Errorf("output echoes the private key: %q", out)
		}
	})

	t.Run("every companion file", func(t *testing.T) {
		doc, _, err := runTypedCreate(t, "", "deploy", "--type", "ssh-key", "--from-file", keyFile,
			"--passphrase-file", passphraseFile, "--public-key-file", pubFile,
			"--certificate-file", certFile, "--known-hosts-file", hostsFile,
			"--realm", "r", "--space", "s")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if doc.Metadata.Realm != "r" || doc.Metadata.Space != "s" {
			t.Errorf("scope = %+v, want realm r space s", doc.Metadata)
		}
		key := doc.Spec.SSHKey
		if key.Passphrase != "pp-from-file" {
			t.Errorf("passphrase = %q, want pp-from-file", key.Passphrase)
		}
		if key.PublicKey != "ssh-ed25519 AAAA me@host" {
			t.Errorf("public key = %q, want the trimmed single line", key.PublicKey)
		}
		if key.Certificate != "ssh-ed25519-cert-v01@openssh.com AAAA" {
			t.Errorf("certificate = %q, want the trimmed single line", key.Certificate)
		}
		if key.KnownHosts != knownHosts {
			t.Errorf("known hosts = %q, want every line kept", key.KnownHosts)
		}
	})

	t.Run("passphrase from stdin", func(t *testing.T) {
		doc, out, err := runTypedCreate(t, "pp-from-stdin\n",
			"deploy", "--type", "ssh-key", "--from-file", keyFile, "--passphrase-stdin")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if doc.Spec.SSHKey.Passphrase != "pp-from-stdin" {
			t.Errorf("passphrase = %q, want pp-from-stdin", doc.Spec.SSHKey.Passphrase)
		}
		if strings.Contains(out, "pp-from-stdin") {
			t.Errorf("output echoes the passphrase: %q", out)
		}
	})
}

// TestCreateSecret_TypedFlagRejections locks the flag-combination guards: a
// flag that belongs to another type, a missing or duplicated value source, an
// empty value and an unknown type all fail before the daemon is called.
func TestCreateSecret_TypedFlagRejections(t *testing.T) {
	keyFile := writeTypedFile(t, "id_ed25519", typedTestPrivateKey)
	tokenFile := writeTypedFile(t, "token", "tok\n")
	emptyFile := writeTypedFile(t, "empty", "\n")
	missing := filepath.Join(t.TempDir(), "absent")

	tests := []struct {
		name    string
		stdin   string
		args    []string
		wantErr string
	}{
		{
			name:    "unknown type",
			args:    []string{"--type", "oauth", "--from-literal=x"},
			wantErr: `--type "oauth": want opaque, basic-auth or ssh-key`,
		},
		{
			name:    "opaque with username",
			args:    []string{"--from-literal=x", "--username", "git"},
			wantErr: "--username does not apply to --type opaque",
		},
		{
			name:    "opaque with token file",
			args:    []string{"--from-literal=x", "--token-file", tokenFile},
			wantErr: "--token-file does not apply to --type opaque",
		},
		{
			name:    "opaque with passphrase",
			args:    []string{"--from-file", keyFile, "--passphrase-stdin"},
			wantErr: "--passphrase-stdin does not apply to --type opaque",
		},
		{
			name:    "basic-auth without username",
			stdin:   "pw\n",
			args:    []string{"--type", "basic-auth", "--password-stdin"},
			wantErr: "--type basic-auth requires --username",
		},
		{
			name:    "basic-auth without a value source",
			args:    []string{"--type", "basic-auth", "--username", "git"},
			wantErr: "requires exactly one of --password-stdin, --token-stdin",
		},
		{
			name:  "basic-auth with two value sources",
			stdin: "pw\n",
			args: []string{
				"--type",
				"basic-auth",
				"--username",
				"git",
				"--password-stdin",
				"--token-file",
				tokenFile,
			},
			wantErr: "requires exactly one of --password-stdin, --token-stdin",
		},
		{
			name:    "basic-auth with literal",
			args:    []string{"--type", "basic-auth", "--username", "git", "--from-literal=pw"},
			wantErr: "--from-literal does not apply to --type basic-auth",
		},
		{
			name:  "basic-auth with ssh-key flag",
			stdin: "pw\n",
			args: []string{
				"--type",
				"basic-auth",
				"--username",
				"git",
				"--password-stdin",
				"--known-hosts-file",
				keyFile,
			},
			wantErr: "--known-hosts-file does not apply to --type basic-auth",
		},
		{
			name:    "basic-auth empty stdin",
			stdin:   "\n",
			args:    []string{"--type", "basic-auth", "--username", "git", "--token-stdin"},
			wantErr: "stdin is empty",
		},
		{
			name:    "basic-auth empty token file",
			args:    []string{"--type", "basic-auth", "--username", "git", "--token-file", emptyFile},
			wantErr: "is empty",
		},
		{
			name:    "basic-auth missing token file",
			args:    []string{"--type", "basic-auth", "--username", "git", "--token-file", missing},
			wantErr: "read --token-file",
		},
		{
			name:    "ssh-key without key file",
			args:    []string{"--type", "ssh-key"},
			wantErr: "--type ssh-key requires --from-file <private key>",
		},
		{
			name:    "ssh-key missing key file",
			args:    []string{"--type", "ssh-key", "--from-file", missing},
			wantErr: "read --from-file",
		},
		{
			name:    "ssh-key with username",
			args:    []string{"--type", "ssh-key", "--from-file", keyFile, "--username", "git"},
			wantErr: "--username does not apply to --type ssh-key",
		},
		{
			name:    "ssh-key with token",
			stdin:   "tok\n",
			args:    []string{"--type", "ssh-key", "--from-file", keyFile, "--token-stdin"},
			wantErr: "--token-stdin does not apply to --type ssh-key",
		},
		{
			name:    "ssh-key with literal",
			args:    []string{"--type", "ssh-key", "--from-file", keyFile, "--from-literal=x"},
			wantErr: "--from-literal does not apply to --type ssh-key",
		},
		{
			name:  "ssh-key with both passphrase sources",
			stdin: "pp\n",
			args: []string{
				"--type", "ssh-key", "--from-file", keyFile, "--passphrase-stdin", "--passphrase-file", tokenFile,
			},
			wantErr: "--passphrase-stdin and --passphrase-file are mutually exclusive",
		},
		{
			name:    "ssh-key missing public key file",
			args:    []string{"--type", "ssh-key", "--from-file", keyFile, "--public-key-file", missing},
			wantErr: "read --public-key-file",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, _, err := runTypedCreate(t, tt.stdin, append([]string{"cred"}, tt.args...)...)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
			if doc != nil {
				t.Errorf("CreateSecret was called despite the rejected flags: %+v", doc.Metadata)
			}
		})
	}
}

// TestCreateSecret_ExplicitOpaqueType confirms --type opaque is the default
// path spelled out: the document carries no type-specific block.
func TestCreateSecret_ExplicitOpaqueType(t *testing.T) {
	doc, _, err := runTypedCreate(t, "", "plain", "--type", "opaque", "--from-literal=KEY=value")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if doc.Spec.Data != "value" || doc.Spec.BasicAuth != nil || doc.Spec.SSHKey != nil {
		t.Errorf("spec = %+v, want opaque data only", doc.Spec)
	}
}
