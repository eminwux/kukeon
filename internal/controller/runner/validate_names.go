// Copyright 2026 Emiliano Spinella (eminwux)
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
	"os"
	"strings"

	"github.com/eminwux/kukeon/internal/util/fs"
	"github.com/eminwux/kukeon/internal/util/naming"
)

// validateScopeNames applies the shared name rule (issue #1719) to every set
// scope coordinate before it is joined into a path under <RunPath>/data. The
// runner entry points call it whichever client reached them (CLI in-process or
// the daemon socket), so a "..", ".", "_" or "/" name never reaches a read,
// write or remove. Empty coordinates are skipped: whether a coordinate is
// required is each caller's concern, reported with its own sentinel first.
// Realm names fail with errdefs.ErrInvalidRealmName, the rest with
// errdefs.ErrInvalidName.
func validateScopeNames(realm, space, stack, cell string) error {
	if realm = strings.TrimSpace(realm); realm != "" {
		if err := naming.ValidateRealmName(realm); err != nil {
			return err
		}
	}
	for _, seg := range []struct{ kind, value string }{
		{"space", space},
		{"stack", stack},
		{"cell", cell},
	} {
		if strings.TrimSpace(seg.value) == "" {
			continue
		}
		if err := naming.ValidateHierarchyName(seg.kind, seg.value); err != nil {
			return err
		}
	}
	return nil
}

// removeCellDir removes a cell's metadata directory after the containment
// guard confirms it is exactly a cell's depth below fs.MetadataRoot (issue
// #1719), so a "." or ".." smuggled into any scope name can never widen the
// RemoveAll to a stack, space, realm or the whole tree.
func (r *Exec) removeCellDir(cellDir string) error {
	if err := fs.EnsureContained(fs.MetadataRoot(r.opts.RunPath), cellDir, fs.CellDepth); err != nil {
		return err
	}
	return os.RemoveAll(cellDir)
}

// guardKindRemove is the containment guard for a per-resource remove (Secret,
// CellBlueprint, CellConfig, Volume, Team, Source; issue #1719): kindDir must
// sit exactly kindDepth levels below fs.MetadataRoot (the scope's depth plus
// one for the kind's own subdirectory) and target must be a direct child of
// kindDir.
func (r *Exec) guardKindRemove(kindDir string, kindDepth int, target string) error {
	if err := fs.EnsureContained(fs.MetadataRoot(r.opts.RunPath), kindDir, kindDepth); err != nil {
		return err
	}
	return fs.EnsureContained(kindDir, target, 1)
}

// kindDirDepth is the depth below fs.MetadataRoot of a kind's subdirectory
// (secrets/, blueprints/, configs/, volumes/, ...) in the scope named by the
// coordinates: the deepest set coordinate picks the scope, as the fs scope-dir
// builders do, and the kind's subdirectory adds one level.
func kindDirDepth(space, stack, cell string) int {
	switch {
	case cell != "":
		return fs.CellDepth + 1
	case stack != "":
		return fs.StackDepth + 1
	case space != "":
		return fs.SpaceDepth + 1
	default:
		return fs.RealmDepth + 1
	}
}
