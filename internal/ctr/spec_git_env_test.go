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
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/gitcred/gitplan"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

// fullGitSpec mirrors the canonical pre-#1472 team blueprint git block: author
// + committer identity, an SSH signing key path, commit + tag signing, and an
// allowed-signers file.
func fullGitSpec() *intmodel.ContainerGit {
	return &intmodel.ContainerGit{
		Author:         &intmodel.GitIdentity{Name: "Emiliano Spinella", Email: "dev@eminwux.com"},
		Committer:      &intmodel.GitIdentity{Name: "Emiliano Spinella", Email: "dev@eminwux.com"},
		SigningKey:     "/home/claude/.ssh/id_ed25519.pub",
		Sign:           []string{intmodel.GitSignCommits, intmodel.GitSignTags},
		AllowedSigners: "/home/claude/.ssh/allowed_signers",
	}
}

func gitEnvMap(env []string) map[string]string {
	out := make(map[string]string, len(env))
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		out[k] = v
	}
	return out
}

// TestKukeonContainerEnv_GitPointsAtStagedConfig locks the #1472 rendering: a
// git block no longer expands to GIT_AUTHOR_* / GIT_CONFIG_* env (which would
// outrank a repo's local config and so defeat per-repo identity). The only git
// entry is GIT_CONFIG_SYSTEM naming the staged cell-global gitconfig.
func TestKukeonContainerEnv_GitPointsAtStagedConfig(t *testing.T) {
	env := kukeonContainerEnv(intmodel.ContainerSpec{ID: "work", Git: fullGitSpec()})
	got := gitEnvMap(env)
	if got[gitplan.ConfigEnv] != gitplan.ConfigPath() {
		t.Errorf("%s = %q, want %q (env: %v)", gitplan.ConfigEnv, got[gitplan.ConfigEnv], gitplan.ConfigPath(), env)
	}
	for k := range got {
		if strings.HasPrefix(k, "GIT_AUTHOR_") || strings.HasPrefix(k, "GIT_COMMITTER_") ||
			strings.HasPrefix(k, "GIT_CONFIG_KEY_") || k == "GIT_CONFIG_COUNT" {
			t.Errorf("env carries %q; identity must live in the gitconfig file, not env", k)
		}
	}
}

// TestKukeonContainerEnv_RepoGitAloneNeedsConfig asserts a container whose only
// git declaration is on a repo still gets the staged config wired.
func TestKukeonContainerEnv_RepoGitAloneNeedsConfig(t *testing.T) {
	env := kukeonContainerEnv(intmodel.ContainerSpec{
		ID: "work",
		Repos: []intmodel.ContainerRepo{{
			Name: "r", URL: "https://example.com/o/r.git",
			Git: &intmodel.RepoGit{Author: &intmodel.GitIdentity{Name: "A", Email: "a@example.com"}},
		}},
	})
	if gitEnvMap(env)[gitplan.ConfigEnv] != gitplan.ConfigPath() {
		t.Errorf("repo-scoped git did not wire %s (env: %v)", gitplan.ConfigEnv, env)
	}
}

// TestKukeonContainerEnv_UserEnvWinsOverGitConfig asserts an explicit env entry
// overrides the kukeon-provided config pointer, with no duplicate left behind.
func TestKukeonContainerEnv_UserEnvWinsOverGitConfig(t *testing.T) {
	env := kukeonContainerEnv(intmodel.ContainerSpec{
		ID:  "work",
		Git: fullGitSpec(),
		Env: []string{gitplan.ConfigEnv + "=/custom"},
	})
	n := 0
	for _, kv := range env {
		if strings.HasPrefix(kv, gitplan.ConfigEnv+"=") {
			n++
		}
	}
	if n != 1 || gitEnvMap(env)[gitplan.ConfigEnv] != "/custom" {
		t.Errorf("explicit env should win exactly once, got %v", env)
	}
}

// TestKukeonContainerEnv_NoGitNoEntries locks that a spec without any git
// declaration emits no GIT_* entries at all.
func TestKukeonContainerEnv_NoGitNoEntries(t *testing.T) {
	for _, kv := range kukeonContainerEnv(intmodel.ContainerSpec{ID: "work"}) {
		if strings.HasPrefix(kv, "GIT_") {
			t.Errorf("env carries unexpected git entry %q with no git block", kv)
		}
	}
}

