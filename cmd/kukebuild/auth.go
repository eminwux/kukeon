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
	"fmt"
	"path/filepath"
	"strings"

	"github.com/distribution/reference"
	"github.com/docker/cli/cli/config"
	"github.com/docker/cli/cli/config/configfile"
	"github.com/docker/cli/cli/config/types"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/auth/authprovider"
)

// envKukeonRegistryAuth is the ephemeral / CI credential fallback: a
// base64-encoded `user:pass` matching the docker-config `auth` field. It is
// consulted only when neither $DOCKER_CONFIG/config.json nor
// ~/.docker/config.json carries credentials for the target registry host —
// step 3 of the --push credential precedence (issue #524).
const envKukeonRegistryAuth = "KUKEON_REGISTRY_AUTH"

// dockerHubDomain is the domain reference.Domain returns for a docker-hub
// reference (e.g. docker.io/library/app:dev). Docker stores hub credentials
// under a distinct config-file key (authprovider.DockerHubConfigfileKey), so
// the KUKEON_REGISTRY_AUTH fallback for an explicit docker.io push must be
// injected under that key, not "docker.io", to be served back to BuildKit.
const dockerHubDomain = "docker.io"

// hasExplicitRegistry reports whether tag names a registry host explicitly. It
// mirrors docker's splitDockerDomain heuristic (github.com/distribution/
// reference): the segment before the first '/' is a registry host only when it
// is "localhost" or contains a '.' or ':'. A bare `name:tag`, or a docker-hub
// `user/repo:tag` that would silently normalize to docker.io, is therefore not
// explicit — `--push` requires the operator to name the registry so a push
// never lands somewhere unintended.
func hasExplicitRegistry(tag string) bool {
	i := strings.IndexByte(tag, '/')
	if i < 0 {
		return false
	}
	prefix := tag[:i]
	return prefix == "localhost" || strings.ContainsAny(prefix, ".:")
}

// requirePushableReference enforces the --push contract: the -t tag must be a
// fully qualified registry reference. Returns a *usageError otherwise so main()
// maps it to exitCodeUsage.
func requirePushableReference(tag string) error {
	if !hasExplicitRegistry(tag) {
		return &usageError{msg: fmt.Sprintf(
			"--push requires a fully qualified registry reference: REGISTRY/REPO:TAG (got %q)", tag)}
	}
	return nil
}

// registryHost returns the registry host of a reference, e.g. "registry:5000"
// for "registry:5000/app:dev" or "ghcr.io" for "ghcr.io/eminwux/kukeon:v1". It
// is the AuthConfigs key under which the KUKEON_REGISTRY_AUTH fallback is
// injected and the host the anonymous-rejection error names.
func registryHost(tag string) (string, error) {
	named, err := reference.ParseNormalizedNamed(tag)
	if err != nil {
		return "", fmt.Errorf("invalid image tag %q: %w", tag, err)
	}
	return reference.Domain(named), nil
}

// authConfigKey maps a registry host to the docker config-file key under which
// its credentials live. Every host keys under itself except docker hub, whose
// credentials docker stores under the legacy index URL — the same mapping
// BuildKit's authprovider applies when it serves credentials back.
func authConfigKey(host string) string {
	if host == dockerHubDomain || host == authprovider.DockerHubRegistryHost {
		return authprovider.DockerHubConfigfileKey
	}
	return host
}

// dockerConfigDir resolves the docker config directory using the --push
// credential precedence's first two layers: $DOCKER_CONFIG when set, else
// ~/.docker. getenv is injected so the resolver is unit-testable without
// touching the host environment.
func dockerConfigDir(getenv func(string) string) string {
	if d := strings.TrimSpace(getenv("DOCKER_CONFIG")); d != "" {
		return d
	}
	if home := strings.TrimSpace(getenv("HOME")); home != "" {
		return filepath.Join(home, ".docker")
	}
	return ""
}

