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

package runner

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/eminwux/kukeon/internal/ctr"
)

// kukepauseSourcePathInsideDaemon is the path inside the kukeond container
// image where a bundled kukepause binary would live (parallel to
// /bin/kuketty). The kukeond image does not currently ship kukepause — the
// canonical bootstrap is `kuke init` staging from the host before the
// daemon's own cell is created — so this lookup is the most-preferred path
// purely for symmetry with kuketty. If absent, resolveKukepauseBinary falls
// through to the sibling-of-executable and $PATH lookups that the e2e
// harness (./kukeond next to ./kukepause) and operator installs depend on.
const kukepauseSourcePathInsideDaemon = "/bin/" + ctr.RootContainerPauseBinaryName

// stageKukepauseBinary ensures a host-visible copy of the kukepause binary
// exists at <RunPath>/bin/kukepause and returns that path. Mirrors
// stageKukettyBinary's contract for the same reason: every default root
// container bind-mounts the staged binary at /pause, so the daemon must
// self-heal when it provisions a cell whose RunPath was not previously
// touched by `kuke init` — exactly the e2e suite's startKukeondDaemon path,
// which boots `kukeond serve --run-path <tempdir>` without an `init` pass.
//
// Idempotent: once the destination exists with non-zero size and an exec
// bit, subsequent calls return its path without re-copying. This matters
// because every CreateCell / StartCell trip lands here; doing the lstat
// early avoids reading a multi-MiB binary off disk per cell.
//
// Atomicity is handled with a tmp+rename so two concurrent provisions on
// the same RunPath never see a partial binary at the destination.
func stageKukepauseBinary(runPath string) (string, error) {
	dstDir := filepath.Join(runPath, kukettyBinaryStagedSubdir)
	dst := filepath.Join(dstDir, ctr.RootContainerPauseBinaryName)

	if ok, err := stagedBinaryUsable(dst); err != nil {
		return "", err
	} else if ok {
		return dst, nil
	}

	src, err := resolveKukepauseBinary()
	if err != nil {
		return "", err
	}

	if err = os.MkdirAll(dstDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %q: %w", dstDir, err)
	}

	if err = copyBinaryAtomic(src, dst, ctr.RootContainerPauseBinaryName); err != nil {
		return "", err
	}
	return dst, nil
}

// resolveKukepauseBinary locates a kukepause binary on the host. Lookup
// order mirrors resolveKukettyBinary so operators familiar with one
// reason about the other the same way:
//
//  1. /bin/kukepause — bundled-in-image path (currently unused, see the
//     constant comment).
//  2. Sibling of the currently-running executable — the e2e harness path
//     (./kukeond exec'd from the repo root with ./kukepause next to it),
//     also covered by `make install-dev` placing both binaries under
//     /usr/local/bin.
//  3. $PATH — operator installs that landed kukepause out-of-band.
//
// The error names every location tried so a "not found" miss is
// debuggable end-to-end.
func resolveKukepauseBinary() (string, error) {
	tried := []string{kukepauseSourcePathInsideDaemon}
	if ok, _ := stagedBinaryUsable(kukepauseSourcePathInsideDaemon); ok {
		return kukepauseSourcePathInsideDaemon, nil
	}

	if self, err := os.Executable(); err == nil {
		sibling := filepath.Join(filepath.Dir(self), ctr.RootContainerPauseBinaryName)
		tried = append(tried, sibling)
		if ok, _ := stagedBinaryUsable(sibling); ok {
			return sibling, nil
		}
	}

	if p, err := exec.LookPath(ctr.RootContainerPauseBinaryName); err == nil {
		return p, nil
	}
	tried = append(tried, "$PATH/"+ctr.RootContainerPauseBinaryName)
	return "", fmt.Errorf("%s binary not found in: %v", ctr.RootContainerPauseBinaryName, tried)
}
