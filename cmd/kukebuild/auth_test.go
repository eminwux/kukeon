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
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moby/buildkit/session/auth/authprovider"
)

func TestHasExplicitRegistry(t *testing.T) {
	cases := []struct {
		tag  string
		want bool
	}{
		{"app:dev", false},
		{"app", false},
		{"eminwux/kukeon:v1", false}, // docker-hub repo, not an explicit host
		{"library/app:dev", false},
		{"ghcr.io/eminwux/kukeon:v1", true},
		{"docker.io/library/app:dev", true},
		{"registry:5000/app:dev", true},
		{"localhost/app:dev", true},
		{"localhost:5000/app:dev", true},
		{"myregistry.local/app", true},
	}
	for _, tc := range cases {
		if got := hasExplicitRegistry(tc.tag); got != tc.want {
			t.Errorf("hasExplicitRegistry(%q) = %v, want %v", tc.tag, got, tc.want)
		}
	}
}

func TestRequirePushableReference(t *testing.T) {
	// A bare reference is a usageError naming the required form.
	err := requirePushableReference("app:dev")
	if err == nil {
		t.Fatal("requirePushableReference(app:dev): expected error, got nil")
	}
	var ue *usageError
	if !errors.As(err, &ue) {
		t.Errorf("error %v is not a *usageError", err)
	}
	if !strings.Contains(err.Error(), "REGISTRY/REPO:TAG") {
		t.Errorf("error %q does not name the required form REGISTRY/REPO:TAG", err)
	}
	// A fully qualified reference passes.
	if err := requirePushableReference("registry:5000/app:dev"); err != nil {
		t.Errorf("requirePushableReference(registry:5000/app:dev): unexpected error: %v", err)
	}
}

func TestRegistryHost(t *testing.T) {
	cases := []struct {
		tag  string
		want string
	}{
		{"registry:5000/app:dev", "registry:5000"},
		{"ghcr.io/eminwux/kukeon:v1", "ghcr.io"},
		{"localhost:5000/app", "localhost:5000"},
		{"docker.io/library/app:dev", "docker.io"},
	}
	for _, tc := range cases {
		got, err := registryHost(tc.tag)
		if err != nil {
			t.Errorf("registryHost(%q): unexpected error: %v", tc.tag, err)
			continue
		}
		if got != tc.want {
			t.Errorf("registryHost(%q) = %q, want %q", tc.tag, got, tc.want)
		}
	}
}

func TestAuthConfigKey(t *testing.T) {
	if got := authConfigKey("registry:5000"); got != "registry:5000" {
		t.Errorf("authConfigKey(registry:5000) = %q, want registry:5000", got)
	}
	if got := authConfigKey("docker.io"); got != authprovider.DockerHubConfigfileKey {
		t.Errorf("authConfigKey(docker.io) = %q, want %q", got, authprovider.DockerHubConfigfileKey)
	}
	if got := authConfigKey(authprovider.DockerHubRegistryHost); got != authprovider.DockerHubConfigfileKey {
		t.Errorf("authConfigKey(%q) = %q, want %q",
			authprovider.DockerHubRegistryHost, got, authprovider.DockerHubConfigfileKey)
	}
}

