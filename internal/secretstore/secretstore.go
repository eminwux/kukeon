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

// Package secretstore is the on-disk encoding of a kind: Secret (issue #619,
// typed in #1472). An opaque secret is the raw bytes file it has always been.
// A typed secret (basic-auth, ssh-key) stores its fields as one JSON object in
// the same file and records its type in a sidecar under TypesDir, so the type
// can be reported by `kuke get secret` without reading the material and an
// opaque secret whose bytes happen to be JSON is never misread as typed.
package secretstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
)

// TypesDir is the per-scope sidecar directory (under the secrets directory)
// holding one file per typed secret whose content is the secret's type. It is
// a directory so the secret listing, which skips directories, never reports it.
const TypesDir = ".types"

// typeFileMode matches the secret bytes file: root read/write only.
const typeFileMode os.FileMode = 0o600

// typedMaterial is the JSON body of a typed secret's bytes file.
type typedMaterial struct {
	BasicAuth *intmodel.SecretBasicAuth `json:"basicAuth,omitempty"`
	SSHKey    *intmodel.SecretSSHKey    `json:"sshKey,omitempty"`
}

// ValidateSpec enforces the per-type shape of a secret's material.
func ValidateSpec(spec intmodel.SecretSpec) error {
	switch spec.EffectiveType() {
	case intmodel.SecretTypeOpaque:
		if spec.BasicAuth != nil || spec.SSHKey != nil {
			return fmt.Errorf("%w: an opaque secret sets spec.data only", errdefs.ErrSecretTypeFields)
		}
		if strings.TrimSpace(spec.Data) == "" {
			return errdefs.ErrSecretDataRequired
		}
	case intmodel.SecretTypeBasicAuth:
		if spec.Data != "" || spec.SSHKey != nil || spec.BasicAuth == nil {
			return fmt.Errorf("%w: a basic-auth secret sets spec.basicAuth only", errdefs.ErrSecretTypeFields)
		}
		ba := spec.BasicAuth
		if strings.TrimSpace(ba.Username) == "" {
			return fmt.Errorf("%w: spec.basicAuth.username is required", errdefs.ErrSecretTypeFields)
		}
		if (ba.Password == "") == (ba.Token == "") {
			return fmt.Errorf(
				"%w: spec.basicAuth requires exactly one of password or token", errdefs.ErrSecretTypeFields)
		}
		if strings.ContainsAny(ba.Username+ba.Password+ba.Token, "\n\r\x00") {
			return fmt.Errorf(
				"%w: spec.basicAuth fields must be single-line", errdefs.ErrSecretTypeFields)
		}
	case intmodel.SecretTypeSSHKey:
		if spec.Data != "" || spec.BasicAuth != nil || spec.SSHKey == nil {
			return fmt.Errorf("%w: an ssh-key secret sets spec.sshKey only", errdefs.ErrSecretTypeFields)
		}
		key := spec.SSHKey
		if !strings.Contains(key.PrivateKey, "PRIVATE KEY-----") {
			return fmt.Errorf(
				"%w: spec.sshKey.privateKey must be an OpenSSH or PEM private key", errdefs.ErrSecretTypeFields)
		}
		if strings.ContainsAny(key.Passphrase, "\n\r\x00") {
			return fmt.Errorf(
				"%w: spec.sshKey.passphrase must be single-line", errdefs.ErrSecretTypeFields)
		}
		for _, line := range []struct{ field, value string }{
			{"publicKey", key.PublicKey}, {"certificate", key.Certificate},
		} {
			if strings.ContainsAny(strings.TrimSpace(line.value), "\n\r\x00") {
				return fmt.Errorf(
					"%w: spec.sshKey.%s must be a single line", errdefs.ErrSecretTypeFields, line.field)
			}
		}
	default:
		return fmt.Errorf("%w (got %q)", errdefs.ErrSecretTypeUnknown, spec.Type)
	}
	return nil
}

