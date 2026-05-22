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
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseArgsDefaults(t *testing.T) {
	cfg, err := parseArgs([]string{"-t", "demo:latest", "/ctx"})
	if err != nil {
		t.Fatalf("parseArgs: unexpected error: %v", err)
	}
	if cfg.tag != "demo:latest" {
		t.Errorf("tag = %q, want demo:latest", cfg.tag)
	}
	if cfg.contextDir != "/ctx" {
		t.Errorf("contextDir = %q, want /ctx", cfg.contextDir)
	}
	if want := filepath.Join("/ctx", "Dockerfile"); cfg.dockerfile != want {
		t.Errorf("dockerfile = %q, want %q", cfg.dockerfile, want)
	}
	if cfg.realm != defaultRealm {
		t.Errorf("realm = %q, want %q", cfg.realm, defaultRealm)
	}
	if cfg.containerdSocket != defaultContainerdSocket {
		t.Errorf("containerdSocket = %q, want %q", cfg.containerdSocket, defaultContainerdSocket)
	}
	if cfg.root != defaultBuildRoot {
		t.Errorf("root = %q, want %q", cfg.root, defaultBuildRoot)
	}
	if cfg.rootExplicit {
		t.Error("rootExplicit = true, want false when --root is not supplied")
	}
	if len(cfg.buildArgs) != 0 {
		t.Errorf("buildArgs = %v, want empty", cfg.buildArgs)
	}
	if cfg.kukeondConfig != "" {
		t.Errorf("kukeondConfig = %q, want empty (default path resolved at build time)", cfg.kukeondConfig)
	}
}

func TestParseArgsAllFlags(t *testing.T) {
	cfg, err := parseArgs([]string{
		"--file", "/ctx/Dockerfile.alt",
		"--tag", "reg/app:v1",
		"--realm", "kuke-system",
		"--containerd-socket", "/tmp/c.sock",
		"--root", "/tmp/state",
		"--kukeond-config", "/tmp/kukeond.yaml",
		"--build-arg", "FOO=bar",
		"--build-arg", "BAZ=qux=quux",
		"/ctx",
	})
	if err != nil {
		t.Fatalf("parseArgs: unexpected error: %v", err)
	}
	if cfg.dockerfile != "/ctx/Dockerfile.alt" {
		t.Errorf("dockerfile = %q", cfg.dockerfile)
	}
	if cfg.kukeondConfig != "/tmp/kukeond.yaml" {
		t.Errorf("kukeondConfig = %q, want /tmp/kukeond.yaml", cfg.kukeondConfig)
	}
	if cfg.realm != "kuke-system" {
		t.Errorf("realm = %q", cfg.realm)
	}
	if cfg.containerdSocket != "/tmp/c.sock" {
		t.Errorf("containerdSocket = %q", cfg.containerdSocket)
	}
	if cfg.root != "/tmp/state" {
		t.Errorf("root = %q", cfg.root)
	}
	if !cfg.rootExplicit {
		t.Error("rootExplicit = false, want true when --root is supplied")
	}
	if cfg.buildArgs["FOO"] != "bar" {
		t.Errorf("buildArgs[FOO] = %q, want bar", cfg.buildArgs["FOO"])
	}
	// A value containing '=' must survive intact (only the first '=' splits).
	if cfg.buildArgs["BAZ"] != "qux=quux" {
		t.Errorf("buildArgs[BAZ] = %q, want qux=quux", cfg.buildArgs["BAZ"])
	}
}

func TestParseArgsSecretsAndCache(t *testing.T) {
	cfg, err := parseArgs([]string{
		"-t", "x:1",
		"--secret", "id=npmrc,src=/host/.npmrc",
		"--secret", "id=tok,src=/host/tok",
		"--cache-to", "type=local,dest=/cache/out",
		"--cache-from", "type=local,src=/cache/in",
		"/ctx",
	})
	if err != nil {
		t.Fatalf("parseArgs: unexpected error: %v", err)
	}
	wantSecrets := []secretSpec{
		{id: "npmrc", src: "/host/.npmrc"},
		{id: "tok", src: "/host/tok"},
	}
	if !reflect.DeepEqual(cfg.secrets, wantSecrets) {
		t.Errorf("secrets = %+v, want %+v", cfg.secrets, wantSecrets)
	}
	if len(cfg.cacheExports) != 1 || cfg.cacheExports[0].typ != "local" ||
		cfg.cacheExports[0].attrs["dest"] != "/cache/out" {
		t.Errorf("cacheExports = %+v, want one local entry dest=/cache/out", cfg.cacheExports)
	}
	if len(cfg.cacheImports) != 1 || cfg.cacheImports[0].typ != "local" ||
		cfg.cacheImports[0].attrs["src"] != "/cache/in" {
		t.Errorf("cacheImports = %+v, want one local entry src=/cache/in", cfg.cacheImports)
	}
}

