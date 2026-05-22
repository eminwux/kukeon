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

// blueprintsDirMode is the mode of the per-scope blueprints/ directory. Unlike
// the root-only secrets/ directory, a blueprint carries no credential bytes —
// only template references — so the directory is world-readable (issue #620).
const blueprintsDirMode os.FileMode = 0o755

// blueprintFileMode is the mode of an individual blueprint document file:
// root-owned, world-readable. References only, no secret material.
const blueprintFileMode os.FileMode = 0o644

// WriteBlueprint persists a CellBlueprint's serialized document to
// <RunPath>/data/<scope>/blueprints/<name>, root-owned and world-readable
// (issue #620). The document is written atomically via a temp file + rename so
// a reader never observes a partially-written blueprint. Returns whether the
// file was newly created (vs. overwritten). The caller (ReconcileBlueprint) is
// responsible for having verified the scope exists.
func (r *Exec) WriteBlueprint(blueprint intmodel.CellBlueprint) (bool, error) {
	md := blueprint.Metadata
	dir := fs.BlueprintsDir(r.opts.RunPath, md.Realm, md.Space, md.Stack)
	path := fs.BlueprintPath(r.opts.RunPath, md.Realm, md.Space, md.Stack, md.Name)

	if err := os.MkdirAll(dir, blueprintsDirMode); err != nil {
		return false, fmt.Errorf("%w: create blueprints dir: %w", errdefs.ErrWriteBlueprint, err)
	}
	// MkdirAll honors only the rwx bits and leaves a pre-existing directory's
	// mode intact; chmod unconditionally so the world-readable contract holds
	// even when a parent created the dir with tighter bits or the umask
	// stripped them.
	if err := os.Chmod(dir, blueprintsDirMode); err != nil {
		return false, fmt.Errorf("%w: chmod blueprints dir: %w", errdefs.ErrWriteBlueprint, err)
	}

	_, statErr := os.Stat(path)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return false, fmt.Errorf("%w: stat blueprint: %w", errdefs.ErrWriteBlueprint, statErr)
	}

	if err := atomicWriteFileMode(dir, path, ".blueprint-*.tmp", blueprint.Document, blueprintFileMode); err != nil {
		return false, fmt.Errorf("%w: %w", errdefs.ErrWriteBlueprint, err)
	}

	action := "updated"
	if created {
		action = "created"
	}
	r.logger.InfoContext(r.ctx, "blueprint "+action,
		"name", md.Name,
		"realm", md.Realm,
		"space", md.Space,
		"stack", md.Stack,
	)
	return created, nil
}

// GetBlueprint reads a single named, scoped CellBlueprint's document off disk
// (issue #620). Unlike GetSecret — which is metadata-only because secret bytes
// must never round-trip — a blueprint carries only template references, so the
// full document is read back and returned for `kuke run -b` to materialize.
// Returns errdefs.ErrBlueprintNotFound when the file is absent.
func (r *Exec) GetBlueprint(blueprint intmodel.CellBlueprint) (intmodel.CellBlueprint, error) {
	md := blueprint.Metadata
	path := fs.BlueprintPath(r.opts.RunPath, md.Realm, md.Space, md.Stack, md.Name)

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return intmodel.CellBlueprint{}, errdefs.ErrBlueprintNotFound
		}
		return intmodel.CellBlueprint{}, fmt.Errorf("%w: %w", errdefs.ErrGetBlueprint, err)
	}

	return intmodel.CellBlueprint{Metadata: md, Document: data}, nil
}

// atomicWriteFileMode writes data to path via a temp file in the same
// directory followed by a rename, so a concurrent reader sees either the old
// inode or the fully-written new one — never a torn write. The temp file is
// chmod'd to mode (stripping the umask) before the rename. Pattern is the
// CreateTemp prefix glob used for the in-flight temp file.
func atomicWriteFileMode(dir, path, pattern string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, writeErr := tmp.Write(data); writeErr != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file: %w", writeErr)
	}
	if closeErr := tmp.Close(); closeErr != nil {
		return fmt.Errorf("close temp file: %w", closeErr)
	}
	if chmodErr := os.Chmod(tmpName, mode); chmodErr != nil {
		return fmt.Errorf("chmod temp file: %w", chmodErr)
	}
	if renameErr := os.Rename(tmpName, path); renameErr != nil {
		return fmt.Errorf("rename file into place: %w", renameErr)
	}
	return nil
}
