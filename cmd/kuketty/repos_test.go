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
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// discardLogger is a logger that drops output, for tests that exercise
// processRepos without asserting on log lines.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// gitRun runs git in dir with a hermetic identity so the test never depends on
// (or mutates) the host's global git config.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{
		"-c", "user.email=test@kukeon.invalid",
		"-c", "user.name=kukeon test",
		"-c", "init.defaultBranch=main",
		"-c", "commit.gpgsign=false",
	}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %q: %v\n%s", args, dir, err, out)
	}
}

// makeSourceRepo creates a non-bare source repo on branch main with one commit
// adding fileName, and returns its path (usable as a clone URL).
func makeSourceRepo(t *testing.T, fileName string) string {
	t.Helper()
	src := t.TempDir()
	gitRun(t, src, "init")
	if err := os.WriteFile(filepath.Join(src, fileName), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, src, "add", ".")
	gitRun(t, src, "commit", "-m", "initial")
	return src
}

func TestProcessRepos_EmptyIsNoOp(t *testing.T) {
	if err := processRepos(context.Background(), nil, discardLogger()); err != nil {
		t.Fatalf("nil repos should be a no-op, got %v", err)
	}
	if err := processRepos(context.Background(), []v1beta1.ContainerRepo{}, discardLogger()); err != nil {
		t.Fatalf("empty repos should be a no-op, got %v", err)
	}
}

func TestProcessRepos_ClonesIntoTarget(t *testing.T) {
	src := makeSourceRepo(t, "README.md")
	target := filepath.Join(t.TempDir(), "checkout")

	repos := []v1beta1.ContainerRepo{
		{Name: "project", Target: target, Branch: "main", URL: src, Required: true},
	}
	if err := processRepos(context.Background(), repos, discardLogger()); err != nil {
		t.Fatalf("processRepos: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "README.md")); err != nil {
		t.Fatalf("expected cloned file: %v", err)
	}
}

func TestProcessRepos_FetchesExistingCheckout(t *testing.T) {
	src := makeSourceRepo(t, "README.md")
	target := filepath.Join(t.TempDir(), "checkout")
	repos := []v1beta1.ContainerRepo{
		{Name: "project", Target: target, Branch: "main", URL: src, Required: true},
	}

	// First pass clones.
	if err := processRepos(context.Background(), repos, discardLogger()); err != nil {
		t.Fatalf("first processRepos (clone): %v", err)
	}

	// Add a new commit upstream, then re-run: existing .git → fetch path.
	if err := os.WriteFile(filepath.Join(src, "second.txt"), []byte("more\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, src, "add", ".")
	gitRun(t, src, "commit", "-m", "second")

	if err := processRepos(context.Background(), repos, discardLogger()); err != nil {
		t.Fatalf("second processRepos (fetch): %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "second.txt")); err != nil {
		t.Errorf("fetch should have fast-forwarded the new commit: %v", err)
	}
}

func TestProcessRepos_RequiredFailurePropagates(t *testing.T) {
	dir := t.TempDir()
	repos := []v1beta1.ContainerRepo{
		{
			Name:     "missing",
			Target:   filepath.Join(dir, "checkout"),
			URL:      filepath.Join(dir, "does-not-exist"),
			Required: true,
		},
	}

	err := processRepos(context.Background(), repos, discardLogger())
	if !errors.Is(err, errRequiredRepoFailed) {
		t.Fatalf("want errRequiredRepoFailed, got %v", err)
	}
}

func TestProcessRepos_NonRequiredFailureDoesNotPropagate(t *testing.T) {
	dir := t.TempDir()
	repos := []v1beta1.ContainerRepo{
		{
			Name:     "optional",
			Target:   filepath.Join(dir, "checkout"),
			URL:      filepath.Join(dir, "does-not-exist"),
			Required: false,
		},
	}

	if err := processRepos(context.Background(), repos, discardLogger()); err != nil {
		t.Fatalf("non-required failure must not propagate, got %v", err)
	}
}

func TestProcessRepos_RequiredFailureAfterSuccessStillPropagates(t *testing.T) {
	src := makeSourceRepo(t, "README.md")
	dir := t.TempDir()
	repos := []v1beta1.ContainerRepo{
		{Name: "good", Target: filepath.Join(dir, "good"), Branch: "main", URL: src, Required: false},
		{Name: "bad", Target: filepath.Join(dir, "bad"), URL: filepath.Join(dir, "nope"), Required: true},
	}

	err := processRepos(context.Background(), repos, discardLogger())
	if !errors.Is(err, errRequiredRepoFailed) {
		t.Fatalf("want errRequiredRepoFailed, got %v", err)
	}
	// The good repo (declared before the failing one) was still resolved.
	if _, statErr := os.Stat(filepath.Join(dir, "good", "README.md")); statErr != nil {
		t.Errorf("expected the non-required repo to clone despite a later required failure: %v", statErr)
	}
}
