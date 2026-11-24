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
	"path/filepath"

	"github.com/eminwux/kukeon/internal/util/fs"
)

const (
	// kukeClientStagedSubdir is the <RunPath>/bin subdirectory `kuke init`
	// and `kuke daemon recreate` stage the kuke client into (issue #1483).
	// Cells with the `kuke-binary` host-mount selector bind-mount this
	// directory, not the file, so a re-stage (tmp + rename, a new inode)
	// reaches running cells.
	kukeClientStagedSubdir = "client"

	// kukeClientShimName is the <RunPath>/bin file cells bind-mount at
	// /usr/local/bin/kuke. Its content is constant, so it is written once and
	// never replaced: its pinned inode never goes stale. It execs the staged
	// binary through the directory mount.
	kukeClientShimName = "kuke-shim"

	// kukeClientShim execs the staged client at the cell-side target of the
	// `kuke-binary` directory mount (pkg/team/compose partials.tmpl.yaml).
	kukeClientShim = "#!/bin/sh\nexec /.kukeon/client/kuke \"$@\"\n"

	// KukeClientStagedMode lets any cell user run the client: the old file
	// mount of the host's /usr/local/bin/kuke was world-executable.
	KukeClientStagedMode os.FileMode = 0o755
)

// KukeClientPaths are the staged client artifacts the caller re-asserts
// root:kukeon KukeClientStagedMode on (`kuke init` after
// applyKukeonOwnership's recursive chown, `kuke daemon recreate` right after
// staging).
type KukeClientPaths struct {
	Dir    string
	Binary string
	Shim   string
}

// StageKukeClient copies the running kuke binary into
// <runPath>/bin/client/kuke and writes the exec shim beside it. Both are
// content-idempotent: an unchanged binary and the constant shim are left
// untouched, so their inodes survive repeated runs.
//
// `kuke init` calls it on every run, and `kuke daemon recreate` calls it
// alongside StageKukepause: the installer's live-host path and `kuke update`
// skip `kuke init`, so without the recreate call a host initialized before
// the client was staged never gets the `kuke-binary` mount source, and an
// upgraded client never reaches running cells (issue #1647).
func StageKukeClient(runPath string) (KukeClientPaths, error) {
	binDir := filepath.Join(runPath, "bin")
	paths := KukeClientPaths{
		Dir:  filepath.Join(binDir, kukeClientStagedSubdir),
		Shim: filepath.Join(binDir, kukeClientShimName),
	}
	paths.Binary = filepath.Join(paths.Dir, "kuke")
	if err := os.MkdirAll(paths.Dir, KukeClientStagedMode); err != nil {
		return KukeClientPaths{}, fmt.Errorf("create kuke client stage dir %q: %w", paths.Dir, err)
	}
	self, err := os.Executable()
	if err != nil {
		return KukeClientPaths{}, fmt.Errorf("resolve kuke executable: %w", err)
	}
	content, err := os.ReadFile(self)
	if err != nil {
		return KukeClientPaths{}, fmt.Errorf("read kuke executable %q: %w", self, err)
	}
	if _, err = fs.WriteFileIfChanged(paths.Binary, content, KukeClientStagedMode, "kuke client"); err != nil {
		return KukeClientPaths{}, err
	}
	shim := []byte(kukeClientShim)
	if _, err = fs.WriteFileIfChanged(paths.Shim, shim, KukeClientStagedMode, "kuke client shim"); err != nil {
		return KukeClientPaths{}, err
	}
	return paths, nil
}
