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
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/eminwux/kukeon/internal/consts"
)

// reservedScopeSubdirs is the single source of truth for the per-scope
// subdirectory names that hold resources (not child scopes) under
// <RunPath>/data/<realm>/[<space>/[<stack>/[<cell>/]]]: secrets/ (#619),
// blueprints/ (#620), configs/ (#624), volumes/ (#1018), and volume-meta/
// (#1237, the daemon-owned reclaim manifests for volumes). The subtree-list
// walkers in secret.go / blueprint.go / config.go / volume.go enumerate child
// scopes by reading the scope dir and must skip every entry in this set,
// otherwise a reserved resource subdir would be mistaken for a phantom
// space/stack/cell.
//
// All four walkers consume this set via childScopeNames so a future reserved
// subdir is added in exactly one place — the inconsistency that motivated
// issue #734 (blueprintChildScopeNames missed configs/, secret childScopeNames
// missed blueprints/ and configs/) reappears the moment any walker
// re-implements its own exclusion list.
var reservedScopeSubdirs = map[string]struct{}{
	consts.KukeonSecretsSubdir:    {},
	consts.KukeonBlueprintsSubdir: {},
	consts.KukeonConfigsSubdir:    {},
	consts.KukeonVolumesSubdir:    {},
	consts.KukeonVolumeMetaSubdir: {},
}

// childScopeNames returns the child-scope subdirectory names directly under
// dir, excluding every reserved resource subdirectory (see
// reservedScopeSubdirs) so none is mistaken for a child space, stack, or cell.
// When want is non-empty it is treated as a filter: only that child is
// returned, and only if it exists as a directory. A missing dir yields no
// children (graceful, matching the list verbs' "no filter match ⇒ empty"
// contract).
//
// All three resource-subtree walkers (ListSecrets, ListBlueprints,
// ListConfigs) share this implementation. The want filter is honored even
// when it names a reserved subdir — that mirrors the prior behavior of the
// three near-duplicate helpers and lets a future walker over a reserved
// subdir reuse this same routine; an empty-want walk over a regular scope dir
// never surfaces reserved subdirs.
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
		if _, reserved := reservedScopeSubdirs[entry.Name()]; reserved {
			continue
		}
		names = append(names, entry.Name())
	}
	return names, nil
}
