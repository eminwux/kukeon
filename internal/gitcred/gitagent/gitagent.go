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

// Package gitagent runs a private ssh-agent for the passphrase-protected keys
// of a staged git bundle (issue #1472, step 4): kuketty does it inside a cell,
// kuke on the host for a fetch with a stored identity (issue #1643, D11). The
// binary that loads the keys is its own SSH_ASKPASS helper, so a passphrase
// never appears in an environment variable or on a command line.
package gitagent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/eminwux/kukeon/internal/gitcred/gitplan"
)

// HostAskpassFileEnv makes kuke act as an SSH_ASKPASS helper when set: it
// prints the named file and exits (issue #1643). kuketty's own variable is
// gitplan.AskpassFileEnv.
const HostAskpassFileEnv = "KUKE_ASKPASS_FILE" //nolint:gosec // an environment variable name, not a credential

const (
	agentSocketWait = 5 * time.Second
	agentSocketPoll = 20 * time.Millisecond
	askpassFailed   = 1
	// askpassRetryPrompt prefixes ssh-add's prompt after a rejected passphrase.
	askpassRetryPrompt = "Bad passphrase"
)

// Askpass is the SSH_ASKPASS mode: it prints the passphrase held in file (a
// secret the daemon staged) for ssh-add to read, and returns the exit code.
//
// prompt is the text ssh-add hands its askpass helper. ssh-add re-prompts
// ("Bad passphrase, try again for …") until it is given an empty answer, and
// the answer never changes, so the retry is refused: that is what turns a
// wrong stored passphrase into a failed ssh-add instead of an endless loop.
func Askpass(file, prompt string, out io.Writer) int {
	if strings.HasPrefix(prompt, askpassRetryPrompt) {
		return askpassFailed
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read passphrase: %v\n", err)
		return askpassFailed
	}
	if _, err = fmt.Fprintln(out, strings.TrimRight(string(raw), "\r\n")); err != nil {
		return askpassFailed
	}
	return 0
}

// Run starts `ssh-agent -D` on a private socket and waits for the socket to
// appear. The agent runs in the foreground as the caller's child, so it lives
// exactly as long as the caller or until stop is called; stop also removes
// the socket's directory.
func Run(ctx context.Context) (string, func(), error) {
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

// AddKey runs ssh-add for one key, with self as the askpass helper and the
// key's staged passphrase file named in the askpassEnv variable (the one self
// checks first thing to enter Askpass mode). SSH_ASKPASS_REQUIRE forces the
// helper on OpenSSH 8.4+; DISPLAY and a detached stdin make older releases
// use it too.
func AddKey(ctx context.Context, self, socket, askpassEnv string, key gitplan.AgentKey) error {
	//nolint:gosec // key.Key is a path kukeond staged in the plan, not workload input
	cmd := exec.CommandContext(ctx, "ssh-add", key.Key)
	cmd.Stdin = nil
	cmd.Env = append(os.Environ(),
		"SSH_AUTH_SOCK="+socket,
		"SSH_ASKPASS="+self,
		"SSH_ASKPASS_REQUIRE=force",
		"DISPLAY=kukeon:0",
		askpassEnv+"="+key.PassphraseFile,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ssh-add: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
