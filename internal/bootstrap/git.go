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

package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/eminwux/kukeon/pkg/config"
)

var (
	// ErrMissing marks an optional global Git key that is not configured.
	ErrMissing = errors.New("global Git key is absent")
	// ErrUnavailable marks an unavailable Git executable, allowing defaults only.
	ErrUnavailable = errors.New("git executable is unavailable")
)

// GitReader reads global settings. Boolean lookups must use Git's conversion
// semantics and return canonical "true" or "false"; missing keys are ErrMissing.
// All other failures must remain distinguishable from missing settings.
type GitReader interface {
	Lookup(ctx context.Context, key string, boolean bool) (string, error)
}

// CommandGit discovers global settings using context-aware Git subprocesses.
type CommandGit struct{}

func (CommandGit) Lookup(ctx context.Context, key string, boolean bool) (string, error) {
	args := []string{"config", "--global"}
	if boolean {
		args = append(args, "--bool")
	}
	args = append(args, "--get", key)
	cmd := exec.CommandContext(ctx, "git", args...)
	output, err := cmd.Output()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if errors.Is(err, exec.ErrNotFound) {
		return "", ErrUnavailable
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 && len(output) == 0 && len(exit.Stderr) == 0 {
			return "", ErrMissing
		}
		// Git stderr can echo configured values or input lines. Keep diagnostics
		// limited to the key (added by discoverGit) and the subprocess exit status.
		return "", fmt.Errorf("git config --global lookup failed (check global configuration): %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

func discoverGit(ctx context.Context, reader GitReader) (*config.Git, []string, error) {
	values := make(map[string]string)
	for _, key := range []string{"user.name", "user.email", "user.signingkey", "commit.gpgsign", "tag.gpgsign", "gpg.ssh.allowedSignersFile"} {
		boolean := key == "commit.gpgsign" || key == "tag.gpgsign"
		value, err := reader.Lookup(ctx, key, boolean)
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		switch {
		case errors.Is(err, ErrMissing):
			continue
		case errors.Is(err, ErrUnavailable):
			return nil, []string{"Git discovery skipped: Git executable is unavailable."}, nil
		case err != nil:
			return nil, nil, fmt.Errorf("discover global Git key %s: %w", key, err)
		}
		value = strings.TrimSpace(value)
		if boolean && value != "true" && value != "false" {
			return nil, nil, fmt.Errorf("discover global Git key %s: expected a Git boolean", key)
		}
		values[key] = value
	}
	git, diagnostics := gitSettings(values)
	return git, diagnostics, nil
}

func gitSettings(values map[string]string) (*config.Git, []string) {
	git := &config.Git{SigningKey: values["user.signingkey"], AllowedSigners: values["gpg.ssh.allowedSignersFile"]}
	var diagnostics []string
	name, email := values["user.name"], values["user.email"]
	if name != "" && email != "" {
		git.Author = &config.Identity{Name: name, Email: email}
		git.Committer = &config.Identity{Name: name, Email: email}
	} else if name != "" || email != "" {
		diagnostics = append(diagnostics, "Global Git user.name/user.email is incomplete; omitted author and committer identities.")
	}
	for _, entry := range []struct{ key, selection string }{{"commit.gpgsign", "commits"}, {"tag.gpgsign", "tags"}} {
		if values[entry.key] != "true" {
			continue
		}
		if git.SigningKey != "" {
			git.Sign = append(git.Sign, entry.selection)
		} else {
			diagnostics = append(diagnostics, "Global Git "+entry.key+" is enabled without user.signingkey; omitted "+entry.selection+" signing.")
		}
	}
	if git.Author == nil && git.SigningKey == "" && git.AllowedSigners == "" {
		diagnostics = append(diagnostics, "No usable global Git settings; omitted spec.git.")
		return nil, diagnostics
	}
	return git, diagnostics
}
