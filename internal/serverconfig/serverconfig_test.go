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

package serverconfig

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func TestLoadAbsentFileReturnsZeroDoc(t *testing.T) {
	doc, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if doc == nil {
		t.Fatal("Load() returned nil doc, want zero-value")
	}
	if doc.Kind != "" || doc.Spec.Socket != "" || doc.Spec.RunPath != "" {
		t.Fatalf("Load() returned non-zero doc for absent file: %+v", doc)
	}
}

func TestLoadValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kukeond.yaml")
	content := `apiVersion: v1beta1
kind: ServerConfiguration
metadata:
  name: default
spec:
  socket: /run/kukeon/test.sock
  socketGID: 1000
  runPath: /opt/kukeon-test
  containerdSocket: /run/containerd/test.sock
  logLevel: debug
  kukeondImage: docker.io/library/kukeon:test
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	doc, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if doc.Kind != v1beta1.KindServerConfiguration {
		t.Errorf("Kind: got %q, want %q", doc.Kind, v1beta1.KindServerConfiguration)
	}
	if doc.Spec.Socket != "/run/kukeon/test.sock" {
		t.Errorf("Socket: got %q", doc.Spec.Socket)
	}
	if doc.Spec.SocketGID != 1000 {
		t.Errorf("SocketGID: got %d, want 1000", doc.Spec.SocketGID)
	}
	if doc.Spec.RunPath != "/opt/kukeon-test" {
		t.Errorf("RunPath: got %q", doc.Spec.RunPath)
	}
	if doc.Spec.ContainerdSocket != "/run/containerd/test.sock" {
		t.Errorf("ContainerdSocket: got %q", doc.Spec.ContainerdSocket)
	}
	if doc.Spec.LogLevel != "debug" {
		t.Errorf("LogLevel: got %q", doc.Spec.LogLevel)
	}
	if doc.Spec.KukeondImage != "docker.io/library/kukeon:test" {
		t.Errorf("KukeondImage: got %q", doc.Spec.KukeondImage)
	}
}

func TestLoadReconcileInterval(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kukeond.yaml")
	content := `apiVersion: v1beta1
kind: ServerConfiguration
metadata:
  name: default
spec:
  reconcileInterval: 7s
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	doc, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if doc.Spec.ReconcileInterval != "7s" {
		t.Errorf("ReconcileInterval: got %q, want %q", doc.Spec.ReconcileInterval, "7s")
	}
}

func TestLoadWrongKindRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kukeond.yaml")
	content := `apiVersion: v1beta1
kind: Cell
metadata:
  name: nope
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() expected error for wrong kind, got nil")
	}
	if !errors.Is(err, errdefs.ErrServerConfigurationInvalid) {
		t.Errorf("Load() error = %v, want wrapping ErrServerConfigurationInvalid", err)
	}
}

func TestLoadMalformedYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kukeond.yaml")
	// Mapping value where a string is expected.
	content := `apiVersion: v1beta1
kind: ServerConfiguration
spec:
  socket:
    nested: bad
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() expected error for malformed YAML, got nil")
	}
	if !errors.Is(err, errdefs.ErrServerConfigurationInvalid) {
		t.Errorf("Load() error = %v, want wrapping ErrServerConfigurationInvalid", err)
	}
}

func TestLoadEmptyKindAccepted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kukeond.yaml")
	content := `spec:
  socket: /run/kukeon/test.sock
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	doc, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if doc.Spec.Socket != "/run/kukeon/test.sock" {
		t.Errorf("Socket: got %q", doc.Spec.Socket)
	}
}

func TestWriteDefaultCreatesFileWithDefaults(t *testing.T) {
	dir := t.TempDir()
	// Nested parent directory exercises the MkdirAll branch.
	path := filepath.Join(dir, "a", "b", "kukeond.yaml")

	wrote, err := WriteDefault(path)
	if err != nil {
		t.Fatalf("WriteDefault: %v", err)
	}
	if !wrote {
		t.Fatal("WriteDefault returned wrote=false on a fresh path")
	}

	// Round-trip: the freshly written file must parse cleanly through Load
	// and surface every documented default. AC: "Generated YAML round-trips
	// through the loader without errors."
	doc, err := Load(path)
	if err != nil {
		t.Fatalf("Load() round-trip: %v", err)
	}
	if doc.Kind != v1beta1.KindServerConfiguration {
		t.Errorf("Kind: got %q, want %q", doc.Kind, v1beta1.KindServerConfiguration)
	}
	if doc.APIVersion != v1beta1.APIVersionV1Beta1 {
		t.Errorf("APIVersion: got %q, want %q", doc.APIVersion, v1beta1.APIVersionV1Beta1)
	}
	if doc.Spec.Socket != "/run/kukeon/kukeond.sock" {
		t.Errorf("Spec.Socket: got %q", doc.Spec.Socket)
	}
	if doc.Spec.SocketGID != 0 {
		t.Errorf("Spec.SocketGID: got %d, want 0", doc.Spec.SocketGID)
	}
	if doc.Spec.RunPath != "/opt/kukeon" {
		t.Errorf("Spec.RunPath: got %q", doc.Spec.RunPath)
	}
	if doc.Spec.ContainerdSocket != "/run/containerd/containerd.sock" {
		t.Errorf("Spec.ContainerdSocket: got %q", doc.Spec.ContainerdSocket)
	}
	if doc.Spec.LogLevel != "info" {
		t.Errorf("Spec.LogLevel: got %q", doc.Spec.LogLevel)
	}
	if doc.Spec.KukeondImage != "" {
		t.Errorf("Spec.KukeondImage: got %q, want empty (kuke init resolves at runtime)", doc.Spec.KukeondImage)
	}
	if doc.Spec.ReconcileInterval != "30s" {
		t.Errorf("Spec.ReconcileInterval: got %q, want %q", doc.Spec.ReconcileInterval, "30s")
	}

	// AC: "Header comment explains each field's purpose and default."
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	rawStr := string(raw)
	if !strings.Contains(rawStr, "# kukeond ServerConfiguration") {
		t.Errorf("missing header comment block")
	}
	for _, marker := range []string{
		"# Default: /run/kukeon/kukeond.sock",
		"# Default: 0",
		"# Default: /opt/kukeon",
		"# Default: /run/containerd/containerd.sock",
		"# Default: info",
		"# Default: 30s",
		`# Default: ""`,
	} {
		if !strings.Contains(rawStr, marker) {
			t.Errorf("missing per-field default marker %q in dumped YAML", marker)
		}
	}
}

func TestWriteDefaultLeavesExistingFileUntouched(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kukeond.yaml")
	preexisting := []byte("# operator-edited file\nkind: ServerConfiguration\n")
	if err := os.WriteFile(path, preexisting, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	wrote, err := WriteDefault(path)
	if err != nil {
		t.Fatalf("WriteDefault: %v", err)
	}
	if wrote {
		t.Fatal("WriteDefault overwrote an existing file (wrote=true)")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after WriteDefault: %v", err)
	}
	if string(got) != string(preexisting) {
		t.Errorf("file contents changed:\n got: %q\nwant: %q", got, preexisting)
	}
}
