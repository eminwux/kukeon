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

	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
)

// volumesDirMode is the mode of the per-scope volumes/ container directory.
// Root-owned and world-traversable (0o755), mirroring the blueprints/ dir: it
// holds the per-volume directories but no data of its own, so a non-root party
// only needs to traverse it to reach a volume it has been granted access to.
const volumesDirMode os.FileMode = 0o755

// volumeDirRootMode is the mode of an individual volume directory when the
// kukeon group GID is configured: setgid + rwx for owner (root) and group
// (kukeon), no world access. The setgid bit makes files a container creates
// inside inherit the kukeon group, mirroring attachableTTYDirRootMode's
// model — the directory is owned root:kukeon so the daemon and kuke-group
// operators can manage it without exposing volume contents world-wide. The
// mounting container's own uid is wired in as the directory owner at container
// create by volumePostCreateChown, the same two-phase shape as
// attachablePostCreateChown (step 4 follow-up, #1291).
const volumeDirRootMode os.FileMode = os.ModeSetgid | 0o0770

// volumeDirFallbackMode is the mode used when no kukeon group GID is
// configured: rwx for owner (root) and group, no world access and no setgid
// (there is no group to inherit). Mirrors attachableTTYDirInitialPerms's
// gid==0 fallback.
const volumeDirFallbackMode os.FileMode = 0o0770

// volumeDirInitialPerms returns the (mode, gid) tuple to apply to a freshly
// provisioned volume directory. When kukeonGroupGID is non-zero the directory
// is root:kukeon with the setgid mode; otherwise it stays root:root without
// setgid. Mirrors attachableTTYDirInitialPerms (issue #1018, #1016).
func volumeDirInitialPerms(kukeonGroupGID int) (os.FileMode, int) {
	if kukeonGroupGID > 0 {
		return volumeDirRootMode, kukeonGroupGID
	}
	return volumeDirFallbackMode, 0
}

// WriteVolume provisions a Volume's directory at
// <RunPath>/data/<scope>/volumes/<name>, root-owned and container-writable
// (issue #1018). The per-scope volumes/ container dir is created world-
// traversable; the volume dir itself is created setgid root:kukeon (group-
// writable) when the kukeon GID is configured, root:root 0o770 otherwise.
// MkdirAll is idempotent, so re-applying an existing volume re-asserts the
// mode/owner and reports created=false. The caller (ReconcileVolume) is
// responsible for having verified the scope exists.
//
// Cross-uid sharing contract. At container create the mounting container's
// resolved process uid is granted *owner* write on the resolved directory by
// volumePostCreateChown, so a non-root workload can write into the Volume even
// without kukeon-group membership. Only the owner is reset — the setgid
// root:kukeon group is preserved — so two cells that mount the same Volume as
// different non-root uids each re-own it at their own create and the last
// writer wins the owner bit. Cross-uid *shared* write therefore relies on the
// kukeon group (setgid group-write), not the owner chown. Two consequences of
// the WriteVolume re-apply make this durable rather than racy: when the kukeon
// group is configured a reconcile re-apply chowns the owner back to root, but
// group-write persists; in the no-group fallback WriteVolume issues no chown at
// all, so the mount-time owner grant survives reconcile. Per-cell volume
// identity (step 5, #1294 — ${CELL_NAME} claims) gives each cell its own Volume
// directory and is the supported pattern when distinct uids each need exclusive
// owner-write. Step 4 follow-up (#1291).
func (r *Exec) WriteVolume(volume intmodel.Volume) (bool, error) {
	md := volume.Metadata
	dir := fs.VolumesDir(r.opts.RunPath, md.Realm, md.Space, md.Stack)
	path := fs.VolumePath(r.opts.RunPath, md.Realm, md.Space, md.Stack, md.Name)

	if err := os.MkdirAll(dir, volumesDirMode); err != nil {
		return false, fmt.Errorf("%w: create volumes dir: %w", errdefs.ErrWriteVolume, err)
	}
	// MkdirAll honors only the rwx bits and leaves a pre-existing directory's
	// mode intact; chmod unconditionally so the world-traversable contract
	// holds even when a parent created the dir with tighter bits or the umask
	// stripped them.
	if err := os.Chmod(dir, volumesDirMode); err != nil {
		return false, fmt.Errorf("%w: chmod volumes dir: %w", errdefs.ErrWriteVolume, err)
	}

	info, statErr := os.Stat(path)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return false, fmt.Errorf("%w: stat volume: %w", errdefs.ErrWriteVolume, statErr)
	}
	if statErr == nil && !info.IsDir() {
		// A non-directory squatting on the volume path is a corrupt state, not
		// a volume — refuse rather than chmod a stray file into place.
		return false, fmt.Errorf("%w: %q exists and is not a directory", errdefs.ErrWriteVolume, path)
	}

	mode, gid := volumeDirInitialPerms(r.opts.KukeonGroupGID)
	if err := os.MkdirAll(path, mode); err != nil {
		return false, fmt.Errorf("%w: create volume dir: %w", errdefs.ErrWriteVolume, err)
	}
	// MkdirAll masks the setgid bit and honors the umask, so chmod the full
	// mode (setgid included) unconditionally to assert the contract.
	if err := os.Chmod(path, mode); err != nil {
		return false, fmt.Errorf("%w: chmod volume dir: %w", errdefs.ErrWriteVolume, err)
	}
	if gid > 0 {
		if err := os.Chown(path, 0, gid); err != nil {
			return false, fmt.Errorf("%w: chown volume dir to root:%d: %w", errdefs.ErrWriteVolume, gid, err)
		}
	}

	// Reconcile the reclaim manifest after the volume dir is in place: a Retain
	// policy writes the root-only marker cascade purge consults; any other value
	// drops a stale marker so re-applying with the policy flipped to Delete loses
	// protection (step 3, #1237).
	if err := r.persistVolumeReclaimPolicy(volume); err != nil {
		return false, err
	}

	action := "updated"
	if created {
		action = "created"
	}
	r.logger.InfoContext(r.ctx, "volume "+action,
		"name", md.Name,
		"realm", md.Realm,
		"space", md.Space,
		"stack", md.Stack,
		"reclaimPolicy", string(volume.Spec.ReclaimPolicy),
	)
	return created, nil
}

