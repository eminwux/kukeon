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
	"errors"
	"fmt"
	"os"
)

// ErrMustRunAsRoot mirrors the root module's errdefs.ErrMustRunAsRoot
// sentinel. kukebuild is a separate Go module (#662) whose BuildKit
// closure is deliberately incompatible with the root module's graph, so
// the shared sentinel can't be imported across the boundary without
// re-coupling the two — it's redeclared here so kukebuild callers can
// classify the root gate via errors.Is.
var ErrMustRunAsRoot = errors.New("command must run as root")

// geteuid is indirected via a package var so unit tests can drive the
// non-root branch without forking under a different UID. Mirrors the
// seam in cmd/kuke/shared.RequireRoot.
//
//nolint:gochecknoglobals // test seam for the production euid lookup
var geteuid = os.Geteuid

// SetGeteuidForTesting replaces the euid lookup with f and returns a
// restore function. Test-only.
func SetGeteuidForTesting(f func() int) func() {
	prev := geteuid
	geteuid = f
	return func() { geteuid = prev }
}

// requireRoot is kukebuild's fail-fast UID gate. kukebuild writes
// directly into containerd's content store, which is root-only on a
// stock host, so a non-root invocation fails fast with a wrapped
// ErrMustRunAsRoot rather than letting containerd surface an opaque
// EACCES several phases into the build. Same posture as
// `kuke image load --no-daemon`.
func requireRoot() error {
	if geteuid() == 0 {
		return nil
	}
	return fmt.Errorf(
		"%w: kukebuild writes to root-owned containerd state — re-run as root (e.g. via `sudo kuke build`)",
		ErrMustRunAsRoot,
	)
}
