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

package ctr_test

import (
	"strconv"
	"testing"

	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// fullGitSpec mirrors the canonical team blueprint git block: author +
// committer identity, an SSH signing key, commit + tag signing, and an
// allowed-signers file. This is the 15-var rendering (GIT_CONFIG_COUNT=5)
// the live team blueprint templates emit today.
func fullGitSpec() *intmodel.ContainerGit {
	return &intmodel.ContainerGit{
		Author:         &intmodel.GitIdentity{Name: "Emiliano Spinella", Email: "dev@eminwux.com"},
		Committer:      &intmodel.GitIdentity{Name: "Emiliano Spinella", Email: "dev@eminwux.com"},
		SigningKey:     "/home/claude/.ssh/id_ed25519.pub",
		Sign:           []string{intmodel.GitSignCommits, intmodel.GitSignTags},
		AllowedSigners: "/home/claude/.ssh/allowed_signers",
	}
}

// TestBuildContainerSpec_GitEnv asserts the full git block expands to the
// exact GIT_AUTHOR_* / GIT_COMMITTER_* / GIT_CONFIG_* env-var set the team
// blueprint templates emit by hand today, including gpg.format=ssh implied
// by signingKey. Issue #618.
func TestBuildContainerSpec_GitEnv(t *testing.T) {
	spec := applyBuiltSpec(t, intmodel.ContainerSpec{
		ID:    "work",
		Image: "registry.eminwux.com/busybox:latest",
		Git:   fullGitSpec(),
	})
	got := envMap(spec.Process.Env)

	want := map[string]string{
		"GIT_AUTHOR_NAME":     "Emiliano Spinella",
		"GIT_AUTHOR_EMAIL":    "dev@eminwux.com",
		"GIT_COMMITTER_NAME":  "Emiliano Spinella",
		"GIT_COMMITTER_EMAIL": "dev@eminwux.com",
		"GIT_CONFIG_COUNT":    "5",
		"GIT_CONFIG_KEY_0":    "user.signingkey",
		"GIT_CONFIG_VALUE_0":  "/home/claude/.ssh/id_ed25519.pub",
		"GIT_CONFIG_KEY_1":    "gpg.format",
		"GIT_CONFIG_VALUE_1":  "ssh",
		"GIT_CONFIG_KEY_2":    "commit.gpgsign",
		"GIT_CONFIG_VALUE_2":  "true",
		"GIT_CONFIG_KEY_3":    "tag.gpgsign",
		"GIT_CONFIG_VALUE_3":  "true",
		"GIT_CONFIG_KEY_4":    "gpg.ssh.allowedSignersFile",
		"GIT_CONFIG_VALUE_4":  "/home/claude/.ssh/allowed_signers",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("Process.Env[%q] = %q, want %q (full env: %v)", k, got[k], v, spec.Process.Env)
		}
	}
}

// TestBuildContainerSpec_GitEnv_NoSigningRendersNoConfig locks that an
// identity-only git block (no signingKey, no sign) emits the author/committer
// vars but zero GIT_CONFIG_* entries — so a spec that only sets identity never
// leaks an empty GIT_CONFIG_COUNT.
func TestBuildContainerSpec_GitEnv_NoSigningRendersNoConfig(t *testing.T) {
	spec := applyBuiltSpec(t, intmodel.ContainerSpec{
		ID:    "work",
		Image: "registry.eminwux.com/busybox:latest",
		Git: &intmodel.ContainerGit{
			Author:    &intmodel.GitIdentity{Name: "A", Email: "a@example.com"},
			Committer: &intmodel.GitIdentity{Name: "A", Email: "a@example.com"},
		},
	})
	got := envMap(spec.Process.Env)
	if got["GIT_AUTHOR_NAME"] != "A" {
		t.Errorf("GIT_AUTHOR_NAME = %q, want %q", got["GIT_AUTHOR_NAME"], "A")
	}
	for _, k := range []string{"GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0"} {
		if _, ok := got[k]; ok {
			t.Errorf("Process.Env carries %q with no signing config: %q", k, got[k])
		}
	}
}