// volumePostCreateChown grants the mounting container's resolved process uid
// owner write on every writable kind: volume directory the container mounts,
// mirroring attachablePostCreateChown's two-phase shape: containerd resolves
// the image USER (and any container.user override) only when the runtime spec
// is built during container create, so the uid is unknowable before
// CreateContainerFromSpec. Without this a non-root workload that carries
// neither uid 0 nor the kukeon group GID cannot write into a Volume it mounts
// on a host where no kukeon group is configured (the fallback dir is root:root
// 0o770).
//
// A no-op when the container declares no kind: volume mount (the common case
// pays no containerd round-trip) and when the resolved uid is 0 (a root
// container already owns the root-owned dir). Read-only mounts are skipped —
// the container cannot write through them, so re-owning the directory away from
// another mounter would be pointless. The mount references are re-resolved from
// the stored spec via the same ResolveVolumeMount walk the create path uses;
// CreateContainerFromSpec rewrites only its own by-value copy of the spec, so
// the runner's spec still carries the kind: volume reference here.
//
// Multi-mounter contract: only the *owner* is reset, so cross-uid shared write
// relies on the kukeon group (setgid group-write), not this chown — see the
// WriteVolume godoc for the full cross-uid sharing contract. Step 4 follow-up
// (#1291).
func (r *Exec) volumePostCreateChown(namespace string, spec intmodel.ContainerSpec) error {
	scope := ctr.VolumeScope{
		Realm: spec.RealmName,
		Space: spec.SpaceName,
		Stack: spec.StackName,
	}
	var targets []string
	for i := range spec.Volumes {
		m := spec.Volumes[i]
		if m.Kind != intmodel.VolumeKindVolume || m.ReadOnly {
			continue
		}
		resolved, err := ctr.ResolveVolumeMount(r.opts.RunPath, scope, m)
		if err != nil {
			// The mount already resolved when the container was created, so a
			// miss here is unexpected — surface it rather than silently leaving
			// a directory the workload expects to be writable owned by root.
			return fmt.Errorf("resolve volume mount for target %q: %w", m.Target, err)
		}
		targets = append(targets, resolved.HostPath)
	}
	if len(targets) == 0 {
		return nil
	}

	containerdID := spec.ContainerdID
	if containerdID == "" {
		containerdID = spec.ID
	}
	container, err := r.ctrClient.GetContainer(namespace, containerdID)
	if err != nil {
		return fmt.Errorf("get container %q: %w", containerdID, err)
	}
	uid, err := r.ctrClient.ContainerProcessUID(namespace, container)
	if err != nil {
		return fmt.Errorf("resolve process uid for %q: %w", containerdID, err)
	}
	if uid == 0 {
		// Root container: it already owns the root-owned volume dir, and a
		// reconcile re-apply would chown it back to root anyway. Nothing to grant.
		return nil
	}

	for _, path := range targets {
		if chownErr := r.chownVolumeDirToUID(path, int(uid)); chownErr != nil {
			return chownErr
		}
	}
	return nil
}

