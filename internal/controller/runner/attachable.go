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
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
	extmodel "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// attachableTTYDirRootMode is the mode applied to the per-container tty
// directory while it is still root-owned (host-side and group-owned by the
// kukeon group). 02750 = setgid + rwx for owner + r-x for group + no world
// access, matching the rest of /opt/kukeon set up by `kuke init` so members
// of the kukeon group can traverse without world access. The setgid bit
// makes future siblings (kuketty socket, log, capture) inherit the kukeon
// group automatically.
const attachableTTYDirRootMode os.FileMode = os.ModeSetgid | 0o0750

// attachableTTYDirNoGroupMode is the legacy fallback when the kukeon group
// GID is not configured (e.g., older `kuke init` runs that never invoked
// sysuser.EnsureUserGroup, or `--no-daemon` smoke tests under a tmp
// runPath). In that mode there is no group to delegate access to, so the
// directory stays owner-only (0700).
const attachableTTYDirNoGroupMode os.FileMode = 0o0700

// kukettyBinaryStagedSubdir is the subdirectory under the daemon's RunPath
// where the kuketty binary is staged for the OCI bind mount. The path under
// /opt/kukeon survives daemon restarts (so concurrent provisions reuse the
// staged copy) and lives on the same filesystem the workload's bind-mount
// source must, since /opt/kukeon is the daemon ↔ host shared bind.
const kukettyBinaryStagedSubdir = "bin"

// kukettySourcePathInsideDaemon is where the kukeon container image places
// the kuketty binary (see Dockerfile). The daemon stages from here on first
// attachable provision; for --no-daemon mode this path won't exist on the
// host, so resolveKukettyBinary falls back to the kuke binary's sibling and
// $PATH (used in dev / e2e setups that wire the binary in manually).
const kukettySourcePathInsideDaemon = "/bin/kuketty"

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
// runner:
//
//   - pre-creates the per-container tty/ directory (kuketty's bind-mount
//     source — kuketty creates the socket and its future capture/log
//     siblings there);
//   - stages the kuketty binary from kukeond's own /bin/kuketty (where the
//     kukeond image places it) to a stable host path under <RunPath>/bin/
//     so runc has a host-visible source for the kuketty bind mount;
//   - registers a metadata renderer that fires from inside the OCI
//     args-wrap closure once containerd has resolved the workload argv,
//     producing the per-container api/model/v1beta1.ContainerDoc at
//     <containerDir>/kuketty-metadata.json that the bind mount maps onto
//     kuketty's fixed in-container metadata path.
//
// The tty directory is created with mode 02750 owned by root:kukeon group
// when the kukeon group GID is configured, so non-root operators in the
// kukeon group can dial the host-side socket via the same group-traversal
// path `kuke init` sets up on /opt/kukeon. The owner is corrected to the
// container's resolved uid by attachablePostCreateChown after
// CreateContainerFromSpec runs.
//
// priorStages carries the controller-side ContainerStatus.Stages snapshot
// for this container (sourced from cell.Status.Containers at each call
// site). The renderer threads it into writeKukettyDoc so the phase-C2 (#737)
// render-time gate can omit already-done runOn: create stages from the next
// boot's ContainerDoc. Pass nil on a fresh-create boot — every create stage
// renders normally and runs on the next boot.
func (r *Exec) attachableBuildOpts(_ string, spec intmodel.ContainerSpec, _ []ctr.RegistryCredentials, priorStages []intmodel.StageStatus) ([]ctr.BuildOption, error) {
	if !spec.Attachable {
		return nil, nil
	}

	if err := ensureAttachableSocketSymlink(r.opts.RunPath, spec); err != nil {
		return nil, err
	}

	ttyDir := fs.ContainerTTYDir(
		r.opts.RunPath,
		spec.RealmName, spec.SpaceName, spec.StackName, spec.CellName,
		spec.ID,
	)
	mode, gid := attachableTTYDirInitialPerms(r.opts.KukeonGroupGID)
	if err := os.MkdirAll(ttyDir, mode); err != nil {
		return nil, err
	}
	// MkdirAll leaves any pre-existing directory's mode intact and a fresh
	// dir's mode is filtered by umask, so apply the desired mode explicitly
	// to both the leaf tty/ dir and its per-container parent. The parent
	// holds the per-container kuketty metadata file and is the dir host-side
	// kuke attach has to traverse to reach the socket inside tty/; a
	// pre-existing 0o2700 from a daemon predating this fix would otherwise
	// leave it unreachable to kukeon-group operators.
	containerDir := filepath.Dir(ttyDir)
	if err := os.Chmod(containerDir, mode); err != nil {
		return nil, fmt.Errorf("chmod %q to %v: %w", containerDir, mode, err)
	}
	if err := os.Chmod(ttyDir, mode); err != nil {
		return nil, fmt.Errorf("chmod %q to %v: %w", ttyDir, mode, err)
	}
	if gid > 0 {
		if err := os.Chown(containerDir, 0, gid); err != nil {
			return nil, fmt.Errorf("chown %q to root:%d: %w", containerDir, gid, err)
		}
		if err := os.Chown(ttyDir, 0, gid); err != nil {
			return nil, fmt.Errorf("chown %q to root:%d: %w", ttyDir, gid, err)
		}
	}

	binaryPath, err := stageKukettyBinary(r.opts.RunPath)
	if err != nil {
		return nil, err
	}

	metadataPath := filepath.Join(containerDir, ctr.AttachableMetadataFile)
	kukeonGID := r.opts.KukeonGroupGID
	renderer := func(workloadArgv []string) error {
		return r.writeKukettyDoc(metadataPath, spec, kukeonGID, workloadArgv, priorStages)
	}

	return []ctr.BuildOption{
		ctr.WithAttachableInjection(ctr.AttachableInjection{
			KukettyBinaryPath: binaryPath,
			HostTTYDir:        ttyDir,
			HostMetadataPath:  metadataPath,
			RenderMetadata:    renderer,
		}),
	}, nil
}