// hasCreds reports whether cf carries inline credentials for key. It inspects
// the AuthConfigs map directly rather than calling GetAuthConfig so the check
// never execs a credential helper. Helper-sourced credentials still flow to the
// push through authprovider.LoadAuthConfig (which does consult helpers) — this
// signal only tailors the anonymous-rejection error message.
func hasCreds(cf *configfile.ConfigFile, key string) bool {
	ac, ok := cf.AuthConfigs[key]
	if !ok {
		return false
	}
	return ac.Username != "" || ac.Password != "" || ac.Auth != "" ||
		ac.IdentityToken != "" || ac.RegistryToken != ""
}

// resolveRegistryAuth builds the docker-compatible credential config kukebuild
// hands BuildKit's auth provider for a push to host. Precedence (issue #524):
//
//  1. $DOCKER_CONFIG/config.json when DOCKER_CONFIG is set;
//  2. else ~/.docker/config.json when present;
//  3. else the KUKEON_REGISTRY_AUTH env var (base64 user:pass), applied only
//     when layers 1-2 carry no entry for host.
//
// credsFound reports whether any layer yielded inline credentials for host, so
// the caller can tailor an anonymous-rejection error. getenv is injected for
// testability.
func resolveRegistryAuth(getenv func(string) string, host string) (*configfile.ConfigFile, bool, error) {
	cf, err := config.Load(dockerConfigDir(getenv))
	if err != nil {
		return nil, false, fmt.Errorf("load docker registry config: %w", err)
	}
	key := authConfigKey(host)
	credsFound := hasCreds(cf, key)
	if !credsFound {
		if raw := strings.TrimSpace(getenv(envKukeonRegistryAuth)); raw != "" {
			cf.AuthConfigs[key] = types.AuthConfig{ServerAddress: host, Auth: raw}
			credsFound = true
		}
	}
	return cf, credsFound, nil
}

// newAuthProvider resolves registry credentials for host per the --push
// precedence and wraps them in a BuildKit session attachable. credsFound flows
// through so a later anonymous-rejection can name the precedence list.
func newAuthProvider(getenv func(string) string, host string) (session.Attachable, bool, error) {
	cf, credsFound, err := resolveRegistryAuth(getenv, host)
	if err != nil {
		return nil, false, err
	}
	ap := authprovider.NewDockerAuthProvider(authprovider.DockerAuthProviderConfig{
		AuthConfigProvider: authprovider.LoadAuthConfig(cf),
	})
	return ap, credsFound, nil
}

// isRegistryAuthError reports whether err looks like a registry rejecting the
// push for authentication/authorization reasons. BuildKit / containerd surface
// these as opaque wrapped strings, so this matches the canonical registry-auth
// substrings rather than a typed error. Used only to decide whether to rewrite
// a push failure into the credential-precedence hint.
func isRegistryAuthError(err error) bool {
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{
		"401",
		"403",
		"unauthorized",
		"authentication required",
		"failed to authorize",
		"denied",
		"no basic auth credentials",
		"forbidden",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// pushAuthError rewrites a push failure that looks like a registry auth
// rejection into a message that points the operator at the credential
// precedence list. When no credentials resolved (credsFound is false) the
// message leads with "no credentials for <host>"; when credentials did resolve
// but were rejected, it points at verifying them.
func pushAuthError(tag, host string, credsFound bool, cause error) error {
	const precedence = "(1) $DOCKER_CONFIG/config.json, (2) ~/.docker/config.json, " +
		"(3) the KUKEON_REGISTRY_AUTH env var (base64 user:pass)"
	if !credsFound {
		return fmt.Errorf(
			"push %q failed: no credentials for %s. kukebuild resolves registry credentials in order: %s — "+
				"supply one of these for the registry: %w", tag, host, precedence, cause)
	}
	return fmt.Errorf(
		"push %q failed: %s rejected the resolved credentials. kukebuild resolves registry credentials in order: %s — "+
			"verify the credentials for this registry: %w", tag, host, precedence, cause)
}