// chownVolumeDirToUID resets a provisioned Volume directory's owner to uid while
// preserving the group and the setgid + 0o770 contract WriteVolume established.
// The gid is left untouched (Chown -1) so the kukeon group survives the owner
// flip and the kukeon-group-write path is never regressed; the mode is then
// re-asserted from volumeDirInitialPerms so the setgid bit is explicitly
// guaranteed (root's chown preserves it via CAP_FSETID, but the re-chmod makes
// the group-inheritance guarantee independent of that, matching WriteVolume's
// "assert the contract unconditionally" stance).
func (r *Exec) chownVolumeDirToUID(path string, uid int) error {
	if err := os.Chown(path, uid, -1); err != nil {
		return fmt.Errorf("%w: chown volume dir %q to uid %d: %w", errdefs.ErrWriteVolume, path, uid, err)
	}
	mode, _ := volumeDirInitialPerms(r.opts.KukeonGroupGID)
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("%w: chmod volume dir %q: %w", errdefs.ErrWriteVolume, path, err)
	}
	return nil
}

// GetVolume reports whether a single named, scoped Volume exists on disk and
// returns its metadata-only view (issue #1018). Like GetSecret the scope and
// name come from the path, not from any stored document — a Volume carries no
// body. Returns errdefs.ErrVolumeNotFound when the directory is absent, or when
// a non-directory occupies the path (which is not a volume).
func (r *Exec) GetVolume(volume intmodel.Volume) (intmodel.Volume, error) {
	md := volume.Metadata
	path := fs.VolumePath(r.opts.RunPath, md.Realm, md.Space, md.Stack, md.Name)

	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return intmodel.Volume{}, errdefs.ErrVolumeNotFound
		}
		return intmodel.Volume{}, fmt.Errorf("%w: %w", errdefs.ErrGetVolume, err)
	}
	if !info.IsDir() {
		return intmodel.Volume{}, errdefs.ErrVolumeNotFound
	}

	policy, err := r.readVolumeReclaimPolicy(md)
	if err != nil {
		return intmodel.Volume{}, fmt.Errorf("%w: %w", errdefs.ErrGetVolume, err)
	}

	return intmodel.Volume{
		Metadata: md,
		Spec:     intmodel.VolumeSpec{ReclaimPolicy: policy},
	}, nil
}

// ListVolumes enumerates the metadata of every Volume bound to the scope
// identified by the filter coordinates, plus every Volume bound to a deeper
// scope nested within it (issue #1018). The filter is a prefix: an empty
// realmName lists across all realms; a set realmName with an empty spaceName
// lists realm-scoped volumes and everything under that realm; and so on. This
// mirrors ListBlueprints's subtree-filter semantics, bounded at stack depth — a
// Volume is never cell-scoped. Each entry under a volumes/ dir is itself a
// directory (the volume), so the walk collects directories and the metadata is
// the scope coordinates (from the path) plus the directory name.
func (r *Exec) ListVolumes(realmName, spaceName, stackName string) ([]intmodel.Volume, error) {
	realmDirs, err := r.resolveRealmDirs(realmName)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrListVolumes, err)
	}

	var out []intmodel.Volume
	for _, realmDir := range realmDirs {
		realm := filepath.Base(realmDir)
		if walkErr := r.collectVolumeSubtree(&out, realm, spaceName, stackName); walkErr != nil {
			return nil, fmt.Errorf("%w: %w", errdefs.ErrListVolumes, walkErr)
		}
	}
	return out, nil
}

// collectVolumeSubtree appends the metadata of every Volume bound to scope
// (realm, space, stack) — where a trailing coordinate that is "" marks the
// filter floor — and every Volume in scopes nested within it. The rule mirrors
// collectBlueprintSubtree: collect a level's own volumes only when the
// next-deeper filter coordinate is empty, and descend into a child only when it
// matches a set filter coordinate or the filter is empty at that level. The
// walk is bounded at stack depth, so cell directories are never descended.
func (r *Exec) collectVolumeSubtree(out *[]intmodel.Volume, realm, space, stack string) error {
	if space == "" {
		if err := r.collectVolumesInScope(out, realm, "", ""); err != nil {
			return err
		}
	}

	spaces, err := r.childScopeNames(fs.RealmMetadataDir(r.opts.RunPath, realm), space)
	if err != nil {
		return err
	}
	for _, sp := range spaces {
		if stack == "" {
			if err = r.collectVolumesInScope(out, realm, sp, ""); err != nil {
				return err
			}
		}

		stacks, stErr := r.childScopeNames(fs.SpaceMetadataDir(r.opts.RunPath, realm, sp), stack)
		if stErr != nil {
			return stErr
		}
		for _, st := range stacks {
			if err = r.collectVolumesInScope(out, realm, sp, st); err != nil {
				return err
			}
		}
	}
	return nil
}