func TestParseSecretsForwardsExtraCacheAttrs(t *testing.T) {
	// Non-type cache attrs (e.g. mode) pass through to BuildKit verbatim.
	cfg, err := parseArgs([]string{
		"-t", "x:1",
		"--cache-to", "type=local,dest=/c,mode=max",
		"/ctx",
	})
	if err != nil {
		t.Fatalf("parseArgs: unexpected error: %v", err)
	}
	if got := cfg.cacheExports[0].attrs["mode"]; got != "max" {
		t.Errorf("cacheExports[0].attrs[mode] = %q, want max", got)
	}
	if _, ok := cfg.cacheExports[0].attrs["type"]; ok {
		t.Error("type must not be carried in attrs (it is the cacheSpec.typ field)")
	}
}

func TestParseSecretsUsageErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"secret missing id", []string{"-t", "x:1", "--secret", "src=/p", "/ctx"}},
		{"secret missing src", []string{"-t", "x:1", "--secret", "id=n", "/ctx"}},
		{"secret env source deferred", []string{"-t", "x:1", "--secret", "id=n,env=TOKEN", "/ctx"}},
		{"secret unknown key", []string{"-t", "x:1", "--secret", "id=n,src=/p,foo=bar", "/ctx"}},
		{"secret no equals", []string{"-t", "x:1", "--secret", "id", "/ctx"}},
		{"cache-to missing type", []string{"-t", "x:1", "--cache-to", "dest=/c", "/ctx"}},
		{"cache-to non-local type", []string{"-t", "x:1", "--cache-to", "type=registry,ref=r", "/ctx"}},
		{"cache-to missing dest", []string{"-t", "x:1", "--cache-to", "type=local", "/ctx"}},
		{"cache-from missing src", []string{"-t", "x:1", "--cache-from", "type=local", "/ctx"}},
		{"cache-from non-local type", []string{"-t", "x:1", "--cache-from", "type=s3", "/ctx"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseArgs(tc.args)
			if err == nil {
				t.Fatalf("parseArgs(%v): expected error, got nil", tc.args)
			}
			var ue *usageError
			if !errors.As(err, &ue) {
				t.Errorf("parseArgs(%v): error %v is not a *usageError", tc.args, err)
			}
		})
	}
}

