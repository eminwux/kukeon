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
	"os"

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
)

// volumeMetaDirMode is the mode of the per-scope volume-meta/ directory: root-
// only (0o700, no group or world access). Unlike the volumes/ container dir —
// which a non-root party traverses to reach a volume it mounts — the reclaim
// manifests are daemon-private: only kukeond (root) reads them, at cascade-purge
// time, to decide which volumes survive. Keeping the directory root-only is what
// makes a Retain marker un-tamperable by a mounting container.
const volumeMetaDirMode os.FileMode = 0o700

// volumeMetaFileMode is the mode of a single reclaim manifest (0o600, root-only).
const volumeMetaFileMode os.FileMode = 0o600

// volumeReclaimManifest is the on-disk shape of a volume's reclaim manifest.
// Only a Retain volume has one (a Delete/omitted policy writes nothing and
// removes any stale manifest), so its mere presence already implies Retain; the
// explicit field future-proofs the format and lets GetVolume echo the exact
// value back.
type volumeReclaimManifest struct {
	ReclaimPolicy string `json:"reclaimPolicy"`
}

// persistVolumeReclaimPolicy reconciles the on-disk reclaim manifest for a
// volume to match its desired spec. A Retain policy writes the manifest
// (creating the root-only volume-meta/ dir on demand); any other value
// (including the empty default) removes any manifest left from a prior Retain so
// re-applying a volume with the policy flipped back to Delete drops its
// protection. Called from WriteVolume after the volume directory is in place.
func (r *Exec) persistVolumeReclaimPolicy(volume intmodel.Volume) error {
	md := volume.Metadata
	metaPath := fs.VolumeMetaPath(r.opts.RunPath, md.Realm, md.Space, md.Stack, md.Name)

	if volume.Spec.ReclaimPolicy != intmodel.ReclaimRetain {
		if err := os.Remove(metaPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("%w: remove stale reclaim manifest: %w", errdefs.ErrWriteVolume, err)
		}
		return nil
	}

	dir := fs.VolumeMetaDir(r.opts.RunPath, md.Realm, md.Space, md.Stack)
	if err := os.MkdirAll(dir, volumeMetaDirMode); err != nil {
		return fmt.Errorf("%w: create volume-meta dir: %w", errdefs.ErrWriteVolume, err)
	}
	// MkdirAll honors only the rwx bits and leaves a pre-existing directory's
	// mode intact; chmod so the root-only contract holds even if a parent
	// created the dir with looser bits or the umask stripped them.
	if err := os.Chmod(dir, volumeMetaDirMode); err != nil {
		return fmt.Errorf("%w: chmod volume-meta dir: %w", errdefs.ErrWriteVolume, err)
	}

	data, err := json.Marshal(volumeReclaimManifest{ReclaimPolicy: string(intmodel.ReclaimRetain)})
	if err != nil {
		return fmt.Errorf("%w: marshal reclaim manifest: %w", errdefs.ErrWriteVolume, err)
	}
	if err := atomicWriteFileMode(dir, metaPath, ".volume-meta-*.tmp", data, volumeMetaFileMode); err != nil {
		return fmt.Errorf("%w: write reclaim manifest: %w", errdefs.ErrWriteVolume, err)
	}
	return nil
}

// readVolumeReclaimPolicy returns the reclaim policy persisted for a volume, or
// the empty policy (Delete semantics) when no manifest exists. A manifest that
// fails to parse is reported as an error so a corrupt marker surfaces rather
// than silently downgrading a Retain volume to Delete.
func (r *Exec) readVolumeReclaimPolicy(md intmodel.VolumeMetadata) (intmodel.ReclaimPolicy, error) {
	metaPath := fs.VolumeMetaPath(r.opts.RunPath, md.Realm, md.Space, md.Stack, md.Name)
	data, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	var manifest volumeReclaimManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", fmt.Errorf("parse reclaim manifest %q: %w", metaPath, err)
	}
	return intmodel.ReclaimPolicy(manifest.ReclaimPolicy), nil
}

// removeVolumeReclaimManifest deletes a volume's reclaim manifest, ignoring a
// missing manifest (the common case — only Retain volumes have one). Called
// from DeleteVolume so deleting a retained volume leaves no orphan marker.
func (r *Exec) removeVolumeReclaimManifest(md intmodel.VolumeMetadata) error {
	metaPath := fs.VolumeMetaPath(r.opts.RunPath, md.Realm, md.Space, md.Stack, md.Name)
	if err := os.Remove(metaPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
