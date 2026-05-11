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

	"github.com/eminwux/kukeon/internal/ctr"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
	sbshapi "github.com/eminwux/sbsh/pkg/api"
	sbshbuilder "github.com/eminwux/sbsh/pkg/builder"
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
//     producing the per-container api.TerminalDoc at <containerDir>/
//     kuketty-metadata.json that the bind mount maps onto kuketty's fixed
//     in-container metadata path.
//
// The tty directory is created with mode 02750 owned by root:kukeon group
// when the kukeon group GID is configured, so non-root operators in the
// kukeon group can dial the host-side socket via the same group-traversal
// path `kuke init` sets up on /opt/kukeon. The owner is corrected to the
// container's resolved uid by attachablePostCreateChown after
// CreateContainerFromSpec runs.
func (r *Exec) attachableBuildOpts(_ string, spec intmodel.ContainerSpec, _ []ctr.RegistryCredentials) ([]ctr.BuildOption, error) {
	if !spec.Attachable {
		return nil, nil
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
		return r.writeKukettyMetadata(metadataPath, spec, kukeonGID, workloadArgv)
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

// writeKukettyMetadata renders the per-container api.TerminalDoc and writes
// it atomically (tmp + rename) so a partially-written file is never visible
// to a racing kuketty in the container. 0o600 because the file lives inside
// the per-container parent directory, which is daemon-private (0o2750 or
// 0o700) — keeping the inner file similarly tight guards against a future
// loosening of the parent dir.
//
// workloadArgv is the resolved Process.Args captured by the OCI args-wrap
// closure (image ENTRYPOINT+CMD merged with any user override). It is moved
// into TerminalSpec.Command / TerminalSpec.CommandArgs via sbsh's
// builder.WithCommand so the kuketty side spawns the workload via sbsh's
// terminal runner instead of the wrapper's argv. An empty workloadArgv
// leaves the builder's profile default in place (sbsh's hardcoded default
// profile is /bin/bash -i, matching the legacy fallback when the user
// supplied no command).
func (r *Exec) writeKukettyMetadata(
	path string,
	spec intmodel.ContainerSpec,
	kukeonGroupGID int,
	workloadArgv []string,
) error {
	opts := []sbshbuilder.TerminalOption{
		sbshbuilder.WithSocketFile(ctr.AttachableSocketPath),
		// DisableSetPrompt: phase 4 (#290) wires the cell's Tty.Prompt
		// through to sbsh's PS1 rewriting; until then, leave the
		// workload's PS1 alone — writing the default `(sbsh-$ID) $PS1`
		// export into a non-shell workload (nginx, python) would inject
		// literal text into its stdin.
		sbshbuilder.WithDisableSetPrompt(true),
	}
	if mode := socketModeIfGroupSet(kukeonGroupGID, ctr.AttachableSocketMode); mode != "" {
		opts = append(opts, sbshbuilder.WithSocketMode(mode))
	}
	if kukeonGroupGID > 0 {
		opts = append(opts, sbshbuilder.WithSocketGID(kukeonGroupGID))
	}
	if len(workloadArgv) > 0 {
		opts = append(opts, sbshbuilder.WithCommand(workloadArgv))
	}

	terminalSpec, err := sbshbuilder.BuildTerminalSpec(r.ctx, r.logger, ctr.AttachableTTYDir, opts...)
	if err != nil {
		return fmt.Errorf("build kuketty terminal spec: %w", err)
	}

	doc := sbshapi.TerminalDoc{
		APIVersion: sbshapi.APIVersionV1Beta1,
		Kind:       sbshapi.KindTerminal,
		Metadata: sbshapi.TerminalMetadata{
			Name:   spec.ID,
			Labels: kukettyMetadataLabels(spec),
		},
		Spec: *terminalSpec,
	}

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal kuketty metadata: %w", err)
	}
	tmp := path + ".tmp"
	if err = os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write kuketty metadata %q: %w", tmp, err)
	}
	if err = os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename kuketty metadata %q -> %q: %w", tmp, path, err)
	}
	return nil
}

// kukettyMetadataLabels stamps the cell-context identity on the rendered
// TerminalDoc.Metadata so an operator inspecting the host-side file can
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

// socketModeIfGroupSet returns the octal-mode string only when the kukeon
// group GID is configured; otherwise empty so kuketty applies the OS-default
// (umask-clipped) mode — matching the legacy 0o600-owner-only fallback the
// sbsh wrapper had when no kukeon group existed.
func socketModeIfGroupSet(gid int, mode string) string {
	if gid > 0 {
		return mode
	}
	return ""
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

	if err = copyBinaryAtomic(src, dst); err != nil {
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
// owner-x bits but not setgid-style elevations.
func copyBinaryAtomic(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open kuketty source %q: %w", src, err)
	}
	defer func() { _ = in.Close() }()

	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("create kuketty staged tmp %q: %w", tmp, err)
	}
	if _, err = io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("copy kuketty %q -> %q: %w", src, tmp, err)
	}
	if err = out.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close kuketty staged tmp %q: %w", tmp, err)
	}
	if err = os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename kuketty staged %q -> %q: %w", tmp, dst, err)
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
	return nil
}
