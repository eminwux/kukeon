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
// OCI semantics: process.args is the merged "ENTRYPOINT + CMD" by the time
// this opt runs (containerd's WithImageConfigArgs has already resolved image
// defaults and any user override of either). We just wrap the result, which
// is what Kubernetes failed to do correctly for years and what this issue
// explicitly tests.
func withAttachableArgsWrap() oci.SpecOpts {
	return func(_ context.Context, _ oci.Client, _ *containers.Container, s *runtimespec.Spec) error {
		if s.Process == nil {
			s.Process = &runtimespec.Process{}
		}
		original := append([]string(nil), s.Process.Args...)
		wrapped := []string{
			AttachableBinaryPath,
			AttachableSubcommand,
			"--run-path", AttachableTTYDir,
			"--socket", AttachableSocketPath,
			"--capture-file", AttachableCapturePath,
			"--log-file", AttachableLogfilePath,
			"--",
		}
		s.Process.Args = append(wrapped, original...)
		return nil
	}
}
