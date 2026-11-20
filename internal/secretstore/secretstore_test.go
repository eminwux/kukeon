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

package secretstore_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/secretstore"
	"github.com/eminwux/kukeon/internal/util/fs"
)

const testPrivateKey = "-----BEGIN OPENSSH PRIVATE" + " KEY-----\nc2VjcmV0LWtleS1ib2R5\n-----END OPENSSH PRIVATE" + " KEY-----\n"

func TestValidateSpec(t *testing.T) {
	basic := func(u, p, tok string) *intmodel.SecretBasicAuth {
		return &intmodel.SecretBasicAuth{Username: u, Password: p, Token: tok}
	}
	key := func(mut func(*intmodel.SecretSSHKey)) *intmodel.SecretSSHKey {
		k := &intmodel.SecretSSHKey{PrivateKey: testPrivateKey}
		if mut != nil {
			mut(k)
		}
		return k
	}
	tests := []struct {
		name    string
		spec    intmodel.SecretSpec
		wantErr error
	}{
		{name: "opaque default type", spec: intmodel.SecretSpec{Data: "v"}},
		{name: "opaque explicit", spec: intmodel.SecretSpec{Type: intmodel.SecretTypeOpaque, Data: "v"}},
		{name: "opaque empty data", spec: intmodel.SecretSpec{Data: "  \n"}, wantErr: errdefs.ErrSecretDataRequired},
		{
			name:    "opaque with basicAuth block",
			spec:    intmodel.SecretSpec{Data: "v", BasicAuth: basic("u", "p", "")},
			wantErr: errdefs.ErrSecretTypeFields,
		},
		{
			name:    "opaque with sshKey block",
			spec:    intmodel.SecretSpec{Data: "v", SSHKey: key(nil)},
			wantErr: errdefs.ErrSecretTypeFields,
		},
		{
			name: "basic-auth password",
			spec: intmodel.SecretSpec{Type: intmodel.SecretTypeBasicAuth, BasicAuth: basic("u", "p", "")},
		},
		{
			name: "basic-auth token",
			spec: intmodel.SecretSpec{Type: intmodel.SecretTypeBasicAuth, BasicAuth: basic("u", "", "t")},
		},
		{
			name:    "basic-auth missing block",
			spec:    intmodel.SecretSpec{Type: intmodel.SecretTypeBasicAuth},
			wantErr: errdefs.ErrSecretTypeFields,
		},
		{
			name:    "basic-auth with data",
			spec:    intmodel.SecretSpec{Type: intmodel.SecretTypeBasicAuth, Data: "v", BasicAuth: basic("u", "p", "")},
			wantErr: errdefs.ErrSecretTypeFields,
		},
		{
			name: "basic-auth with sshKey",
			spec: intmodel.SecretSpec{
				Type: intmodel.SecretTypeBasicAuth, BasicAuth: basic("u", "p", ""), SSHKey: key(nil),
			},
			wantErr: errdefs.ErrSecretTypeFields,
		},
		{
			name:    "basic-auth missing username",
			spec:    intmodel.SecretSpec{Type: intmodel.SecretTypeBasicAuth, BasicAuth: basic(" ", "p", "")},
			wantErr: errdefs.ErrSecretTypeFields,
		},
		{
			name:    "basic-auth neither password nor token",
			spec:    intmodel.SecretSpec{Type: intmodel.SecretTypeBasicAuth, BasicAuth: basic("u", "", "")},
			wantErr: errdefs.ErrSecretTypeFields,
		},
		{
			name:    "basic-auth both password and token",
			spec:    intmodel.SecretSpec{Type: intmodel.SecretTypeBasicAuth, BasicAuth: basic("u", "p", "t")},
			wantErr: errdefs.ErrSecretTypeFields,
		},
		{
			name:    "basic-auth multi-line password",
			spec:    intmodel.SecretSpec{Type: intmodel.SecretTypeBasicAuth, BasicAuth: basic("u", "p\nq", "")},
			wantErr: errdefs.ErrSecretTypeFields,
		},
		{name: "ssh-key minimal", spec: intmodel.SecretSpec{Type: intmodel.SecretTypeSSHKey, SSHKey: key(nil)}},
		{
			name: "ssh-key full",
			spec: intmodel.SecretSpec{Type: intmodel.SecretTypeSSHKey, SSHKey: key(func(k *intmodel.SecretSSHKey) {
				k.Passphrase = "pp"
				k.PublicKey = "ssh-ed25519 AAAA comment\n"
				k.Certificate = "ssh-ed25519-cert-v01@openssh.com AAAA"
				k.KnownHosts = "github.com ssh-ed25519 AAAA\ngitlab.com ssh-ed25519 BBBB\n"
			})},
		},
		{
			name:    "ssh-key missing block",
			spec:    intmodel.SecretSpec{Type: intmodel.SecretTypeSSHKey},
			wantErr: errdefs.ErrSecretTypeFields,
		},
		{
			name:    "ssh-key with data",
			spec:    intmodel.SecretSpec{Type: intmodel.SecretTypeSSHKey, Data: "v", SSHKey: key(nil)},
			wantErr: errdefs.ErrSecretTypeFields,
		},
		{
			name: "ssh-key with basicAuth",
			spec: intmodel.SecretSpec{
				Type: intmodel.SecretTypeSSHKey, SSHKey: key(nil), BasicAuth: basic("u", "p", ""),
			},
			wantErr: errdefs.ErrSecretTypeFields,
		},
		{
			name: "ssh-key not a private key",
			spec: intmodel.SecretSpec{
				Type: intmodel.SecretTypeSSHKey, SSHKey: &intmodel.SecretSSHKey{PrivateKey: "ssh-ed25519 AAAA"},
			},
			wantErr: errdefs.ErrSecretTypeFields,
		},
		{
			name: "ssh-key multi-line passphrase",
			spec: intmodel.SecretSpec{Type: intmodel.SecretTypeSSHKey, SSHKey: key(func(k *intmodel.SecretSSHKey) {
				k.Passphrase = "a\nb"
			})},
			wantErr: errdefs.ErrSecretTypeFields,
		},
		{
			name: "ssh-key multi-line public key",
			spec: intmodel.SecretSpec{Type: intmodel.SecretTypeSSHKey, SSHKey: key(func(k *intmodel.SecretSSHKey) {
				k.PublicKey = "ssh-ed25519 AAAA\nssh-ed25519 BBBB"
			})},
			wantErr: errdefs.ErrSecretTypeFields,
		},
		{
			name: "ssh-key multi-line certificate",
			spec: intmodel.SecretSpec{Type: intmodel.SecretTypeSSHKey, SSHKey: key(func(k *intmodel.SecretSSHKey) {
				k.Certificate = "a\nb"
			})},
			wantErr: errdefs.ErrSecretTypeFields,
		},
		{
			name:    "unknown type",
			spec:    intmodel.SecretSpec{Type: "oauth", Data: "v"},
			wantErr: errdefs.ErrSecretTypeUnknown,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := secretstore.ValidateSpec(tt.spec)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("ValidateSpec: unexpected error %v", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ValidateSpec error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// TestValidateSpec_ErrorsNeverEchoMaterial locks that a rejected spec's error
// names the offending field but never the value supplied for it.
func TestValidateSpec_ErrorsNeverEchoMaterial(t *testing.T) {
	const material = "s3cr3t-material-do-not-leak"
	specs := []intmodel.SecretSpec{
		{Type: intmodel.SecretTypeBasicAuth, BasicAuth: &intmodel.SecretBasicAuth{
			Username: "u", Password: material, Token: material,
		}},
		{Type: intmodel.SecretTypeBasicAuth, BasicAuth: &intmodel.SecretBasicAuth{
			Username: "u", Password: material + "\n" + material,
		}},
		{Type: intmodel.SecretTypeSSHKey, SSHKey: &intmodel.SecretSSHKey{PrivateKey: material}},
		{Type: intmodel.SecretTypeSSHKey, SSHKey: &intmodel.SecretSSHKey{
			PrivateKey: testPrivateKey, Passphrase: material + "\n",
		}},
		{Type: intmodel.SecretTypeBasicAuth, Data: material, BasicAuth: &intmodel.SecretBasicAuth{Username: "u"}},
	}
	for i, spec := range specs {
		err := secretstore.ValidateSpec(spec)
		if err == nil {
			t.Fatalf("spec %d: expected an error", i)
		}
		if strings.Contains(err.Error(), material) {
			t.Errorf("spec %d: error echoes secret material: %v", i, err)
		}
	}
}

func TestEncode_OpaqueIsRawBytes(t *testing.T) {
	got, err := secretstore.Encode(intmodel.SecretSpec{Data: `{"basicAuth":{"username":"x"}}`})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if string(got) != `{"basicAuth":{"username":"x"}}` {
		t.Errorf("opaque Encode = %q, want the raw data verbatim", got)
	}
}

// writeSecret persists spec the way the storage runner does: type sidecar
// first, then the bytes file.
func writeSecret(t *testing.T, runPath string, md intmodel.SecretMetadata, spec intmodel.SecretSpec) {
	t.Helper()
	body, err := secretstore.Encode(spec)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	path := fs.SecretPath(runPath, md.Realm, md.Space, md.Stack, md.Cell, md.Name)
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err = secretstore.WriteType(runPath, md, spec.EffectiveType()); err != nil {
		t.Fatalf("WriteType: %v", err)
	}
	if err = os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
}

func refOf(md intmodel.SecretMetadata) intmodel.ContainerSecretRef {
	return intmodel.ContainerSecretRef(md)
}

func TestEncodeReadRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		md   intmodel.SecretMetadata
		spec intmodel.SecretSpec
	}{
		{
			name: "opaque realm scope",
			md:   intmodel.SecretMetadata{Name: "tok", Realm: "r"},
			spec: intmodel.SecretSpec{Type: intmodel.SecretTypeOpaque, Data: "raw-value\n"},
		},
		{
			name: "basic-auth stack scope",
			md:   intmodel.SecretMetadata{Name: "https", Realm: "r", Space: "s", Stack: "st"},
			spec: intmodel.SecretSpec{
				Type:      intmodel.SecretTypeBasicAuth,
				BasicAuth: &intmodel.SecretBasicAuth{Username: "git", Token: "tok-123"},
			},
		},
		{
			name: "ssh-key cell scope",
			md:   intmodel.SecretMetadata{Name: "key", Realm: "r", Space: "s", Stack: "st", Cell: "c"},
			spec: intmodel.SecretSpec{
				Type: intmodel.SecretTypeSSHKey,
				SSHKey: &intmodel.SecretSSHKey{
					PrivateKey: testPrivateKey,
					Passphrase: "pp",
					PublicKey:  "ssh-ed25519 AAAA me",
					KnownHosts: "example.com ssh-ed25519 AAAA\n",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runPath := t.TempDir()
			writeSecret(t, runPath, tt.md, tt.spec)

			gotType, err := secretstore.ReadType(runPath, tt.md)
			if err != nil {
				t.Fatalf("ReadType: %v", err)
			}
			if gotType != tt.spec.Type {
				t.Errorf("ReadType = %q, want %q", gotType, tt.spec.Type)
			}

			got, err := secretstore.Read(runPath, refOf(tt.md))
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if got.Type != tt.spec.Type || got.Data != tt.spec.Data {
				t.Errorf("Read = type %q data %q, want type %q data %q", got.Type, got.Data, tt.spec.Type, tt.spec.Data)
			}
			if (got.BasicAuth == nil) != (tt.spec.BasicAuth == nil) ||
				(got.BasicAuth != nil && *got.BasicAuth != *tt.spec.BasicAuth) {
				t.Errorf("Read BasicAuth = %+v, want %+v", got.BasicAuth, tt.spec.BasicAuth)
			}
			if (got.SSHKey == nil) != (tt.spec.SSHKey == nil) ||
				(got.SSHKey != nil && *got.SSHKey != *tt.spec.SSHKey) {
				t.Errorf("Read SSHKey mismatch")
			}
		})
	}
}

// TestRead_OpaqueJSONIsNeverMisreadAsTyped locks the reason the type lives in
// a sidecar: an opaque secret whose bytes happen to be the typed JSON shape
// still reads back as opaque data.
func TestRead_OpaqueJSONIsNeverMisreadAsTyped(t *testing.T) {
	runPath := t.TempDir()
	md := intmodel.SecretMetadata{Name: "blob", Realm: "r"}
	data := `{"basicAuth":{"username":"u","password":"p"}}`
	writeSecret(t, runPath, md, intmodel.SecretSpec{Data: data})

	got, err := secretstore.Read(runPath, refOf(md))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Type != intmodel.SecretTypeOpaque || got.Data != data || got.BasicAuth != nil {
		t.Errorf("Read = %+v, want opaque with the raw JSON as data", got)
	}
}

func TestWriteType_Lifecycle(t *testing.T) {
	runPath := t.TempDir()
	md := intmodel.SecretMetadata{Name: "key", Realm: "r", Space: "s"}

	// No sidecar: opaque, and removing/clearing a missing one is not an error.
	if got, err := secretstore.ReadType(runPath, md); err != nil || got != intmodel.SecretTypeOpaque {
		t.Fatalf("ReadType without sidecar = %q, %v; want opaque", got, err)
	}
	if err := secretstore.RemoveType(runPath, md); err != nil {
		t.Fatalf("RemoveType on missing sidecar: %v", err)
	}
	if err := secretstore.WriteType(runPath, md, intmodel.SecretTypeOpaque); err != nil {
		t.Fatalf("WriteType(opaque) on missing sidecar: %v", err)
	}

	if err := secretstore.WriteType(runPath, md, intmodel.SecretTypeSSHKey); err != nil {
		t.Fatalf("WriteType: %v", err)
	}
	path := secretstore.TypePath(runPath, md)
	if want := filepath.Join(fs.SecretsDir(runPath, "r", "s", "", ""), secretstore.TypesDir, "key"); path != want {
		t.Errorf("TypePath = %q, want %q", path, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat sidecar: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("sidecar mode = %o, want 600", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat types dir: %v", err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Errorf("types dir mode = %o, want 700", dirInfo.Mode().Perm())
	}
	if got, readErr := secretstore.ReadType(runPath, md); readErr != nil || got != intmodel.SecretTypeSSHKey {
		t.Fatalf("ReadType = %q, %v; want ssh-key", got, readErr)
	}
	// No temp files are left next to the sidecar.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("read types dir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("types dir holds %d entries, want only the sidecar", len(entries))
	}

	// Re-typing replaces the marker; writing opaque clears it.
	if err = secretstore.WriteType(runPath, md, intmodel.SecretTypeBasicAuth); err != nil {
		t.Fatalf("WriteType retype: %v", err)
	}
	if got, _ := secretstore.ReadType(runPath, md); got != intmodel.SecretTypeBasicAuth {
		t.Errorf("ReadType after retype = %q, want basic-auth", got)
	}
	if err = secretstore.WriteType(runPath, md, ""); err != nil {
		t.Fatalf("WriteType clear: %v", err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("sidecar still present after writing opaque: %v", statErr)
	}

	if err = secretstore.WriteType(runPath, md, intmodel.SecretTypeSSHKey); err != nil {
		t.Fatalf("WriteType: %v", err)
	}
	if err = secretstore.RemoveType(runPath, md); err != nil {
		t.Fatalf("RemoveType: %v", err)
	}
	if got, _ := secretstore.ReadType(runPath, md); got != intmodel.SecretTypeOpaque {
		t.Errorf("ReadType after RemoveType = %q, want opaque", got)
	}
}

func TestReadType_UnknownStoredType(t *testing.T) {
	runPath := t.TempDir()
	md := intmodel.SecretMetadata{Name: "odd", Realm: "r"}
	path := secretstore.TypePath(runPath, md)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("oauth\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := secretstore.ReadType(runPath, md); !errors.Is(err, errdefs.ErrSecretTypeUnknown) {
		t.Fatalf("ReadType error = %v, want ErrSecretTypeUnknown", err)
	}
}

func TestRead_NotFound(t *testing.T) {
	runPath := t.TempDir()
	_, err := secretstore.Read(runPath, intmodel.ContainerSecretRef{Name: "absent", Realm: "r", Space: "s"})
	if !errors.Is(err, errdefs.ErrSecretRefNotFound) {
		t.Fatalf("Read error = %v, want ErrSecretRefNotFound", err)
	}
	if !strings.Contains(err.Error(), `"absent"`) {
		t.Errorf("error should name the secret: %v", err)
	}
}

// TestRead_ErrorsNeverEchoMaterial covers the two failure modes that hold the
// stored bytes in hand: a typed marker over bytes that are not the typed JSON
// (the crash window WriteType documents), and typed JSON that fails validation.
func TestRead_ErrorsNeverEchoMaterial(t *testing.T) {
	const material = "s3cr3t-material-do-not-leak"
	tests := []struct {
		name string
		body string
	}{
		{name: "undecodable bytes under a typed marker", body: material},
		{
			name: "decodable but invalid",
			body: `{"basicAuth":{"username":"u","password":"` + material + `","token":"` + material + `"}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runPath := t.TempDir()
			md := intmodel.SecretMetadata{Name: "cred", Realm: "r"}
			path := fs.SecretPath(runPath, md.Realm, "", "", "", md.Name)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := secretstore.WriteType(runPath, md, intmodel.SecretTypeBasicAuth); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tt.body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := secretstore.Read(runPath, refOf(md))
			if err == nil {
				t.Fatal("Read: expected an error")
			}
			if strings.Contains(err.Error(), material) {
				t.Errorf("Read error echoes secret material: %v", err)
			}
			if !strings.Contains(err.Error(), `"cred"`) {
				t.Errorf("Read error should name the secret: %v", err)
			}
		})
	}
}

func TestValidateMeta(t *testing.T) {
	opaque := func(connector string, identity map[string]string) intmodel.SecretSpec {
		return intmodel.SecretSpec{Data: "token", Connector: connector, Identity: identity}
	}
	for _, tc := range []struct {
		name string
		spec intmodel.SecretSpec
		want string // empty: accepted
	}{
		{"untagged", opaque("", nil), ""},
		{"tagged", opaque("forge", map[string]string{"account": "work", "hosts": "a.example, b.example"}), ""},
		{"tag is not an identifier", opaque("Forge Hub", nil), "spec.connector"},
		{"identity without a tag", opaque("", map[string]string{"account": "work"}), "spec.identity requires spec.connector"},
		{"field is not an identifier", opaque("forge", map[string]string{"my field": "x"}), "is not an identifier"},
		{"multi-line value", opaque("forge", map[string]string{"account": "a\nb"}), "must be a single line"},
		{"oversized value", opaque("forge", map[string]string{"account": strings.Repeat("a", 1025)}), "at most 1024 bytes"},
		{
			"key material in a readable field",
			opaque("forge", map[string]string{"account": "-----BEGIN OPENSSH PRIVATE" + " KEY-----"}),
			"looks like key material",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// ValidateSpec applies the same checks on every create.
			for _, err := range []error{secretstore.ValidateMeta(tc.spec), secretstore.ValidateSpec(tc.spec)} {
				if tc.want == "" {
					if err != nil {
						t.Fatal(err)
					}
					continue
				}
				if !errors.Is(err, errdefs.ErrSecretTypeFields) || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("err = %v, want %q", err, tc.want)
				}
				if strings.Contains(err.Error(), "PRIVATE KEY") {
					t.Fatalf("error echoes the value: %v", err)
				}
			}
		})
	}
}

// The identity sidecar records the readable facts of a secret — and only
// those — next to its bytes, with the same lifecycle as the type sidecar.
func TestWriteMeta_Lifecycle(t *testing.T) {
	runPath := t.TempDir()
	md := intmodel.SecretMetadata{Name: "key", Realm: "r", Space: "s"}

	if got, err := secretstore.ReadMeta(runPath, md); err != nil || !got.IsZero() {
		t.Fatalf("ReadMeta without sidecar = %+v, %v; want zero", got, err)
	}
	if err := secretstore.RemoveMeta(runPath, md); err != nil {
		t.Fatalf("RemoveMeta on missing sidecar: %v", err)
	}
	if err := secretstore.WriteMeta(runPath, md, secretstore.Meta{}); err != nil {
		t.Fatalf("WriteMeta(zero) on missing sidecar: %v", err)
	}

	want := secretstore.Meta{Connector: "forge", Identity: map[string]string{"account": "work"}}
	if err := secretstore.WriteMeta(runPath, md, want); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	path := secretstore.MetaPath(runPath, md)
	if dir := filepath.Join(fs.SecretsDir(runPath, "r", "s", "", ""), secretstore.MetaDir); filepath.Dir(path) != dir {
		t.Errorf("MetaPath = %q, want it under %q", path, dir)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat sidecar: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("sidecar mode = %o, want 600", info.Mode().Perm())
	}
	if dirInfo, statErr := os.Stat(filepath.Dir(path)); statErr != nil || dirInfo.Mode().Perm() != 0o700 {
		t.Errorf("meta dir = %v, %v; want mode 700", dirInfo, statErr)
	}
	got, err := secretstore.ReadMeta(runPath, md)
	if err != nil || got.Connector != want.Connector || got.Identity["account"] != "work" || len(got.Identity) != 1 {
		t.Fatalf("ReadMeta = %+v, %v", got, err)
	}
	if entries, readErr := os.ReadDir(filepath.Dir(path)); readErr != nil || len(entries) != 1 {
		t.Errorf("meta dir holds %d entries (%v), want only the sidecar", len(entries), readErr)
	}

	// Re-tagging replaces the sidecar; the zero Meta clears it.
	if err = secretstore.WriteMeta(runPath, md, secretstore.Meta{Connector: "other"}); err != nil {
		t.Fatalf("WriteMeta retag: %v", err)
	}
	if got, _ = secretstore.ReadMeta(runPath, md); got.Connector != "other" || len(got.Identity) != 0 {
		t.Errorf("ReadMeta after retag = %+v", got)
	}
	if err = secretstore.WriteMeta(runPath, md, secretstore.Meta{}); err != nil {
		t.Fatalf("WriteMeta clear: %v", err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("sidecar still present after clearing: %v", statErr)
	}

	if err = secretstore.WriteMeta(runPath, md, want); err != nil {
		t.Fatal(err)
	}
	if err = secretstore.RemoveMeta(runPath, md); err != nil {
		t.Fatalf("RemoveMeta: %v", err)
	}
	if got, _ = secretstore.ReadMeta(runPath, md); !got.IsZero() {
		t.Errorf("ReadMeta after RemoveMeta = %+v", got)
	}

	// A damaged sidecar is an error, not a silently untagged secret.
	if err = os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = secretstore.ReadMeta(runPath, md); err == nil || !strings.Contains(err.Error(), "unreadable") {
		t.Fatalf("ReadMeta on a damaged sidecar: %v", err)
	}
}
