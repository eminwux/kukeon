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
	"errors"
	"fmt"
	"os"

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
)

// configsDirMode is the mode of the per-scope configs/ directory. Like the
// blueprints/ directory and unlike the root-only secrets/ directory, a config
// carries no credential bytes — only references — so it is world-readable
// (issue #624).
const configsDirMode os.FileMode = 0o755

// configFileMode is the mode of an individual config document file: root-owned,
// world-readable. References only, no secret material.
const configFileMode os.FileMode = 0o644

// WriteConfig persists a CellConfig's serialized document to
// <RunPath>/data/<scope>/configs/<name>, root-owned and world-readable (issue
// #624). The document is written atomically via a temp file + rename so a
// reader never observes a partially-written config. Returns whether the file
// was newly created (vs. overwritten). The caller (ReconcileConfig) is
// responsible for having verified the scope exists and the referenced blueprint
// resolves.
func (r *Exec) WriteConfig(config intmodel.CellConfig) (bool, error) {
	md := config.Metadata
	dir := fs.ConfigsDir(r.opts.RunPath, md.Realm, md.Space, md.Stack)
	path := fs.ConfigPath(r.opts.RunPath, md.Realm, md.Space, md.Stack, md.Name)

	if err := os.MkdirAll(dir, configsDirMode); err != nil {
		return false, fmt.Errorf("%w: create configs dir: %w", errdefs.ErrWriteConfig, err)
	}
	// MkdirAll honors only the rwx bits and leaves a pre-existing directory's
	// mode intact; chmod unconditionally so the world-readable contract holds
	// even when a parent created the dir with tighter bits or the umask
	// stripped them.
	if err := os.Chmod(dir, configsDirMode); err != nil {
		return false, fmt.Errorf("%w: chmod configs dir: %w", errdefs.ErrWriteConfig, err)
	}

	_, statErr := os.Stat(path)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return false, fmt.Errorf("%w: stat config: %w", errdefs.ErrWriteConfig, statErr)
	}

	if err := atomicWriteFileMode(dir, path, ".config-*.tmp", config.Document, configFileMode); err != nil {
		return false, fmt.Errorf("%w: %w", errdefs.ErrWriteConfig, err)
	}

	action := "updated"
	if created {
		action = "created"
	}
	r.logger.InfoContext(r.ctx, "config "+action,
		"name", md.Name,
		"realm", md.Realm,
		"space", md.Space,
		"stack", md.Stack,
	)
	return created, nil
}
