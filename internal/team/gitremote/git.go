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

// Package gitremote uses native Git with caller credentials and bounded,
// noninteractive transport. Subprocess output is never included in errors.
package gitremote

import (
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
	"unicode"
)

const (
	guidance  = "git source access failed; check the URL, revision, network, configured HTTPS credential helper, or SSH known_hosts and unlocked/agent-loaded identity; then rerun (Git output withheld)"
	sshScheme = "ssh"
)

var errURL = errors.New(
	"source: require SCP-style [user@]host:path (colon after host), ssh://[user@]host[:port]/path, or https://host/path; embedded credentials, queries and fragments are forbidden",
)

// URL validates remote-only input without exposing it in diagnostics. HTTPS
// userinfo and all query/fragment components are forbidden, including tokens.
func URL(raw string) (string, error) {
	if raw == "" || strings.ContainsFunc(raw, invalidURLRune) || strings.ContainsAny(raw, "\\\"'`?#") {
		return "", errURL
	}
	if strings.HasPrefix(raw, "github.com/") {
		raw = "https://" + raw
	}
	if strings.Contains(raw, "://") {
		return networkURL(raw)
	}
	return scpURL(raw)
}

func invalidURLRune(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }

func validHost(host string) bool { return host != "" && !strings.HasPrefix(host, "-") }

func networkURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", errURL
	}
	if u.Scheme != sshScheme && u.Scheme != "https" {
		return "", errURL
	}
	if !validHost(u.Hostname()) || u.Path == "" || u.Path == "/" || u.Opaque != "" {
		return "", errURL
	}
	if !validUser(u) {
		return "", errURL
	}
	return raw, nil
}

func validUser(u *url.URL) bool {
	if u.User == nil {
		return true
	}
	if u.Scheme != sshScheme || !validHost(u.User.Username()) {
		return false
	}
	_, password := u.User.Password()
	return !password
}

func scpURL(raw string) (string, error) {
	hostPart, path, ok := strings.Cut(raw, ":")
	if !ok || hostPart == "" || path == "" || strings.ContainsAny(hostPart, "/%") || strings.Contains(path, "@") {
		return "", errURL
	}
	user, host, hasUser := strings.Cut(hostPart, "@")
	if !hasUser {
		host = user
	} else if !validHost(user) {
		return "", errURL
	}
	if !validHost(host) || strings.Contains(host, "@") {
		return "", errURL
	}
	return raw, nil
}

// Run retains Git credential helpers, SSH agent/identities, aliases, ports, and
// trust settings. BatchMode prevents SSH password/passphrase/trust questions;
// Git/GCM terminal prompting is disabled. Each subprocess has a fixed deadline.
func Run(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	env := transportEnv(ctx)
	ssh, err := sshCommand(ctx, env)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.WaitDelay = time.Second
	// A new session has no controlling terminal, so even an explicitly
	// interactive SSH configuration cannot prompt through /dev/tty.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error {
		// Kill the complete Git/SSH/helper process group on cancellation.
		killErr := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(killErr, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return killErr
	}
	cmd.Env = append(
		env,
		"GIT_TERMINAL_PROMPT=0",
		"GCM_INTERACTIVE=never",
		"GIT_ASKPASS=/bin/false",
		"SSH_ASKPASS=/bin/false",
		"SSH_ASKPASS_REQUIRE=never",
		"GIT_SSH_COMMAND="+ssh+" -oBatchMode=yes",
	)
	output, err := cmd.Output()
	if err != nil {
		return nil, errors.New(guidance)
	}
	return output, nil
}

func sshCommand(ctx context.Context, env []string) (string, error) {
	if command := os.Getenv("GIT_SSH_COMMAND"); command != "" {
		return command, nil
	}
	cmd := exec.CommandContext(ctx, "git", "config", "--get", "core.sshCommand")
	cmd.Env = env
	cmd.WaitDelay = time.Second
	output, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return "", errors.New(guidance)
		}
	}
	command := strings.TrimSpace(string(output))
	if command != "" {
		return command, nil
	}
	if binary := os.Getenv("GIT_SSH"); binary != "" {
		return "'" + strings.ReplaceAll(binary, "'", "'\\''") + "'", nil
	}

	command = sshScheme
	return command, nil
}

// DefaultBranch discovers the symbolic remote HEAD rather than assuming main.
func DefaultBranch(ctx context.Context, source string) (string, error) {
	remote, err := URL(source)
	if err != nil {
		return "", err
	}
	output, err := Run(ctx, "ls-remote", "--symref", "--", remote, "HEAD")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(output), "\n") {
		if ref, ok := strings.CutPrefix(line, "ref: refs/heads/"); ok {
			branch, tail, found := strings.Cut(ref, "\t")
			if found && tail == "HEAD" && branch != "" {
				return branch, nil
			}
		}
	}
	return "", errors.New("source: remote HEAD has no default branch; select --branch, --tag, or --commit explicitly")
}
