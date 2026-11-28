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

// Package cloudhost is the host side of the kukeon cloud link (issue #1755)
// that `kuke cloud` and `kuke serve --cloud-link` share: the enrolled
// credential, kept as a Secret in kuke-system, and the link status file a
// running `kuke serve` writes for `kuke cloud status`.
package cloudhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/cloudlink"
)

// SecretReader reads a Secret with its material.
type SecretReader interface {
	ReadSecret(context.Context, v1beta1.SecretDoc) (kukeonv1.GetSecretResult, error)
}

// SecretWriter writes a Secret.
type SecretWriter interface {
	CreateSecret(context.Context, v1beta1.SecretDoc) (kukeonv1.CreateSecretResult, error)
}

func secretDoc() v1beta1.SecretDoc {
	return v1beta1.SecretDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindSecret,
		Metadata: v1beta1.SecretMetadata{
			Name:  consts.CloudLinkSecretName,
			Realm: consts.KukeSystemRealmName,
		},
	}
}

// SecretRef names the credential Secret, for messages.
func SecretRef() string {
	return consts.CloudLinkSecretName + "@" + consts.KukeSystemRealmName
}

// LoadCredential reads the enrolled credential. A host that never enrolled
// is cloudlink.ErrNotEnrolled.
func LoadCredential(ctx context.Context, client SecretReader) (*cloudlink.Credential, error) {
	result, err := client.ReadSecret(ctx, secretDoc())
	if err != nil {
		return nil, fmt.Errorf("read secret %s: %w", SecretRef(), err)
	}
	if !result.MetadataExists {
		return nil, fmt.Errorf("%w (no secret %s; run `kuke cloud enroll <token>`)",
			cloudlink.ErrNotEnrolled, SecretRef())
	}
	var cred cloudlink.Credential
	if err = json.Unmarshal([]byte(result.Secret.Spec.Data), &cred); err != nil {
		return nil, fmt.Errorf("%w: secret %s is not a cloud link credential: %w",
			cloudlink.ErrNotEnrolled, SecretRef(), err)
	}
	if err = cred.Validate(); err != nil {
		return nil, fmt.Errorf("secret %s: %w", SecretRef(), err)
	}
	return &cred, nil
}

// StoreCredential writes cred as the credential Secret, replacing any
// earlier enrollment.
func StoreCredential(ctx context.Context, client SecretWriter, cred *cloudlink.Credential) error {
	data, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	doc := secretDoc()
	doc.Spec = v1beta1.SecretSpec{Type: v1beta1.SecretTypeOpaque, Data: string(data)}
	if _, err = client.CreateSecret(ctx, doc); err != nil {
		return fmt.Errorf("store secret %s: %w", SecretRef(), err)
	}
	return nil
}

// Status is the link status file a running `kuke serve --cloud-link`
// keeps up to date.
type Status struct {
	cloudlink.State

	// PID is the `kuke serve` process.
	PID int `json:"pid"`
	// URL is the link URL it dials.
	URL string `json:"url"`
	// HostID is the enrolled host identity.
	HostID string `json:"hostId"`
}

// StatusFileName is the status file's name in the kuke directory (~/.kuke).
const StatusFileName = "cloud-link.json"

// WriteStatus replaces the status file atomically.
func WriteStatus(path string, status Status) error {
	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+StatusFileName+"-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err = tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// RemoveStatus removes the status file if it still belongs to pid.
func RemoveStatus(path string, pid int) {
	if status, err := ReadStatus(path); err == nil && status.PID == pid {
		_ = os.Remove(path)
	}
}

// ErrNoStatus is a status file that does not exist or names a process that
// is gone: no `kuke serve --cloud-link` is running.
var ErrNoStatus = errors.New("no kuke serve --cloud-link is running")

// ReadStatus reads the status file. A missing file, or one left by a
// process that has exited, is ErrNoStatus.
func ReadStatus(path string) (Status, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Status{}, ErrNoStatus
	}
	if err != nil {
		return Status{}, err
	}
	var status Status
	if err = json.Unmarshal(data, &status); err != nil {
		return Status{}, fmt.Errorf("read %s: %w", path, err)
	}
	if !processAlive(status.PID) {
		return Status{}, ErrNoStatus
	}
	return status, nil
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// Age renders how long ago t was, for status output.
func Age(now, t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return now.Sub(t).Round(time.Second).String()
}
