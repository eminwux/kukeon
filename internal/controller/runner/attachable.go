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

	"github.com/eminwux/kukeon/internal/ctr"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
)

// attachableTTYDirRootMode is the mode applied to the per-container tty
// directory while it is still root-owned (host-side and group-owned by the
// kukeon group). 02750 = setgid + rwx for owner + r-x for group + no world
// access, matching the rest of /opt/kukeon set up by `kuke init` so members
// of the kukeon group can traverse without world access. The setgid bit
// makes future siblings (sbsh socket, log, capture) inherit the kukeon
// group automatically.
const attachableTTYDirRootMode os.FileMode = os.ModeSetgid | 0o0750

// attachableTTYDirNoGroupMode is the legacy fallback when the kukeon group
// GID is not configured (e.g., older `kuke init` runs that never invoked
// sysuser.EnsureUserGroup, or `--no-daemon` smoke tests under a tmp
// runPath). In that mode there is no group to delegate access to, so the
// directory stays owner-only (0700).
const attachableTTYDirNoGroupMode os.FileMode = 0o0700

// attachableTTYDirInitialPerms returns the (mode, gid) tuple to apply to a
// freshly-prepared per-container tty directory before the workload
// container starts. When kukeonGroupGID is non-zero, the directory is
// group-traversable for members of the kukeon group (matches `kuke init`'s
// /opt/kukeon layout); when it is zero, the directory stays root-only.
//
// The owner is set to root in both cases — the post-create step resets it
// to the resolved container uid once containerd has parsed the image's
// USER directive (#258 repro B).
func attachableTTYDirInitialPerms(kukeonGroupGID int) (os.FileMode, int) {
	if kukeonGroupGID > 0 {
		return attachableTTYDirRootMode, kukeonGroupGID
	}
	return attachableTTYDirNoGroupMode, 0
}

// attachableBuildOpts returns the ctr.BuildOption slice to pass to
// CreateContainerFromSpec for a given container spec. When Attachable=false
// the slice is empty and the call is a no-op. When Attachable=true the
// runner pre-creates the per-container tty/ directory (sbsh's bind-mount
// source — sbsh creates the socket and its capture/log siblings there) and
// resolves the sbsh binary path keyed off the *image* arch, not the host
// arch — a cross-arch image running under emulation would otherwise pick a
// binary the in-container ELF interpreter cannot run.
//
// The directory is created with mode 02750 owned by root:kukeon group when
// the kukeon group GID is configured, so non-root operators in the kukeon
// group can dial the host-side socket via the same group-traversal path
// `kuke init` sets up on /opt/kukeon. The owner is corrected to the
// container's resolved uid by attachablePostCreateChown after
// CreateContainerFromSpec runs.
func (r *Exec) attachableBuildOpts(spec intmodel.ContainerSpec) ([]ctr.BuildOption, error) {
	if !spec.Attachable {
		return nil, nil
	}

	ttyDir := fs.ContainerTTYDir(
		r.opts.RunPath,
		spec.RealmName, spec.SpaceName, spec.StackName, spec.CellName,
		spec.ID,
	)
	if err := os.MkdirAll(ttyDir, 0o0700); err != nil {
		return nil, err
	}
	// MkdirAll leaves any pre-existing directory's mode intact and a fresh
	// dir's mode is filtered by umask, so apply the desired mode explicitly.
	mode, gid := attachableTTYDirInitialPerms(r.opts.KukeonGroupGID)
	if err := os.Chmod(ttyDir, mode); err != nil {
		return nil, fmt.Errorf("chmod %q to %v: %w", ttyDir, mode, err)
	}
	if gid > 0 {
		if err := os.Chown(ttyDir, 0, gid); err != nil {
			return nil, fmt.Errorf("chown %q to root:%d: %w", ttyDir, gid, err)
		}
	}

	binaryPath, err := r.ctrClient.ResolveSbshCachePath(spec.Image, r.opts.RunPath)
	if err != nil {
		return nil, fmt.Errorf("resolve sbsh cache path for %q: %w", spec.Image, err)
	}

	useProfile := !spec.Tty.IsEmpty()
	if useProfile {
		if err = writeSbshProfile(ttyDir, spec); err != nil {
			return nil, err
		}
	}

	return []ctr.BuildOption{
		ctr.WithAttachableInjection(ctr.AttachableInjection{
			SbshBinaryPath: binaryPath,
			HostTTYDir:     ttyDir,
			UseProfile:     useProfile,
		}),
	}, nil
}

// attachablePostCreateChown resets the owner of the per-container tty
// directory to the container's resolved process uid, so a non-root image
// USER (or an explicit container.user override) can create the sbsh
// socket and its capture/log siblings inside the bind-mounted dir.
//
// A no-op for non-Attachable containers. Called after
// CreateContainerFromSpec because containerd resolves USER (including
// usernames like "claude" via the rootfs's /etc/passwd) only when the
// runtime spec is built — which is during NewContainer, not before.
//
// Group ownership and mode are preserved (the dir was already chmod'd to
// 02750 root:kukeon by attachableBuildOpts), so kukeon-group members on
// the host keep traverse access to the socket.
func (r *Exec) attachablePostCreateChown(spec intmodel.ContainerSpec) error {
	if !spec.Attachable {
		return nil
	}

	containerdID := spec.ContainerdID
	if containerdID == "" {
		containerdID = spec.ID
	}
	container, err := r.ctrClient.GetContainer(containerdID)
	if err != nil {
		return fmt.Errorf("get container %q: %w", containerdID, err)
	}

	uid, err := r.ctrClient.ContainerProcessUID(container)
	if err != nil {
		return fmt.Errorf("resolve process uid for %q: %w", containerdID, err)
	}

	ttyDir := fs.ContainerTTYDir(
		r.opts.RunPath,
		spec.RealmName, spec.SpaceName, spec.StackName, spec.CellName,
		spec.ID,
	)
	// Group: kukeon group when configured, root otherwise (-1 would also
	// work but plumbing 0 is consistent with attachableBuildOpts).
	gid := r.opts.KukeonGroupGID
	if chownErr := os.Chown(ttyDir, int(uid), gid); chownErr != nil {
		return fmt.Errorf("chown %q to (uid=%d, gid=%d): %w", ttyDir, uid, gid, chownErr)
	}
	return nil
}
