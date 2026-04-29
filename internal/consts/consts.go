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

const (
	KukeonCgroupRoot = "/kukeon"

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

	// RealmNamespaceSuffix is the suffix appended to every realm name to form
	// its containerd namespace. See RealmNamespace and IsKukeonNamespace.
	RealmNamespaceSuffix = ".kukeon.io"

	// KukeonSystemUser and KukeonSystemGroup name the system identity created
	// by `kuke init` so a non-root operator added to the kukeon group can
	// dial the kukeond socket without sudo. Writes under /opt/kukeon still
	// require root; they go through the daemon.
	KukeonSystemUser  = "kukeon"
	KukeonSystemGroup = "kukeon"
)

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