// TestBuildContainerSpec_GitEnv_SigningKeyWithoutAllowedSigners reproduces the
// issue's documented 13-var rendering (GIT_CONFIG_COUNT=4): a signing block
// with no allowedSigners omits the gpg.ssh.allowedSignersFile pair entirely.
func TestBuildContainerSpec_GitEnv_SigningKeyWithoutAllowedSigners(t *testing.T) {
	spec := applyBuiltSpec(t, intmodel.ContainerSpec{
		ID:    "work",
		Image: "registry.eminwux.com/busybox:latest",
		Git: &intmodel.ContainerGit{
			SigningKey: "/k.pub",
			Sign:       []string{intmodel.GitSignCommits, intmodel.GitSignTags},
		},
	})
	got := envMap(spec.Process.Env)
	if got["GIT_CONFIG_COUNT"] != "4" {
		t.Errorf("GIT_CONFIG_COUNT = %q, want %q", got["GIT_CONFIG_COUNT"], "4")
	}
	for i := 0; i < 4; i++ {
		key := got["GIT_CONFIG_KEY_"+strconv.Itoa(i)]
		if key == "gpg.ssh.allowedSignersFile" {
			t.Errorf("GIT_CONFIG_KEY_%d unexpectedly = gpg.ssh.allowedSignersFile", i)
		}
	}
	if _, ok := got["GIT_CONFIG_KEY_4"]; ok {
		t.Errorf("GIT_CONFIG_KEY_4 present, want only 4 pairs (env: %v)", spec.Process.Env)
	}
}

// TestBuildContainerSpec_GitEnv_CommitsOnly asserts a single sign target
// renders only its config key — tags signing stays off.
func TestBuildContainerSpec_GitEnv_CommitsOnly(t *testing.T) {
	spec := applyBuiltSpec(t, intmodel.ContainerSpec{
		ID:    "work",
		Image: "registry.eminwux.com/busybox:latest",
		Git: &intmodel.ContainerGit{
			SigningKey: "/k.pub",
			Sign:       []string{intmodel.GitSignCommits},
		},
	})
	got := envMap(spec.Process.Env)
	// user.signingkey, gpg.format, commit.gpgsign — no tag.gpgsign.
	if got["GIT_CONFIG_COUNT"] != "3" {
		t.Errorf("GIT_CONFIG_COUNT = %q, want %q", got["GIT_CONFIG_COUNT"], "3")
	}
	for k, v := range envMap(spec.Process.Env) {
		if v == "tag.gpgsign" {
			t.Errorf("found tag.gpgsign key %q with commits-only sign", k)
		}
	}
}

// TestBuildContainerSpec_GitEnv_UserEnvWinsOnCollision asserts the issue's
// merge rule: an explicit env: entry overrides a git-expanded var of the same
// key, with no duplicate left behind.
func TestBuildContainerSpec_GitEnv_UserEnvWinsOnCollision(t *testing.T) {
	spec := applyBuiltSpec(t, intmodel.ContainerSpec{
		ID:    "work",
		Image: "registry.eminwux.com/busybox:latest",
		Git:   fullGitSpec(),
		Env:   []string{"GIT_AUTHOR_EMAIL=override@example.com"},
	})
	got := envMap(spec.Process.Env)
	if got["GIT_AUTHOR_EMAIL"] != "override@example.com" {
		t.Errorf("GIT_AUTHOR_EMAIL = %q, want override (explicit env should win)", got["GIT_AUTHOR_EMAIL"])
	}
	if n := envKeyOccurrences(spec.Process.Env, "GIT_AUTHOR_EMAIL"); n != 1 {
		t.Errorf("GIT_AUTHOR_EMAIL appeared %d times, want 1 (env: %v)", n, spec.Process.Env)
	}
	// Untouched git var still present.
	if got["GIT_AUTHOR_NAME"] != "Emiliano Spinella" {
		t.Errorf("GIT_AUTHOR_NAME = %q, want untouched git value", got["GIT_AUTHOR_NAME"])
	}
}

// TestBuildContainerSpec_GitEnv_NilNoEntries locks that a spec without a git
// block emits no GIT_* entries at all — no behaviour change for existing specs.
func TestBuildContainerSpec_GitEnv_NilNoEntries(t *testing.T) {
	spec := applyBuiltSpec(t, intmodel.ContainerSpec{
		ID:    "work",
		Image: "registry.eminwux.com/busybox:latest",
	})
	for _, kv := range spec.Process.Env {
		if len(kv) >= 4 && kv[:4] == "GIT_" {
			t.Errorf("Process.Env carries unexpected git entry %q with no git block", kv)
		}
	}
}