// writeKukettyDoc renders the per-container ContainerDoc and writes it
// atomically (tmp + rename) so a partially-written file is never visible to a
// racing kuketty in the container. 0o600 because the file lives inside the
// per-container parent directory, which is daemon-private (0o2750 or 0o700) —
// keeping the inner file similarly tight guards against a future loosening of
// the parent dir.
//
// The mounted artifact carries kukeon's own ContainerSpec (issue #641), not a
// pre-rendered sbsh TerminalDoc: kuketty reads ContainerDoc.Spec, runs sbsh's
// builder, and serves. Three inputs the transform folds in are not knowable
// inside the container, so the daemon resolves and stamps them here:
//
//   - the resolved workload argv (image ENTRYPOINT+CMD merged with any user
//     override, captured by the OCI args-wrap closure) into Spec.Command /
//     Spec.Args. An empty argv (image with no ENTRYPOINT/CMD and no override)
//     leaves the converted Command/Args as-is so kuketty falls through to
//     sbsh's inline-builder default (/bin/bash -i), matching the legacy
//     WithCommand gate;
//   - the kukeon-group GID into Spec.KukeonGroupGID, so kuketty applies the
//     kukeon-group socket/capture/log ownership the daemon used to fold into
//     the rendered TerminalSpec;
//   - the resolved kuketty log level (per-container → server-config → "info")
//     into Spec.Tty.LogLevel, allocating the Tty block when the cell omitted
//     it so kuketty reads a non-empty level verbatim (sbsh's NewFileLogger
//     rejects an empty level) with no daemon-side defaulting of its own.
//
// Everything else the transform needs — the fixed in-container socket /
// capture / log paths and their modes — are kukeon contract constants kuketty
// knows directly, so they are not carried in the doc.
//
// priorStages is the controller-side ContainerStatus.Stages snapshot for this
// container (after mergeStageStatuses has dropped stale-hash and non-done
// entries). The render-time gate (applyStageRenderGate) consults its
// State == "done" entries to clear the Script of any matching runOn: create
// stage in the rendered OnInit, so kuketty's runStage no-ops the gated
// execution — phase C2 (#737) of the run-once-per-cell-instance setup. The
// rendered ContainerDoc is the render-time evidence the gate fired (gated
// stages carry an empty Script in Spec.Tty.OnInit). A nil priorStages is a
// fresh-create boot — every create stage renders normally and runs.
func (r *Exec) writeKukettyDoc(
	path string,
	spec intmodel.ContainerSpec,
	kukeonGroupGID int,
	workloadArgv []string,
	priorStages []intmodel.StageStatus,
) error {
	extSpec := apischeme.BuildContainerSpecExternalFromInternal(spec)

	// The terminal command derives solely from the resolved Process.Args the
	// args-wrap captured — overwrite the user-authored Command/Args the
	// conversion carried so the doc's Command is the authoritative resolved
	// argv. An empty argv (image with no ENTRYPOINT/CMD and no override)
	// leaves Command/Args empty so kuketty falls through to sbsh's
	// inline-builder default (/bin/bash -i), matching the legacy gate.
	extSpec.Command = ""
	extSpec.Args = nil
	if len(workloadArgv) > 0 {
		extSpec.Command = workloadArgv[0]
		extSpec.Args = append([]string(nil), workloadArgv[1:]...)
	}

	extSpec.KukeonGroupGID = kukeonGroupGID

	// Resolve the log level daemon-side (the server-config tier is not visible
	// inside the container) and stamp it onto Tty.LogLevel so kuketty reads it
	// verbatim. Allocate the Tty block when the cell omitted it entirely —
	// buildContainerTtyExternalFromInternal returns nil for an empty Tty.
	resolvedLevel := resolveTtyLogLevel(spec.Tty, r.opts.KukettyLogLevel)
	if extSpec.Tty == nil {
		extSpec.Tty = &extmodel.ContainerTty{}
	}
	extSpec.Tty.LogLevel = resolvedLevel

	// Phase C2 (#737) render-time gate: clear Script on any runOn: create
	// stage whose content hash matches a prior State == "done" record so
	// kuketty's pre-Serve executor no-ops the execution on this boot. The
	// TtyStage entry stays at its position in the rendered OnInit so
	// kuketty's createStages emits Stage.Index aligned with the live
	// spec.Tty.OnInit position the daemon-side mergeStageStatuses anchors
	// on. See applyStageRenderGate for the contract.
	applyStageRenderGate(extSpec.Tty.OnInit, priorStages)

	doc := extmodel.ContainerDoc{
		APIVersion: extmodel.APIVersionV1Beta1,
		Kind:       extmodel.KindContainer,
		Metadata: extmodel.ContainerMetadata{
			Name:   spec.ID,
			Labels: kukettyMetadataLabels(spec),
		},
		Spec: extSpec,
		// Status is left zero: the daemon stamps spec only. kuketty (and the
		// later crew-absorption phases of #423) own the status channel back
		// into ContainerStatus. AC #1.
	}

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal kuketty doc: %w", err)
	}
	tmp := path + ".tmp"
	if err = os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write kuketty doc %q: %w", tmp, err)
	}
	if err = os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename kuketty doc %q -> %q: %w", tmp, path, err)
	}
	return nil
}

