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
	"path/filepath"
	"strings"
	"time"

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
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

// volumeReclaimManifest is the on-disk shape of a volume's manifest: the part
// of its spec that is not the directory itself. A volume has one when it is
// retained or tagged as a login identity (issue #1685); a volume with neither
// writes nothing and removes any stale manifest. Presence alone therefore does
// not imply Retain — cascade purge reads the ReclaimPolicy field.
type volumeReclaimManifest struct {
	ReclaimPolicy string `json:"reclaimPolicy,omitempty"`
	Connector     string `json:"connector,omitempty"`
	Method        string `json:"method,omitempty"`
	LoginMarker   string `json:"loginMarker,omitempty"`
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

	if volume.Spec.ReclaimPolicy != intmodel.ReclaimRetain && volume.Spec.Connector == "" {
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

	manifest := volumeReclaimManifest{
		Connector:   volume.Spec.Connector,
		Method:      volume.Spec.Method,
		LoginMarker: volume.Spec.LoginMarker,
	}
	if volume.Spec.ReclaimPolicy == intmodel.ReclaimRetain {
		manifest.ReclaimPolicy = string(intmodel.ReclaimRetain)
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("%w: marshal reclaim manifest: %w", errdefs.ErrWriteVolume, err)
	}
	if err := atomicWriteFileMode(dir, metaPath, ".volume-meta-*.tmp", data, volumeMetaFileMode); err != nil {
		return fmt.Errorf("%w: write reclaim manifest: %w", errdefs.ErrWriteVolume, err)
	}
	return nil
}

// readVolumeSpec returns the spec persisted in a volume's manifest: its
// reclaim policy and its login-identity tag. A volume without a manifest has
// the zero spec.
func (r *Exec) readVolumeSpec(md intmodel.VolumeMetadata) (intmodel.VolumeSpec, error) {
	metaPath := fs.VolumeMetaPath(r.opts.RunPath, md.Realm, md.Space, md.Stack, md.Name)
	data, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return intmodel.VolumeSpec{}, nil
		}
		return intmodel.VolumeSpec{}, err
	}
	var manifest volumeReclaimManifest
	if err = json.Unmarshal(data, &manifest); err != nil {
		return intmodel.VolumeSpec{}, fmt.Errorf("parse reclaim manifest %q: %w", metaPath, err)
	}
	return intmodel.VolumeSpec{
		ReclaimPolicy: intmodel.ReclaimPolicy(manifest.ReclaimPolicy),
		Connector:     manifest.Connector,
		Method:        manifest.Method,
		LoginMarker:   manifest.LoginMarker,
	}, nil
}

// VolumeLoginStatus reports the login state of a Volume from the tag the
// carrier states, whether or not that tag is stored: a caller probes a Volume
// that predates tagging with the marker its connector declares.
func (r *Exec) VolumeLoginStatus(volume intmodel.Volume) *intmodel.LoginStatus {
	return r.volumeLoginStatus(volume.Metadata, volume.Spec)
}

// volumeLoginStatus reports whether a login identity holds a login: whether
// the marker its spec names exists in the volume. It stats the marker and
// never opens it. A volume that is not a login identity, or whose connector
// declares no marker, has no login status.
func (r *Exec) volumeLoginStatus(md intmodel.VolumeMetadata, spec intmodel.VolumeSpec) *intmodel.LoginStatus {
	if spec.Connector == "" || spec.LoginMarker == "" || !filepath.IsLocal(spec.LoginMarker) ||
		strings.Contains(spec.LoginMarker, "..") || !volumeCoordsSafe(md) {
		return nil
	}
	root := filepath.Clean(fs.VolumePath(r.opts.RunPath, md.Realm, md.Space, md.Stack, md.Name))
	marker := filepath.Clean(filepath.Join(root, spec.LoginMarker))
	if !strings.HasPrefix(marker, root+string(filepath.Separator)) {
		return nil
	}
	// The volume's content is written from inside a container: resolve the
	// marker within the volume, so a symlink on any component of its path —
	// `.claude -> /` included — cannot point the stat outside it. Lstat
	// does not follow the marker itself.
	info, err := lstatIn(root, marker[len(root)+1:])
	if err != nil || info.IsDir() {
		return &intmodel.LoginStatus{State: v1beta1.LoginStateEmpty}
	}
	return &intmodel.LoginStatus{
		State: v1beta1.LoginStateLoggedIn,
		Since: info.ModTime().UTC().Format(time.RFC3339),
	}
}

// lstatIn stats name, a path relative to dir, without leaving dir.
func lstatIn(dir, name string) (os.FileInfo, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.Lstat(name)
}

// volumeCoordsSafe reports whether a volume's name and scope are single path
// segments, so its path stays in the volumes tree.
func volumeCoordsSafe(md intmodel.VolumeMetadata) bool {
	for _, seg := range []string{md.Name, md.Realm, md.Space, md.Stack} {
		if seg == "." || seg == ".." || strings.ContainsAny(seg, "/\\\x00") {
			return false
		}
	}
	return md.Name != "" && md.Realm != ""
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
