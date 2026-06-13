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
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/eminwux/kukeon/internal/consts"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// reclaimScopeMetadata removes a scope's on-disk metadata tree on cascade purge,
// preserving any `reclaimPolicy: Retain` volume in the subtree (step 3, #1237).
//
// When the scope holds no retained volume it falls through to the blunt
// os.RemoveAll(scopeDir) the three purge paths ran before this rework, so a
// scope with only default-policy volumes is reclaimed byte-identically to step
// 1 — no behavior change for existing specs.
//
// When at least one retained volume exists, the scope dir, the intervening
// scope skeleton, the volumes/ container dir, the retained volume directory
// (with its container-written contents) and its volume-meta/ reclaim manifest
// are all preserved; everything else under the scope — the scope's own
// metadata.json, cells, child scopes, non-retained volumes, secrets/blueprints/
// configs — is removed. The surviving scope dir carries no metadata.json, so a
// list/get of the scope itself still reports it gone (those gate on the
// metadata file), while the retained volume stays discoverable and deletable at
// its original coordinates (ListVolumes/GetVolume/DeleteVolume walk by
// directory presence).
func (r *Exec) reclaimScopeMetadata(scopeDir string) error {
	leaves, ancestors, err := r.collectRetainedPreserveSet(scopeDir)
	if err != nil {
		return err
	}
	if len(leaves) == 0 {
		return os.RemoveAll(scopeDir)
	}
	return pruneExcept(scopeDir, leaves, ancestors)
}

// collectRetainedPreserveSet walks scopeDir for reclaim manifests marking a
// volume Retain and returns two sets keyed by absolute path: leaves (retained
// volume directories and their manifest files — kept whole, not recursed) and
// ancestors (every directory on the path from scopeDir down to a leaf — kept and
// recursed so the skeleton survives). An empty leaves set means no retained
// volume, the blunt-RemoveAll fast path.
func (r *Exec) collectRetainedPreserveSet(scopeDir string) (leaves, ancestors map[string]struct{}, err error) {
	leaves = map[string]struct{}{}
	ancestors = map[string]struct{}{}

	walkErr := filepath.WalkDir(scopeDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() {
			return nil
		}
		switch d.Name() {
		case consts.KukeonVolumesSubdir:
			// Volume data directories never hold manifests; don't descend into
			// container-written contents looking for them.
			return filepath.SkipDir
		case consts.KukeonVolumeMetaSubdir:
			if collectErr := r.collectRetainedInMetaDir(path, leaves, ancestors, scopeDir); collectErr != nil {
				return collectErr
			}
			return filepath.SkipDir
		default:
			return nil
		}
	})
	if walkErr != nil {
		return nil, nil, fmt.Errorf("scan scope %q for retained volumes: %w", scopeDir, walkErr)
	}
	return leaves, ancestors, nil
}

// collectRetainedInMetaDir reads every reclaim manifest in a volume-meta/ dir
// and, for each one marking Retain, records the manifest file and the matching
// volume directory as preserve leaves plus their ancestor chain up to scopeDir.
func (r *Exec) collectRetainedInMetaDir(metaDir string, leaves, ancestors map[string]struct{}, scopeDir string) error {
	entries, err := os.ReadDir(metaDir)
	if err != nil {
		return fmt.Errorf("read volume-meta dir %q: %w", metaDir, err)
	}
	// The scope dir holding this volume-meta/ is its parent; the sibling volumes/
	// dir holds the actual volume directories.
	ownerScopeDir := filepath.Dir(metaDir)
	volumesDir := filepath.Join(ownerScopeDir, consts.KukeonVolumesSubdir)

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".json" {
			// Skip subdirectories and in-flight ".volume-meta-*.tmp" temp files.
			continue
		}
		manifestPath := filepath.Join(metaDir, name)
		data, readErr := os.ReadFile(manifestPath)
		if readErr != nil {
			return fmt.Errorf("read reclaim manifest %q: %w", manifestPath, readErr)
		}
		var manifest volumeReclaimManifest
		if jsonErr := json.Unmarshal(data, &manifest); jsonErr != nil {
			return fmt.Errorf("parse reclaim manifest %q: %w", manifestPath, jsonErr)
		}
		if intmodel.ReclaimPolicy(manifest.ReclaimPolicy) != intmodel.ReclaimRetain {
			continue
		}
		volName := name[:len(name)-len(".json")]
		volDir := filepath.Join(volumesDir, volName)

		leaves[manifestPath] = struct{}{}
		leaves[volDir] = struct{}{}
		addAncestors(ancestors, scopeDir, manifestPath)
		addAncestors(ancestors, scopeDir, volDir)
	}
	return nil
}

// addAncestors records every directory on the path from scopeDir (inclusive)
// down to leaf's parent (inclusive) so pruneExcept keeps and recurses into them
// rather than removing them. leaf itself is not added (it is a preserve leaf).
func addAncestors(ancestors map[string]struct{}, scopeDir, leaf string) {
	for dir := filepath.Dir(leaf); ; dir = filepath.Dir(dir) {
		ancestors[dir] = struct{}{}
		if dir == scopeDir {
			return
		}
		// Defensive stop: never climb above scopeDir (the leaf is always a
		// descendant, so this only guards against a malformed input).
		if len(dir) <= len(scopeDir) {
			return
		}
	}
}

// pruneExcept removes every entry under dir except preserve leaves (kept whole)
// and ancestors (kept and recursed). dir itself is never removed.
func pruneExcept(dir string, leaves, ancestors map[string]struct{}) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read dir %q during selective reclaim: %w", dir, err)
	}
	for _, entry := range entries {
		full := filepath.Join(dir, entry.Name())
		if _, ok := leaves[full]; ok {
			continue
		}
		if _, ok := ancestors[full]; ok {
			if err := pruneExcept(full, leaves, ancestors); err != nil {
				return err
			}
			continue
		}
		if err := os.RemoveAll(full); err != nil {
			return fmt.Errorf("remove %q during selective reclaim: %w", full, err)
		}
	}
	return nil
}
