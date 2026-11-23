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
	"os/exec"
	"path/filepath"

	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/util/fs"
)

const (
	// KukepauseBinaryName is the binary `kuke init` and `kuke daemon recreate`
	// stage under <RunPath>/bin so every cell's root container — including
	// kukeond's own — can bind-mount it at /.kukeon/bin/kukepause as PID 1
	// (issue #931). It is pre-staged on the host because root containers are
	// created before kukeond is up, so it cannot be copied out of the daemon
	// image the way kuketty is. Sourced from the ctr package so the staged
	// name matches the root-container builder's bind source.
	KukepauseBinaryName = ctr.RootContainerPauseBinaryName

	// KukepauseStagedMode is the on-disk mode kukepause is staged with. It
	// must carry an execute bit so the kernel can exec the bind-mounted
	// kukepause through the root container's read-only bind mount. The
	// recursive RunPath chown in `kuke init` otherwise drops every file to
	// 0o640 (no exec), so init re-asserts this mode on the staged binary
	// afterward.
	KukepauseStagedMode os.FileMode = 0o750
)

// StageKukepause copies the kukepause binary into <runPath>/bin/kukepause and
// returns the staged path. The staged copy is what every cell's root container
// bind-mounts at /.kukeon/bin/kukepause (issue #931). Mirrors the runner's stageKukettyBinary
// contract: <RunPath>/bin destination, atomic tmp+rename copy, executable mode.
//
// `kuke init` calls it on every run, and `kuke daemon recreate` calls it right
// before re-provisioning the kukeond cell, so an upgraded kukepause reaches
// the host without a full re-init (issue #1638). The daemon's own
// stageKukepauseBinary never overwrites an existing copy.
func StageKukepause(runPath string) (string, error) {
	src, err := ResolveKukepauseSource()
	if err != nil {
		return "", err
	}
	binDir := filepath.Join(runPath, "bin")
	if err = os.MkdirAll(binDir, 0o750); err != nil {
		return "", fmt.Errorf("create kukepause stage dir %q: %w", binDir, err)
	}
	dst := filepath.Join(binDir, KukepauseBinaryName)
	if err = fs.CopyFileAtomic(src, dst, KukepauseStagedMode, KukepauseBinaryName); err != nil {
		return "", err
	}
	return dst, nil
}

// ResolveKukepauseSource locates a kukepause binary on the host. Lookup order:
// $PATH first (the installed / `make install-dev` layout, kukepause alongside
// kuke), then a sibling of the running executable (dev runs of `./kuke` from the
// repo root next to a freshly built `./kukepause`, and the e2e harness which
// execs the repo-root kuke). The error names the install path so a "not found"
// is actionable.
func ResolveKukepauseSource() (string, error) {
	if p, err := exec.LookPath(KukepauseBinaryName); err == nil {
		return p, nil
	}
	if self, err := os.Executable(); err == nil {
		sibling := filepath.Join(filepath.Dir(self), KukepauseBinaryName)
		if info, statErr := os.Stat(sibling); statErr == nil && !info.IsDir() {
			return sibling, nil
		}
	}
	return "", fmt.Errorf(
		"%s not found on $PATH or alongside kuke; build it with `make kukepause` and install it (`make install-dev`)",
		KukepauseBinaryName,
	)
}
