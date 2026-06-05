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

// canonicalDefaultsSpec mirrors the compile-time defaults documented in each
// `# Default: …` comment in defaultDocumentTemplate. Use as the spec for
// WriteDefault when a test wants the dumped YAML to match the documented
// defaults verbatim.
func canonicalDefaultsSpec() v1beta1.ServerConfigurationSpec {
	return v1beta1.ServerConfigurationSpec{
		Socket:                    "/run/kukeon/kukeond.sock",
		SocketGID:                 0,
		RunPath:                   "/opt/kukeon",
		ContainerdSocket:          "/run/containerd/containerd.sock",
		LogLevel:                  "info",
		ReconcileInterval:         "30s",
		KukeondImage:              "",
		ContainerdNamespaceSuffix: "kukeon.io",
		CgroupRoot:                "/kukeon",
		PodSubnetCIDR:             "10.88.0.0/16",
		DefaultMemoryLimitBytes:   0,
	}
}

func TestWriteDefaultCreatesFileWithDefaults(t *testing.T) {
	dir := t.TempDir()
	// Nested parent directory exercises the MkdirAll branch.
	path := filepath.Join(dir, "a", "b", "kukeond.yaml")

	wrote, err := WriteDefault(path, canonicalDefaultsSpec())
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
	if doc.Spec.ContainerdNamespaceSuffix != "kukeon.io" {
		t.Errorf("Spec.ContainerdNamespaceSuffix: got %q, want %q",
			doc.Spec.ContainerdNamespaceSuffix, "kukeon.io")
	}
	if doc.Spec.CgroupRoot != "/kukeon" {
		t.Errorf("Spec.CgroupRoot: got %q, want %q", doc.Spec.CgroupRoot, "/kukeon")
	}
	if doc.Spec.PodSubnetCIDR != "10.88.0.0/16" {
		t.Errorf("Spec.PodSubnetCIDR: got %q, want %q", doc.Spec.PodSubnetCIDR, "10.88.0.0/16")
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
		"# Default: kukeon.io",
		"# Default: /kukeon",
		"# Default: 10.88.0.0/16",
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

	wrote, err := WriteDefault(path, canonicalDefaultsSpec())
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

// TestWriteDefaultRendersResolvedSpec is the regression guard for issue #581:
// the dumped YAML must reflect the spec the caller passes (which on the
// kukeond boot path mirrors the flag-and-env-resolved viper state), not a
// hardcoded snapshot of the compile-time defaults. Before the fix,
// `kukeond serve --run-path /tmp/A` wrote `runPath: /opt/kukeon` and
// `socket: /run/kukeon/kukeond.sock` to /etc/kukeon/kukeond.yaml regardless
// of the launched values; subsequent `kuke init --run-path /tmp/B`
// invocations then read that lying file back via applyServerConfiguration
// and silently bound the daemon to the leftover socket.
func TestWriteDefaultRendersResolvedSpec(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kukeond.yaml")

	spec := v1beta1.ServerConfigurationSpec{
		Socket:                    "/tmp/A/kukeond.sock",
		SocketGID:                 4242,
		RunPath:                   "/tmp/A",
		ContainerdSocket:          "/run/containerd/test.sock",
		LogLevel:                  "debug",
		ReconcileInterval:         "45s",
		KukeondImage:              "docker.io/library/kukeon:test",
		ContainerdNamespaceSuffix: "dev.kukeon.io",
		CgroupRoot:                "/kukeon-dev",
		PodSubnetCIDR:             "10.89.0.0/16",
		DefaultMemoryLimitBytes:   2 * 1024 * 1024 * 1024,
	}

	wrote, err := WriteDefault(path, spec)
	if err != nil {
		t.Fatalf("WriteDefault: %v", err)
	}
	if !wrote {
		t.Fatal("WriteDefault returned wrote=false on a fresh path")
	}

	doc, err := Load(path)
	if err != nil {
		t.Fatalf("Load() round-trip: %v", err)
	}
	if doc.Spec.Socket != spec.Socket {
		t.Errorf("Spec.Socket: got %q, want %q (the launched value, not the binary default)",
			doc.Spec.Socket, spec.Socket)
	}
	if doc.Spec.SocketGID != spec.SocketGID {
		t.Errorf("Spec.SocketGID: got %d, want %d", doc.Spec.SocketGID, spec.SocketGID)
	}
	if doc.Spec.RunPath != spec.RunPath {
		t.Errorf("Spec.RunPath: got %q, want %q (the launched value, not the binary default)",
			doc.Spec.RunPath, spec.RunPath)
	}
	if doc.Spec.ContainerdSocket != spec.ContainerdSocket {
		t.Errorf("Spec.ContainerdSocket: got %q, want %q", doc.Spec.ContainerdSocket, spec.ContainerdSocket)
	}
	if doc.Spec.LogLevel != spec.LogLevel {
		t.Errorf("Spec.LogLevel: got %q, want %q", doc.Spec.LogLevel, spec.LogLevel)
	}
	if doc.Spec.ReconcileInterval != spec.ReconcileInterval {
		t.Errorf("Spec.ReconcileInterval: got %q, want %q",
			doc.Spec.ReconcileInterval, spec.ReconcileInterval)
	}
	if doc.Spec.KukeondImage != spec.KukeondImage {
		t.Errorf("Spec.KukeondImage: got %q, want %q", doc.Spec.KukeondImage, spec.KukeondImage)
	}
	if doc.Spec.ContainerdNamespaceSuffix != spec.ContainerdNamespaceSuffix {
		t.Errorf("Spec.ContainerdNamespaceSuffix: got %q, want %q",
			doc.Spec.ContainerdNamespaceSuffix, spec.ContainerdNamespaceSuffix)
	}
	if doc.Spec.CgroupRoot != spec.CgroupRoot {
		t.Errorf("Spec.CgroupRoot: got %q, want %q", doc.Spec.CgroupRoot, spec.CgroupRoot)
	}
	if doc.Spec.PodSubnetCIDR != spec.PodSubnetCIDR {
		t.Errorf("Spec.PodSubnetCIDR: got %q, want %q", doc.Spec.PodSubnetCIDR, spec.PodSubnetCIDR)
	}
	if doc.Spec.DefaultMemoryLimitBytes != spec.DefaultMemoryLimitBytes {
		t.Errorf("Spec.DefaultMemoryLimitBytes: got %d, want %d",
			doc.Spec.DefaultMemoryLimitBytes, spec.DefaultMemoryLimitBytes)
	}

	// The compile-time-default markers in the header comments must survive
	// even when the rendered values diverge — the `# Default: …` line
	// documents the binary's intrinsic default for the operator, not the
	// current effective value.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	rawStr := string(raw)
	for _, marker := range []string{
		"# Default: /run/kukeon/kukeond.sock",
		"# Default: /opt/kukeon",
		"# Default: /run/containerd/containerd.sock",
	} {
		if !strings.Contains(rawStr, marker) {
			t.Errorf("missing compile-time-default marker %q in rendered YAML", marker)
		}
	}
}