// writeDockerConfig writes a minimal docker config.json carrying an auth entry
// for host into a temp dir and returns that dir (for use as DOCKER_CONFIG).
func writeDockerConfig(t *testing.T, host, userPass string) string {
	t.Helper()
	dir := t.TempDir()
	auth := base64.StdEncoding.EncodeToString([]byte(userPass))
	doc := `{"auths":{"` + host + `":{"auth":"` + auth + `"}}}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(doc), 0o600); err != nil {
		t.Fatalf("write config.json: %v", err)
	}
	return dir
}

// fakeEnv returns a getenv backed by a fixed map, so the resolver's env
// precedence is exercised without touching the host environment.
func fakeEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestResolveRegistryAuthDockerConfig(t *testing.T) {
	dir := writeDockerConfig(t, "registry:5000", "alice:secret")
	cf, found, err := resolveRegistryAuth(fakeEnv(map[string]string{"DOCKER_CONFIG": dir}), "registry:5000")
	if err != nil {
		t.Fatalf("resolveRegistryAuth: %v", err)
	}
	if !found {
		t.Error("credsFound = false, want true when DOCKER_CONFIG carries the host")
	}
	// docker's loader decodes the `auth` field into Username/Password, so assert
	// via hasCreds rather than the (now-cleared) Auth string.
	if !hasCreds(cf, "registry:5000") {
		t.Error("expected the docker-config credentials to survive into the config file")
	}
}

func TestResolveRegistryAuthHomeFallback(t *testing.T) {
	// No DOCKER_CONFIG: resolution falls through to $HOME/.docker/config.json.
	home := t.TempDir()
	docker := filepath.Join(home, ".docker")
	if err := os.MkdirAll(docker, 0o700); err != nil {
		t.Fatalf("mkdir .docker: %v", err)
	}
	auth := base64.StdEncoding.EncodeToString([]byte("bob:pw"))
	doc := `{"auths":{"ghcr.io":{"auth":"` + auth + `"}}}`
	if err := os.WriteFile(filepath.Join(docker, "config.json"), []byte(doc), 0o600); err != nil {
		t.Fatalf("write config.json: %v", err)
	}
	cf, found, err := resolveRegistryAuth(fakeEnv(map[string]string{"HOME": home}), "ghcr.io")
	if err != nil {
		t.Fatalf("resolveRegistryAuth: %v", err)
	}
	if !found {
		t.Error("credsFound = false, want true when ~/.docker/config.json carries the host")
	}
	if !hasCreds(cf, "ghcr.io") {
		t.Error("expected the ~/.docker credentials to survive into the config file")
	}
}

func TestResolveRegistryAuthEnvFallback(t *testing.T) {
	// No docker config at all (empty HOME dir, no DOCKER_CONFIG): the
	// KUKEON_REGISTRY_AUTH env var supplies the credentials.
	raw := base64.StdEncoding.EncodeToString([]byte("ci:token"))
	cf, found, err := resolveRegistryAuth(fakeEnv(map[string]string{
		"HOME":                t.TempDir(),
		envKukeonRegistryAuth: raw,
	}), "registry:5000")
	if err != nil {
		t.Fatalf("resolveRegistryAuth: %v", err)
	}
	if !found {
		t.Error("credsFound = false, want true when KUKEON_REGISTRY_AUTH is set")
	}
	if got := cf.AuthConfigs["registry:5000"].Auth; got != raw {
		t.Errorf("AuthConfigs[registry:5000].Auth = %q, want %q", got, raw)
	}
}

func TestResolveRegistryAuthDockerConfigBeatsEnv(t *testing.T) {
	// When DOCKER_CONFIG carries the host, the env fallback must not override it
	// — the env var is only a fallback (precedence step 3).
	dir := writeDockerConfig(t, "registry:5000", "alice:fromconfig")
	envAuth := base64.StdEncoding.EncodeToString([]byte("ci:fromenv"))
	cf, found, err := resolveRegistryAuth(fakeEnv(map[string]string{
		"DOCKER_CONFIG":       dir,
		envKukeonRegistryAuth: envAuth,
	}), "registry:5000")
	if err != nil {
		t.Fatalf("resolveRegistryAuth: %v", err)
	}
	if !found {
		t.Fatal("credsFound = false, want true")
	}
	// The docker-config credential (alice, decoded into Username) must win — the
	// env fallback is applied only when the config carries no entry for the host.
	if got := cf.AuthConfigs["registry:5000"].Username; got != "alice" {
		t.Errorf("AuthConfigs[registry:5000].Username = %q, want the docker-config value \"alice\" (env must not override)", got)
	}
}

func TestResolveRegistryAuthNoneResolve(t *testing.T) {
	// No docker config, no env var: nothing resolves and credsFound is false so
	// the caller can emit the "no credentials" variant of the push error.
	cf, found, err := resolveRegistryAuth(fakeEnv(map[string]string{"HOME": t.TempDir()}), "registry:5000")
	if err != nil {
		t.Fatalf("resolveRegistryAuth: %v", err)
	}
	if found {
		t.Error("credsFound = true, want false when no layer carries credentials")
	}
	if hasCreds(cf, "registry:5000") {
		t.Error("hasCreds = true, want false for an empty config")
	}
}

func TestResolveRegistryAuthDockerHubKey(t *testing.T) {
	// An explicit docker.io push with only the env fallback must inject under
	// the docker-hub config-file key, not "docker.io", so authprovider serves it.
	raw := base64.StdEncoding.EncodeToString([]byte("ci:token"))
	cf, found, err := resolveRegistryAuth(fakeEnv(map[string]string{
		"HOME":                t.TempDir(),
		envKukeonRegistryAuth: raw,
	}), "docker.io")
	if err != nil {
		t.Fatalf("resolveRegistryAuth: %v", err)
	}
	if !found {
		t.Fatal("credsFound = false, want true")
	}
	if _, ok := cf.AuthConfigs["docker.io"]; ok {
		t.Error("env fallback keyed under docker.io; want the docker-hub config-file key")
	}
	if cf.AuthConfigs[authprovider.DockerHubConfigfileKey].Auth != raw {
		t.Errorf("env fallback not keyed under %q", authprovider.DockerHubConfigfileKey)
	}
}

func TestNewAuthProvider(t *testing.T) {
	ap, found, err := newAuthProvider(fakeEnv(map[string]string{
		"HOME":                t.TempDir(),
		envKukeonRegistryAuth: base64.StdEncoding.EncodeToString([]byte("u:p")),
	}), "registry:5000")
	if err != nil {
		t.Fatalf("newAuthProvider: %v", err)
	}
	if ap == nil {
		t.Error("newAuthProvider returned a nil attachable")
	}
	if !found {
		t.Error("credsFound = false, want true")
	}
}

func TestIsRegistryAuthError(t *testing.T) {
	authy := []string{
		"failed to push: unexpected status: 401 Unauthorized",
		"denied: requested access to the resource is denied",
		"unauthorized: authentication required",
		"failed to authorize: no basic auth credentials",
		"403 Forbidden",
	}
	for _, m := range authy {
		if !isRegistryAuthError(errors.New(m)) {
			t.Errorf("isRegistryAuthError(%q) = false, want true", m)
		}
	}
	notAuthy := []string{
		"dockerfile parse error on line 3",
		"failed to solve: executor failed running [/bin/sh -c make]: exit code 2",
	}
	for _, m := range notAuthy {
		if isRegistryAuthError(errors.New(m)) {
			t.Errorf("isRegistryAuthError(%q) = true, want false", m)
		}
	}
}

func TestPushAuthError(t *testing.T) {
	cause := errors.New("401 Unauthorized")
	// No credentials resolved: leads with "no credentials" and names the host
	// and the precedence list.
	noCreds := pushAuthError("registry:5000/app:dev", "registry:5000", false, cause)
	for _, want := range []string{"no credentials", "registry:5000", "DOCKER_CONFIG", "KUKEON_REGISTRY_AUTH"} {
		if !strings.Contains(noCreds.Error(), want) {
			t.Errorf("no-creds push error %q missing %q", noCreds, want)
		}
	}
	if !errors.Is(noCreds, cause) {
		t.Error("no-creds push error does not wrap the cause")
	}
	// Credentials resolved but rejected: points at verifying them, still cites
	// the precedence list and wraps the cause.
	rejected := pushAuthError("registry:5000/app:dev", "registry:5000", true, cause)
	for _, want := range []string{"rejected", "DOCKER_CONFIG", "KUKEON_REGISTRY_AUTH"} {
		if !strings.Contains(rejected.Error(), want) {
			t.Errorf("rejected push error %q missing %q", rejected, want)
		}
	}
	if !errors.Is(rejected, cause) {
		t.Error("rejected push error does not wrap the cause")
	}
}