// Encode returns the bytes written to the secret's file: the raw data of an
// opaque secret, or the JSON body of a typed one.
func Encode(spec intmodel.SecretSpec) ([]byte, error) {
	if spec.EffectiveType() == intmodel.SecretTypeOpaque {
		return []byte(spec.Data), nil
	}
	return json.Marshal(typedMaterial{BasicAuth: spec.BasicAuth, SSHKey: spec.SSHKey})
}

// TypePath returns the sidecar file recording a typed secret's type.
func TypePath(runPath string, md intmodel.SecretMetadata) string {
	return filepath.Join(
		fs.SecretsDir(runPath, md.Realm, md.Space, md.Stack, md.Cell), TypesDir, md.Name)
}

// WriteType records (or, for an opaque secret, clears) the type sidecar. It is
// called before the bytes file is replaced, so a crash in between leaves a
// typed marker over stale bytes — a decode error — rather than typed material
// readable as an opaque value.
func WriteType(runPath string, md intmodel.SecretMetadata, secretType string) error {
	path := TypePath(runPath, md)
	if secretType == "" || secretType == intmodel.SecretTypeOpaque {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("clear secret type: %w", err)
		}
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create secret types dir: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // a directory: 0700 is owner-only traversal
		return fmt.Errorf("chmod secret types dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".type-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp secret type: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err = tmp.WriteString(secretType); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write secret type: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close secret type: %w", err)
	}
	if err = os.Chmod(tmpName, typeFileMode); err != nil {
		return fmt.Errorf("chmod secret type: %w", err)
	}
	if err = os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename secret type into place: %w", err)
	}
	return nil
}

// RemoveType deletes the type sidecar; a missing sidecar is not an error.
func RemoveType(runPath string, md intmodel.SecretMetadata) error {
	if err := os.Remove(TypePath(runPath, md)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// ReadType returns the secret's type without reading its material: opaque when
// no sidecar exists.
func ReadType(runPath string, md intmodel.SecretMetadata) (string, error) {
	raw, err := os.ReadFile(TypePath(runPath, md))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return intmodel.SecretTypeOpaque, nil
		}
		return "", err
	}
	secretType := strings.TrimSpace(string(raw))
	switch secretType {
	case intmodel.SecretTypeBasicAuth, intmodel.SecretTypeSSHKey:
		return secretType, nil
	default:
		return "", fmt.Errorf("%w (stored type %q)", errdefs.ErrSecretTypeUnknown, secretType)
	}
}

// Read loads a referenced secret's full material. A missing bytes file returns
// errdefs.ErrSecretRefNotFound naming the secret and its scope path; the
// material itself never appears in an error.
func Read(runPath string, ref intmodel.ContainerSecretRef) (intmodel.SecretSpec, error) {
	md := intmodel.SecretMetadata(ref)
	path := fs.SecretPath(runPath, md.Realm, md.Space, md.Stack, md.Cell, md.Name)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return intmodel.SecretSpec{}, fmt.Errorf(
				"%w (secret %q at %q)", errdefs.ErrSecretRefNotFound, ref.Name, path)
		}
		return intmodel.SecretSpec{}, fmt.Errorf("failed to read referenced secret %q from %q: %w", ref.Name, path, err)
	}
	secretType, err := ReadType(runPath, md)
	if err != nil {
		return intmodel.SecretSpec{}, fmt.Errorf("referenced secret %q: %w", ref.Name, err)
	}
	if secretType == intmodel.SecretTypeOpaque {
		return intmodel.SecretSpec{Type: secretType, Data: string(data)}, nil
	}
	var body typedMaterial
	if err = json.Unmarshal(data, &body); err != nil {
		// The decode error can quote the bytes it choked on; never forward it.
		return intmodel.SecretSpec{}, fmt.Errorf(
			"referenced secret %q: stored %s material is unreadable; re-create the secret", ref.Name, secretType)
	}
	spec := intmodel.SecretSpec{Type: secretType, BasicAuth: body.BasicAuth, SSHKey: body.SSHKey}
	if err = ValidateSpec(spec); err != nil {
		return intmodel.SecretSpec{}, fmt.Errorf("referenced secret %q: %w", ref.Name, err)
	}
	return spec, nil
}
