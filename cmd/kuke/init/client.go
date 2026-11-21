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

package init

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/eminwux/kukeon/internal/util/fs"
)

const (
	// kukeClientStagedSubdir is the <RunPath>/bin subdirectory `kuke init`
	// stages its own binary into (issue #1483). Cells with the `kuke-binary`
	// host-mount selector bind-mount this directory, not the file, so a
	// re-stage (tmp + rename, a new inode) reaches running cells.
	kukeClientStagedSubdir = "client"

	// kukeClientShimName is the <RunPath>/bin file cells bind-mount at
	// /usr/local/bin/kuke. Its content is constant, so it is written once and
	// never replaced: its pinned inode never goes stale. It execs the staged
	// binary through the directory mount.
	kukeClientShimName = "kuke-shim"

	// kukeClientShim execs the staged client at the cell-side target of the
	// `kuke-binary` directory mount (pkg/team/compose partials.tmpl.yaml).
	kukeClientShim = "#!/bin/sh\nexec /.kukeon/client/kuke \"$@\"\n"

	// kukeClientStagedMode lets any cell user run the client: the old file
	// mount of the host's /usr/local/bin/kuke was world-executable.
	kukeClientStagedMode os.FileMode = 0o755
)

// kukeClientPaths are the staged client artifacts `kuke init` re-asserts the
// mode of after applyKukeonOwnership's recursive chown.
type kukeClientPaths struct {
	Dir    string
	Binary string
	Shim   string
}

// stageKukeClient copies the running kuke binary into
// <runPath>/bin/client/kuke and writes the exec shim beside it. Both are
// content-idempotent: an unchanged binary and the constant shim are left
// untouched, so their inodes survive repeated `kuke init` runs.
func stageKukeClient(runPath string) (kukeClientPaths, error) {
	binDir := filepath.Join(runPath, "bin")
	paths := kukeClientPaths{
		Dir:  filepath.Join(binDir, kukeClientStagedSubdir),
		Shim: filepath.Join(binDir, kukeClientShimName),
	}
	paths.Binary = filepath.Join(paths.Dir, "kuke")
	if err := os.MkdirAll(paths.Dir, kukeClientStagedMode); err != nil {
		return kukeClientPaths{}, fmt.Errorf("create kuke client stage dir %q: %w", paths.Dir, err)
	}
	self, err := os.Executable()
	if err != nil {
		return kukeClientPaths{}, fmt.Errorf("resolve kuke executable: %w", err)
	}
	content, err := os.ReadFile(self)
	if err != nil {
		return kukeClientPaths{}, fmt.Errorf("read kuke executable %q: %w", self, err)
	}
	if _, err = fs.WriteFileIfChanged(paths.Binary, content, kukeClientStagedMode, "kuke client"); err != nil {
		return kukeClientPaths{}, err
	}
	shim := []byte(kukeClientShim)
	if _, err = fs.WriteFileIfChanged(paths.Shim, shim, kukeClientStagedMode, "kuke client shim"); err != nil {
		return kukeClientPaths{}, err
	}
	return paths, nil
}
