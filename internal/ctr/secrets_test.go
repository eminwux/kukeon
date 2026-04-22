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

package ctr

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

func TestResolveSecretsFromFileEnvInjection(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	secretPath := filepath.Join(dir, "anthropic.key")
	if err := os.WriteFile(secretPath, []byte("sk-ant-xyz"), 0o600); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	secrets := []intmodel.ContainerSecret{
		{Name: "ANTHROPIC_API_KEY", FromFile: secretPath},
	}

	got, err := resolveSecrets("cntr-1", secrets, filepath.Join(dir, "staging"))
	if err != nil {
		t.Fatalf("resolveSecrets: %v", err)
	}
	if len(got.MountAdds) != 0 {
		t.Fatalf("expected no mount adds, got %d", len(got.MountAdds))
	}
	if len(got.EnvAdds) != 1 || got.EnvAdds[0] != "ANTHROPIC_API_KEY=sk-ant-xyz" {
		t.Fatalf("unexpected env adds: %#v", got.EnvAdds)
	}
}

func TestResolveSecretsFromEnvInjection(t *testing.T) {
	t.Setenv("KUKEON_TEST_SCOPED_TOKEN", "ghp_scoped") // not parallel: t.Setenv forbids it

	secrets := []intmodel.ContainerSecret{
		{Name: "GITHUB_TOKEN", FromEnv: "KUKEON_TEST_SCOPED_TOKEN"},
	}

	got, err := resolveSecrets("cntr-2", secrets, t.TempDir())
	if err != nil {
		t.Fatalf("resolveSecrets: %v", err)
	}
	if len(got.EnvAdds) != 1 || got.EnvAdds[0] != "GITHUB_TOKEN=ghp_scoped" {
		t.Fatalf("unexpected env adds: %#v", got.EnvAdds)
	}
}

func TestResolveSecretsFileMountMode(t *testing.T) {
	t.Parallel()

	sourceDir := t.TempDir()
	stagingDir := t.TempDir()
	sourcePath := filepath.Join(sourceDir, "tls.crt")
	if err := os.WriteFile(sourcePath, []byte("-----BEGIN CERT-----"), 0o600); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	secrets := []intmodel.ContainerSecret{
		{
			Name:      "tls.crt",
			FromFile:  sourcePath,
			MountPath: "/etc/secrets/tls.crt",
		},
	}

	got, err := resolveSecrets("cntr-3", secrets, stagingDir)
	if err != nil {
		t.Fatalf("resolveSecrets: %v", err)
	}
	if len(got.EnvAdds) != 0 {
		t.Fatalf("expected no env adds, got %#v", got.EnvAdds)
	}
	if len(got.MountAdds) != 1 {
		t.Fatalf("expected 1 mount add, got %d", len(got.MountAdds))
	}
	mount := got.MountAdds[0]
	wantSource := filepath.Join(stagingDir, "cntr-3", "tls.crt")
	if mount.Source != wantSource {
		t.Fatalf("mount source = %q want %q", mount.Source, wantSource)
	}
	if mount.Target != "/etc/secrets/tls.crt" {
		t.Fatalf("mount target = %q", mount.Target)
	}
	if !mount.ReadOnly {
		t.Fatalf("mount should be read-only")
	}

	info, err := os.Stat(wantSource)
	if err != nil {
		t.Fatalf("stat staged file: %v", err)
	}
	if info.Mode().Perm() != 0o400 {
		t.Fatalf("staged file perms = %o want 0400", info.Mode().Perm())
	}
	data, err := os.ReadFile(wantSource)
	if err != nil {
		t.Fatalf("read staged file: %v", err)
	}
	if string(data) != "-----BEGIN CERT-----" {
		t.Fatalf("staged contents = %q", data)
	}
}