// resolveTtyLogLevel returns the operator-supplied LogLevel from the cell
// schema, falling through to the daemon-wide kuketty.logLevel set on
// ServerConfigurationSpec (plumbed via runner.Options.KukettyLogLevel) and
// finally to a hardcoded "info" when both are empty. The empty-fallback
// chain matters because sbsh's pkg/logging.NewFileLogger rejects an empty
// level at file-open time — pinning the default daemon-side keeps the
// wire format ("kuketty always reads Spec.LogLevel verbatim") clean and
// lets test fixtures that build the runner directly with zero-value
// Options behave the same as production. Validated against the four-value
// enum by apischeme.validateContainerTty. Issue #599.
func resolveTtyLogLevel(t *intmodel.ContainerTty, serverConfigLevel string) string {
	if t != nil && t.LogLevel != "" {
		return t.LogLevel
	}
	if serverConfigLevel != "" {
		return serverConfigLevel
	}
	return "info"
}

// kukettyMetadataLabels stamps the cell-context identity on the rendered
// ContainerDoc.Metadata so an operator inspecting the host-side file can
// trace it back to the realm/space/stack/cell/container it belongs to
// without having to walk the bind-mount source. Empty fields are dropped
// rather than producing empty string values, so the labels map mirrors
// what `kuke get` would show.
func kukettyMetadataLabels(spec intmodel.ContainerSpec) map[string]string {
	pairs := []struct{ key, value string }{
		{"kukeon.io/realm", spec.RealmName},
		{"kukeon.io/space", spec.SpaceName},
		{"kukeon.io/stack", spec.StackName},
		{"kukeon.io/cell", spec.CellName},
		{"kukeon.io/container-id", spec.ID},
	}
	out := map[string]string{}
	for _, p := range pairs {
		if p.value == "" {
			continue
		}
		out[p.key] = p.value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// stageKukettyBinary ensures a host-visible copy of the kuketty binary
// exists at <RunPath>/bin/kuketty and returns that path. The source is the
// daemon's own /bin/kuketty (kukeond image ships kuketty alongside the
// daemon binary — see Dockerfile); for --no-daemon mode the function falls
// back to a sibling of the running binary and then $PATH.
//
// The stage is idempotent: once the destination exists with non-zero size
// and the executable bit, subsequent calls return its path without
// re-copying. This is the hot path for every attachable container start,
// so doing the lstat early avoids re-reading and re-writing a multi-MiB
// binary per container.
//
// Atomicity is handled with a tmp-file rename so two concurrent provisions
// never see a partial binary at the destination. The rename is on the same
// filesystem as the destination so it is a single-syscall atomic move.
func stageKukettyBinary(runPath string) (string, error) {
	dstDir := filepath.Join(runPath, kukettyBinaryStagedSubdir)
	dst := filepath.Join(dstDir, "kuketty")

	if ok, err := stagedBinaryUsable(dst); err != nil {
		return "", err
	} else if ok {
		return dst, nil
	}

	src, err := resolveKukettyBinary()
	if err != nil {
		return "", err
	}

	if err = os.MkdirAll(dstDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %q: %w", dstDir, err)
	}

	if err = copyBinaryAtomic(src, dst, "kuketty"); err != nil {
		return "", err
	}
	return dst, nil
}

// stagedBinaryUsable reports whether the destination already holds an
// executable file with non-zero size. Defensive: a zero-byte file from a
// crashed prior copy would otherwise be silently bind-mounted and the
// workload would get ENOEXEC at exec time, hard to attribute.
func stagedBinaryUsable(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("stat %q: %w", path, err)
	}
	if info.Size() == 0 {
		return false, nil
	}
	if info.Mode()&0o111 == 0 {
		return false, nil
	}
	return true, nil
}

// resolveKukettyBinary locates a kuketty binary on the host. Lookup order:
//
//  1. /bin/kuketty — where the kukeond image places it; the daemon path.
//  2. Sibling of the currently-running executable — so `make kuketty`
//     in the repo root makes the binary visible to a controller running
//     in-process from a dev `./kuke --no-daemon` invocation.
//  3. $PATH — last-resort fallback for whoever installed kuketty
//     out-of-band.
//
// Returns the first existing executable path. The error names every
// location tried so an operator hitting "kuketty not found" gets a
// debuggable trace, not "file not found".
func resolveKukettyBinary() (string, error) {
	tried := []string{kukettySourcePathInsideDaemon}
	if ok, _ := stagedBinaryUsable(kukettySourcePathInsideDaemon); ok {
		return kukettySourcePathInsideDaemon, nil
	}

	if self, err := os.Executable(); err == nil {
		sibling := filepath.Join(filepath.Dir(self), "kuketty")
		tried = append(tried, sibling)
		if ok, _ := stagedBinaryUsable(sibling); ok {
			return sibling, nil
		}
	}

	// $PATH lookup is intentionally last: the error names the explicit
	// paths first so an operator hitting "not found" sees the daemon path
	// and the sibling lookup before the PATH miss. LookPath errors are
	// swallowed — the only signal the caller cares about is "did any
	// location resolve", and the path string is appended either way so
	// the error trace shows PATH was consulted.
	if p, err := exec.LookPath("kuketty"); err == nil {
		return p, nil
	}
	tried = append(tried, "$PATH/kuketty")
	return "", fmt.Errorf("kuketty binary not found in: %v", tried)
}

// copyBinaryAtomic copies src to dst via a sibling tmp file, then renames
// the tmp file over dst. Mode is 0o755 — the binary must be exec'able by
// the workload's uid through the bind mount, which carries through unix
// owner-x bits but not setgid-style elevations. The label string is woven
// into every error so callers (kuketty, kukepause) get attributable
// diagnostics when a stage trip fails.
func copyBinaryAtomic(src, dst, label string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s source %q: %w", label, src, err)
	}
	defer func() { _ = in.Close() }()

	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("create %s staged tmp %q: %w", label, tmp, err)
	}
	if _, err = io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("copy %s %q -> %q: %w", label, src, tmp, err)
	}
	if err = out.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close %s staged tmp %q: %w", label, tmp, err)
	}
	if err = os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %s staged %q -> %q: %w", label, tmp, dst, err)
	}
	return nil
}

