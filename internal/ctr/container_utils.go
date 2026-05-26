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
	"strings"

	"github.com/containerd/containerd/v2/pkg/oci"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

const (
	// DefaultRootContainerImage is the image used when none is provided.
	DefaultRootContainerImage = "docker.io/library/busybox:latest"
	defaultRootContainerCmd   = "sleep"
	defaultRootContainerArg   = "infinity"

	rootContainerLabelKey   = "kukeon.io/container-type"
	rootContainerLabelValue = "root"
)

// DefaultRootContainerSpec returns a minimal ContainerSpec suitable for keeping
// the root container alive while other workload containers are managed.
// containerdID is the hierarchical ID used for containerd operations.
// The ID field will be set to "root" (base name).
func DefaultRootContainerSpec(
	containerdID,
	cellID,
	realmID,
	spaceID,
	stackID,
	cniConfigPath string,
) intmodel.ContainerSpec {
	return intmodel.ContainerSpec{
		ID:            "root",
		ContainerdID:  containerdID,
		CellName:      cellID,
		RealmName:     realmID,
		SpaceName:     spaceID,
		StackName:     stackID,
		Root:          true,
		Image:         DefaultRootContainerImage,
		Command:       defaultRootContainerCmd,
		Args:          []string{defaultRootContainerArg},
		CNIConfigPath: cniConfigPath,
	}
}

// BuildRootContainerSpec converts the internal root container spec into an
// internal ctr.ContainerSpec with the expected defaults applied.
// Uses ContainerdID if available, otherwise falls back to ID.
//
// Variadic BuildOption is honored for the daemon-wide knobs that also affect
// non-root containers — today the daemon-default memory cap (issue #531).
// Per-spec-only BuildOptions like WithAttachableInjection have no effect on a
// root container spec.
func BuildRootContainerSpec(
	rootSpec intmodel.ContainerSpec,
	labels map[string]string,
	options ...BuildOption,
) ContainerSpec {
	var opts buildOpts
	for _, apply := range options {
		apply(&opts)
	}
	// Use ContainerdID if available, otherwise fall back to ID
	containerdID := rootSpec.ContainerdID
	if containerdID == "" {
		containerdID = rootSpec.ID
	}

	image := rootSpec.Image
	if image == "" {
		image = DefaultRootContainerImage
	}

	specOpts := []oci.SpecOpts{
		oci.WithDefaultPathEnv,
	}

	// Hostname identifies the cell, not the hierarchical containerd ID. All
	// containers in the cell share this root's UTS namespace via
	// JoinContainerNamespaces, so the cell-name hostname is what `hostname`
	// returns for every container. Falls back to the containerd ID only when
	// the runner failed to populate CellName (defensive — every CreateCell /
	// StartCell path stamps it). Issue #345.
	hostname := strings.TrimSpace(rootSpec.CellName)
	if hostname == "" {
		hostname = containerdID
	}
	if hostname != "" {
		specOpts = append(specOpts, oci.WithHostname(hostname))
	}

	// Per-cell /etc/hostname and /etc/hosts bind-mounts (issue #345). The
	// runner renders both files under the cell's metadata directory before
	// invoking this builder; an empty path here means the bind-mount is
	// disabled (e.g. host-network root containers like kukeond, where the
	// host's /etc/hosts is the right view).
	if mounts := etcFileBindMounts(rootSpec.EtcHostsPath, rootSpec.EtcHostnamePath); len(mounts) > 0 {
		specOpts = append(specOpts, oci.WithMounts(mounts))
	}

	if processArgs := buildRootProcessArgs(rootSpec); len(processArgs) > 0 {
		specOpts = append(specOpts, oci.WithProcessArgs(processArgs...))
	}

	if rootSpec.WorkingDir != "" {
		specOpts = append(specOpts, oci.WithProcessCwd(rootSpec.WorkingDir))
	}

	// KUKEON_* identity vars (issue #351) are merged with the user-supplied
	// rootSpec.Env on the same rules as BuildContainerSpec: user entries win
	// on key collisions, empty cell-context fields contribute nothing.
	if env := kukeonContainerEnv(rootSpec); len(env) > 0 {
		specOpts = append(specOpts, oci.WithEnv(env))
	}

	if rootSpec.Privileged {
		specOpts = append(specOpts, oci.WithPrivileged)
	}

	// Host network: drop the network LinuxNamespace entry from the OCI spec so
	// the container shares the host's netns. The runner separately skips CNI
	// attach for these containers (no per-container veth to wire up).
	if rootSpec.HostNetwork {
		specOpts = append(specOpts, oci.WithHostNamespace(runtimespec.NetworkNamespace))
	}

	// Host PID: drop the PID LinuxNamespace entry so the root container
	// shares the host's PID namespace. Mirrors the BuildContainerSpec rule
	// so an explicit HostPID=true on a user-supplied root spec is honored.
	if rootSpec.HostPID {
		specOpts = append(specOpts, oci.WithHostNamespace(runtimespec.PIDNamespace))
	}

	// Cgroup namespace: append the entry on the default (private) path so
	// the root container is cgroup-isolated; omit it when HostCgroup=true
	// so the root joins its parent's cgroup-ns. Mirrors the
	// BuildContainerSpec rule for the root-container builder used by the
	// kukeond cell, where HostCgroup=true is the carve-out.
	if !rootSpec.HostCgroup {
		specOpts = append(specOpts, oci.WithLinuxNamespace(runtimespec.LinuxNamespace{
			Type: runtimespec.CgroupNamespace,
		}))
	}

	// NestedCgroupRuntime mount: mirrors the BuildContainerSpec path so a
	// non-default root container in a NestedCgroupRuntime cell still gets
	// /sys/fs/cgroup populated from its delegated subtree (issue #322). The
	// kukeond cell's own root sets HostCgroup=true and is exempted by the
	// guard; the daemon already gets cgroup2 visibility via the bootstrap
	// bind-mount path in internal/controller/bootstrap.go.
	if rootSpec.NestedCgroupRuntime && !rootSpec.HostCgroup {
		specOpts = append(specOpts, oci.WithMounts([]runtimespec.Mount{nestedCgroupMount()}))
	}

	// Bind mounts and security/isolation fields share the same translators as
	// BuildContainerSpec so a user-supplied root container (RootContainerID)
	// keeps its Volumes, User, Capabilities, SecurityOpts, Tmpfs, and
	// Resources instead of having them silently dropped.
	if mounts := buildVolumeMounts(rootSpec.Volumes); len(mounts) > 0 {
		specOpts = append(specOpts, oci.WithMounts(mounts))
	}

	// Linux.CgroupsPath: same cell-rooted placement as BuildContainerSpec so
	// the root container task lands inside the cell's cgroup subtree rather
	// than the runc-shim default (issue #312).
	if cgPath := cellCgroupsPath(rootSpec.CellCgroupPath, containerdID); cgPath != "" {
		specOpts = append(specOpts, oci.WithCgroup(cgPath))
	}

	specOpts = append(specOpts, securitySpecOpts(rootSpec)...)

	// Mirror BuildContainerSpec's kukeon-group-GID hop so a non-default root
	// container that runs as a non-root user (a user-supplied RootContainerID
	// image) can still reach kukeon-group-owned bind-mounts. Zero is a no-op.
	if opts.kukeonGroupGID > 0 {
		specOpts = append(specOpts, withKukeonGroupGIDSpecOpt(opts.kukeonGroupGID))
	}

	// Daemon-default memory cap: mirrors BuildContainerSpec so the same fallback
	// reaches root containers when the daemon is configured with one (#531).
	// The spec wins when it already carries a positive limit.
	if opts.defaultMemoryLimitBytes > 0 && !specHasMemoryLimit(rootSpec) {
		//nolint:gosec // bounded by the > 0 guard above and clamped to >= 0 in cmd/kukeond/serve.go
		specOpts = append(specOpts, oci.WithMemoryLimit(uint64(opts.defaultMemoryLimitBytes)))
	}

	rootLabels := copyLabels(labels)
	rootLabels[rootContainerLabelKey] = rootContainerLabelValue

	return ContainerSpec{
		ID:            containerdID,
		Image:         image,
		Labels:        rootLabels,
		SpecOpts:      specOpts,
		CNIConfigPath: rootSpec.CNIConfigPath,
	}
}

