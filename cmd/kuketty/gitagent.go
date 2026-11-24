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
	"io"
	"log/slog"
	"os"

	"github.com/eminwux/kukeon/internal/gitcred/gitagent"
	"github.com/eminwux/kukeon/internal/gitcred/gitplan"
)

// askpass is kuketty's SSH_ASKPASS mode: it prints the passphrase held in file
// (a secret mounted read-only by the daemon) for ssh-add to read. See
// gitagent.Askpass.
func askpass(file, prompt string, out io.Writer) int {
	return gitagent.Askpass(file, prompt, out)
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
	socket, stop, err := gitagent.Run(ctx)
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
		if addErr := gitagent.AddKey(ctx, self, socket, gitplan.AskpassFileEnv, key); addErr != nil {
			logger.WarnContext(ctx, "load git key into ssh-agent", "key", key.Key, "error", addErr)
			continue
		}
		logger.InfoContext(ctx, "loaded git key into ssh-agent", "key", key.Key)
	}
	return stop
}
