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

package gitimport

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
)

// gitTrue is how Git prints a boolean setting that is on.
const gitTrue = "true"

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
// It reads the global configuration of the account the process runs as.
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
		// limited to the key (added by discover) and the subprocess exit status.
		return "", fmt.Errorf("git config --global lookup failed (check global configuration): %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

// settings are the global git settings an import reads.
type settings struct {
	name, email    string
	signingKey     string
	allowedSigners string
	// sign lists what the operator signs: commits (commit.gpgsign), tags
	// (tag.gpgsign).
	sign []string
}

// discover reads the global git settings. A missing git executable reads as
// no settings, with a note.
func discover(ctx context.Context, reader GitReader) (settings, []string, error) {
	values := make(map[string]string)
	for _, key := range []string{
		"user.name", "user.email", "user.signingkey", "commit.gpgsign", "tag.gpgsign",
		"gpg.ssh.allowedSignersFile",
	} {
		boolean := key == "commit.gpgsign" || key == "tag.gpgsign"
		value, err := reader.Lookup(ctx, key, boolean)
		if ctx.Err() != nil {
			return settings{}, nil, ctx.Err()
		}
		switch {
		case errors.Is(err, ErrMissing):
			continue
		case errors.Is(err, ErrUnavailable):
			return settings{}, []string{"git is unavailable; the global git configuration was not read."}, nil
		case err != nil:
			return settings{}, nil, fmt.Errorf("discover global Git key %s: %w", key, err)
		}
		value = strings.TrimSpace(value)
		if boolean && value != gitTrue && value != "false" {
			return settings{}, nil, fmt.Errorf(
				"%w: discover global Git key %s: expected a Git boolean", errdefs.ErrGitImport, key)
		}
		values[key] = value
	}
	out, notes := gitSettings(values)
	return out, notes, nil
}

// gitSettings reads the discovered values. An incomplete author, and signing
// enabled without a signing key, are dropped with a note.
func gitSettings(values map[string]string) (settings, []string) {
	out := settings{
		signingKey:     values["user.signingkey"],
		allowedSigners: values["gpg.ssh.allowedSignersFile"],
	}
	var notes []string
	name, email := values["user.name"], values["user.email"]
	if name != "" && email != "" {
		out.name, out.email = name, email
	} else if name != "" || email != "" {
		notes = append(notes,
			"global git user.name/user.email is incomplete; the identity carries no author or committer.")
	}
	for _, entry := range []struct{ key, selection string }{{"commit.gpgsign", "commits"}, {"tag.gpgsign", "tags"}} {
		if values[entry.key] != gitTrue {
			continue
		}
		if out.signingKey != "" {
			out.sign = append(out.sign, entry.selection)
		} else {
			notes = append(notes,
				"global git "+entry.key+" is enabled without user.signingkey; "+entry.selection+" are not signed.")
		}
	}
	return out, notes
}