func TestResolveSecretsMissingFileErrors(t *testing.T) {
	t.Parallel()

	secrets := []intmodel.ContainerSecret{
		{Name: "MISSING", FromFile: "/nonexistent/path/secret"},
	}

	_, err := resolveSecrets("cntr-4", secrets, t.TempDir())
	if err == nil {
		t.Fatalf("expected error for missing file")
	}
	if !errors.Is(err, errdefs.ErrSecretFromFileNotFound) {
		t.Fatalf("want ErrSecretFromFileNotFound, got %v", err)
	}
}

func TestResolveSecretsMissingEnvErrors(t *testing.T) {
	t.Parallel()

	// Ensure the env var is not set.
	_ = os.Unsetenv("KUKEON_TEST_MISSING_SECRET_VAR")

	secrets := []intmodel.ContainerSecret{
		{Name: "MISSING", FromEnv: "KUKEON_TEST_MISSING_SECRET_VAR"},
	}

	_, err := resolveSecrets("cntr-5", secrets, t.TempDir())
	if err == nil {
		t.Fatalf("expected error for missing env var")
	}
	if !errors.Is(err, errdefs.ErrSecretFromEnvNotSet) {
		t.Fatalf("want ErrSecretFromEnvNotSet, got %v", err)
	}
}

func TestResolveSecretsRejectsMultipleSources(t *testing.T) {
	t.Parallel()

	secrets := []intmodel.ContainerSecret{
		{Name: "BOTH", FromFile: "/tmp/x", FromEnv: "Y"},
	}

	_, err := resolveSecrets("cntr-6", secrets, t.TempDir())
	if !errors.Is(err, errdefs.ErrSecretMultipleSources) {
		t.Fatalf("want ErrSecretMultipleSources, got %v", err)
	}
}

func TestResolveSecretsRejectsMissingSource(t *testing.T) {
	t.Parallel()

	secrets := []intmodel.ContainerSecret{{Name: "NOSRC"}}

	_, err := resolveSecrets("cntr-7", secrets, t.TempDir())
	if !errors.Is(err, errdefs.ErrSecretSourceRequired) {
		t.Fatalf("want ErrSecretSourceRequired, got %v", err)
	}
}

func TestResolveSecretsRejectsRelativeMountPath(t *testing.T) {
	t.Parallel()

	secrets := []intmodel.ContainerSecret{
		{Name: "X", FromEnv: "HOME", MountPath: "relative/path"},
	}

	_, err := resolveSecrets("cntr-8", secrets, t.TempDir())
	if !errors.Is(err, errdefs.ErrSecretMountPathNotAbsolute) {
		t.Fatalf("want ErrSecretMountPathNotAbsolute, got %v", err)
	}
}

func TestResolveSecretsEmptySliceIsNoop(t *testing.T) {
	t.Parallel()

	got, err := resolveSecrets("cntr-9", nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.EnvAdds) != 0 || len(got.MountAdds) != 0 {
		t.Fatalf("expected empty result, got %#v", got)
	}
}

func TestResolveSecretsMixesEnvAndMountModes(t *testing.T) {
	sourceDir := t.TempDir()
	stagingDir := t.TempDir()
	certPath := filepath.Join(sourceDir, "tls.crt")
	if err := os.WriteFile(certPath, []byte("cert-bytes"), 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	t.Setenv("KUKEON_TEST_API_KEY", "api-value")

	secrets := []intmodel.ContainerSecret{
		{Name: "API_KEY", FromEnv: "KUKEON_TEST_API_KEY"},
		{Name: "tls.crt", FromFile: certPath, MountPath: "/run/secrets/tls.crt"},
	}

	got, err := resolveSecrets("cntr-10", secrets, stagingDir)
	if err != nil {
		t.Fatalf("resolveSecrets: %v", err)
	}
	if len(got.EnvAdds) != 1 || got.EnvAdds[0] != "API_KEY=api-value" {
		t.Fatalf("env adds = %#v", got.EnvAdds)
	}
	if len(got.MountAdds) != 1 || got.MountAdds[0].Target != "/run/secrets/tls.crt" {
		t.Fatalf("mount adds = %#v", got.MountAdds)
	}
}
