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
	"os"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
)

const (
	CgroupFilesystemPath = "/sys/fs/cgroup"

	KukeonMetadataFile = "metadata.json"

	// KukeonMetadataSubdir is the basename of the subdirectory under the
	// daemon's RunPath that owns kukeon's realm/space/stack/cell metadata
	// tree. Walkers (ListRealms, the subnet allocator, daemon reset
	// --purge-system) scope themselves to this subtree so non-metadata
	// siblings of the RunPath — e.g. <RunPath>/bin staging kuketty, or the
	// .kukeon-instance.json file — are not mistaken for realm directories.
	KukeonMetadataSubdir = "data"

	// KukeonSecretsSubdir is the basename of the per-scope subdirectory that
	// owns daemon-managed `kind: Secret` bytes (issue #619). It lives inside
	// the scope's metadata directory (e.g. <RunPath>/data/<realm>/secrets/) so
	// the same os.RemoveAll that purge/delete already runs on a scope's
	// metadata dir reclaims its secrets too. Unlike the 0o2750 setgid
	// metadata directories the kuke group can traverse, the secrets directory
	// and the files in it are root-only (0o700 / 0o600).
	KukeonSecretsSubdir = "secrets"

	// KukeonBlueprintsSubdir is the basename of the per-scope subdirectory that
	// owns daemon-managed `kind: CellBlueprint` documents (issue #620). Like
	// the secrets subdir it lives inside the scope's metadata directory (e.g.
	// <RunPath>/data/<realm>/blueprints/) so a scope teardown reclaims it.
	// Unlike secrets, a blueprint carries no credential bytes — only template
	// references — so the directory and files are root-owned but world-readable
	// (0o755 / 0o644).
	KukeonBlueprintsSubdir = "blueprints"

	// KukeonConfigsSubdir is the basename of the per-scope subdirectory that
	// owns daemon-managed `kind: CellConfig` documents (issue #624). Like the
	// blueprints subdir it lives inside the scope's metadata directory (e.g.
	// <RunPath>/data/<realm>/configs/) so a scope teardown reclaims it, and like
	// a blueprint a config carries only references (a blueprint name, repo URLs,
	// secretRefs) — no credential bytes — so the directory and files are
	// root-owned but world-readable (0o755 / 0o644).
	KukeonConfigsSubdir = "configs"

	// KukeonVolumesSubdir is the basename of the per-scope subdirectory that
	// owns daemon-managed `kind: Volume` directories (issue #1018). Like the
	// secrets/blueprints/configs subdirs it lives inside the scope's metadata
	// directory (e.g. <RunPath>/data/<realm>/volumes/) so the same os.RemoveAll
	// that owning-scope cascade purge already runs on a scope's metadata dir
	// reclaims its volumes too. Unlike those kinds — which store a single file
	// per resource — each entry here is itself a directory the daemon
	// provisions container-writable (root-owned, setgid to the kukeon group
	// when configured) so a mounting container can write into it (#1016).
	KukeonVolumesSubdir = "volumes"

	// KukeonVolumeMetaSubdir is the basename of the per-scope subdirectory that
	// holds daemon-owned reclaim manifests for `kind: Volume` resources (step 3,
	// #1237). It is a sibling of KukeonVolumesSubdir under the same scope
	// metadata directory (e.g. <RunPath>/data/<realm>/volume-meta/<name>.json)
	// rather than living inside the volume's own directory: that directory is the
	// container-writable mount target (#1016), so a manifest stored there would
	// both leak into the container mount and be tamperable by the mounting
	// container — neither acceptable for a retention guarantee. A manifest is
	// written only for a `Retain` volume, so a scope with none keeps the step-1
	// blunt-RemoveAll cascade unchanged. Like the other reserved resource
	// subdirs it is excluded from child-scope enumeration (see
	// runner.reservedScopeSubdirs) so it is never mistaken for a phantom scope.
	KukeonVolumeMetaSubdir = "volume-meta"

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

	// KukeonSocketSymlinkSubdir is the basename of the per-RunPath directory
	// that holds SUN_PATH-safe symlinks to the deep per-container kuketty
	// sockets (`<RunPath>/data/<realm>/<space>/<stack>/<cell>/<container>/
	// tty/socket`). Linux `connect(2)` caps the path passed in
	// sockaddr_un.sun_path at 108 bytes including the terminator (issue #521),
	// and the deep metadata-rooted path overflows on long RunPaths or
	// long realm/space/stack/cell IDs. The daemon stages a short symlink at
	// `<RunPath>/s/<short>` at provision time; `kuke attach` connects via
	// the symlink so the literal sun_path stays SUN_PATH-safe regardless of
	// how deep the resolved target lives. Single-letter basename to spend
	// the budget on the per-container short id, not the directory.
	KukeonSocketSymlinkSubdir = "s"

	// KukeonMaxSocketPath is Linux's UNIX_PATH_MAX minus the terminating
	// NUL — the maximum number of bytes a path passed to `connect(2)` on a
	// unix socket may occupy in sockaddr_un.sun_path. The daemon refuses to
	// provision an Attachable container whose resolved host-side socket
	// path would exceed this so the failure surfaces at provision time
	// rather than at first `kuke attach` (issue #521).
	KukeonMaxSocketPath = 107

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

	// KukeonContainerKukettyLogFile is the basename of the per-Attachable-
	// container kuketty wrapper's own slog output, inside KukeonContainerTTYDir
	// (peer to the socket and capture files, same bind-mount visibility). The
	// path is daemon-controlled — operators do not pick it, the same way they
	// do not pick where `capture` lands — so kuketty always writes here and an
	// operator who needs to debug an attach session knows exactly where to
	// look. Distinct from the workload-capture file (KukeonContainerCaptureFile,
	// "capture"), which carries the workload's tty byte stream and stays
	// opt-in for the workload side. Issue #599.
	KukeonContainerKukettyLogFile = "kuketty.log"

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

	// KukeonRunDirMode is the mode applied to the kukeond socket's parent
	// directory (the /run/kukeon bind-mount source) by `kuke init` and
	// re-asserted by `kuke daemon start`/`restart` so the directory survives
	// a tmpfs clear (host reboot). The SGID bit makes files the daemon later
	// writes there inherit the kukeon group instead of root:root; 0o750 lets
	// the kukeon group traverse without world access. Single source of truth
	// shared by the init bootstrap and the daemon start/restart recreate path
	// so the two cannot drift on what `kuke init` produces.
	KukeonRunDirMode os.FileMode = os.ModeSetgid | 0o750

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

	// KukebuildBaseDir is the directory under which `kukebuild` keeps its
	// per-namespace BuildKit state (`cache.db`, `history.db`, snapshot
	// metadata) — one subdirectory per containerd namespace it has built
	// into, at <KukebuildBaseDir>/<namespace>. `kuke uninstall` removes the
	// matching subdir for every realm it successfully purges; the BuildKit
	// cache references containerd by snapshot ID and content digest, so a
	// purged namespace whose cache survives strands the next `kuke build`
	// with "parent snapshot does not exist" or "content digest ... not
	// found" (issue #904).
	//
	// Must mirror the `defaultBuildRoot` constant in cmd/kukebuild/main.go;
	// the two cannot be a single source of truth because cmd/kukebuild is a
	// separate Go module (its go-1.25 BuildKit closure is deliberately
	// disjoint from the root module's graph). On change, update both.
	KukebuildBaseDir = "/var/lib/kukebuild"

	// InternalImageRegistry is the reserved, non-routable "host" every
	// locally-built `kuke team init --build` image is tagged under. It is
	// ICANN's `.internal` private-use TLD, so a pull against it can never
	// accidentally reach a real registry. Three subsystems key off this one
	// definition so they cannot drift:
	//   - the build path (internal/teambuild) tags each built image
	//     <InternalImageRegistry>/<name>:<version> and threads it as the
	//     `--build-arg REGISTRY=…` so leaf FROMs resolve to the in-realm base;
	//   - the bind path (internal/teamrender) binds the cell blueprint's image
	//     to the same ref in `--build` mode (vs the catalog's published image);
	//   - the runtime resolver (internal/ctr) treats refs hosted here as
	//     local-only — never pulled, with a clear "build it" error on a miss.
	InternalImageRegistry = "kukeon.internal"
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

// InternalImageRef composes the full image reference a locally-built team
// image lands under: <InternalImageRegistry>/<name>:<version>. The build path
// (internal/teambuild) tags with it and the bind path (internal/teamrender)
// binds it, so both share this one formatter and the bound ref always matches
// the tag that was built.
func InternalImageRef(name, version string) string {
	return InternalImageRegistry + "/" + name + ":" + version
}

// IsInternalImageRef reports whether ref is hosted under InternalImageRegistry
// — i.e. a local-only `kuke team init --build` image that must never be pulled
// from a network registry. The runtime resolver (internal/ctr) uses this to
// short-circuit the registry pull on a local miss and surface a "build it"
// error instead.
func IsInternalImageRef(ref string) bool {
	return strings.HasPrefix(strings.TrimSpace(ref), InternalImageRegistry+"/")
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

// BuildKitHistoryNamespaceSuffix is the suffix BuildKit (driven by kukebuild,
// BuildKit-as-library) appends to a containerd namespace to form its companion
// history-store namespace. BuildKit's solver/llbsolver/history.go derives it as
// ns + "_history", so a `kuke build` into realm R leaves both R's namespace and
// <R-namespace>_history on the host. `kuke uninstall` drains and removes the
// companion alongside the realm namespace, and `kuke status` flags a stray one
// as residue (issue #1183).
const BuildKitHistoryNamespaceSuffix = "_history"

// BuildKitHistoryNamespace returns the BuildKit history-store companion
// namespace for a containerd namespace (the realm namespace -> <ns>_history).
func BuildKitHistoryNamespace(ns string) string {
	return ns + BuildKitHistoryNamespaceSuffix
}

// IsBuildKitHistoryNamespace reports whether ns is a BuildKit history-store
// companion of a kukeon-managed namespace — i.e. it carries the _history suffix
// and, with that suffix stripped, the remainder is itself a kukeon namespace.
// Non-kukeon companions (e.g. "moby_history") are excluded so this path never
// claims a co-tenant's namespace.
func IsBuildKitHistoryNamespace(ns string) bool {
	if !strings.HasSuffix(ns, BuildKitHistoryNamespaceSuffix) {
		return false
	}
	return IsKukeonNamespace(ns[:len(ns)-len(BuildKitHistoryNamespaceSuffix)])
}
