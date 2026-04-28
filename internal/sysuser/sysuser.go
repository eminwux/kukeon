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

// Package sysuser provisions the kukeon system user/group and applies
// kukeon-managed file ownership during `kuke init`. The package is
// invoked from the init command so that a non-root user added to the
// kukeon group can dial the kukeond socket without sudo while writes
// under /opt/kukeon still require root (they go through the daemon).
package sysuser

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
)

// EnsureResult reports the outcome of EnsureUserGroup.
type EnsureResult struct {
	UID          int
	GID          int
	UserCreated  bool
	GroupCreated bool
}

// CommandRunner runs system commands. The default implementation shells out
// via os/exec; tests can substitute a fake to avoid mutating the host.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) error
}

// ExecRunner shells out via os/exec. It is the production CommandRunner.
type ExecRunner struct{}

// Run wraps exec.CommandContext + Run, surfacing combined output on failure
// so the caller's error message names what went wrong with groupadd/useradd.
func (ExecRunner) Run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %w: %s", name, args, err, out)
	}
	return nil
}

// LookupGroupFunc lets tests stub user.LookupGroup without populating
// /etc/group. Production callers should leave it nil to fall back to the
// stdlib lookup.
type LookupGroupFunc func(name string) (*user.Group, error)

// LookupUserFunc lets tests stub user.Lookup without populating /etc/passwd.
// Production callers should leave it nil to fall back to the stdlib lookup.
type LookupUserFunc func(name string) (*user.User, error)

// EnsureOptions configures EnsureUserGroup. Zero-valued fields fall back to
// the production lookups and the default runner.
type EnsureOptions struct {
	Runner      CommandRunner
	LookupGroup LookupGroupFunc
	LookupUser  LookupUserFunc
	// NoLoginShell overrides the picked nologin shell. If empty, the helper
	// chooses /usr/sbin/nologin or /sbin/nologin based on what exists.
	NoLoginShell string
}

// EnsureUserGroup creates the named system group and user if they don't
// already exist, then returns the resolved UID/GID. Idempotent — re-running
// after a prior init is a no-op aside from the lookups.
//
// Requires CAP_SYS_ADMIN (effectively, root) when creation is needed because
// it shells out to groupadd/useradd. The caller (kuke init) is already root,
// so this is fine in the production path.
func EnsureUserGroup(ctx context.Context, username, groupname string, opts EnsureOptions) (EnsureResult, error) {
	var res EnsureResult

	runner := opts.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	lookupGroup := opts.LookupGroup
	if lookupGroup == nil {
		lookupGroup = user.LookupGroup
	}
	lookupUser := opts.LookupUser
	if lookupUser == nil {
		lookupUser = user.Lookup
	}

	grp, lookupErr := lookupGroup(groupname)
	if lookupErr != nil {
		var unk user.UnknownGroupError
		if !errors.As(lookupErr, &unk) {
			return res, fmt.Errorf("lookup group %q: %w", groupname, lookupErr)
		}
		if runErr := runner.Run(ctx, "groupadd", "--system", groupname); runErr != nil {
			return res, fmt.Errorf("create group %q: %w", groupname, runErr)
		}
		res.GroupCreated = true
		grp, lookupErr = lookupGroup(groupname)
		if lookupErr != nil {
			return res, fmt.Errorf("lookup group %q after create: %w", groupname, lookupErr)
		}
	}
	gid, parseErr := strconv.Atoi(grp.Gid)
	if parseErr != nil {
		return res, fmt.Errorf("parse gid %q for group %q: %w", grp.Gid, groupname, parseErr)
	}
	res.GID = gid

	usr, lookupErr := lookupUser(username)
	if lookupErr != nil {
		var unk user.UnknownUserError
		if !errors.As(lookupErr, &unk) {
			return res, fmt.Errorf("lookup user %q: %w", username, lookupErr)
		}
		shell := opts.NoLoginShell
		if shell == "" {
			shell = pickNoLoginShell()
		}
		if runErr := runner.Run(
			ctx,
			"useradd",
			"--system",
			"--gid", groupname,
			"--no-create-home",
			"--shell", shell,
			username,
		); runErr != nil {
			return res, fmt.Errorf("create user %q: %w", username, runErr)
		}
		res.UserCreated = true
		usr, lookupErr = lookupUser(username)
		if lookupErr != nil {
			return res, fmt.Errorf("lookup user %q after create: %w", username, lookupErr)
		}
	}
	uid, parseErr := strconv.Atoi(usr.Uid)
	if parseErr != nil {
		return res, fmt.Errorf("parse uid %q for user %q: %w", usr.Uid, username, parseErr)
	}
	res.UID = uid

	return res, nil
}

// pickNoLoginShell returns whichever nologin shell exists on the host.
// Debian-derived distros ship /usr/sbin/nologin; Red Hat-derived distros
// ship /sbin/nologin. Falls back to /usr/sbin/nologin when neither is
// found so useradd still gets a non-empty value.
func pickNoLoginShell() string {
	for _, candidate := range []string{"/usr/sbin/nologin", "/sbin/nologin"} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return "/usr/sbin/nologin"
}

// ChownAndChmod sets ownership and mode on a single path. Use this for the
// kukeond socket and for the /run/kukeon top-level directory.
func ChownAndChmod(path string, uid, gid int, mode os.FileMode) error {
	if err := os.Chown(path, uid, gid); err != nil {
		return fmt.Errorf("chown %q: %w", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("chmod %q: %w", path, err)
	}
	return nil
}

// ChownTreeAndChmod walks a directory tree and applies the requested owner
// to every entry, dirMode to directories, and fileMode to regular files.
// Splitting the modes keeps the recursive descent from making JSON metadata
// files executable when the caller wants 0o750-style group-traverse on dirs.
//
// Symlinks are lchowned but not chmoded — Linux stores no mode bits on
// symlinks, and os.Chmod follows the link, which would mutate the wrong
// file.
func ChownTreeAndChmod(root string, uid, gid int, dirMode, fileMode os.FileMode) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := os.Lchown(path, uid, gid); err != nil {
			return fmt.Errorf("chown %q: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		mode := fileMode
		if info.IsDir() {
			mode = dirMode
		}
		if err := os.Chmod(path, mode); err != nil {
			return fmt.Errorf("chmod %q: %w", path, err)
		}
		return nil
	})
}