func buildRootProcessArgs(rootSpec intmodel.ContainerSpec) []string {
	switch {
	case rootSpec.Command != "":
		args := []string{rootSpec.Command}
		if len(rootSpec.Args) > 0 {
			args = append(args, rootSpec.Args...)
		}
		return args
	case len(rootSpec.Args) > 0:
		args := make([]string, len(rootSpec.Args))
		copy(args, rootSpec.Args)
		return args
	default:
		return []string{defaultRootContainerCmd, defaultRootContainerArg}
	}
}

func copyLabels(src map[string]string) map[string]string {
	if len(src) == 0 {
		return map[string]string{
			rootContainerLabelKey: rootContainerLabelValue,
		}
	}
	dst := make(map[string]string, len(src)+1)
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// NormalizeImageReference normalizes an image reference to a fully qualified format.
// Examples:
//   - "debian:latest" -> "docker.io/library/debian:latest"
//   - "alpine" -> "docker.io/library/alpine:latest"
//   - "user/image:tag" -> "docker.io/user/image:tag"
//   - "docker.io/library/debian:latest" -> "docker.io/library/debian:latest" (unchanged)
//   - "registry.example.com/image:tag" -> "registry.example.com/image:tag" (unchanged)
func NormalizeImageReference(image string) string {
	if image == "" {
		return image
	}

	// If it already contains a registry (has "://" or starts with a known registry), return as-is
	if strings.Contains(image, "://") {
		return image
	}

	// Check if it starts with a known registry (contains a dot before the first slash, or is a known registry)
	firstSlash := strings.Index(image, "/")
	if firstSlash > 0 {
		// Check if the part before the first slash looks like a registry (contains a dot or is a known registry)
		registryPart := image[:firstSlash]
		if strings.Contains(registryPart, ".") || strings.Contains(registryPart, ":") {
			// Already has a registry, return as-is
			return image
		}
		// No registry, add docker.io prefix
		return "docker.io/" + image
	}

	// No slash means it's a library image
	// Add default tag if not present
	if !strings.Contains(image, ":") {
		image += ":latest"
	}
	return "docker.io/library/" + image
}
