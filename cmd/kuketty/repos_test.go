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
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/kuketty/setupstatus"
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
		"-c", "tag.gpgsign=false",
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
	statuses, err := processRepos(context.Background(), nil, discardLogger())
	if err != nil {
		t.Fatalf("nil repos should be a no-op, got %v", err)
	}
	if statuses != nil {
		t.Fatalf("nil repos should yield nil statuses, got %v", statuses)
	}
	statuses, err = processRepos(context.Background(), []v1beta1.ContainerRepo{}, discardLogger())
	if err != nil {
		t.Fatalf("empty repos should be a no-op, got %v", err)
	}
	if statuses != nil {
		t.Fatalf("empty repos should yield nil statuses, got %v", statuses)
	}
}

func TestProcessRepos_ClonesIntoTarget(t *testing.T) {
	src := makeSourceRepo(t, "README.md")
	target := filepath.Join(t.TempDir(), "checkout")

	repos := []v1beta1.ContainerRepo{
		{Name: "project", Target: target, Branch: "main", URL: src, Required: true},
	}
	statuses, err := processRepos(context.Background(), repos, discardLogger())
	if err != nil {
		t.Fatalf("processRepos: %v", err)
	}
	if _, err = os.Stat(filepath.Join(target, "README.md")); err != nil {
		t.Fatalf("expected cloned file: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("want 1 status, got %d: %v", len(statuses), statuses)
	}
	got := statuses[0]
	if got.Name != "project" || got.Target != target || got.State != setupstatus.StateCloned {
		t.Errorf("unexpected status: %+v", got)
	}
	if got.Commit == "" {
		t.Errorf("cloned repo should report a resolved HEAD commit, got empty")
	}
	if got.Error != "" {
		t.Errorf("successful clone should report no error, got %q", got.Error)
	}
}

func TestProcessRepos_FetchesExistingCheckout(t *testing.T) {
	src := makeSourceRepo(t, "README.md")
	target := filepath.Join(t.TempDir(), "checkout")
	repos := []v1beta1.ContainerRepo{
		{Name: "project", Target: target, Branch: "main", URL: src, Required: true},
	}

	// First pass clones.
	if _, err := processRepos(context.Background(), repos, discardLogger()); err != nil {
		t.Fatalf("first processRepos (clone): %v", err)
	}

	// Add a new commit upstream, then re-run: existing .git → fetch path.
	if err := os.WriteFile(filepath.Join(src, "second.txt"), []byte("more\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, src, "add", ".")
	gitRun(t, src, "commit", "-m", "second")

	statuses, err := processRepos(context.Background(), repos, discardLogger())
	if err != nil {
		t.Fatalf("second processRepos (fetch): %v", err)
	}
	if _, err = os.Stat(filepath.Join(target, "second.txt")); err != nil {
		t.Errorf("fetch should have fast-forwarded the new commit: %v", err)
	}
	if len(statuses) != 1 || statuses[0].State != setupstatus.StateFetched {
		t.Fatalf("want a single fetched status, got %+v", statuses)
	}
	if statuses[0].Commit == "" {
		t.Errorf("fetched repo should report a resolved HEAD commit, got empty")
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

	statuses, err := processRepos(context.Background(), repos, discardLogger())
	if !errors.Is(err, errRequiredRepoFailed) {
		t.Fatalf("want errRequiredRepoFailed, got %v", err)
	}
	// Even on the required-failure path the partial statuses are returned so
	// callers (and tests) can see the failure detail, though kuketty exits
	// before Serve so the RPC never reports them.
	if len(statuses) != 1 || statuses[0].State != setupstatus.StateFailed {
		t.Fatalf("want a single failed status, got %+v", statuses)
	}
	if statuses[0].Error == "" {
		t.Errorf("failed repo should carry an error detail, got empty")
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

	statuses, err := processRepos(context.Background(), repos, discardLogger())
	if err != nil {
		t.Fatalf("non-required failure must not propagate, got %v", err)
	}
	if len(statuses) != 1 || statuses[0].State != setupstatus.StateFailed {
		t.Fatalf("a non-required failure should still be reported as a failed status, got %+v", statuses)
	}
}

// gitCapture runs git in dir with the hermetic identity and returns trimmed
// stdout. Used to read out commit SHAs without polluting host config.
func gitCapture(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{
		"-c", "user.email=test@kukeon.invalid",
		"-c", "user.name=kukeon test",
		"-c", "init.defaultBranch=main",
		"-c", "commit.gpgsign=false",
		"-c", "tag.gpgsign=false",
	}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v in %q: %v", args, dir, err)
	}
	return string(out)
}

// TestProcessRepos_RefPinsCloneAndSurvivesRestart exercises the AC for #1034:
// a Ref-pinned repo clones at a detached HEAD on first boot, and a fetch path
// re-run (existing .git → in-place restart) stays idempotent — the prior
// `git pull --ff-only` on a detached HEAD aborted required-repo container
// start.
func TestProcessRepos_RefPinsCloneAndSurvivesRestart(t *testing.T) {
	src := makeSourceRepo(t, "README.md")
	gitRun(t, src, "tag", "v0.1.0")
	tagSHA := strings.TrimSpace(gitCapture(t, src, "rev-parse", "v0.1.0^{commit}"))

	target := filepath.Join(t.TempDir(), "checkout")
	repos := []v1beta1.ContainerRepo{
		{Name: "pinned", Target: target, Ref: "v0.1.0", URL: src, Required: true},
	}

	// First pass: clone at the tag (detached HEAD).
	statuses, err := processRepos(context.Background(), repos, discardLogger())
	if err != nil {
		t.Fatalf("first processRepos (clone): %v", err)
	}
	if len(statuses) != 1 || statuses[0].State != setupstatus.StateCloned {
		t.Fatalf("want a single cloned status, got %+v", statuses)
	}
	if statuses[0].Commit != tagSHA {
		t.Errorf("clone HEAD = %q, want tag commit %q", statuses[0].Commit, tagSHA)
	}

	// Push a new commit on main upstream so a branch-tracking pull would
	// advance HEAD — a Ref pin must ignore it.
	if writeErr := os.WriteFile(filepath.Join(src, "second.txt"), []byte("more\n"), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}
	gitRun(t, src, "add", ".")
	gitRun(t, src, "commit", "-m", "second")

	// Second pass: fetch path — must NOT error on detached HEAD (the
	// pre-fix `git pull --ff-only` did), and must stay on the tag.
	statuses, err = processRepos(context.Background(), repos, discardLogger())
	if err != nil {
		t.Fatalf("second processRepos (fetch on detached HEAD): %v", err)
	}
	if len(statuses) != 1 || statuses[0].State != setupstatus.StateFetched {
		t.Fatalf("want a single fetched status, got %+v", statuses)
	}
	if statuses[0].Commit != tagSHA {
		t.Errorf("fetch HEAD = %q, want tag commit %q (Ref pin should not advance)", statuses[0].Commit, tagSHA)
	}
	if _, statErr := os.Stat(filepath.Join(target, "second.txt")); statErr == nil {
		t.Errorf("Ref-pinned repo should not pull the upstream second commit")
	}
}

// TestProcessRepos_RefPinAcceptsCommitSHA verifies that Ref also accepts a
// full commit SHA (not just tag names) — the AC's "tag or full SHA" wording.
func TestProcessRepos_RefPinAcceptsCommitSHA(t *testing.T) {
	src := makeSourceRepo(t, "README.md")
	firstSHA := strings.TrimSpace(gitCapture(t, src, "rev-parse", "HEAD"))
	// Add a second commit so HEAD ≠ pinned SHA: if the clone did not detach,
	// HEAD would land on the newer commit.
	if err := os.WriteFile(filepath.Join(src, "second.txt"), []byte("more\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, src, "add", ".")
	gitRun(t, src, "commit", "-m", "second")

	target := filepath.Join(t.TempDir(), "checkout")
	repos := []v1beta1.ContainerRepo{
		{Name: "pinned", Target: target, Ref: firstSHA, URL: src, Required: true},
	}
	statuses, err := processRepos(context.Background(), repos, discardLogger())
	if err != nil {
		t.Fatalf("processRepos: %v", err)
	}
	if len(statuses) != 1 || statuses[0].Commit != firstSHA {
		t.Fatalf("want clone pinned at %q, got %+v", firstSHA, statuses)
	}
	if _, statErr := os.Stat(filepath.Join(target, "second.txt")); statErr == nil {
		t.Errorf("commit-SHA pin should not contain files from later commits")
	}
}

func TestProcessRepos_RequiredFailureAfterSuccessStillPropagates(t *testing.T) {
	src := makeSourceRepo(t, "README.md")
	dir := t.TempDir()
	repos := []v1beta1.ContainerRepo{
		{Name: "good", Target: filepath.Join(dir, "good"), Branch: "main", URL: src, Required: false},
		{Name: "bad", Target: filepath.Join(dir, "bad"), URL: filepath.Join(dir, "nope"), Required: true},
	}

	statuses, err := processRepos(context.Background(), repos, discardLogger())
	if !errors.Is(err, errRequiredRepoFailed) {
		t.Fatalf("want errRequiredRepoFailed, got %v", err)
	}
	// The good repo (declared before the failing one) was still resolved.
	if _, statErr := os.Stat(filepath.Join(dir, "good", "README.md")); statErr != nil {
		t.Errorf("expected the non-required repo to clone despite a later required failure: %v", statErr)
	}
	// Statuses preserve declaration order: good (cloned) then bad (failed).
	if len(statuses) != 2 {
		t.Fatalf("want 2 statuses, got %d: %+v", len(statuses), statuses)
	}
	if statuses[0].Name != "good" || statuses[0].State != setupstatus.StateCloned {
		t.Errorf("status[0] = %+v, want good/cloned", statuses[0])
	}
	if statuses[1].Name != "bad" || statuses[1].State != setupstatus.StateFailed {
		t.Errorf("status[1] = %+v, want bad/failed", statuses[1])
	}
}
