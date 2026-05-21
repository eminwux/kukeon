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
	"fmt"

	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/pkg/oci"
	"github.com/eminwux/kukeon/internal/consts"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

// Reserved in-container paths the kuketty wrapper claims. Documented as such
// in pkg/api/model/v1beta1/container.go.
//
// kuketty (issue #165) replaces sbsh on the OCI injection path. Phase 1b
// (#410) lands the attach-socket RPC server. Issue #641 makes the daemon mount
// kukeon's own api/model/v1beta1.ContainerDoc instead of a pre-rendered sbsh
// TerminalDoc: kuketty reads ContainerDoc.Spec, builds the TerminalSpec, and
// serves it via sbsh's pkg/terminal/server facade. The wrapper is invoked with
// no CLI flags — every runtime input flows through the bind-mounted metadata
// file.
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
	// connects to.
	AttachableSocketPath = AttachableTTYDir + "/socket"

	// AttachableCapturePath is the declared in-container path for the
	// kuketty capture transcript. Honored starting in phase 2 (#288);
	// kuketty does not yet write the transcript in phase 1b.
	AttachableCapturePath = AttachableTTYDir + "/capture"

	// AttachableKukettyLogPath is the in-container path of the kuketty
	// wrapper's own slog output. Peer to AttachableSocketPath and
	// AttachableCapturePath inside the per-container TTY directory bind
	// mount, so the host-side ContainerKukettyLogPath resolves to the same
	// inode. The path is daemon-controlled — operators do not configure it
	// per cell — so this file always exists after first attach regardless
	// of cell YAML. Distinct from AttachableCapturePath (the workload's
	// tty byte stream); this carries the wrapper's own debug log
	// (issue #599, reversing #289 phase 3's opt-in design for the kuketty-
	// process log specifically).
	AttachableKukettyLogPath = AttachableTTYDir + "/" + consts.KukeonContainerKukettyLogFile

	// AttachableMetadataDir is the in-container directory where the
	// kukeond-rendered terminal metadata file lives. Kept under
	// /.kukeon/kuketty/ to mirror the /.kukeon/bin/ layout of the
	// binary mount and keep both kuketty-owned paths in one subtree
	// the workload's rootfs cannot collide with.
	AttachableMetadataDir = "/.kukeon/kuketty"

	// AttachableMetadataPath is the fixed in-container path kuketty reads
	// its runtime configuration from. The daemon bind-mounts the
	// per-container metadata file over this path at OCI spec build time.
	// Kept in sync with cmd/kuketty/main.go's defaultConfigPath constant.
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

	// RenderMetadata, when non-nil, is invoked from inside the OCI
	// args-wrap spec opt with the resolved workload argv — i.e. the merge
	// of the image's ENTRYPOINT + CMD and any user override that
	// containerd's WithImageConfig and our WithProcessArgs have already
	// applied to s.Process.Args by the time the wrap runs. The callback
	// is expected to render the ContainerDoc with the workload argv baked
	// into Spec.Command / Spec.Args and write it atomically to
	// HostMetadataPath. nil disables metadata rendering — used by unit
	// tests that exercise only the args-wrap shape.
	RenderMetadata func(workloadArgv []string) error
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
// The tty mount is also pinned to `rprivate` propagation (issue #547). The
// attachable cell hosts contributor workflows that nest a second kukeond
// inside (`make dev-init` inside a `kukeon-dev-root` cell); without
// `rprivate`, mount events from the nested kukeond's `/run/kukeon/...`
// activity could propagate back through the directory bind into the parent
// host's `<HostTTYDir>` and break the parent's `kuke attach` plumbing for
// the cell. `rprivate` makes the bind a one-way window: the nested daemon
// still sees the parent's socket / capture / log inodes, but no mount
// event in either direction escapes the boundary.
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
			Options:     []string{"rbind", "rw", "rprivate"},
		},
		{
			Destination: AttachableMetadataPath,
			Source:      inj.HostMetadataPath,
			Type:        "bind",
			Options:     []string{"rbind", "ro"},
		},
	})
}

// withAttachableArgsWrap captures the resolved workload argv from
// s.Process.Args (containerd's WithImageConfigArgs and any user-supplied
// WithProcessArgs have already run by this point), hands the argv to the
// injection's metadata renderer so kuketty receives the workload in
// Spec.Command / Spec.Args of the bind-mounted ContainerDoc, and then
// rewrites Process.Args to a single element: the kuketty binary path.
// No CLI flags by design (issue #410 redirect): kuketty reads every runtime
// input — including the workload to spawn — from the metadata file. The
// optional `--config` override exists only for test/debug ergonomics and is
// never set by this wrap.
func withAttachableArgsWrap(inj AttachableInjection) oci.SpecOpts {
	return func(_ context.Context, _ oci.Client, _ *containers.Container, s *runtimespec.Spec) error {
		if s.Process == nil {
			s.Process = &runtimespec.Process{}
		}
		workload := append([]string(nil), s.Process.Args...)
		if inj.RenderMetadata != nil {
			if err := inj.RenderMetadata(workload); err != nil {
				return fmt.Errorf("render kuketty metadata: %w", err)
			}
		}
		s.Process.Args = []string{AttachableBinaryPath}
		return nil
	}
}
