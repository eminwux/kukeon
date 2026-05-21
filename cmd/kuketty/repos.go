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
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/eminwux/kukeon/internal/kuketty/setupstatus"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// errRequiredRepoFailed signals that at least one required repo failed to
// clone or fetch. run() maps it to a non-zero exit before sbshserver.Serve so
// the daemon observes the container task as Failed (issue #617, AC: required
// failure is fatal). The per-repo detail is logged to kuketty's slog output as
// each repo is resolved.
var errRequiredRepoFailed = errors.New("one or more required repos failed to resolve")

// processRepos is kuketty's pre-Serve step: it clones (or fetches) each repo
// declared on the mounted ContainerDoc.Spec into its target using the
// container's own git identity (inherited env: HOME, ~/.ssh, ~/.gitconfig,
// GIT_SSH_COMMAND — the same identity onInit scripts use today). Reading
// repos[] straight from the spec (since kuketty owns the spec→TerminalSpec
// build, issue #641) means there is no daemon-rendered sidecar doc.
//
// An empty repos[] is a no-op (nil statuses) — the common case. When any repo
// marked required fails, processRepos logs each outcome and returns
// errRequiredRepoFailed alongside the partial statuses collected so far;
// non-required failures are logged but do not propagate. The returned
// statuses are the payload the GetSetupStatus RPC reports into
// ContainerStatus.Repos (phase 1b, #642) — kukeond pulls them post-Serve. On
// a required failure kuketty exits before Serve, so those statuses are never
// reachable over the RPC; that path stays exit-code-driven (AC #5).
func processRepos(ctx context.Context, repos []v1beta1.ContainerRepo, logger *slog.Logger) ([]setupstatus.Repo, error) {
	if len(repos) == 0 {
		return nil, nil
	}

	statuses := make([]setupstatus.Repo, 0, len(repos))
	var requiredFailed bool
	for i := range repos {
		repo := repos[i]
		status := resolveRepo(ctx, repo, logger)
		statuses = append(statuses, status)
		if status.State == setupstatus.StateFailed {
			logger.WarnContext(ctx, "repo resolution failed",
				"repo", repo.Name, "target", repo.Target, "required", repo.Required, "error", status.Error)
			if repo.Required {
				requiredFailed = true
			}
		}
	}

	if requiredFailed {
		return statuses, errRequiredRepoFailed
	}
	return statuses, nil
}

// resolveRepo clones or fetches a single repo and returns its resolved
// setupstatus.Repo. If target/.git already exists it fetches + fast-forwards
// (clone-if-absent idempotency: the writable rootfs persists across
// kuke stop/start, so a restart never re-clones); otherwise it clones. On
// success it logs the resolved HEAD commit and reports State cloned/fetched
// with the commit; on failure it reports State failed with the error detail.
func resolveRepo(ctx context.Context, repo v1beta1.ContainerRepo, logger *slog.Logger) setupstatus.Repo {
	status := setupstatus.Repo{Name: repo.Name, Target: repo.Target}

	gitDir := filepath.Join(repo.Target, ".git")
	exists := false
	if info, statErr := os.Stat(gitDir); statErr == nil && info.IsDir() {
		exists = true
	}

	state := setupstatus.StateCloned
	var opErr error
	if exists {
		state = setupstatus.StateFetched
		opErr = fetchRepo(ctx, repo)
	} else {
		opErr = cloneRepo(ctx, repo)
	}
	if opErr != nil {
		status.State = setupstatus.StateFailed
		status.Error = opErr.Error()
		return status
	}

	status.State = state
	if head, headErr := repoHead(ctx, repo.Target); headErr == nil {
		status.Commit = head
	}
	logger.InfoContext(ctx, "repo resolved",
		"repo", repo.Name, "target", repo.Target, "state", status.State, "commit", status.Commit)
	return status
}

// cloneRepo clones repo.URL into repo.Target, optionally pinned to repo.Branch.
func cloneRepo(ctx context.Context, repo v1beta1.ContainerRepo) error {
	args := []string{"clone"}
	if repo.Branch != "" {
		args = append(args, "--branch", repo.Branch)
	}
	args = append(args, repo.URL, repo.Target)
	return runGit(ctx, "", args...)
}

// fetchRepo updates an existing checkout: fetch the remote, then (when a branch
// is requested) check it out and fast-forward. A non-fast-forwardable local
// state surfaces as an error rather than a silent divergence.
func fetchRepo(ctx context.Context, repo v1beta1.ContainerRepo) error {
	if err := runGit(ctx, repo.Target, "fetch", "--prune", "origin"); err != nil {
		return err
	}
	if repo.Branch != "" {
		if err := runGit(ctx, repo.Target, "checkout", repo.Branch); err != nil {
			return err
		}
	}
	return runGit(ctx, repo.Target, "pull", "--ff-only")
}

// repoHead returns the full HEAD commit SHA of the checkout at target.
func repoHead(ctx context.Context, target string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", target, "rev-parse", "HEAD")
	cmd.Env = gitEnv()
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// runGit runs a git subcommand with the container's git identity (HOME,
// ~/.ssh, ~/.gitconfig — inherited via gitEnv) plus the non-interactive guards
// gitEnv adds. dir is the working directory ("" for the process cwd, e.g.
// clone). On failure the combined output is folded into the error so the log
// line carries an actionable message.
func runGit(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = gitEnv()
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(out))
		if trimmed != "" {
			return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, trimmed)
		}
		return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

// gitEnv returns kuketty's environment (so the container's git identity —
// HOME, ~/.ssh, ~/.gitconfig — is used) augmented with non-interactive guards
// so a first-time clone of an unseen host never hangs waiting for operator
// input (issue #617, non-interactive host-key handling):
//
//   - GIT_TERMINAL_PROMPT=0 makes git fail instead of prompting for HTTPS
//     credentials.
//   - GIT_SSH_COMMAND defaults to `ssh -o StrictHostKeyChecking=accept-new
//     -o BatchMode=yes` so a first-time git@host clone trust-on-first-use
//     accepts the new key (but still errors on a *changed* key) rather than
//     blocking on the interactive yes/no prompt.
//
// Both defaults yield to a value the container already set: an image that
// pre-seeds known_hosts and exports its own GIT_SSH_COMMAND, or one that wants
// HTTPS credential prompting, wins. kuketty only fills the gap so the
// out-of-the-box path is non-interactive.
func gitEnv() []string {
	env := os.Environ()
	if _, ok := os.LookupEnv("GIT_TERMINAL_PROMPT"); !ok {
		env = append(env, "GIT_TERMINAL_PROMPT=0")
	}
	if _, ok := os.LookupEnv("GIT_SSH_COMMAND"); !ok {
		env = append(env, "GIT_SSH_COMMAND=ssh -o StrictHostKeyChecking=accept-new -o BatchMode=yes")
	}
	return env
}
