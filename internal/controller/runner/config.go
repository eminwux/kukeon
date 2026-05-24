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
	"path/filepath"
	"strings"

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

// WriteConfigIfAbsent atomically persists a CellConfig document only when no
// file at the target path exists yet (issue #839). Used by `kuke run <src>
// --clone`'s gap-fill counter allocator: the loop tries each candidate name
// and retries on errdefs.ErrConfigExists so two concurrent invocations cannot
// race onto the same slot. The implementation writes to a same-directory
// temp file, then uses `os.Link` to claim the destination — link is the
// portable atomic "create-or-fail" primitive on POSIX, returning EEXIST when
// the target already exists. On any failure the temp file is best-effort
// removed.
func (r *Exec) WriteConfigIfAbsent(config intmodel.CellConfig) error {
	md := config.Metadata
	dir := fs.ConfigsDir(r.opts.RunPath, md.Realm, md.Space, md.Stack)
	path := fs.ConfigPath(r.opts.RunPath, md.Realm, md.Space, md.Stack, md.Name)

	if err := os.MkdirAll(dir, configsDirMode); err != nil {
		return fmt.Errorf("%w: create configs dir: %w", errdefs.ErrCreateConfig, err)
	}
	if err := os.Chmod(dir, configsDirMode); err != nil {
		return fmt.Errorf("%w: chmod configs dir: %w", errdefs.ErrCreateConfig, err)
	}

	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("%w: create temp file: %w", errdefs.ErrCreateConfig, err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, writeErr := tmp.Write(config.Document); writeErr != nil {
		_ = tmp.Close()
		return fmt.Errorf("%w: write temp file: %w", errdefs.ErrCreateConfig, writeErr)
	}
	if closeErr := tmp.Close(); closeErr != nil {
		return fmt.Errorf("%w: close temp file: %w", errdefs.ErrCreateConfig, closeErr)
	}
	if chmodErr := os.Chmod(tmpName, configFileMode); chmodErr != nil {
		return fmt.Errorf("%w: chmod temp file: %w", errdefs.ErrCreateConfig, chmodErr)
	}

	// os.Link is the portable atomic "create destination, fail if exists"
	// primitive on POSIX (rename overwrites silently and so cannot serve the
	// concurrent-clone AC). On success the temp file is the same inode as
	// the destination; the deferred Remove unlinks the temp name only.
	if linkErr := os.Link(tmpName, path); linkErr != nil {
		if errors.Is(linkErr, os.ErrExist) {
			return errdefs.ErrConfigExists
		}
		return fmt.Errorf("%w: link temp into place: %w", errdefs.ErrCreateConfig, linkErr)
	}

	r.logger.InfoContext(r.ctx, "config created",
		"name", md.Name,
		"realm", md.Realm,
		"space", md.Space,
		"stack", md.Stack,
	)
	return nil
}

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

// GetConfig reads a single named, scoped CellConfig's document off disk (issue
// #644). Like GetBlueprint — and unlike GetSecret — the full document is read
// back and returned: a Config carries only a blueprint reference, scalar
// values, and slot fills (no credential bytes), so the whole document is safe
// to surface for `kuke get config`. Returns errdefs.ErrConfigNotFound when the
// file is absent.
func (r *Exec) GetConfig(config intmodel.CellConfig) (intmodel.CellConfig, error) {
	md := config.Metadata
	path := fs.ConfigPath(r.opts.RunPath, md.Realm, md.Space, md.Stack, md.Name)

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return intmodel.CellConfig{}, errdefs.ErrConfigNotFound
		}
		return intmodel.CellConfig{}, fmt.Errorf("%w: %w", errdefs.ErrGetConfig, err)
	}

	return intmodel.CellConfig{Metadata: md, Document: data}, nil
}

