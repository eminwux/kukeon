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

package ctr

import (
	"context"
	"strconv"

	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/pkg/oci"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

// Reserved in-container paths that the Attachable wrapper claims. Documented
// as such in pkg/api/model/v1beta1/container.go. The binary path is
// configurable in spirit (see #67) but fixed for this slice.
const (
	// AttachableBinaryPath is where the static sbsh binary is bind-mounted
	// read-only inside the container.
	AttachableBinaryPath = "/.kukeon/bin/sbsh"

	// AttachableTTYDir is where the per-container tty directory is
	// bind-mounted inside the container. sbsh creates and owns the socket,
	// capture, and log files inside this directory; because it is a
	// directory bind mount (not a file mount), sbsh's unlink-and-recreate
	// of the socket inode stays host-visible.
	AttachableTTYDir = "/run/kukeon/tty"

	// AttachableSocketPath is the in-container path of the sbsh terminal
	// socket. sbsh listens here; the host peer is the bind-mount source
	// directory's `socket` entry, which `kuke attach` connects to.
	AttachableSocketPath = AttachableTTYDir + "/socket"

	// AttachableCapturePath is the in-container path of the sbsh capture
	// file. Surfacing it to `kuke logs` is a follow-up.
	AttachableCapturePath = AttachableTTYDir + "/capture"

	// AttachableLogfilePath is the in-container path of the sbsh terminal
	// log file. Surfacing it to `kuke logs` is a follow-up.
	AttachableLogfilePath = AttachableTTYDir + "/log"

	// AttachableSubcommand is the sbsh entrypoint subcommand used to wrap
	// the workload's process. Hard-coded for the foundation slice; the
	// resolver in #67 will not change it.
	AttachableSubcommand = "terminal"

	// AttachableProfileFile is the basename of the sbsh TerminalProfile YAML
	// the daemon writes into the per-container tty directory when a
	// container declares a tty block. The host pre-writes the file in the
	// bind-mount source so it appears under AttachableTTYDir inside the
	// container, where sbsh resolves it via --profiles-dir + --profile.
	AttachableProfileFile = "profile.yaml"

	// AttachableProfileName is the profile.metadata.name written into the
	// generated TerminalProfile and passed to `sbsh terminal --profile`.
	AttachableProfileName = "kukeon"

	// AttachableSocketMode is the octal mode passed to `sbsh terminal
	// --socket-mode` when SocketGID is configured. 0660 = rw for owner
	// (the container's runtime uid) + rw for group (the kukeon group), no
	// world. Combined with `--socket-gid <kukeonGID>` this lets a non-root
	// member of the kukeon group on the host `connect()` to the per-
	// container sbsh control socket. Linux requires write permission on a
	// socket inode to connect — group-readable alone is not enough.
	//
	// Available since sbsh v0.10.0; older sbsh binaries reject the flag and
	// the wrapper omits it when the kukeon group GID is unset.
	AttachableSocketMode = "0660"
)

// AttachableInjection carries the host-side paths needed to wrap a container's
// OCI spec so it runs under sbsh. The caller (the daemon) computes both paths
// from the cell/container identity and the configured run path. Both fields
// are required when Attachable=true; an empty struct disables injection.
type AttachableInjection struct {
	// SbshBinaryPath is the host path of the static sbsh binary that will be
	// bind-mounted RO at AttachableBinaryPath inside the container.
	SbshBinaryPath string

	// HostTTYDir is the host path of the per-container tty directory that
	// will be bind-mounted at AttachableTTYDir inside the container. The
	// host-visible socket that `kuke attach` (#66) connects to is the
	// `socket` entry inside this directory.
	HostTTYDir string

	// UseProfile, when true, instructs the wrapper to append
	// `--profiles-dir AttachableTTYDir --profile AttachableProfileName` to
	// the sbsh terminal invocation. The runner is responsible for writing
	// the profile YAML to <HostTTYDir>/AttachableProfileFile before the
	// container starts; the wrapper itself never touches the filesystem.
	UseProfile bool

	// SocketGID, when non-zero, is the numeric GID of the kukeon system
	// group on the host. The wrapper emits `sbsh terminal --socket-mode
	// AttachableSocketMode --socket-gid <SocketGID>` so the per-container
	// control socket is created with mode 0660 owned by the kukeon group,
	// matching the group-traversal layout on the parent tty/ directory and
	// `/opt/kukeon`. Zero (the default) preserves sbsh's hard-coded 0o600
	// owner-only behavior — the legacy contract for callers that have no
	// kukeon group configured. Requires sbsh v0.10.0 or later inside the
	// container; the staged binary at /.kukeon/bin/sbsh must support the
	// `--socket-mode` and `--socket-gid` flags.
	SocketGID int
}

// withAttachableMounts adds the two bind mounts that make sbsh reachable from
// inside the container: the static binary (RO) and the per-container tty
// directory (RW). Used as an oci.SpecOpts so it composes with the rest of the
// spec build.
//
// The tty mount is a *directory* bind mount, not a file mount: sbsh's
// listener does `os.Remove` + `Listen` on the destination, which under a
// file bind would unlink the bind dirent and create a fresh socket inode on
// the container's overlay — invisible to the host. A directory bind mount
// keeps the inode host-visible by construction.
func withAttachableMounts(inj AttachableInjection) oci.SpecOpts {
	return oci.WithMounts([]runtimespec.Mount{
		{
			Destination: AttachableBinaryPath,
			Source:      inj.SbshBinaryPath,
			Type:        "bind",
			Options:     []string{"rbind", "ro"},
		},
		{
			Destination: AttachableTTYDir,
			Source:      inj.HostTTYDir,
			Type:        "bind",
			Options:     []string{"rbind", "rw"},
		},
	})
}

// withAttachableArgsWrap prepends the sbsh terminal wrapper, with the
// per-tty paths inside AttachableTTYDir, to the container's process.args.
// It is composed *after* the normal WithProcessArgs (or the image's default
// ENTRYPOINT/CMD) so the wrapped command line is whatever would have run
// otherwise.
//
// When inj.UseProfile is true, the wrapper additionally points sbsh at the
// per-container profile YAML the runner pre-wrote into HostTTYDir, so
// the generated prompt and onInit scripts take effect on first attach.
// `--profiles-dir` is a global flag on `sbsh` and must precede the
// `terminal` subcommand; `--profile` is a per-subcommand flag on
// `terminal` and must follow it. Swapping either placement makes sbsh
// reject the invocation with `unknown flag`.
//
// When inj.SocketGID > 0, the wrapper also passes `--socket-mode 0660
// --socket-gid <SocketGID>` to `sbsh terminal` so the per-container
// control socket lands as 0660 owned by the kukeon group — without it,
// sbsh's default 0o600 root-owned socket is unreachable for non-root
// kukeon-group operators on the host even when the parent tty/ directory
// is group-traversable. Both flags require sbsh v0.10.0 or later.
//
// OCI semantics: process.args is the merged "ENTRYPOINT + CMD" by the time
// this opt runs (containerd's WithImageConfigArgs has already resolved image
// defaults and any user override of either). We just wrap the result, which
// is what Kubernetes failed to do correctly for years and what this issue
// explicitly tests.
func withAttachableArgsWrap(inj AttachableInjection) oci.SpecOpts {
	return func(_ context.Context, _ oci.Client, _ *containers.Container, s *runtimespec.Spec) error {
		if s.Process == nil {
			s.Process = &runtimespec.Process{}
		}
		original := append([]string(nil), s.Process.Args...)
		wrapped := []string{AttachableBinaryPath}
		if inj.UseProfile {
			wrapped = append(wrapped,
				"--profiles-dir", AttachableTTYDir,
			)
		}
		wrapped = append(wrapped, AttachableSubcommand)
		if inj.UseProfile {
			wrapped = append(wrapped, "--profile", AttachableProfileName)
		}
		wrapped = append(wrapped,
			"--run-path", AttachableTTYDir,
			"--socket", AttachableSocketPath,
			"--capture-file", AttachableCapturePath,
			"--log-file", AttachableLogfilePath,
		)
		if inj.SocketGID > 0 {
			wrapped = append(wrapped,
				"--socket-mode", AttachableSocketMode,
				"--socket-gid", strconv.Itoa(inj.SocketGID),
			)
		}
		wrapped = append(wrapped, "--")
		s.Process.Args = append(wrapped, original...)
		return nil
	}
}
