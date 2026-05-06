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

package consts

import (
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
)

const (
	CgroupFilesystemPath = "/sys/fs/cgroup"

	KukeonMetadataFile = "metadata.json"

	// KukeonContainerTTYDir is the basename of the per-container directory
	// that owns the sbsh terminal socket plus its capture and log siblings.
	// kukeon bind-mounts this directory (not a single file) into the
	// container so sbsh's unlink-and-recreate of the socket inode stays
	// host-visible.
	KukeonContainerTTYDir = "tty"

	// KukeonContainerSocketFile is the basename of the per-container sbsh
	// terminal socket inside KukeonContainerTTYDir. The container sees the
	// same inode at /run/kukeon/tty/socket via the directory bind mount
	// injected by Attachable=true specs.
	KukeonContainerSocketFile = "socket"

	// KukeonContainerCaptureFile is the basename of the per-container sbsh
	// capture file inside KukeonContainerTTYDir. sbsh writes the full tty
	// byte stream — every byte the workload produced and every byte typed
	// by an attached operator — into this file. `kuke log` tails the host
	// path that resolves to the same inode as the in-container path
	// /run/kukeon/tty/capture (see ctr.AttachableCapturePath).
	KukeonContainerCaptureFile = "capture"

	// KukeonContainerLogFile is the basename of the per-container stdout/
	// stderr log file written by the containerd runtime shim via cio.LogFile
	// for non-Attachable containers (Attachable containers route output
	// through sbsh's capture file instead). The shim is the writer; kuke
	// only reads it. `kuke log` tails this file when targeting a non-
	// Attachable container.
	KukeonContainerLogFile = "log"

	// Label keys shared across the user default hierarchy and the system hierarchy.
	KukeonRealmLabelKey     = "realm.kukeon.io"
	KukeonSpaceLabelKey     = "space.kukeon.io"
	KukeonStackLabelKey     = "stack.kukeon.io"
	KukeonCellLabelKey      = "cell.kukeon.io"
	KukeonContainerLabelKey = "container.kukeon.io"

	// Default user hierarchy created by `kuke init` for user workloads.
	KukeonDefaultRealmName = "default"
	KukeonDefaultSpaceName = "default"
	KukeonDefaultStackName = "default"

	// System hierarchy created by `kuke init` for the kukeond daemon.
	KukeSystemRealmName     = "kuke-system"
	KukeSystemSpaceName     = "kukeon"
	KukeSystemStackName     = "kukeon"
	KukeSystemCellName      = "kukeond"
	KukeSystemContainerName = "kukeond"

	// KukeonSystemUser and KukeonSystemGroup name the system identity created
	// by `kuke init` so a non-root operator added to the kukeon group can
	// dial the kukeond socket without sudo. Writes under /opt/kukeon still
	// require root; they go through the daemon.
	KukeonSystemUser  = "kukeon"
	KukeonSystemGroup = "kukeon"

	// DefaultRealmNamespaceSuffix is the in-binary default for the
	// containerd namespace suffix appended to every realm name (without a
	// leading dot — RealmNamespace adds the dot when joining). Operators
	// override it via ServerConfiguration.spec.containerdNamespaceSuffix to
	// run a parallel kukeon instance under a disjoint namespace.
	DefaultRealmNamespaceSuffix = "kukeon.io"

	// DefaultKukeonCgroupRoot is the in-binary default for the cgroup root
	// under which all realms / spaces / stacks / cells live. Operators
	// override it via ServerConfiguration.spec.cgroupRoot.
	DefaultKukeonCgroupRoot = "/kukeon"
)

// RealmNamespaceSuffix is the suffix appended to every realm name to form
// its containerd namespace. Always carries a leading "." so RealmNamespace
// can append it directly to a realm name. Mutated by ConfigureRuntime at
// process start when the operator supplies a non-default suffix via
// ServerConfiguration; subsequent reads from controller / runner code
// observe the configured value through the existing helpers.
//
//nolint:gochecknoglobals // process-wide runtime configuration override
var RealmNamespaceSuffix = "." + DefaultRealmNamespaceSuffix