// collectVolumesInScope appends the metadata of every Volume stored directly at
// the given scope (realm, space, stack). Unlike the blueprint/secret collectors
// — which skip directories because each resource is a file — a volume *is* a
// directory, so non-directory entries (none are written by WriteVolume) are
// skipped instead.
func (r *Exec) collectVolumesInScope(out *[]intmodel.Volume, realm, space, stack string) error {
	dir := fs.VolumesDir(r.opts.RunPath, realm, space, stack)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read volumes dir %q: %w", dir, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		vmd := intmodel.VolumeMetadata{
			Name:  entry.Name(),
			Realm: realm,
			Space: space,
			Stack: stack,
		}
		policy, err := r.readVolumeReclaimPolicy(vmd)
		if err != nil {
			return err
		}
		*out = append(*out, intmodel.Volume{
			Metadata: vmd,
			Spec:     intmodel.VolumeSpec{ReclaimPolicy: policy},
		})
	}
	return nil
}

// DeleteVolume removes the daemon-provisioned directory for a single named,
// scoped Volume (issue #1018). Returns errdefs.ErrVolumeNotFound when the
// directory is absent so the caller can report a clear "not found" instead of a
// silent success. Uses RemoveAll (the volume is a directory that may hold
// container-written contents), gated on a prior stat so a missing volume still
// surfaces NotFound rather than RemoveAll's silent nil.
func (r *Exec) DeleteVolume(volume intmodel.Volume) error {
	md := volume.Metadata
	path := fs.VolumePath(r.opts.RunPath, md.Realm, md.Space, md.Stack, md.Name)

	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errdefs.ErrVolumeNotFound
		}
		return fmt.Errorf("%w: stat volume: %w", errdefs.ErrDeleteVolume, err)
	}
	if !info.IsDir() {
		return errdefs.ErrVolumeNotFound
	}

	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrDeleteVolume, err)
	}
	// Drop the reclaim manifest too so deleting a retained volume leaves no
	// orphan marker behind (step 3, #1237).
	if err := r.removeVolumeReclaimManifest(md); err != nil {
		return fmt.Errorf("%w: remove reclaim manifest: %w", errdefs.ErrDeleteVolume, err)
	}

	r.logger.InfoContext(r.ctx, "volume deleted",
		"name", md.Name,
		"realm", md.Realm,
		"space", md.Space,
		"stack", md.Stack,
	)
	return nil
}

// VolumeMountedByLiveCell scans every cell across all realms for a running cell
// that mounts the given Volume through a kind: volume mount, the live-reference
// gate `kuke delete volume` consults (issue #1016). A cell counts as a live
// mounter when its last-reconciled state is Ready and one of its container
// specs declares a kind: volume mount that resolves — via the same scope walk
// the create path uses — to this Volume's exact scope + name. The first match
// short-circuits and returns the cell's scoped reference (realm/space/stack/
// cell); a mount that fails to resolve is skipped rather than failing the gate
// (it cannot be referencing the still-present target). Returns ("", false, nil)
// when no running cell mounts the Volume.
func (r *Exec) VolumeMountedByLiveCell(volume intmodel.Volume) (string, bool, error) {
	cells, err := r.ListCells("", "", "")
	if err != nil {
		return "", false, fmt.Errorf("%w: enumerate cells: %w", errdefs.ErrDeleteVolume, err)
	}
	target := volume.Metadata
	for i := range cells {
		cell := cells[i]
		if cell.Status.State != intmodel.CellStateReady {
			continue
		}
		scope := ctr.VolumeScope{
			Realm: cell.Spec.RealmName,
			Space: cell.Spec.SpaceName,
			Stack: cell.Spec.StackName,
		}
		for ci := range cell.Spec.Containers {
			for _, m := range cell.Spec.Containers[ci].Volumes {
				if m.Kind != intmodel.VolumeKindVolume {
					continue
				}
				resolved, rerr := ctr.ResolveVolumeMount(r.opts.RunPath, scope, m)
				if rerr != nil {
					continue
				}
				if resolved.Realm == target.Realm &&
					resolved.Space == target.Space &&
					resolved.Stack == target.Stack &&
					resolved.Name == target.Name {
					return cellScopedRef(scope, cell.Metadata.Name), true, nil
				}
			}
		}
	}
	return "", false, nil
}

// cellScopedRef renders a cell's realm/space/stack/name as a slash-joined
// reference for the in-use error message, skipping empty scope coordinates.
func cellScopedRef(scope ctr.VolumeScope, cellName string) string {
	parts := make([]string, 0, 4)
	for _, p := range []string{scope.Realm, scope.Space, scope.Stack, cellName} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "/")
}
