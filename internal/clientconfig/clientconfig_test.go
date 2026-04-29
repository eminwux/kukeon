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

package clientconfig

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
	if doc.Kind != "" || doc.Spec.Host != "" || doc.Spec.LogLevel != "" {
		t.Fatalf("Load() returned non-zero doc for absent file: %+v", doc)
	}
}

func TestLoadValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kuke.yaml")
	content := `apiVersion: v1beta1
kind: ClientConfiguration
metadata:
  name: default
spec:
  host: unix:///tmp/kuke-test.sock
  runPath: /opt/kukeon-test
  containerdSocket: /run/containerd/test.sock
  logLevel: debug
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	doc, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if doc.Kind != v1beta1.KindClientConfiguration {
		t.Errorf("Kind: got %q, want %q", doc.Kind, v1beta1.KindClientConfiguration)
	}
	if doc.Spec.Host != "unix:///tmp/kuke-test.sock" {
		t.Errorf("Host: got %q", doc.Spec.Host)
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
}

func TestLoadWrongKindRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kuke.yaml")
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
	if !errors.Is(err, errdefs.ErrClientConfigurationInvalid) {
		t.Errorf("Load() error = %v, want wrapping ErrClientConfigurationInvalid", err)
	}
}

func TestLoadMalformedYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kuke.yaml")
	// Mapping value where a string is expected.
	content := `apiVersion: v1beta1
kind: ClientConfiguration
spec:
  host:
    nested: bad
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() expected error for malformed YAML, got nil")
	}
	if !errors.Is(err, errdefs.ErrClientConfigurationInvalid) {
		t.Errorf("Load() error = %v, want wrapping ErrClientConfigurationInvalid", err)
	}
}

func TestLoadEmptyKindAccepted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kuke.yaml")
	content := `spec:
  host: unix:///tmp/kuke-test.sock
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	doc, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if doc.Spec.Host != "unix:///tmp/kuke-test.sock" {
		t.Errorf("Host: got %q", doc.Spec.Host)
	}
}

func TestWriteDefaultCreatesFileWithDefaults(t *testing.T) {
	dir := t.TempDir()
	// Nested parent directory exercises the MkdirAll branch.
	path := filepath.Join(dir, "a", "b", "kuke.yaml")

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
	if doc.Kind != v1beta1.KindClientConfiguration {
		t.Errorf("Kind: got %q, want %q", doc.Kind, v1beta1.KindClientConfiguration)
	}
	if doc.APIVersion != v1beta1.APIVersionV1Beta1 {
		t.Errorf("APIVersion: got %q, want %q", doc.APIVersion, v1beta1.APIVersionV1Beta1)
	}
	if doc.Spec.Host != "unix:///run/kukeon/kukeond.sock" {
		t.Errorf("Spec.Host: got %q", doc.Spec.Host)
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

	// AC: "Header comment explains each field's purpose and default."
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	rawStr := string(raw)
	if !strings.Contains(rawStr, "# kuke ClientConfiguration") {
		t.Errorf("missing header comment block")
	}
	for _, marker := range []string{
		"# Default: unix:///run/kukeon/kukeond.sock",
		"# Default: /opt/kukeon",
		"# Default: /run/containerd/containerd.sock",
		"# Default: info",
	} {
		if !strings.Contains(rawStr, marker) {
			t.Errorf("missing per-field default marker %q in dumped YAML", marker)
		}
	}
}

func TestWriteDefaultLeavesExistingFileUntouched(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kuke.yaml")
	preexisting := []byte("# operator-edited file\nkind: ClientConfiguration\n")
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
