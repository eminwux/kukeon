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

// secretsDirMode is the mode of the per-scope secrets/ directory. Unlike the
// 0o2750 setgid metadata directories the kuke group may traverse, the
// secrets directory is root-only: only the daemon (root) reads or writes
// secret material.
const secretsDirMode os.FileMode = 0o700

// secretFileMode is the mode of an individual secret's bytes file: read/write
// for the owner (root) only, matching the AC's chmod-600 requirement.
const secretFileMode os.FileMode = 0o600

// WriteSecret persists a Secret's bytes to <RunPath>/data/<scope>/secrets/<name>,
// root-owned and 0o600 (issue #619). The bytes are written atomically via a
// temp file + rename so a reader never observes a partially-written secret,
// and the data is never logged. Returns whether the file was newly created.
//
// The daemon runs as root, so files it creates are root-owned by default;
// WriteSecret additionally chmods the directory and file to strip any umask
// bits, so the 0o700/0o600 contract holds regardless of the daemon's umask.
func (r *Exec) WriteSecret(secret intmodel.Secret) (bool, error) {
	md := secret.Metadata
	dir := fs.SecretsDir(r.opts.RunPath, md.Realm, md.Space, md.Stack, md.Cell)
	path := fs.SecretPath(r.opts.RunPath, md.Realm, md.Space, md.Stack, md.Cell, md.Name)

	if err := os.MkdirAll(dir, secretsDirMode); err != nil {
		return false, fmt.Errorf("%w: create secrets dir: %w", errdefs.ErrWriteSecret, err)
	}
	// MkdirAll honors only the rwx bits and leaves a pre-existing directory's
	// mode intact; chmod unconditionally so the root-only contract holds even
	// when a parent created the dir with looser bits or the umask stripped them.
	if err := os.Chmod(dir, secretsDirMode); err != nil {
		return false, fmt.Errorf("%w: chmod secrets dir: %w", errdefs.ErrWriteSecret, err)
	}

	_, statErr := os.Stat(path)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return false, fmt.Errorf("%w: stat secret: %w", errdefs.ErrWriteSecret, statErr)
	}

	if err := atomicWriteSecret(dir, path, []byte(secret.Spec.Data)); err != nil {
		return false, fmt.Errorf("%w: %w", errdefs.ErrWriteSecret, err)
	}

	action := "updated"
	if created {
		action = "created"
	}
	// Log the scope + name + action only — never the bytes.
	r.logger.InfoContext(r.ctx, "secret "+action,
		"name", md.Name,
		"realm", md.Realm,
		"space", md.Space,
		"stack", md.Stack,
		"cell", md.Cell,
	)
	return created, nil
}

// atomicWriteSecret writes data to path via a temp file in the same directory
// followed by a rename, so a concurrent reader sees either the old inode or
// the fully-written new one — never a torn write. The temp file is created at
// secretFileMode and chmod'd to strip the umask before the rename.
func atomicWriteSecret(dir, path string, data []byte) error {
	tmp, err := os.CreateTemp(dir, ".secret-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp secret: %w", err)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup if we bail before the rename succeeds.
	defer func() { _ = os.Remove(tmpName) }()

	if _, writeErr := tmp.Write(data); writeErr != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp secret: %w", writeErr)
	}
	if closeErr := tmp.Close(); closeErr != nil {
		return fmt.Errorf("close temp secret: %w", closeErr)
	}
	if chmodErr := os.Chmod(tmpName, secretFileMode); chmodErr != nil {
		return fmt.Errorf("chmod temp secret: %w", chmodErr)
	}
	if renameErr := os.Rename(tmpName, path); renameErr != nil {
		return fmt.Errorf("rename secret into place: %w", renameErr)
	}
	return nil
}
