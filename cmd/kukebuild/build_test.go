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

// newSolveOptFixture writes a minimal build context (an empty Dockerfile) into
// a temp dir and returns a buildConfig pointed at it. Callers add secrets /
// cache entries before calling newSolveOpt.
func newSolveOptFixture(t *testing.T) *buildConfig {
	t.Helper()
	dir := t.TempDir()
	dockerfile := filepath.Join(dir, "Dockerfile")
	if err := os.WriteFile(dockerfile, []byte("FROM scratch\n"), 0o600); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	return &buildConfig{contextDir: dir, dockerfile: dockerfile, tag: "x:1"}
}

func TestNewSolveOptWiresCache(t *testing.T) {
	cfg := newSolveOptFixture(t)
	cfg.cacheExports = []cacheSpec{{typ: "local", attrs: map[string]string{"dest": "/c/out"}}}
	cfg.cacheImports = []cacheSpec{{typ: "local", attrs: map[string]string{"src": "/c/in"}}}

	so, err := newSolveOpt(cfg, nil)
	if err != nil {
		t.Fatalf("newSolveOpt: %v", err)
	}
	if len(so.CacheExports) != 1 || so.CacheExports[0].Type != "local" ||
		so.CacheExports[0].Attrs["dest"] != "/c/out" {
		t.Errorf("CacheExports = %+v, want one local dest=/c/out", so.CacheExports)
	}
	if len(so.CacheImports) != 1 || so.CacheImports[0].Type != "local" ||
		so.CacheImports[0].Attrs["src"] != "/c/in" {
		t.Errorf("CacheImports = %+v, want one local src=/c/in", so.CacheImports)
	}
	if len(so.Session) != 0 {
		t.Errorf("Session = %d entries, want 0 when no secrets", len(so.Session))
	}
}

func TestNewSolveOptWiresSecrets(t *testing.T) {
	cfg := newSolveOptFixture(t)
	secretFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(secretFile, []byte("s3cr3t"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	cfg.secrets = []secretSpec{{id: "tok", src: secretFile}}

	so, err := newSolveOpt(cfg, nil)
	if err != nil {
		t.Fatalf("newSolveOpt: %v", err)
	}
	// One secretsprovider attachable is added to the session.
	if len(so.Session) != 1 {
		t.Errorf("Session = %d entries, want 1 secrets provider", len(so.Session))
	}
}

func TestNewSolveOptSecretFileMissing(t *testing.T) {
	cfg := newSolveOptFixture(t)
	cfg.secrets = []secretSpec{{id: "tok", src: filepath.Join(t.TempDir(), "does-not-exist")}}

	if _, err := newSolveOpt(cfg, nil); err == nil {
		t.Fatal("newSolveOpt: expected error for missing secret file, got nil")
	}
}

func TestNewSolveOptWiresPush(t *testing.T) {
	cfg := newSolveOptFixture(t)
	cfg.tag = "registry:5000/app:dev"
	cfg.push = true
	// A real auth attachable, resolved with the env fallback so the test needs
	// no on-disk docker config.
	ap, _, err := newAuthProvider(fakeEnv(map[string]string{"HOME": t.TempDir()}), "registry:5000")
	if err != nil {
		t.Fatalf("newAuthProvider: %v", err)
	}

	so, err := newSolveOpt(cfg, ap)
	if err != nil {
		t.Fatalf("newSolveOpt: %v", err)
	}
	if got := so.Exports[0].Attrs["push"]; got != "true" {
		t.Errorf(`Exports[0].Attrs["push"] = %q, want "true"`, got)
	}
	if len(so.Session) != 1 {
		t.Errorf("Session = %d entries, want 1 (the auth provider)", len(so.Session))
	}
}

func TestNewSolveOptNoPushByDefault(t *testing.T) {
	cfg := newSolveOptFixture(t) // push defaults to false
	so, err := newSolveOpt(cfg, nil)
	if err != nil {
		t.Fatalf("newSolveOpt: %v", err)
	}
	if _, ok := so.Exports[0].Attrs["push"]; ok {
		t.Error(`Exports[0].Attrs["push"] is set, want absent when --push is off`)
	}
	if len(so.Session) != 0 {
		t.Errorf("Session = %d entries, want 0 when not pushing", len(so.Session))
	}
}

// A push build with a nil auth provider (anonymous push) still sets the push
// attr and must not panic appending to the session.
func TestNewSolveOptPushNilAuth(t *testing.T) {
	cfg := newSolveOptFixture(t)
	cfg.tag = "registry:5000/app:dev"
	cfg.push = true
	so, err := newSolveOpt(cfg, nil)
	if err != nil {
		t.Fatalf("newSolveOpt: %v", err)
	}
	if so.Exports[0].Attrs["push"] != "true" {
		t.Error(`Exports[0].Attrs["push"] != "true" for an anonymous push`)
	}
	if len(so.Session) != 0 {
		t.Errorf("Session = %d entries, want 0 for nil auth provider", len(so.Session))
	}
}

func TestResolveBuildRoot(t *testing.T) {
	cases := []struct {
		name      string
		root      string
		explicit  bool
		namespace string
		want      string
	}{
		{
			name:      "default root scoped per namespace",
			root:      defaultBuildRoot,
			explicit:  false,
			namespace: "default.kukeon.io",
			want:      "/var/lib/kukebuild/default.kukeon.io",
		},
		{
			name:      "default root scoped per different namespace",
			root:      defaultBuildRoot,
			explicit:  false,
			namespace: "kuke-system.kukeon.io",
			want:      "/var/lib/kukebuild/kuke-system.kukeon.io",
		},
		{
			name:      "default root scoped per custom-suffix namespace",
			root:      defaultBuildRoot,
			explicit:  false,
			namespace: "default.dev.kukeon.io",
			want:      "/var/lib/kukebuild/default.dev.kukeon.io",
		},
		{
			name:      "explicit root honored verbatim",
			root:      "/tmp/freshroot",
			explicit:  true,
			namespace: "default.kukeon.io",
			want:      "/tmp/freshroot",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveBuildRoot(tc.root, tc.explicit, tc.namespace); got != tc.want {
				t.Errorf("resolveBuildRoot(%q, %v, %q) = %q, want %q",
					tc.root, tc.explicit, tc.namespace, got, tc.want)
			}
		})
	}
}

// Two consecutive default-root builds into different namespaces must resolve to
// distinct BuildKit state roots — the isolation that prevents the cross-
// namespace cache reuse in issue #663.
func TestResolveBuildRootIsolatesNamespaces(t *testing.T) {
	first := resolveBuildRoot(defaultBuildRoot, false, "default.kukeon.io")
	second := resolveBuildRoot(defaultBuildRoot, false, "kuke-system.kukeon.io")
	if first == second {
		t.Errorf("default roots for distinct namespaces collide: %q == %q", first, second)
	}
}
