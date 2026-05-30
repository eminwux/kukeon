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

package lifecycle

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/sysuser"
	"github.com/spf13/cobra"
)

// EnsureSocketDirKey injects a socket-dir recreate func via the command
// context so unit tests can drive `kuke daemon start`/`restart` without root
// or a real /run/kukeon. Same pattern as MockClientKey / ReachableProbeKey:
// the key lives here so every verb's resolver agrees on a single identity.
type EnsureSocketDirKey struct{}

// ResolveEnsureSocketDir picks the recreate-/run/kukeon step the start and
// restart verbs run before launching the cell. Tests inject a stub via
// EnsureSocketDirKey; production recreates the parent directory of the
// resolved kukeond socket. The indirection lets both verbs call the same
// one-liner without duplicating the context-value lookup.
func ResolveEnsureSocketDir(cmd *cobra.Command) func() error {
	if f, ok := cmd.Context().Value(EnsureSocketDirKey{}).(func() error); ok && f != nil {
		return f
	}
	return func() error { return EnsureSocketDir(ResolveSocketPath()) }
}

// applyRunDirOwnership re-asserts root:kukeon ownership and the kukeon SGID
// mode on the kukeond socket's parent directory. It is a package var so the
// unit suite can substitute a non-root stub: the production implementation
// chowns to uid 0, which only root may do, and resolves the kukeon group that
// `kuke init` provisions on the host but a CI test box does not have.
//
//nolint:gochecknoglobals // test seam for the production run-dir ownership step
var applyRunDirOwnership = func(dir string) error {
	grp, err := user.LookupGroup(consts.KukeonSystemGroup)
	if err != nil {
		return fmt.Errorf("lookup group %q: %w", consts.KukeonSystemGroup, err)
	}
	gid, err := strconv.Atoi(grp.Gid)
	if err != nil {
		return fmt.Errorf("parse gid %q for group %q: %w", grp.Gid, consts.KukeonSystemGroup, err)
	}
	return sysuser.ChownAndChmod(dir, 0, gid, consts.KukeonRunDirMode)
}

// SetRunDirOwnershipForTesting replaces the ownership step EnsureSocketDir
// runs and returns a restore func. Tests use it to assert the directory the
// step receives without needing root or a real kukeon group.
func SetRunDirOwnershipForTesting(f func(dir string) error) func() {
	prev := applyRunDirOwnership
	applyRunDirOwnership = f
	return func() { applyRunDirOwnership = prev }
}

// EnsureSocketDir recreates the kukeond socket's parent directory — the
// /run/kukeon bind-mount source the kukeond cell spec requires — and
// re-asserts the root:kukeon SGID 0750 ownership `kuke init` applies.
//
// `kuke init` creates this directory once via the controller bootstrap, but
// /run is a tmpfs that is wiped on every reboot, so the bind source is gone
// when `kuke daemon start`/`restart` later launch the cell — runc then fails
// the mount with "open /run/kukeon: no such file or directory". Calling this
// before the start path makes the daemon recoverable after a reboot without a
// manual `mkdir` or a full re-`init`. Idempotent: on a healthy host the
// MkdirAll is a no-op and the ownership/mode are simply re-applied.
//
// socketPath is the resolved kukeond socket; its parent directory is the
// bind-mount source, so passing the configured socket (which honours a
// nested-mode KUKEOND_SOCKET override) recreates the directory the persisted
// cell spec actually binds.
func EnsureSocketDir(socketPath string) error {
	dir := filepath.Dir(socketPath)
	if err := os.MkdirAll(dir, consts.KukeonRunDirMode.Perm()); err != nil {
		return fmt.Errorf("create kukeond socket dir %q: %w", dir, err)
	}
	if err := applyRunDirOwnership(dir); err != nil {
		return fmt.Errorf("apply kukeon ownership to %q: %w", dir, err)
	}
	return nil
}
