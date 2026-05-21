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
// An empty repos[] is a no-op (nil) — the common case. When any repo marked
// required fails, processRepos logs each outcome and returns
// errRequiredRepoFailed; non-required failures are logged but do not
// propagate. Per-repo status reporting into ContainerStatus.Repos rides the
// GetSetupStatus RPC in phase 1b (#642) — phase 1a clones and logs only.
func processRepos(ctx context.Context, repos []v1beta1.ContainerRepo, logger *slog.Logger) error {
	if len(repos) == 0 {
		return nil
	}

	var requiredFailed bool
	for i := range repos {
		repo := repos[i]
		if err := resolveRepo(ctx, repo, logger); err != nil {
			logger.WarnContext(ctx, "repo resolution failed",
				"repo", repo.Name, "target", repo.Target, "required", repo.Required, "error", err)
			if repo.Required {
				requiredFailed = true
			}
			continue
		}
	}

	if requiredFailed {
		return errRequiredRepoFailed
	}
	return nil
}

// resolveRepo clones or fetches a single repo. If target/.git already exists it
// fetches + fast-forwards (clone-if-absent idempotency: the writable rootfs
// persists across kuke stop/start, so a restart never re-clones); otherwise it
// clones. On success it logs the resolved HEAD commit.
func resolveRepo(ctx context.Context, repo v1beta1.ContainerRepo, logger *slog.Logger) error {
	gitDir := filepath.Join(repo.Target, ".git")
	exists := false
	if info, statErr := os.Stat(gitDir); statErr == nil && info.IsDir() {
		exists = true
	}

	state := "cloned"
	var opErr error
	if exists {
		state = "fetched"
		opErr = fetchRepo(ctx, repo)
	} else {
		opErr = cloneRepo(ctx, repo)
	}
	if opErr != nil {
		return opErr
	}

	commit := ""
	if head, headErr := repoHead(ctx, repo.Target); headErr == nil {
		commit = head
	}
	logger.InfoContext(ctx, "repo resolved",
		"repo", repo.Name, "target", repo.Target, "state", state, "commit", commit)
	return nil
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