// KukeonCgroupRoot is the cgroup root under which all realms / spaces /
// stacks / cells live. Mutated by ConfigureRuntime at process start when
// the operator supplies a non-default root via ServerConfiguration.
//
//nolint:gochecknoglobals // process-wide runtime configuration override
var KukeonCgroupRoot = DefaultKukeonCgroupRoot

// ConfigureRuntime overrides the package-level RealmNamespaceSuffix and
// KukeonCgroupRoot for this process. The kukeond daemon and `kuke init`
// call it once after loading ServerConfiguration so realm / cgroup
// derivation downstream observes the operator-configured values.
//
// suffix is the operator-facing form without a leading dot (e.g.
// "kukeon.io" or "dev.kukeon.io"); the leading dot is prepended internally.
// cgroupRoot must be an absolute path under the unified cgroup hierarchy
// (e.g. "/kukeon" or "/kukeon-dev"), trimmed of trailing slashes. Empty or
// malformed inputs return an ErrServerConfigurationInvalid-wrapped error;
// the caller is expected to refuse to start.
func ConfigureRuntime(suffix, cgroupRoot string) error {
	suffix = strings.TrimSpace(suffix)
	if suffix == "" {
		return fmt.Errorf("containerdNamespaceSuffix is empty: %w",
			errdefs.ErrServerConfigurationInvalid)
	}
	if strings.HasPrefix(suffix, ".") || strings.HasSuffix(suffix, ".") {
		return fmt.Errorf(
			"containerdNamespaceSuffix %q must not start or end with '.': %w",
			suffix, errdefs.ErrServerConfigurationInvalid)
	}
	if strings.ContainsAny(suffix, "/ \t\n") {
		return fmt.Errorf(
			"containerdNamespaceSuffix %q contains disallowed character: %w",
			suffix, errdefs.ErrServerConfigurationInvalid)
	}

	originalCgroupRoot := cgroupRoot
	cgroupRoot = strings.TrimSpace(cgroupRoot)
	if cgroupRoot == "" {
		return fmt.Errorf("cgroupRoot is empty: %w",
			errdefs.ErrServerConfigurationInvalid)
	}
	if !strings.HasPrefix(cgroupRoot, "/") {
		return fmt.Errorf(
			"cgroupRoot %q must be an absolute path: %w",
			cgroupRoot, errdefs.ErrServerConfigurationInvalid)
	}
	cgroupRoot = strings.TrimRight(cgroupRoot, "/")
	if cgroupRoot == "" {
		return fmt.Errorf("cgroupRoot %q resolves to root: %w",
			originalCgroupRoot, errdefs.ErrServerConfigurationInvalid)
	}

	RealmNamespaceSuffix = "." + suffix
	KukeonCgroupRoot = cgroupRoot
	return nil
}

// RealmNamespace returns the containerd namespace for a realm: <realm>.kukeon.io.
// This is the only place in the codebase that appends the .kukeon.io suffix to a
// realm name; all bootstrap and user-realm code paths route through it so the
// mapping stays consistent.
func RealmNamespace(realm string) string {
	return realm + RealmNamespaceSuffix
}

// IsKukeonNamespace reports whether ns is a containerd namespace owned by
// kukeon — i.e., one with the canonical .kukeon.io suffix and a non-empty
// realm prefix. Used by the uninstall path to enumerate kukeon namespaces by
// suffix so user-created realms whose on-disk metadata was wiped (issue #193's
// partial-uninstall path) are still purged on a `kuke uninstall`.
func IsKukeonNamespace(ns string) bool {
	if len(ns) <= len(RealmNamespaceSuffix) {
		return false
	}
	return ns[len(ns)-len(RealmNamespaceSuffix):] == RealmNamespaceSuffix
}

// RealmFromNamespace returns the realm name encoded in a containerd namespace
// (the inverse of RealmNamespace). Returns the empty string when ns does not
// have the kukeon suffix or when stripping the suffix would leave nothing.
func RealmFromNamespace(ns string) string {
	if !IsKukeonNamespace(ns) {
		return ""
	}
	return ns[:len(ns)-len(RealmNamespaceSuffix)]
}