// TestLegacyGitEnv asserts the pre-#1472 expansion is preserved verbatim for
// containers created before the staged gitconfig existed: GIT_AUTHOR_* /
// GIT_COMMITTER_* / GIT_CONFIG_* including gpg.format=ssh implied by
// signingKey. Issue #618.
func TestLegacyGitEnv(t *testing.T) {
	got := gitEnvMap(legacyGitContainerEnv(intmodel.ContainerSpec{ID: "work", Git: fullGitSpec()}))
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
			t.Errorf("legacy env[%q] = %q, want %q", k, got[k], v)
		}
	}
	if _, ok := got[gitplan.ConfigEnv]; ok {
		t.Errorf("legacy env must not point at a gitconfig the container cannot see")
	}
}

// TestLegacyGitEnv_Shapes covers the remaining pre-#1472 renderings: identity
// only (no GIT_CONFIG_* at all), signing without allowed signers (4 pairs),
// and commits-only signing (3 pairs, no tag.gpgsign).
func TestLegacyGitEnv_Shapes(t *testing.T) {
	identity := gitEnvMap(gitEnv(&intmodel.ContainerGit{
		Author:    &intmodel.GitIdentity{Name: "A", Email: "a@example.com"},
		Committer: &intmodel.GitIdentity{Name: "A", Email: "a@example.com"},
	}))
	if identity["GIT_AUTHOR_NAME"] != "A" {
		t.Errorf("GIT_AUTHOR_NAME = %q, want A", identity["GIT_AUTHOR_NAME"])
	}
	if _, ok := identity["GIT_CONFIG_COUNT"]; ok {
		t.Errorf("identity-only block leaked GIT_CONFIG_COUNT")
	}

	noSigners := gitEnvMap(gitEnv(&intmodel.ContainerGit{
		SigningKey: "/k.pub",
		Sign:       []string{intmodel.GitSignCommits, intmodel.GitSignTags},
	}))
	if noSigners["GIT_CONFIG_COUNT"] != "4" {
		t.Errorf("GIT_CONFIG_COUNT = %q, want 4", noSigners["GIT_CONFIG_COUNT"])
	}
	for i := range 4 {
		if noSigners["GIT_CONFIG_KEY_"+strconv.Itoa(i)] == "gpg.ssh.allowedSignersFile" {
			t.Errorf("GIT_CONFIG_KEY_%d unexpectedly = gpg.ssh.allowedSignersFile", i)
		}
	}

	commitsOnly := gitEnvMap(gitEnv(&intmodel.ContainerGit{
		SigningKey: "/k.pub",
		Sign:       []string{intmodel.GitSignCommits},
	}))
	if commitsOnly["GIT_CONFIG_COUNT"] != "3" {
		t.Errorf("GIT_CONFIG_COUNT = %q, want 3", commitsOnly["GIT_CONFIG_COUNT"])
	}
	for k, v := range commitsOnly {
		if v == "tag.gpgsign" {
			t.Errorf("found tag.gpgsign key %q with commits-only sign", k)
		}
	}
	if gitEnv(nil) != nil {
		t.Errorf("gitEnv(nil) must be nil")
	}
}

// TestFreshProcessEnv_GitFollowsContainerMounts is the upgrade guard: a reused
// container keeps the env rendering matching the mounts it was created with.
// One created before #1472 has no /.kukeon/git mount, so pointing git at the
// staged config would silently drop its identity; it keeps the legacy env. One
// created after carries the mount and gets the config pointer.
func TestFreshProcessEnv_GitFollowsContainerMounts(t *testing.T) {
	ctx := context.Background()
	in := intmodel.ContainerSpec{ID: "work", Git: fullGitSpec()}

	for _, tc := range []struct {
		name   string
		mounts []runtimespec.Mount
		legacy bool
	}{
		{name: "pre-1472 container", mounts: []runtimespec.Mount{{Destination: "/kept"}}, legacy: true},
		{name: "container with staged git", mounts: []runtimespec.Mount{{Destination: gitplan.MountPath}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := &runtimespec.Spec{
				Process: &runtimespec.Process{Env: []string{"PATH=/bin"}},
				Mounts:  tc.mounts,
			}
			if err := withOriginalImageEnv(ctx, nil, nil, spec); err != nil {
				t.Fatal(err)
			}
			if err := WithFreshProcessEnv(in, "")(ctx, nil, nil, spec); err != nil {
				t.Fatal(err)
			}
			got := gitEnvMap(spec.Process.Env)
			_, hasPointer := got[gitplan.ConfigEnv]
			_, hasAuthor := got["GIT_AUTHOR_NAME"]
			if hasPointer == tc.legacy || hasAuthor != tc.legacy {
				t.Errorf(
					"legacy=%v: pointer=%v author=%v (env: %v)",
					tc.legacy,
					hasPointer,
					hasAuthor,
					spec.Process.Env,
				)
			}
		})
	}
}
