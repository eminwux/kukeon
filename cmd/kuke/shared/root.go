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

package shared

import (
	"fmt"
	"os"

	"github.com/eminwux/kukeon/internal/errdefs"
)

// geteuid is indirected via a package var so unit tests can simulate the
// non-root case without forking under a different UID.
//
//nolint:gochecknoglobals // test seam for the production euid lookup
var geteuid = os.Geteuid

// SetGeteuidForTesting replaces the euid lookup with f and returns a
// restore function. Intended only for unit tests in dependent packages
// (cmd/kuke/init, cmd/kuke/daemon/reset, cmd/kuke/doctor/cgroups, …) that
// drive the gated entrypoints under both root and non-root paths without
// forking under a different UID. Not for production use.
func SetGeteuidForTesting(f func() int) func() {
	prev := geteuid
	geteuid = f
	return func() { geteuid = prev }
}

// RequireRoot is the fail-fast UID gate used by direct-write subcommands
// (kuke init, kuke daemon reset, kuke image load --no-daemon, kuke doctor
// cgroups --probe). When the effective UID is non-zero it returns an error
// wrapping errdefs.ErrMustRunAsRoot that names the subcommand and suggests
// re-running under `sudo`, so operators see a clear cause instead of a
// confusing "operation not permitted" several phases in. Daemon-routed
// verbs (`kuke get`, `kuke create`, `kuke apply`, `kuke delete`, …) must
// not call this — those are the supported `kukeon`-group rootless-client
// path.
func RequireRoot(subcommand string) error {
	if geteuid() == 0 {
		return nil
	}
	return fmt.Errorf(
		"%w: `%s` writes to root-owned paths — re-run with `sudo %s`",
		errdefs.ErrMustRunAsRoot, subcommand, subcommand,
	)
}