// attachablePostCreateChown resets the owner of the per-container tty
// directory to the container's resolved process uid, so a non-root image
// USER (or an explicit container.user override) can create the kuketty
// socket and its future capture/log siblings inside the bind-mounted dir.
//
// A no-op for non-Attachable containers. Called after
// CreateContainerFromSpec because containerd resolves USER (including
// usernames like "claude" via the rootfs's /etc/passwd) only when the
// runtime spec is built — which is during NewContainer, not before.
//
// Group ownership and mode are preserved (the dir was already chmod'd to
// 02750 root:kukeon by attachableBuildOpts), so kukeon-group members on
// the host keep traverse access to the socket.
//
// The per-container kuketty metadata file is also chown'd so the
// in-container kuketty process (running as the resolved container uid) can
// open it via the bind mount. The file was created at 0o600 owned by the
// daemon (root); without this chown kuketty's read of the metadata file
// fails with "permission denied" and the wrapper exits before claiming the
// socket inode, leaving `kuke attach` unable to dial.
func (r *Exec) attachablePostCreateChown(namespace string, spec intmodel.ContainerSpec) error {
	if !spec.Attachable {
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
	// Chown the per-container kuketty metadata file the runner pre-wrote
	// in the parent dir so the in-container kuketty wrapper (running as
	// the resolved container uid) can read it through the file bind mount.
	containerDir := filepath.Dir(ttyDir)
	metadataPath := filepath.Join(containerDir, ctr.AttachableMetadataFile)
	switch chownErr := os.Chown(metadataPath, int(uid), gid); {
	case chownErr == nil:
	case errors.Is(chownErr, os.ErrNotExist):
		// Defensive: a non-Attachable code path that somehow lands here
		// would skip writing the metadata file; the chown miss is then
		// expected. The Attachable check at the top of this function
		// already rules out the legitimate case, so this branch is a
		// safety net rather than a hot path.
	default:
		return fmt.Errorf("chown %q to (uid=%d, gid=%d): %w", metadataPath, uid, gid, chownErr)
	}
	// Restart-path complement to the ttyDir chown above (#850). The leaf
	// dir chown covers a fresh provision (MkdirAll just made the dir
	// empty), but on a restart the dir already holds stale files from the
	// prior run — kuketty.log (#599), sbsh's capture/metadata.json/.meta-
	// *.tmp anchored to attachableTTYDir (#672), and any pre-#672
	// terminals/<id>/ subtree — all root-owned because the daemon wrote
	// them. When the work image's resolved USER is non-root (e.g. claude:
	// latest → 1000), kuketty inside the container then hits EACCES at
	// openTerminalLogger trying to O_RDWR-reopen the stale root-owned
	// kuketty.log and exits with code 70 before claiming the socket
	// listener; `kuke attach` later dials the freshly-bound but
	// listenerless socket and gets "connection refused". Walking and
	// chowning every leaf child to the resolved container uid covers the
	// full set of kuketty/sbsh-written inodes without enumerating
	// basenames, so a future addition under tty/ inherits the same
	// restart-safety automatically.
	if err = chownAttachableStaleChildren(ttyDir, int(uid), gid); err != nil {
		return fmt.Errorf("chown stale children under %q: %w", ttyDir, err)
	}
	return nil
}

// chownAttachableStaleChildren resets the owner of every pre-existing
// file and directory under root to (uid, gid). It is the restart-path
// complement to the top-level ttyDir chown in attachablePostCreateChown:
// on a fresh provision the dir is empty (MkdirAll just produced it) and
// the walk is a no-op; on a restart it covers every kuketty/sbsh-written
// leaf inherited from the prior run (#850).
//
// The walk uses Lchown so a stray symlink (none today, defense-in-depth)
// is chowned at the link, never its target. Per-entry ENOENT is tolerated
// (a concurrent unlink during the walk is a benign race — the inode is
// already gone, the chown is moot). Subdirectories are walked too: any
// pre-#672 terminals/<id>/ subtree left over from an older daemon gets
// covered in the same pass.
//
// The root dir itself is intentionally skipped — the caller chowned it
// explicitly with its own error surface, and re-chowning is wasteful.
func chownAttachableStaleChildren(root string, uid, gid int) error {
	return filepath.Walk(root, func(path string, _ os.FileInfo, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if path == root {
			return nil
		}
		if chownErr := os.Lchown(path, uid, gid); chownErr != nil {
			if errors.Is(chownErr, os.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("chown %q to (uid=%d, gid=%d): %w", path, uid, gid, chownErr)
		}
		return nil
	})
}

// attachableSocketReapplyMode is the mode the idempotent StartCell no-op
// path (#935) re-asserts on a live per-container attach socket inode. It
// mirrors ctr.AttachableSocketMode ("0660"): rw for the owning container
// uid + rw for the kukeon group, no world. connect(2) on a Unix socket
// requires write permission on the inode, so a socket an old kuketty
// (pre-sbsh#361) bound with mode 0o640 (group-read only) is permanently
// undialable by a kukeon-group member on the host. Re-chmod'ing the live
// inode closes that window without bouncing the workload.
const attachableSocketReapplyMode os.FileMode = 0o660

// reapplyAttachableSocketPerms re-asserts the mode and group of every live
// per-container attach socket on the StartCell idempotent no-op path (#935).
//
// When StartCell short-circuits because all container tasks are already
// running, it skips the destructive recreate loop and with it
// attachableBuildOpts + attachablePostCreateChown — so a socket an old
// kuketty bound with the wrong mode (0o640 group-read only, the pre-sbsh#361
// mode) is never corrected. A kukeon-group member on the host then dials
// that live, listening socket and gets EACCES forever: the inode's mode
// never changes during the attach retry window because nothing rewrites it
// (the EACCES-retry fix from #933 only helps a socket about to be replaced
// by a starting kuketty, not the permanent listener of a still-running one).
//
// The fix is a root-owned os.Chmod + os.Chown on the live socket inode:
// idempotent when the mode is already correct (a post-#361 kuketty already
// bound 0o660), and safe because it never restarts the workload. It is
// best-effort — a per-container failure is logged and skipped rather than
// failing the no-op StartCell, because the cell is already healthy and
// running and a chmod miss must not regress the idempotent-skip contract.
// A socket kuketty has not bound yet (ENOENT) is a benign no-op: the normal
// startup chown path will set the mode when the listener appears.
func (r *Exec) reapplyAttachableSocketPerms(cell intmodel.Cell) {
	for i := range cell.Spec.Containers {
		spec := cell.Spec.Containers[i]
		if !spec.Attachable {
			continue
		}
		socketPath := fs.ContainerSocketPath(
			r.opts.RunPath,
			spec.RealmName, spec.SpaceName, spec.StackName, spec.CellName, spec.ID,
		)
		if chmodErr := os.Chmod(socketPath, attachableSocketReapplyMode); chmodErr != nil {
			if errors.Is(chmodErr, os.ErrNotExist) {
				continue
			}
			r.logger.WarnContext(r.ctx,
				"failed to re-chmod attachable socket on idempotent StartCell skip",
				"socket", socketPath, "error", chmodErr)
			continue
		}
		// Leave the owning uid untouched (-1); only re-assert the kukeon
		// group when one is configured, mirroring attachablePostCreateChown.
		if gid := r.opts.KukeonGroupGID; gid > 0 {
			if chownErr := os.Chown(socketPath, -1, gid); chownErr != nil &&
				!errors.Is(chownErr, os.ErrNotExist) {
				r.logger.WarnContext(r.ctx,
					"failed to re-chown attachable socket group on idempotent StartCell skip",
					"socket", socketPath, "gid", gid, "error", chownErr)
			}
		}
	}
}

// attachableSocketSymlinkDirMode is the mode applied to <RunPath>/s when
// the runner stages it on first Attachable provision. 0o755 mirrors the
// /opt/kukeon root layout: world-traversable so any process can resolve a
// staged symlink, world-listable so an operator can `ls` the directory
// during troubleshooting. The symlinks themselves are never created
// world-writable (symlink mode is ignored by Linux for symlink-following
// `connect(2)`; the target inode's mode is what gates access).
const attachableSocketSymlinkDirMode os.FileMode = 0o0755

// ensureAttachableSocketSymlink stages the SUN_PATH-safe symlink that
// `kuke attach` connects to (issue #521). The symlink lives at
// fs.ContainerSocketSymlinkPath under a shallow <RunPath>/s/ subtree and
// targets the deep fs.ContainerSocketPath inode that kuketty creates inside
// the bind-mounted /run/kukeon/tty directory at runtime. The target inode
// does not exist when this function runs — symlinks are pure strings — so
// the function never blocks on container startup. Recreating the symlink
// on a re-provision is idempotent: an existing entry is unlinked and
// rewritten so a prior provision with a stale target gets corrected.
//
// The host-side socket path length is the gate: the function refuses to
// stage a symlink whose resolved path would exceed
// consts.KukeonMaxSocketPath bytes, surfacing errdefs.ErrSocketPathTooLong
// with the offending path named. This is the provision-time fail-fast the
// AC requires so a future operator-configured RunPath that overflows the
// SUN_PATH budget cannot defer its failure to first `kuke attach`.
func ensureAttachableSocketSymlink(runPath string, spec intmodel.ContainerSpec) error {
	symlinkPath := fs.ContainerSocketSymlinkPath(
		runPath,
		spec.RealmName, spec.SpaceName, spec.StackName, spec.CellName, spec.ID,
	)
	if len(symlinkPath) > consts.KukeonMaxSocketPath {
		return fmt.Errorf("%w: %q is %d bytes (limit %d)",
			errdefs.ErrSocketPathTooLong, symlinkPath, len(symlinkPath), consts.KukeonMaxSocketPath)
	}

	symlinkDir := fs.ContainerSocketSymlinkDir(runPath)
	if err := os.MkdirAll(symlinkDir, attachableSocketSymlinkDirMode); err != nil {
		return fmt.Errorf("mkdir %q: %w", symlinkDir, err)
	}

	target := fs.ContainerSocketPath(
		runPath,
		spec.RealmName, spec.SpaceName, spec.StackName, spec.CellName, spec.ID,
	)
	// os.Symlink fails with EEXIST when a dirent already lives at the
	// destination; the re-provision case clears it first so a stale target
	// from a prior layout does not silently linger. Use os.Remove (not
	// RemoveAll) so a hostile or buggy actor cannot trick us into deleting a
	// real directory tree if the destination has somehow turned into one.
	if err := os.Remove(symlinkPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale symlink %q: %w", symlinkPath, err)
	}
	if err := os.Symlink(target, symlinkPath); err != nil {
		return fmt.Errorf("symlink %q -> %q: %w", symlinkPath, target, err)
	}
	return nil
}

// removeAttachableSocketSymlink unlinks the SUN_PATH-safe symlink staged by
// ensureAttachableSocketSymlink. Best-effort: a missing entry is not an
// error (idempotent under repeated cell / container deletes), but any
// other failure is returned so the caller can surface the operator-
// actionable case (e.g. permissions) without losing the signal. Skips
// non-Attachable specs so the cell-delete path can call it
// unconditionally.
func removeAttachableSocketSymlink(runPath string, spec intmodel.ContainerSpec) error {
	if !spec.Attachable {
		return nil
	}
	symlinkPath := fs.ContainerSocketSymlinkPath(
		runPath,
		spec.RealmName, spec.SpaceName, spec.StackName, spec.CellName, spec.ID,
	)
	if err := os.Remove(symlinkPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove socket symlink %q: %w", symlinkPath, err)
	}
	return nil
}

// removeAttachableSocketRuntimeArtifacts unlinks both the SUN_PATH-safe
// symlink (under <RunPath>/s/) and the host-side socket inode (the deep
// bind-mount source path kuketty binds inside the container) for an
// Attachable container. Called on every clean teardown of a cell —
// `kuke stop`, `kuke kill`, and the reconciler's wind-down
// (#852). Defense-in-depth complement to the client- and server-side
// task-liveness guards: when a kuketty exits and never restarts (cell
// wound down, daemon restart, host reboot), the inode it bound persists
// across the host, so the next `kuke attach` that slips past the guards
// dials a dead socket and surfaces `connection refused`. Unlinking on
// the way down means the next `connect(2)` sees ENOENT instead — a
// cleaner error class regardless of which guard race surfaced it.
//
// Both deletions are best-effort: a missing path (idempotent under
// repeated stops or a delete that already RemoveAll'd the metadata
// tree) is not an error, and a non-NotExist failure on one path does
// not abort the other — the goal is to leave the host with no orphan
// inode for the next `kuke attach` to dial. Skips non-Attachable specs
// so cell-level teardown can call it unconditionally per container.
func removeAttachableSocketRuntimeArtifacts(runPath string, spec intmodel.ContainerSpec) error {
	if !spec.Attachable {
		return nil
	}
	var errs []error
	if err := removeAttachableSocketSymlink(runPath, spec); err != nil {
		errs = append(errs, err)
	}
	socketPath := fs.ContainerSocketPath(
		runPath,
		spec.RealmName, spec.SpaceName, spec.StackName, spec.CellName, spec.ID,
	)
	if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		errs = append(errs, fmt.Errorf("remove socket inode %q: %w", socketPath, err))
	}
	return errors.Join(errs...)
}
