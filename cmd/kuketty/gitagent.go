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
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/eminwux/kukeon/internal/gitcred/gitplan"
)

const (
	agentSocketWait = 5 * time.Second
	agentSocketPoll = 20 * time.Millisecond
	askpassFailed   = 1
	// askpassRetryPrompt prefixes ssh-add's prompt after a rejected passphrase.
	askpassRetryPrompt = "Bad passphrase"
)

// askpass is kuketty's SSH_ASKPASS mode: it prints the passphrase held in file
// (a secret mounted read-only by the daemon) for ssh-add to read. The
// passphrase therefore never appears in an environment variable or on a
// command line.
//
// prompt is the text ssh-add hands its askpass helper. ssh-add re-prompts
// ("Bad passphrase, try again for …") until it is given an empty answer, and
// kuketty's answer never changes, so the retry is refused: that is what turns
// a wrong stored passphrase into a failed ssh-add instead of an endless loop.
func askpass(file, prompt string, out io.Writer) int {
	if strings.HasPrefix(prompt, askpassRetryPrompt) {
		return askpassFailed
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kuketty: read passphrase: %v\n", err)
		return askpassFailed
	}
	if _, err = fmt.Fprintln(out, strings.TrimRight(string(raw), "\r\n")); err != nil {
		return askpassFailed
	}
	return 0
}

// startGitAgent starts a per-container ssh-agent and loads every
// passphrase-protected key the plan lists (issue #1472, step 4), then exports
// SSH_AUTH_SOCK so git's ssh and ssh-keygen (signing) — in kuketty's own
// clones and in the workload, which inherits kuketty's environment — reach the
// unlocked keys. It returns the function that stops the agent.
//
// A plan with no such key starts nothing. A failure is logged, not fatal: the
// repos that need the key then fail to resolve, and the existing
// required-repo rule decides whether that stops the container.
func startGitAgent(ctx context.Context, plan *gitplan.Plan, logger *slog.Logger) func() {
	noop := func() {}
	if plan == nil || len(plan.AgentKeys) == 0 {
		return noop
	}
	socket, stop, err := runSSHAgent(ctx)
	if err != nil {
		logger.WarnContext(ctx, "ssh-agent unavailable; passphrase-protected git keys stay locked", "error", err)
		return noop
	}
	if err = os.Setenv("SSH_AUTH_SOCK", socket); err != nil {
		logger.WarnContext(ctx, "export SSH_AUTH_SOCK", "error", err)
		stop()
		return noop
	}
	self, err := os.Executable()
	if err != nil {
		logger.WarnContext(ctx, "resolve kuketty path for SSH_ASKPASS", "error", err)
		return stop
	}
	for _, key := range plan.AgentKeys {
		if addErr := addAgentKey(ctx, self, socket, key); addErr != nil {
			logger.WarnContext(ctx, "load git key into ssh-agent", "key", key.Key, "error", addErr)
			continue
		}
		logger.InfoContext(ctx, "loaded git key into ssh-agent", "key", key.Key)
	}
	return stop
}

// runSSHAgent starts `ssh-agent -D` on a private socket and waits for the
// socket to appear. The agent runs in the foreground as kuketty's child, so it
// lives exactly as long as the container's wrapper.
func runSSHAgent(ctx context.Context) (string, func(), error) {
	dir, err := os.MkdirTemp("", "kukeon-ssh-agent-")
	if err != nil {
		return "", nil, fmt.Errorf("create agent dir: %w", err)
	}
	socket := filepath.Join(dir, "agent.sock")
	cmd := exec.CommandContext(ctx, "ssh-agent", "-D", "-a", socket)
	if err = cmd.Start(); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, fmt.Errorf("start ssh-agent: %w", err)
	}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	stop := func() {
		_ = cmd.Process.Kill()
		<-exited
		_ = os.RemoveAll(dir)
	}
	deadline := time.Now().Add(agentSocketWait)
	for {
		if _, statErr := os.Stat(socket); statErr == nil {
			return socket, stop, nil
		}
		select {
		case <-exited:
			_ = os.RemoveAll(dir)
			return "", nil, errors.New("ssh-agent exited before creating its socket")
		default:
		}
		if time.Now().After(deadline) {
			stop()
			return "", nil, errors.New("ssh-agent did not create its socket in time")
		}
		time.Sleep(agentSocketPoll)
	}
}

// addAgentKey runs ssh-add for one key, with kuketty itself as the askpass
// helper pointed at the key's mounted passphrase file. SSH_ASKPASS_REQUIRE
// forces the helper on OpenSSH 8.4+; DISPLAY and a detached stdin make older
// releases use it too.
func addAgentKey(ctx context.Context, self, socket string, key gitplan.AgentKey) error {
	//nolint:gosec // key.Key is a path kukeond staged in the plan, not workload input
	cmd := exec.CommandContext(ctx, "ssh-add", key.Key)
	cmd.Stdin = nil
	cmd.Env = append(os.Environ(),
		"SSH_AUTH_SOCK="+socket,
		"SSH_ASKPASS="+self,
		"SSH_ASKPASS_REQUIRE=force",
		"DISPLAY=kukeon:0",
		gitplan.AskpassFileEnv+"="+key.PassphraseFile,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ssh-add: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
