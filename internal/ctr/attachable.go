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

	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/pkg/oci"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

// Reserved in-container paths the kuketty wrapper claims. Documented as such
// in pkg/api/model/v1beta1/container.go.
//
// kuketty (issue #165) replaces sbsh on the OCI injection path. Phase 1
// honors the binary mount, the per-container tty directory mount, and the
// per-container metadata file mount that drives kuketty's runtime
// configuration. The wrapper is invoked with no CLI flags — every runtime
// input flows through the metadata file (see pkg/kuketty).
const (
	// AttachableBinaryPath is where the kuketty binary is bind-mounted
	// read-only inside the container. The host source is staged from
	// kukeond's own /bin/kuketty by the runner (kuketty travels inside the
	// kukeond image — see Dockerfile).
	AttachableBinaryPath = "/.kukeon/bin/kuketty"

	// AttachableTTYDir is where the per-container tty directory is
	// bind-mounted inside the container. kuketty creates and owns the
	// socket (and, in later phases, capture / log) files inside this
	// directory; because it is a directory bind mount (not a file mount),
	// kuketty's unlink-and-recreate of the socket inode stays host-visible.
	AttachableTTYDir = "/run/kukeon/tty"

	// AttachableSocketPath is the in-container path of the kuketty attach
	// control socket. kuketty listens here; the host peer is the bind-
	// mount source directory's `socket` entry, which `kuke attach`
	// connects to. (Phase 1b lands the RPC server behind this inode;
	// phase 1 only creates the inode.)
	AttachableSocketPath = AttachableTTYDir + "/socket"

	// AttachableCapturePath is the declared in-container path for the
	// kuketty capture transcript. Honored starting in phase 2 (#288); in
	// phase 1 the path is rendered into the metadata file but kuketty
	// does not yet write the transcript.
	AttachableCapturePath = AttachableTTYDir + "/capture"

	// AttachableLogfilePath is the declared in-container path for the
	// kuketty log file. Honored starting in phase 3 (#289).
	AttachableLogfilePath = AttachableTTYDir + "/log"

	// AttachableMetadataDir is the in-container directory where the
	// kukeond-rendered terminal metadata file lives. Kept under
	// /.kukeon/kuketty/ to mirror the /.kukeon/bin/ layout of the
	// binary mount and keep both kuketty-owned paths in one subtree
	// the workload's rootfs cannot collide with.
	AttachableMetadataDir = "/.kukeon/kuketty"

	// AttachableMetadataPath is the fixed in-container path kuketty reads
	// its runtime configuration from. The daemon bind-mounts the
	// per-container metadata file over this path at OCI spec build time.
	// Kept in sync with cmd/kuketty/main.go's metadataPath constant.
	AttachableMetadataPath = AttachableMetadataDir + "/metadata.json"

	// AttachableMetadataFile is the basename of the host-side per-container
	// metadata file rendered into the directory above HostTTYDir before
	// the workload container starts.
	AttachableMetadataFile = "kuketty-metadata.json"

	// AttachableSocketMode is the octal mode applied to the per-container
	// attach control socket when SocketGID is configured. 0660 = rw for
	// owner (the container's runtime uid) + rw for group (the kukeon
	// group), no world. Combined with the metadata file's socket GID this
	// lets a non-root member of the kukeon group on the host `connect()`
	// to the per-container kuketty control socket. Linux requires write
	// permission on a socket inode to connect — group-readable alone is
	// not enough.
	AttachableSocketMode = "0660"

	// AttachableCaptureMode and AttachableLogFileMode are the octal modes
	// that will apply to the capture transcript and log file once phases
	// 2/3 land kuketty's writers. 0640 = rw for owner (the container uid)
	// + r for group (the kukeon group), no world.
	AttachableCaptureMode = "0640"
	AttachableLogFileMode = "0640"
)

// AttachableInjection carries the host-side paths needed to wrap a container's
// OCI spec so it runs under kuketty. The caller (the daemon) computes the
// paths from the cell/container identity and the configured run path. Empty
// fields disable the corresponding bind mount or metadata-file entry.
type AttachableInjection struct {
	// KukettyBinaryPath is the host path of the kuketty binary that will
	// be bind-mounted RO at AttachableBinaryPath inside the container.
	// The daemon stages this from its own /bin/kuketty at provision time
	// (kukeond image ships kuketty alongside the daemon binary).
	KukettyBinaryPath string

	// HostTTYDir is the host path of the per-container tty directory that
	// will be bind-mounted at AttachableTTYDir inside the container. The
	// host-visible socket that `kuke attach` connects to is the `socket`
	// entry inside this directory.
	HostTTYDir string

	// HostMetadataPath is the host path of the per-container kuketty
	// metadata file. The runner renders the metadata to this path before
	// the container starts; the OCI bind mount maps it to
	// AttachableMetadataPath inside the container.
	HostMetadataPath string
}

// withAttachableMounts adds the three bind mounts that make kuketty
// reachable from inside the container: the static binary (RO), the
// per-container tty directory (RW), and the kukeond-rendered metadata file
// (RO). Used as an oci.SpecOpts so it composes with the rest of the spec
// build.
//
// The tty mount is a *directory* bind mount, not a file mount: kuketty's
// listener does `os.Remove` + `Listen` on the destination, which under a
// file bind would unlink the bind dirent and create a fresh socket inode on
// the container's overlay — invisible to the host. A directory bind mount
// keeps the inode host-visible by construction.
//
// The metadata mount is a *file* bind mount: kuketty reads the file once at
// startup and never mutates it, so the unlink-and-recreate trap that
// affects the socket inode does not apply.
func withAttachableMounts(inj AttachableInjection) oci.SpecOpts {
	return oci.WithMounts([]runtimespec.Mount{
		{
			Destination: AttachableBinaryPath,
			Source:      inj.KukettyBinaryPath,
			Type:        "bind",
			Options:     []string{"rbind", "ro"},
		},
		{
			Destination: AttachableTTYDir,
			Source:      inj.HostTTYDir,
			Type:        "bind",
			Options:     []string{"rbind", "rw"},
		},
		{
			Destination: AttachableMetadataPath,
			Source:      inj.HostMetadataPath,
			Type:        "bind",
			Options:     []string{"rbind", "ro"},
		},
	})
}

// withAttachableArgsWrap prepends the kuketty invocation to the container's
// process.args. It is composed *after* the normal WithProcessArgs (or the
// image's default ENTRYPOINT/CMD) so the wrapped command line is whatever
// would have run otherwise.
//
// The wrapper has no CLI flags by design (issue #165 redirect): kuketty
// reads every runtime input from the bind-mounted metadata file. Only the
// `--` positional separator stays on the command line so kuketty can tell
// its own argv from the workload's.
//
// OCI semantics: process.args is the merged "ENTRYPOINT + CMD" by the time
// this opt runs (containerd's WithImageConfigArgs has already resolved image
// defaults and any user override of either). We just wrap the result, which
// is what Kubernetes failed to do correctly for years and what the
// attachable spec test explicitly locks down.
func withAttachableArgsWrap() oci.SpecOpts {
	return func(_ context.Context, _ oci.Client, _ *containers.Container, s *runtimespec.Spec) error {
		if s.Process == nil {
			s.Process = &runtimespec.Process{}
		}
		original := append([]string(nil), s.Process.Args...)
		wrapped := []string{AttachableBinaryPath, "--"}
		s.Process.Args = append(wrapped, original...)
		return nil
	}
}
