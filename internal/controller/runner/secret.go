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

	"github.com/eminwux/kukeon/internal/consts"
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

// GetSecret reports whether a single named, scoped Secret exists on disk and
// returns its metadata-only view (issue #622). The bytes are never read: a
// stat is sufficient to confirm existence, and echoing the material would
// violate the never-round-tripped contract from #619. Returns
// errdefs.ErrSecretNotFound when the file is absent.
func (r *Exec) GetSecret(secret intmodel.Secret) (intmodel.Secret, error) {
	md := secret.Metadata
	path := fs.SecretPath(r.opts.RunPath, md.Realm, md.Space, md.Stack, md.Cell, md.Name)

	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return intmodel.Secret{}, errdefs.ErrSecretNotFound
		}
		return intmodel.Secret{}, fmt.Errorf("%w: %w", errdefs.ErrGetSecret, err)
	}
	if info.IsDir() {
		// A directory at the secret path is not a secret.
		return intmodel.Secret{}, errdefs.ErrSecretNotFound
	}

	// Metadata-only: scope + name come from the path, never the bytes.
	return intmodel.Secret{Metadata: md}, nil
}

// ListSecrets enumerates the metadata of every Secret bound to the scope
// identified by the filter coordinates, plus every Secret bound to a deeper
// scope nested within it (issue #622). The filter is a prefix: an empty
// realmName lists across all realms; a set realmName with an empty spaceName
// lists realm-scoped secrets and everything under that realm; and so on. This
// mirrors the subtree-filter semantics of `kuke get cells --realm <r>`. The
// bytes are never read — only the scope coordinates (from the path) and the
// secret name (the file basename) are returned.
func (r *Exec) ListSecrets(realmName, spaceName, stackName, cellName string) ([]intmodel.Secret, error) {
	realmDirs, err := r.resolveRealmDirs(realmName)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrListSecrets, err)
	}

	var out []intmodel.Secret
	for _, realmDir := range realmDirs {
		realm := filepath.Base(realmDir)
		if walkErr := r.collectSecretSubtree(&out, realm, spaceName, stackName, cellName); walkErr != nil {
			return nil, fmt.Errorf("%w: %w", errdefs.ErrListSecrets, walkErr)
		}
	}
	return out, nil
}

// collectSecretSubtree appends the metadata of every Secret bound to scope
// (realm, space, stack, cell) — where a trailing coordinate that is "" marks
// the filter floor — and every Secret in scopes nested within it. The rule:
// collect a level's own secrets only when the next-deeper filter coordinate is
// empty (so a set deeper coordinate excludes shallower scopes), and descend
// into a child only when it matches a set filter coordinate or the filter is
// empty at that level (collect-all). The walk is bounded at cell depth, so the
// per-container subdirectories under a cell are never mistaken for scopes.
func (r *Exec) collectSecretSubtree(out *[]intmodel.Secret, realm, space, stack, cell string) error {
	// Realm level: collect realm-scoped secrets only when no deeper coordinate
	// is requested (space empty ⇒ realm is the floor or we're collecting the
	// whole realm subtree).
	if space == "" {
		if err := r.collectSecretsInScope(out, realm, "", "", ""); err != nil {
			return err
		}
	}

	spaces, err := r.childScopeNames(fs.RealmMetadataDir(r.opts.RunPath, realm), space)
	if err != nil {
		return err
	}
	for _, sp := range spaces {
		if stack == "" {
			if err = r.collectSecretsInScope(out, realm, sp, "", ""); err != nil {
				return err
			}
		}

		stacks, stErr := r.childScopeNames(fs.SpaceMetadataDir(r.opts.RunPath, realm, sp), stack)
		if stErr != nil {
			return stErr
		}
		for _, st := range stacks {
			if cell == "" {
				if err = r.collectSecretsInScope(out, realm, sp, st, ""); err != nil {
					return err
				}
			}

			cells, cErr := r.childScopeNames(fs.StackMetadataDir(r.opts.RunPath, realm, sp, st), cell)
			if cErr != nil {
				return cErr
			}
			for _, ce := range cells {
				if err = r.collectSecretsInScope(out, realm, sp, st, ce); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// childScopeNames returns the child-resource subdirectory names directly under
// dir, excluding the reserved secrets/ subdirectory. When want is non-empty it
// is treated as a filter: only that child is returned, and only if it exists.
// A missing dir yields no children (graceful, matching the list verbs' "no
// filter match ⇒ empty" contract).
func (r *Exec) childScopeNames(dir, want string) ([]string, error) {
	if strings.TrimSpace(want) != "" {
		child := filepath.Join(dir, want)
		info, err := os.Stat(child)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			return nil, fmt.Errorf("failed to stat scope dir %q: %w", child, err)
		}
		if !info.IsDir() {
			return nil, nil
		}
		return []string{want}, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read scope dir %q: %w", dir, err)
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if entry.Name() == consts.KukeonSecretsSubdir {
			continue
		}
		names = append(names, entry.Name())
	}
	return names, nil
}

// collectSecretsInScope appends the metadata of every Secret stored directly at
// the given scope (realm, space, stack, cell). Regular secret files are bytes
// files; the in-flight ".secret-*.tmp" temp files atomicWriteSecret creates are
// skipped so a concurrent apply never surfaces a half-written name.
func (r *Exec) collectSecretsInScope(out *[]intmodel.Secret, realm, space, stack, cell string) error {
	dir := fs.SecretsDir(r.opts.RunPath, realm, space, stack, cell)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read secrets dir %q: %w", dir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".secret-") && strings.HasSuffix(name, ".tmp") {
			continue
		}
		*out = append(*out, intmodel.Secret{
			Metadata: intmodel.SecretMetadata{
				Name:  name,
				Realm: realm,
				Space: space,
				Stack: stack,
				Cell:  cell,
			},
		})
	}
	return nil
}

// DeleteSecret removes the daemon-stored bytes file for a single named, scoped
// Secret (issue #622). Returns errdefs.ErrSecretNotFound when the file is
// absent so the caller can report a clear "not found" instead of a silent
// success. The bytes are never read before removal.
func (r *Exec) DeleteSecret(secret intmodel.Secret) error {
	md := secret.Metadata
	path := fs.SecretPath(r.opts.RunPath, md.Realm, md.Space, md.Stack, md.Cell, md.Name)

	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errdefs.ErrSecretNotFound
		}
		return fmt.Errorf("%w: %w", errdefs.ErrDeleteSecret, err)
	}

	// Log the scope + name only — never the bytes.
	r.logger.InfoContext(r.ctx, "secret deleted",
		"name", md.Name,
		"realm", md.Realm,
		"space", md.Space,
		"stack", md.Stack,
		"cell", md.Cell,
	)
	return nil
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
