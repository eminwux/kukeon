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
	"sync"

	"github.com/eminwux/kukeon/internal/gitcred/gitplan"
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
// declared on the mounted ContainerDoc.Spec into its target. Each repo is
// resolved with the git setup the daemon planned for it (gitplan.Plan, issue
// #1472): the clone URL whose transport follows the bound credential, and the
// repo-local config carrying the source's own identity, credential and signing
// key. A repo with no plan entry uses the cell-global gitconfig, or — for a
// container that declares no git block at all — whatever the image provides
// (HOME, ~/.ssh, ~/.gitconfig, GIT_SSH_COMMAND). Reading repos[] straight from
// the spec (since kuketty owns the spec→TerminalSpec build, issue #641) means
// there is no daemon-rendered sidecar doc.
// An empty repos[] is a no-op (nil statuses) — the common case. When any repo
// marked required fails, processRepos logs each outcome and returns
// errRequiredRepoFailed alongside the partial statuses collected so far;
// non-required failures are logged but do not propagate. The returned
// statuses are the payload the GetSetupStatus RPC reports into
// ContainerStatus.Repos (phase 1b, #642) — kukeond pulls them post-Serve. On
// a required failure kuketty exits before Serve, so those statuses are never
// reachable over the RPC; that path stays exit-code-driven (AC #5).
func processRepos(
	ctx context.Context, repos []v1beta1.ContainerRepo, plan *gitplan.Plan, logger *slog.Logger,
) ([]setupstatus.Repo, error) {
	if len(repos) == 0 {
		return nil, nil
	}

	statuses := make([]setupstatus.Repo, 0, len(repos))
	var requiredFailed bool
	for i := range repos {
		repo := repos[i]
		status := resolveRepo(ctx, repo, plan, logger)
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

// repoGit is the git invocation context of one repo: the effective clone URL,
// the repo-local config, and the environment git runs with.
type repoGit struct {
	url    string
	config []gitplan.ConfigEntry
	env    []string
}

// planRepoGit resolves the invocation context of a repo from the plan.
func planRepoGit(repo v1beta1.ContainerRepo, plan *gitplan.Plan) repoGit {
	rg := repoGit{url: repo.URL}
	managedSSH := false
	if plan != nil {
		managedSSH = plan.SSHCommand
		if entry, ok := plan.Repos[repo.Name]; ok {
			if entry.URL != "" {
				rg.url = entry.URL
			}
			rg.config = entry.Config
			for _, c := range entry.Config {
				if c.Key == "core.sshCommand" {
					managedSSH = true
				}
			}
		}
	}
	rg.env = gitEnv(managedSSH)
	return rg
}

// configFlags renders the repo-local config as `-c key=value` flags, so the
// clone itself already runs with the source's credential. A repeated key
// accumulates, matching how the entries are later written to the checkout.
func (rg repoGit) configFlags() []string {
	const flagsPerEntry = 2 // "-c" and "key=value"
	out := make([]string, 0, flagsPerEntry*len(rg.config))
	for _, c := range rg.config {
		out = append(out, "-c", c.Key+"="+c.Value)
	}
	return out
}

// resolveRepo clones or fetches a single repo and returns its resolved
// setupstatus.Repo. If target/.git already exists it fetches + fast-forwards
// (clone-if-absent idempotency: the writable rootfs persists across
// kuke stop/start, so a restart never re-clones); otherwise it clones. Either
// way the planned repo-local config and origin URL are (re)written into the
// checkout afterwards, so the workload's own commits and pushes use the
// source's identity and an edited credential takes effect on the next start.
// On success it logs the resolved HEAD commit and reports State cloned/fetched
// with the commit; on failure it reports State failed with the error detail.
func resolveRepo(
	ctx context.Context, repo v1beta1.ContainerRepo, plan *gitplan.Plan, logger *slog.Logger,
) setupstatus.Repo {
	status := setupstatus.Repo{Name: repo.Name, Target: repo.Target}
	rg := planRepoGit(repo, plan)

	gitDir := filepath.Join(repo.Target, ".git")
	exists := false
	if info, statErr := os.Stat(gitDir); statErr == nil && info.IsDir() {
		exists = true
	}

	state := setupstatus.StateCloned
	var opErr error
	if exists {
		state = setupstatus.StateFetched
		// Re-point origin and refresh the local config first: a changed
		// credential must be in place before the fetch that needs it.
		if opErr = applyRepoConfig(ctx, repo, rg); opErr == nil {
			opErr = fetchRepo(ctx, repo, rg)
		}
	} else {
		if opErr = cloneRepo(ctx, repo, rg); opErr == nil {
			opErr = applyRepoConfig(ctx, repo, rg)
		}
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

// applyRepoConfig writes the planned repo-local config into the checkout and
// points origin at the effective URL. Entries are applied in order: a plain
// entry replaces every value of its key, an Add entry appends one.
func applyRepoConfig(ctx context.Context, repo v1beta1.ContainerRepo, rg repoGit) error {
	if rg.url != repo.URL {
		if err := runGit(ctx, rg.env, repo.Target, "remote", "set-url", "origin", rg.url); err != nil {
			return err
		}
	}
	for _, c := range rg.config {
		args := []string{"config", "--local", "--replace-all", c.Key, c.Value}
		if c.Add {
			args = []string{"config", "--local", "--add", c.Key, c.Value}
		}
		if err := runGit(ctx, rg.env, repo.Target, args...); err != nil {
			return err
		}
	}
	return nil
}

// cloneRepo clones the repo's effective URL into repo.Target. When repo.Ref is
// set the clone fetches the default branch (plus all refs/tags) and then
// detaches HEAD at the pinned ref (checkoutRef) — supports both a tag name and
// a full commit SHA. Otherwise repo.Branch (if set) is forwarded to
// `git clone --branch`.
func cloneRepo(ctx context.Context, repo v1beta1.ContainerRepo, rg repoGit) error {
	args := append(rg.configFlags(), "clone")
	if repo.Ref != "" {
		if err := runGit(ctx, rg.env, "", append(args, rg.url, repo.Target)...); err != nil {
			return err
		}
		return checkoutRef(ctx, repo, rg)
	}
	if repo.Branch != "" {
		args = append(args, "--branch", repo.Branch)
	}
	args = append(args, rg.url, repo.Target)
	return runGit(ctx, rg.env, "", args...)
}

// checkoutRef detaches HEAD at the pinned repo.Ref. A clone and a
// `fetch --tags` bring every branch and tag, which holds any tag and any
// commit on a branch. A commit that is on neither — the merge commit of a
// pull request, which a forge keeps under its own ref namespace — is not in
// the checkout yet: the remote is asked for it by name, as the host does when
// it reads a Source at a commit, and HEAD is detached at what it sent. When
// that fetch fails too, the error is the checkout's, which names the ref.
func checkoutRef(ctx context.Context, repo v1beta1.ContainerRepo, rg repoGit) error {
	checkoutErr := runGit(ctx, rg.env, repo.Target, "checkout", "--detach", repo.Ref)
	if checkoutErr == nil {
		return nil
	}
	if err := runGit(ctx, rg.env, repo.Target, "fetch", "origin", repo.Ref); err != nil {
		return checkoutErr
	}
	return runGit(ctx, rg.env, repo.Target, "checkout", "--detach", "FETCH_HEAD")
}

// fetchRepo updates an existing checkout. For a Ref-pinned repo (immutable tag
// or commit SHA) the path is fetch-tags + checkout --detach, with no
// fast-forward — `git pull --ff-only` on a detached HEAD has no upstream and
// would error, so a tag pin only survived a fresh clone before this branch
// (#1034). For a branch-tracking repo the prior fetch + checkout + ff-only
// path is preserved.
func fetchRepo(ctx context.Context, repo v1beta1.ContainerRepo, rg repoGit) error {
	if repo.Ref != "" {
		if err := runGit(ctx, rg.env, repo.Target, "fetch", "--prune", "--tags", "origin"); err != nil {
			return err
		}
		return checkoutRef(ctx, repo, rg)
	}
	if err := runGit(ctx, rg.env, repo.Target, "fetch", "--prune", "origin"); err != nil {
		return err
	}
	if repo.Branch != "" {
		if err := runGit(ctx, rg.env, repo.Target, "checkout", repo.Branch); err != nil {
			return err
		}
	}
	return runGit(ctx, rg.env, repo.Target, "pull", "--ff-only")
}

// repoHead returns the full HEAD commit SHA of the checkout at target.
func repoHead(ctx context.Context, target string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", target, "rev-parse", "HEAD")
	cmd.Env = gitEnv(true)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// runGit runs a git subcommand with env (see gitEnv). dir is the working
// directory ("" for the process cwd, e.g. clone). On failure the combined
// output is folded into the error so the log line carries an actionable
// message; git never prints a stored credential, which reaches it only through
// the credential helper and the ssh key files.
func runGit(ctx context.Context, env []string, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = env
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
// HOME, GIT_CONFIG_SYSTEM, the ssh-agent socket — is used) augmented with
// non-interactive guards so a clone never hangs waiting for operator input:
//   - GIT_TERMINAL_PROMPT=0 makes git fail instead of prompting for HTTPS
//     credentials.
//   - When the remote's ssh setup is managed by kukeon (managedSSH: the
//     cell-global or repo-local config sets core.sshCommand), any inherited
//     GIT_SSH_COMMAND is dropped, because git prefers that variable over
//     core.sshCommand and would bypass the stored credential.
//   - Otherwise GIT_SSH_COMMAND defaults to a non-interactive ssh that checks
//     host keys strictly against the image's known_hosts plus the built-in
//     keys of github.com, gitlab.com and bitbucket.org. This replaces the
//     pre-#1472 `StrictHostKeyChecking=accept-new` fallback: trusting an
//     unseen host is now an explicit opt-in (a credential's
//     hostKeyPolicy: accept-new, or an image-provided GIT_SSH_COMMAND, which
//     still wins).
func gitEnv(managedSSH bool) []string {
	env := os.Environ()
	if _, ok := os.LookupEnv("GIT_TERMINAL_PROMPT"); !ok {
		env = append(env, "GIT_TERMINAL_PROMPT=0")
	}
	if managedSSH {
		kept := env[:0:0]
		for _, kv := range env {
			if !strings.HasPrefix(kv, "GIT_SSH_COMMAND=") {
				kept = append(kept, kv)
			}
		}
		return kept
	}
	if _, ok := os.LookupEnv("GIT_SSH_COMMAND"); !ok {
		env = append(env, "GIT_SSH_COMMAND="+fallbackSSHCommand())
	}
	return env
}

// fallbackSSHCommand builds the default ssh invocation for a remote no stored
// credential covers. The built-in forge keys are written to a private temp
// file once per process; if that fails the command still checks strictly
// against the image's own known_hosts.
func fallbackSSHCommand() string {
	command := "ssh -o BatchMode=yes -o StrictHostKeyChecking=yes"
	builtinKnownHostsOnce.Do(func() {
		f, err := os.CreateTemp("", "kukeon-known-hosts-*")
		if err != nil {
			return
		}
		defer func() { _ = f.Close() }()
		if _, err = f.WriteString(gitplan.AllBuiltinKnownHosts()); err != nil {
			_ = os.Remove(f.Name())
			return
		}
		builtinKnownHostsPath = f.Name()
	})
	if builtinKnownHostsPath != "" {
		// One option, two files: ssh keeps only the first UserKnownHostsFile
		// it is given, and expands the ~ itself.
		command += " -o 'UserKnownHostsFile=~/.ssh/known_hosts " + builtinKnownHostsPath + "'"
	}
	return command
}

//nolint:gochecknoglobals // process-wide cache of the temp known_hosts file.
var (
	builtinKnownHostsOnce sync.Once
	builtinKnownHostsPath string
)
