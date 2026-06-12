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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/pkg/oci"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

// hostRootViaPID1 is the path through which a process that shares the host PID
// namespace reaches the host's root filesystem: PID 1's root, exposed by
// procfs as a magic symlink. kukeond runs inside the kuke-system kukeond cell
// with hostPID: true (required by the CNI bridge path), so /proc/1/root is the
// host root even though the cell's own mount namespace exposes only a minimal
// /dev that contains none of the host's pass-through device nodes.
const hostRootViaPID1 = "/proc/1/root"

// deviceHostRoot returns the filesystem prefix under which the kukeond process
// must resolve *host* device nodes when building a container spec. Device
// resolution (the os.Stat behind oci.DeviceFromPath / oci.HostDevices) runs in
// the kukeond process, not in the target container — so when kukeond is
// containerized its own mount namespace, not the host's, is what gets stat'd.
// The cell binds only /run/kukeon and /opt/kukeon; its /dev is the cell's
// devtmpfs (default nodes only), so a host device path like /dev/kvm fails the
// stat. Resolving against the host root reachable at /proc/1/root fixes that.
//
// Indirected through a var so tests can pin the prefix without a containerized
// daemon.
var deviceHostRoot = resolveDeviceHostRoot

func resolveDeviceHostRoot() string {
	if daemonInPrivateMountNS() {
		return hostRootViaPID1
	}
	return "/"
}

// daemonInPrivateMountNS reports whether the calling process's root filesystem
// differs from PID 1's — i.e. kukeond runs in its own mount namespace (the
// containerized kukeond cell) rather than sharing the host's (un-containerized
// dev loops, host-side kuke init). When the two roots are the same inode the
// daemon's own /dev *is* the host's, so device resolution needs no prefix.
//
// A normal container that does not share the host PID namespace sees its own
// init as PID 1, so /proc/1/root equals its own root and this correctly
// reports false — only the kukeond cell's hostPID + private-mount-ns shape
// (host PID 1 with a different root) reports true. If /proc/1/root cannot be
// stat'd (no procfs, PID 1 not visible, insufficient privilege) the daemon
// falls back to resolving against its own /, the historical behavior.
func daemonInPrivateMountNS() bool {
	self, err := os.Stat("/")
	if err != nil {
		return false
	}
	pid1, err := os.Stat(hostRootViaPID1)
	if err != nil {
		return false
	}
	return !os.SameFile(self, pid1)
}

// deviceFromPath resolves a single host device node into its OCI LinuxDevice.
// Indirected for tests; defaults to containerd's oci.DeviceFromPath, which
// Lstat's the node and reads its type/major/minor/mode.
var deviceFromPath = oci.DeviceFromPath

// hostLinuxDeviceOpt returns a SpecOpts that replicates the host device node at
// containerPath into the container — appending a Linux.Devices entry (so the
// node is visible at containerPath inside the container) and a matching
// Linux.Resources.Devices allow rule (so open() is not denied by the default
// deny-all device cgroup). It is the host-root-aware replacement for
// oci.WithLinuxDevice, which stat's the path in the kukeond process's own mount
// namespace and so fails for any host device absent from the containerized
// daemon cell's minimal /dev. The node is stat'd via the host-root prefix but
// exposed at the un-prefixed containerPath. A missing node fails container
// create with an error naming the requested path. Issue #1261.
func hostLinuxDeviceOpt(containerPath, access string) oci.SpecOpts {
	return func(_ context.Context, _ oci.Client, _ *containers.Container, s *runtimespec.Spec) error {
		hostPath := filepath.Join(deviceHostRoot(), strings.TrimPrefix(containerPath, "/"))
		dev, err := deviceFromPath(hostPath)
		if err != nil {
			return fmt.Errorf("resolve device %q: %w", containerPath, err)
		}
		// Expose the node at the path the caller asked for, not the
		// host-root-prefixed path it was stat'd through.
		dev.Path = containerPath

		if s.Linux == nil {
			s.Linux = &runtimespec.Linux{}
		}
		if s.Linux.Resources == nil {
			s.Linux.Resources = &runtimespec.LinuxResources{}
		}
		s.Linux.Devices = append(s.Linux.Devices, *dev)
		s.Linux.Resources.Devices = append(s.Linux.Resources.Devices, runtimespec.LinuxDeviceCgroup{
			Type:   dev.Type,
			Allow:  true,
			Major:  &dev.Major,
			Minor:  &dev.Minor,
			Access: access,
		})
		return nil
	}
}

// privilegedHostDevicesOpt returns a SpecOpts that populates Linux.Devices with
// every host device node, resolved against the host root so a containerized
// kukeond replicates the *host's* /dev rather than the kukeond cell's minimal
// devtmpfs. Paired with oci.WithAllDevicesAllowed at the privileged call sites,
// it restores `docker run --privileged` / `ctr run --privileged` device parity
// without the namespace blindness of oci.WithHostDevices, which enumerates the
// calling process's own /dev (the daemon cell's, when containerized). When
// kukeond is un-containerized its own /dev is the host's, so the well-tested
// oci.WithHostDevices is reused directly. Issue #1261 (supersedes the
// namespace-blind approach proposed for #1255).
func privilegedHostDevicesOpt() oci.SpecOpts {
	return func(ctx context.Context, client oci.Client, c *containers.Container, s *runtimespec.Spec) error {
		root := deviceHostRoot()
		if root == "/" {
			return oci.WithHostDevices(ctx, client, c, s)
		}
		devRoot := filepath.Join(root, "dev")
		devices, err := enumerateHostDevices(devRoot)
		if err != nil {
			return fmt.Errorf("enumerate host devices under %q: %w", devRoot, err)
		}
		if s.Linux == nil {
			s.Linux = &runtimespec.Linux{}
		}
		s.Linux.Devices = append(s.Linux.Devices, devices...)
		return nil
	}
}

// enumerateHostDevices walks the device tree rooted at devRoot (e.g.
// /proc/1/root/dev) and returns the contained nodes with their Path rewritten
// to the in-container /dev/... path, stripping the host-root prefix. It is the
// host-root-aware analogue of oci.HostDevices(), which is hard-wired to the
// calling process's own /dev. Mirrors containerd's getDevices skip list:
// pseudo-filesystem subdirs, the console, fifos, and non-device entries.
func enumerateHostDevices(devRoot string) ([]runtimespec.LinuxDevice, error) {
	var out []runtimespec.LinuxDevice
	if err := walkHostDevices(devRoot, devRoot, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func walkHostDevices(devRoot, dir string, out *[]runtimespec.LinuxDevice) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		full := filepath.Join(dir, e.Name())
		if e.IsDir() {
			switch e.Name() {
			// Pseudo-filesystems and bookkeeping dirs containerd's getDevices
			// skips; recursing into them yields no real pass-through nodes.
			case "pts", "shm", "fd", "mqueue", ".lxc", ".lxd-mounts", ".udev":
				continue
			}
			if err := walkHostDevices(devRoot, full, out); err != nil {
				return err
			}
			continue
		}
		if e.Name() == "console" {
			continue
		}
		dev, err := deviceFromPath(full)
		if err != nil {
			if errors.Is(err, oci.ErrNotADevice) || os.IsNotExist(err) {
				continue
			}
			return err
		}
		if dev.Type == "p" { // fifo — not a pass-through device
			continue
		}
		rel, err := filepath.Rel(devRoot, full)
		if err != nil {
			return err
		}
		dev.Path = filepath.Join("/dev", rel)
		*out = append(*out, *dev)
	}
	return nil
}
