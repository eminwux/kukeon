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

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// writeConfig writes a kukeond.yaml with the given containerdNamespaceSuffix
// into a temp dir and returns its path.
func writeConfig(t *testing.T, suffix string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "kukeond.yaml")
	body := "apiVersion: kukeon.io/v1beta1\nkind: ServerConfiguration\nspec:\n  containerdNamespaceSuffix: " + suffix + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestResolveNamespaceSuffixExplicitFile(t *testing.T) {
	path := writeConfig(t, "dev.kukeon.io")
	got, err := resolveNamespaceSuffix(path)
	if err != nil {
		t.Fatalf("resolveNamespaceSuffix: %v", err)
	}
	if got != "dev.kukeon.io" {
		t.Errorf("suffix = %q, want dev.kukeon.io", got)
	}
	if ns := realmNamespace("default", got); ns != "default.dev.kukeon.io" {
		t.Errorf("realmNamespace = %q, want default.dev.kukeon.io", ns)
	}
}

func TestResolveNamespaceSuffixExplicitMissingFileErrors(t *testing.T) {
	// An operator-named config path that does not exist is an error — kukebuild
	// must not silently fall back to the default when the operator pointed it
	// at a specific file.
	missing := filepath.Join(t.TempDir(), "does-not-exist.yaml")
	if _, err := resolveNamespaceSuffix(missing); err == nil {
		t.Fatal("resolveNamespaceSuffix(missing explicit path): expected error, got nil")
	}
}

func TestResolveNamespaceSuffixDefaultWhenAbsent(t *testing.T) {
	// No --kukeond-config and (in the test environment) no
	// /etc/kukeon/kukeond.yaml: fall through to the hardcoded default.
	if _, err := os.Stat(defaultKukeondConfigFile); err == nil {
		t.Skipf("%s exists on this host; skipping absent-default case", defaultKukeondConfigFile)
	}
	got, err := resolveNamespaceSuffix("")
	if err != nil {
		t.Fatalf("resolveNamespaceSuffix(\"\"): %v", err)
	}
	if got != defaultRealmNamespaceSuffix {
		t.Errorf("suffix = %q, want %q", got, defaultRealmNamespaceSuffix)
	}
}

func TestResolveNamespaceSuffixEmptyKeyFallsBack(t *testing.T) {
	// File present but containerdNamespaceSuffix omitted: use the default.
	dir := t.TempDir()
	path := filepath.Join(dir, "kukeond.yaml")
	body := "apiVersion: kukeon.io/v1beta1\nkind: ServerConfiguration\nspec:\n  socket: /run/kukeon/kukeond.sock\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	got, err := resolveNamespaceSuffix(path)
	if err != nil {
		t.Fatalf("resolveNamespaceSuffix: %v", err)
	}
	if got != defaultRealmNamespaceSuffix {
		t.Errorf("suffix = %q, want %q", got, defaultRealmNamespaceSuffix)
	}
}

func TestResolveNamespaceSuffixInvalidValueErrors(t *testing.T) {
	cases := []struct {
		name   string
		suffix string
	}{
		{"leading dot", "\".kukeon.io\""},
		{"trailing dot", "\"kukeon.io.\""},
		{"slash", "\"kuke/on.io\""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfig(t, tc.suffix)
			if _, err := resolveNamespaceSuffix(path); err == nil {
				t.Errorf("resolveNamespaceSuffix(%s): expected error, got nil", tc.suffix)
			}
		})
	}
}

func TestResolveNamespaceSuffixMalformedYAMLErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kukeond.yaml")
	if err := os.WriteFile(path, []byte("spec: [this is not: valid"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := resolveNamespaceSuffix(path); err == nil {
		t.Fatal("resolveNamespaceSuffix(malformed yaml): expected error, got nil")
	}
}