// ListConfigs enumerates the metadata of every CellConfig bound to the scope
// identified by the filter coordinates, plus every Config bound to a deeper
// scope nested within it (issue #644). The filter is a prefix: an empty
// realmName lists across all realms; a set realmName with an empty spaceName
// lists realm-scoped configs and everything under that realm; and so on. This
// mirrors the subtree-filter semantics of ListBlueprints, bounded at stack
// depth — a Config is never cell-scoped (#624). The returned carriers are
// metadata-only (Document nil): the scope coordinates come from the path and
// the name from the file basename, so a list never parses every document.
func (r *Exec) ListConfigs(realmName, spaceName, stackName string) ([]intmodel.CellConfig, error) {
	realmDirs, err := r.resolveRealmDirs(realmName)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrListConfigs, err)
	}

	var out []intmodel.CellConfig
	for _, realmDir := range realmDirs {
		realm := filepath.Base(realmDir)
		if walkErr := r.collectConfigSubtree(&out, realm, spaceName, stackName); walkErr != nil {
			return nil, fmt.Errorf("%w: %w", errdefs.ErrListConfigs, walkErr)
		}
	}
	return out, nil
}

// collectConfigSubtree appends the metadata of every CellConfig bound to scope
// (realm, space, stack) — where a trailing coordinate that is "" marks the
// filter floor — and every Config in scopes nested within it. The rule mirrors
// collectBlueprintSubtree: collect a level's own configs only when the
// next-deeper filter coordinate is empty, and descend into a child only when it
// matches a set filter coordinate or the filter is empty at that level. The
// walk is bounded at stack depth, so cell directories are never descended.
func (r *Exec) collectConfigSubtree(out *[]intmodel.CellConfig, realm, space, stack string) error {
	if space == "" {
		if err := r.collectConfigsInScope(out, realm, "", ""); err != nil {
			return err
		}
	}

	spaces, err := r.childScopeNames(fs.RealmMetadataDir(r.opts.RunPath, realm), space)
	if err != nil {
		return err
	}
	for _, sp := range spaces {
		if stack == "" {
			if err = r.collectConfigsInScope(out, realm, sp, ""); err != nil {
				return err
			}
		}

		stacks, stErr := r.childScopeNames(fs.SpaceMetadataDir(r.opts.RunPath, realm, sp), stack)
		if stErr != nil {
			return stErr
		}
		for _, st := range stacks {
			if err = r.collectConfigsInScope(out, realm, sp, st); err != nil {
				return err
			}
		}
	}
	return nil
}

// collectConfigsInScope appends the metadata of every CellConfig stored
// directly at the given scope (realm, space, stack). The in-flight
// ".config-*.tmp" temp files WriteConfig creates are skipped so a concurrent
// apply never surfaces a half-written name.
func (r *Exec) collectConfigsInScope(out *[]intmodel.CellConfig, realm, space, stack string) error {
	dir := fs.ConfigsDir(r.opts.RunPath, realm, space, stack)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read configs dir %q: %w", dir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".config-") && strings.HasSuffix(name, ".tmp") {
			continue
		}
		*out = append(*out, intmodel.CellConfig{
			Metadata: intmodel.CellConfigMetadata{
				Name:  name,
				Realm: realm,
				Space: space,
				Stack: stack,
			},
		})
	}
	return nil
}

// DeleteConfig removes the daemon-stored document file for a single named,
// scoped CellConfig (issue #644). Returns errdefs.ErrConfigNotFound when the
// file is absent so the caller can report a clear "not found" instead of a
// silent success. Deleting a Config never touches the cell it materialized —
// that is `kuke delete cell` — so there is no live-reference gate here; the
// back-reference notice is computed and surfaced by the controller layer.
func (r *Exec) DeleteConfig(config intmodel.CellConfig) error {
	md := config.Metadata
	path := fs.ConfigPath(r.opts.RunPath, md.Realm, md.Space, md.Stack, md.Name)

	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errdefs.ErrConfigNotFound
		}
		return fmt.Errorf("%w: %w", errdefs.ErrDeleteConfig, err)
	}

	r.logger.InfoContext(r.ctx, "config deleted",
		"name", md.Name,
		"realm", md.Realm,
		"space", md.Space,
		"stack", md.Stack,
	)
	return nil
}
