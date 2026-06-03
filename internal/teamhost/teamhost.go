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

// Package teamhost owns the host-side, on-disk lifecycle of the
// team-distribution config maintained by `kuke team init` (epic #792, step 1
// #796): the operator-global facts file (~/.kuke/kuketeams.yaml) and the
// per-project drop-in directory (~/.kuke/kuketeam.d/<project>.yaml).
//
// The drop-in layout (the systemd/sudoers.d pattern) replaces a single shared
// teams[] array: each project owns one file, so a corrupt or partial write —
// which can happen on every init — touches one project rather than the whole
// roster, and two concurrent `kuke team init` runs never race on a shared
// array. The Base directory is injected (rather than resolved from $HOME here)
// so the lifecycle is unit-testable against a temp dir with no live kukeond.
package teamhost

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	model "github.com/eminwux/kukeon/pkg/api/model/kuketeams"
	"gopkg.in/yaml.v3"
)

const (
	// globalConfigName is the operator-global facts file under Base.
	globalConfigName = "kuketeams.yaml"
	// dropInDirName is the per-project drop-in directory under Base.
	dropInDirName = "kuketeam.d"

	// dropInDirPerm is the drop-in directory mode: operator-only (the files
	// reference secret sources and signing keys).
	dropInDirPerm = 0o700
	// configFilePerm is the mode for the global facts file and each per-project
	// entry — operator read/write only.
	configFilePerm = 0o600
)

// Layout resolves the team-distribution file paths under a base directory
// (normally ~/.kuke). The zero value is unusable; construct with NewLayout.
type Layout struct {
	// Base is the directory holding kuketeams.yaml and kuketeam.d/.
	Base string
}

// NewLayout returns a Layout rooted at base.
func NewLayout(base string) Layout {
	return Layout{Base: base}
}

// GlobalConfigPath is the operator-global facts file (<base>/kuketeams.yaml).
func (l Layout) GlobalConfigPath() string {
	return filepath.Join(l.Base, globalConfigName)
}

// DropInDir is the per-project drop-in directory (<base>/kuketeam.d).
func (l Layout) DropInDir() string {
	return filepath.Join(l.Base, dropInDirName)
}

// EntryPath is the per-project file (<base>/kuketeam.d/<project>.yaml).
func (l Layout) EntryPath(project string) string {
	return filepath.Join(l.DropInDir(), project+".yaml")
}

// EnsureGlobalConfig writes cfg to the global facts path only when no file is
// already present, returning created=true when it scaffolded the file and
// false when one already existed (the re-run case — its contents are left
// untouched). The parent directory is created (0o700) if absent.
func EnsureGlobalConfig(l Layout, cfg *model.TeamsConfig) (bool, error) {
	path := l.GlobalConfigPath()
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("stat global config %q: %w", path, err)
	}

	if mkErr := os.MkdirAll(l.Base, dropInDirPerm); mkErr != nil {
		return false, fmt.Errorf("create config dir %q: %w", l.Base, mkErr)
	}
	if err := writeYAML(path, cfg); err != nil {
		return false, err
	}
	return true, nil
}

// WriteEntry persists entry to its per-project path, creating the drop-in
// directory (0o700) if absent. Only the named project's file is touched, so
// rewriting one project's entry never disturbs another's. The write is atomic
// (temp file + rename) and the resulting file is 0o600.
func WriteEntry(l Layout, entry *model.TeamEntry) error {
	project := strings.TrimSpace(entry.Metadata.Name)
	if project == "" {
		return errdefs.ErrTeamEntryNameRequired
	}
	dir := l.DropInDir()
	if err := os.MkdirAll(dir, dropInDirPerm); err != nil {
		return fmt.Errorf("create drop-in dir %q: %w", dir, err)
	}
	return writeYAML(l.EntryPath(project), entry)
}

// writeYAML marshals doc to YAML and writes it atomically to path (temp file in
// the same directory + rename), leaving the file 0o600. Writing into the target
// directory keeps the rename on the same filesystem so it is atomic.
func writeYAML(path string, doc any) error {
	raw, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("marshal %q: %w", path, err)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".kuketeam-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file in %q: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op once the rename succeeds

	if _, writeErr := tmp.Write(raw); writeErr != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file %q: %w", tmpName, writeErr)
	}
	if closeErr := tmp.Close(); closeErr != nil {
		return fmt.Errorf("close temp file %q: %w", tmpName, closeErr)
	}
	if chmodErr := os.Chmod(tmpName, configFilePerm); chmodErr != nil {
		return fmt.Errorf("chmod temp file %q: %w", tmpName, chmodErr)
	}
	if renameErr := os.Rename(tmpName, path); renameErr != nil {
		return fmt.Errorf("rename %q to %q: %w", tmpName, path, renameErr)
	}
	return nil
}
