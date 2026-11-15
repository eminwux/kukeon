// Copyright 2026 Emiliano Spinella (eminwux)
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

package initialize

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/pkg/team/config"
)

func TestMaterializeDeclaredRevisions(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.test",
			"GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.test",
		)
		raw, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, raw)
		}
		return strings.TrimSpace(string(raw))
	}
	git("init", "--initial-branch=main")
	if err := os.WriteFile(filepath.Join(repo, "definition"), []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "definition")
	// These commits are disposable test fixtures, independent of signing setup.
	git(
		"-c",
		"commit.gpgsign=false",
		"-c",
		"user.name=Fixture",
		"-c",
		"user.email=fixture@example.test",
		"commit",
		"-m",
		"fixture",
	)
	first := git("rev-parse", "HEAD")
	git("tag", "-a", "v1", "-m", "fixture", "--no-sign")
	if err := os.WriteFile(filepath.Join(repo, "definition"), []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "definition")
	git(
		"-c",
		"commit.gpgsign=false",
		"-c",
		"user.name=Fixture",
		"-c",
		"user.email=fixture@example.test",
		"commit",
		"-m",
		"second fixture",
	)
	for _, tc := range []struct{ name, branch, tag, commit, want string }{{"branch", "main", "", "", "second"}, {"tag", "", "v1", "", "first"}, {"commit", "", "", first, "first"}} {
		t.Run(tc.name, func(t *testing.T) {
			d := config.Defaults{
				Sources: []config.Source{
					{Name: "selected", URL: repo, Branch: tc.branch, Tag: tc.tag, Commit: tc.commit},
					{Name: "unused", URL: "/not/a/repo", Branch: "main"},
				},
				Roles: []config.Role{{Source: "selected"}},
			}
			sources, cleanup, err := materialize(t.Context(), d)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			raw, err := fs.ReadFile(sources["selected"], "definition")
			if err != nil || string(raw) != tc.want {
				t.Fatalf("definition=%s error=%v", raw, err)
			}
			if sources["unused"] != nil {
				t.Fatal("fetched unused source")
			}
		})
	}
}

func TestMaterializeFailureDoesNotEchoRemoteCredentials(t *testing.T) {
	d := config.Defaults{
		Sources: []config.Source{
			{Name: "selected", URL: "https://sensitive-value@127.0.0.1:1/missing", Branch: "main"},
		},
		Roles: []config.Role{{Source: "selected"}},
	}
	_, _, err := materialize(t.Context(), d)
	if err == nil || strings.Contains(err.Error(), "sensitive-value") {
		t.Fatalf("error=%v", err)
	}
}

// Every listed harness is selected unless the role names one, so a harness
// source no role uses is still fetched (#40), while a narrowed-out one is not.
func TestMaterializeSelectedHarnessSources(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "--initial-branch=main"},
		{
			"-c", "commit.gpgsign=false", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test",
			"commit", "--allow-empty", "-m", "fixture",
		},
	} {
		if raw, err := exec.CommandContext(t.Context(), "git", append([]string{"-C", repo}, args...)...).
			CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, raw)
		}
	}
	d := config.Defaults{
		Sources: []config.Source{
			{Name: "roles", URL: repo, Branch: "main"},
			{Name: "harnesses", URL: repo, Branch: "main"},
			{Name: "unused", URL: "/not/a/repo", Branch: "main"},
		},
		Roles: []config.Role{{Name: "dev", Source: "roles"}},
		Harnesses: []config.Harness{
			{Name: "claude", Source: "harnesses"},
			{Name: "codex", Source: "harnesses"},
		},
	}
	sources, cleanup, err := materialize(t.Context(), d)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if sources["roles"] == nil || sources["harnesses"] == nil || sources["unused"] != nil {
		t.Fatalf("materialized %v", sources)
	}
	// A role narrowed to one harness does not pull other harness sources.
	d.Roles[0].Harness = "claude"
	d.Harnesses[1].Source = "unused"
	narrowed, cleanupNarrowed, err := materialize(t.Context(), d)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupNarrowed()
	if narrowed["harnesses"] == nil || narrowed["unused"] != nil {
		t.Fatalf("narrowed materialized %v", narrowed)
	}
}