func TestParseArgsPush(t *testing.T) {
	// --push defaults off.
	cfg, err := parseArgs([]string{"-t", "registry:5000/app:dev", "/ctx"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if cfg.push {
		t.Error("push = true, want false when --push is not supplied")
	}

	// --push with a fully qualified reference parses and sets the flag.
	cfg, err = parseArgs([]string{"-t", "registry:5000/app:dev", "--push", "/ctx"})
	if err != nil {
		t.Fatalf("parseArgs(--push qualified): %v", err)
	}
	if !cfg.push {
		t.Error("push = false, want true when --push is supplied")
	}
}

func TestParseArgsPushRequiresQualifiedTag(t *testing.T) {
	// --push with a bare name:tag is a usageError naming the required form.
	cases := [][]string{
		{"-t", "app:dev", "--push", "/ctx"},
		{"-t", "eminwux/kukeon:v1", "--push", "/ctx"}, // docker-hub repo, host not explicit
	}
	for _, args := range cases {
		_, err := parseArgs(args)
		if err == nil {
			t.Fatalf("parseArgs(%v): expected error, got nil", args)
		}
		var ue *usageError
		if !errors.As(err, &ue) {
			t.Errorf("parseArgs(%v): error %v is not a *usageError", args, err)
		}
		if !strings.Contains(err.Error(), "REGISTRY/REPO:TAG") {
			t.Errorf("parseArgs(%v): error %q does not name REGISTRY/REPO:TAG", args, err)
		}
	}
}

func TestParseArgsPlatform(t *testing.T) {
	// No --platform: single-image build-host default (nil platforms).
	cfg, err := parseArgs([]string{"-t", "x:1", "/ctx"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if len(cfg.platforms) != 0 {
		t.Errorf("platforms = %v, want empty when --platform is not supplied", cfg.platforms)
	}

	// A single target normalizes and is carried through.
	cfg, err = parseArgs([]string{"-t", "x:1", "--platform", "linux/amd64", "/ctx"})
	if err != nil {
		t.Fatalf("parseArgs(single platform): %v", err)
	}
	if got := []string{"linux/amd64"}; !reflect.DeepEqual(cfg.platforms, got) {
		t.Errorf("platforms = %v, want %v", cfg.platforms, got)
	}

	// A comma-separated list yields the normalized multi-platform set in order.
	cfg, err = parseArgs([]string{"-t", "x:1", "--platform", "linux/amd64,linux/arm64", "/ctx"})
	if err != nil {
		t.Fatalf("parseArgs(multi platform): %v", err)
	}
	want := []string{"linux/amd64", "linux/arm64"}
	if !reflect.DeepEqual(cfg.platforms, want) {
		t.Errorf("platforms = %v, want %v", cfg.platforms, want)
	}
}

func TestParsePlatformsNormalizesAliasesAndSpacing(t *testing.T) {
	// arm64 is normalized to its canonical os/arch; surrounding whitespace and a
	// trailing empty segment are tolerated.
	got, err := parsePlatforms("linux/arm64/v8 , linux/amd64,")
	if err != nil {
		t.Fatalf("parsePlatforms: %v", err)
	}
	want := []string{"linux/arm64", "linux/amd64"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parsePlatforms = %v, want %v", got, want)
	}
}

func TestParsePlatformsUsageErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"platform garbage", []string{"-t", "x:1", "--platform", "not a platform!!", "/ctx"}},
		{"platform empty value", []string{"-t", "x:1", "--platform", " , ", "/ctx"}},
		{"platform duplicate", []string{"-t", "x:1", "--platform", "linux/amd64,linux/amd64", "/ctx"}},
		{"platform duplicate via alias", []string{"-t", "x:1", "--platform", "linux/arm64,linux/arm64/v8", "/ctx"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseArgs(tc.args)
			if err == nil {
				t.Fatalf("parseArgs(%v): expected error, got nil", tc.args)
			}
			var ue *usageError
			if !errors.As(err, &ue) {
				t.Errorf("parseArgs(%v): error %v is not a *usageError", tc.args, err)
			}
		})
	}
}

func TestParseArgsShortAliases(t *testing.T) {
	cfg, err := parseArgs([]string{"-f", "/ctx/Containerfile", "-t", "x:1", "/ctx"})
	if err != nil {
		t.Fatalf("parseArgs: unexpected error: %v", err)
	}
	if cfg.dockerfile != "/ctx/Containerfile" {
		t.Errorf("dockerfile = %q, want /ctx/Containerfile", cfg.dockerfile)
	}
	if cfg.tag != "x:1" {
		t.Errorf("tag = %q, want x:1", cfg.tag)
	}
}

func TestNormalizeImageName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"app:dev", "docker.io/library/app:dev"},
		{"kukeon-smoke:dev", "docker.io/library/kukeon-smoke:dev"},
		{"app", "docker.io/library/app:latest"},
		{"ghcr.io/eminwux/kukeon:v1", "ghcr.io/eminwux/kukeon:v1"},
		{"docker.io/library/app:dev", "docker.io/library/app:dev"},
	}
	for _, tc := range cases {
		got, err := normalizeImageName(tc.in)
		if err != nil {
			t.Errorf("normalizeImageName(%q): unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("normalizeImageName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeImageNameInvalid(t *testing.T) {
	if _, err := normalizeImageName("Invalid Tag!!"); err == nil {
		t.Error("normalizeImageName(invalid): expected error, got nil")
	}
}

func TestParseArgsUsageErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"no context", []string{"-t", "x:1"}},
		{"no tag", []string{"/ctx"}},
		{"empty tag", []string{"-t", "  ", "/ctx"}},
		{"empty realm", []string{"-t", "x:1", "--realm", "", "/ctx"}},
		{"extra positional", []string{"-t", "x:1", "/ctx", "/extra"}},
		{"bad build-arg no equals", []string{"-t", "x:1", "--build-arg", "FOO", "/ctx"}},
		{"bad build-arg empty key", []string{"-t", "x:1", "--build-arg", "=v", "/ctx"}},
		{"unknown flag", []string{"-t", "x:1", "--nope", "/ctx"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseArgs(tc.args)
			if err == nil {
				t.Fatalf("parseArgs(%v): expected error, got nil", tc.args)
			}
			var ue *usageError
			if !errors.As(err, &ue) {
				t.Errorf("parseArgs(%v): error %v is not a *usageError", tc.args, err)
			}
		})
	}
}
