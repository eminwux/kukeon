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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/pkg/oci"
	"github.com/containerd/typeurl/v2"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

// JoinContainerNamespaces returns a copy of spec with namespace spec options applied.
func JoinContainerNamespaces(spec ContainerSpec, ns NamespacePaths) ContainerSpec {
	specCopy := spec
	specCopy.SpecOpts = cloneSpecOpts(spec.SpecOpts)
	specCopy.SpecOpts = append(specCopy.SpecOpts, namespaceSpecOpts(ns)...)
	return specCopy
}

func cloneSpecOpts(opts []oci.SpecOpts) []oci.SpecOpts {
	if len(opts) == 0 {
		return nil
	}
	cloned := make([]oci.SpecOpts, len(opts))
	copy(cloned, opts)
	return cloned
}

func namespaceSpecOpts(ns NamespacePaths) []oci.SpecOpts {
	var opts []oci.SpecOpts
	if ns.Net != "" {
		opts = append(opts, withNamespacePathOpt(runtimespec.NetworkNamespace, ns.Net))
	}
	if ns.IPC != "" {
		opts = append(opts, withNamespacePathOpt(runtimespec.IPCNamespace, ns.IPC))
	}
	if ns.UTS != "" {
		opts = append(opts, withNamespacePathOpt(runtimespec.UTSNamespace, ns.UTS))
	}
	if ns.PID != "" {
		opts = append(opts, withNamespacePathOpt(runtimespec.PIDNamespace, ns.PID))
	}
	return opts
}

func withNamespacePathOpt(nsType runtimespec.LinuxNamespaceType, path string) oci.SpecOpts {
	return func(_ context.Context, _ oci.Client, _ *containers.Container, s *runtimespec.Spec) error {
		if s.Linux == nil {
			s.Linux = &runtimespec.Linux{}
		}

		for i := range s.Linux.Namespaces {
			if s.Linux.Namespaces[i].Type == nsType {
				s.Linux.Namespaces[i].Path = path
				return nil
			}
		}

		s.Linux.Namespaces = append(s.Linux.Namespaces, runtimespec.LinuxNamespace{
			Type: nsType,
			Path: path,
		})
		return nil
	}
}

func (c *client) applySpecOpts(namespace string, container containerd.Container, opts []oci.SpecOpts) error {
	if len(opts) == 0 {
		return nil
	}

	nsCtx := c.namespaceCtx(namespace)
	ociSpec, err := container.Spec(nsCtx)
	if err != nil {
		return fmt.Errorf("failed to load container spec: %w", err)
	}

	// Clear any existing namespace paths that might be stale
	// Namespace paths should be set dynamically when starting containers, not stored in the spec
	if ociSpec.Linux != nil && len(ociSpec.Linux.Namespaces) > 0 {
		for i := range ociSpec.Linux.Namespaces {
			// Clear paths for namespaces that are typically set dynamically (Net, IPC, UTS)
			// Keep PID namespace and other namespace types as-is since they're usually not set via paths
			switch ociSpec.Linux.Namespaces[i].Type {
			case runtimespec.NetworkNamespace, runtimespec.IPCNamespace, runtimespec.UTSNamespace:
				ociSpec.Linux.Namespaces[i].Path = ""
			case runtimespec.PIDNamespace,
				runtimespec.MountNamespace,
				runtimespec.UserNamespace,
				runtimespec.CgroupNamespace,
				runtimespec.TimeNamespace:
				// Other namespace types (PID, Mount, User, Cgroup, Time) are left unchanged
			}
		}
	}

	cc := c.conn()
	for _, opt := range opts {
		if err = opt(nsCtx, cc, nil, ociSpec); err != nil {
			return fmt.Errorf("failed to apply spec option: %w", err)
		}
	}

	if err = container.Update(nsCtx, withUpdatedSpec(ociSpec)); err != nil {
		return fmt.Errorf("failed to persist updated spec: %w", err)
	}
	return nil
}

// ContainerProcessUID returns the resolved process.User.UID from the
// container's OCI runtime spec. The spec carries the post-resolution UID
// after CreateContainerFromSpec has run — containerd's WithImageConfig
// (and any caller-supplied WithUser) populates Process.User by parsing the
// image's USER directive against the rootfs's /etc/passwd, so this returns
// the actual numeric uid even when the image specified a username like
// "claude". Returns an error if the spec cannot be loaded or has no
// Process — both indicate the container was not created or was destroyed
// between create and this call.
func (c *client) ContainerProcessUID(namespace string, container containerd.Container) (uint32, error) {
	if container == nil {
		return 0, errors.New("container is nil")
	}
	nsCtx := c.namespaceCtx(namespace)
	spec, err := container.Spec(nsCtx)
	if err != nil {
		return 0, fmt.Errorf("read container spec: %w", err)
	}
	if spec == nil || spec.Process == nil {
		return 0, errors.New("container spec has no Process")
	}
	return spec.Process.User.UID, nil
}

func withUpdatedSpec(spec *oci.Spec) containerd.UpdateContainerOpts {
	return func(_ context.Context, _ *containerd.Client, c *containers.Container) error {
		if spec == nil {
			return errors.New("oci spec is nil")
		}
		anySpec, err := typeurl.MarshalAnyToProto(spec)
		if err != nil {
			return err
		}
		c.Spec = anySpec
		return nil
	}
}

// BuildOption customizes BuildContainerSpec without changing its return type.
// Used for caller-provided values that don't live on the model spec — today
// the host-side paths required when ContainerSpec.Attachable is true, and
// the daemon-wide fallback memory limit applied when the spec carries none.
type BuildOption func(*buildOpts)

type buildOpts struct {
	attachable              AttachableInjection
	defaultMemoryLimitBytes int64
	secretRunPath           string
}

// WithAttachableInjection configures the host-side paths used when wrapping
// an Attachable container. Has no effect on a spec where Attachable is
// false; in that case the option is silently ignored so callers can pass it
// unconditionally.
func WithAttachableInjection(inj AttachableInjection) BuildOption {
	return func(o *buildOpts) {
		o.attachable = inj
	}
}

// WithDefaultMemoryLimit configures a daemon-wide fallback memory limit (in
// bytes). The limit is applied via oci.WithMemoryLimit only when the
// ContainerSpec does not already carry a positive Resources.MemoryLimitBytes
// — an explicit per-container value always wins. A zero or negative argument
// is a no-op so callers can pass it unconditionally. Issue #531.
func WithDefaultMemoryLimit(bytes int64) BuildOption {
	return func(o *buildOpts) {
		if bytes > 0 {
			o.defaultMemoryLimitBytes = bytes
		}
	}
}

// WithSecretRunPath carries the daemon's RunPath into CreateContainerFromSpec
// so a ContainerSecret with a secretRef can be resolved from the referenced
// scope's secrets tree (<RunPath>/data/<scope>/secrets/<name>, issue #619).
// Has no effect on BuildContainerSpec itself — the value is consumed by
// resolveSecrets before the OCI spec is built. An empty argument is a no-op so
// callers can pass it unconditionally; specs that declare no secretRef never
// touch the path. Issue #623.
func WithSecretRunPath(runPath string) BuildOption {
	return func(o *buildOpts) {
		if runPath != "" {
			o.secretRunPath = runPath
		}
	}
}

// specHasMemoryLimit reports whether the spec already declares a positive
// per-container memory limit. Used by BuildContainerSpec /
// BuildRootContainerSpec to decide whether a daemon-default cap applies.
func specHasMemoryLimit(spec intmodel.ContainerSpec) bool {
	return spec.Resources != nil &&
		spec.Resources.MemoryLimitBytes != nil &&
		*spec.Resources.MemoryLimitBytes > 0
}

// BuildContainerSpec converts an internal ContainerSpec to ctr.ContainerSpec
// with the expected defaults applied.
// Uses ContainerdID if available, otherwise falls back to ID.
func BuildContainerSpec(
	containerSpec intmodel.ContainerSpec,
	options ...BuildOption,
) ContainerSpec {
	var opts buildOpts
	for _, apply := range options {
		apply(&opts)
	}
	// Use ContainerdID if available, otherwise fall back to ID
	containerdID := containerSpec.ContainerdID
	if containerdID == "" {
		containerdID = containerSpec.ID
	}

	cellID := containerSpec.CellName
	spaceID := containerSpec.SpaceName
	realmID := containerSpec.RealmName
	stackID := containerSpec.StackName

	// Build labels
	labels := make(map[string]string)
	// Add kukeon-specific labels
	labels["kukeon.io/container-type"] = "container"
	labels["kukeon.io/cell"] = cellID
	labels["kukeon.io/space"] = spaceID
	labels["kukeon.io/realm"] = realmID
	labels["kukeon.io/stack"] = stackID

	// Build OCI spec options
	specOpts := []oci.SpecOpts{
		oci.WithDefaultPathEnv,
	}

	// Hostname is set on the root container only; non-root containers inherit
	// it via the shared UTS namespace established by JoinContainerNamespaces.
	// See BuildRootContainerSpec for the WithHostname call site (issue #345).

	// Per-cell /etc/hostname and /etc/hosts bind-mounts (issue #345). The host
	// source files are rendered by the runner under the cell's metadata
	// directory and reflect the cell name (and, for /etc/hosts, the CNI-
	// assigned cell IP) so tools that resolve the container's own hostname
	// (sudo, getent) work without timing out on DNS.
	if mounts := etcFileBindMounts(containerSpec.EtcHostsPath, containerSpec.EtcHostnamePath); len(mounts) > 0 {
		specOpts = append(specOpts, oci.WithMounts(mounts))
	}

	// Set command and args
	if containerSpec.Command != "" {
		args := []string{containerSpec.Command}
		if len(containerSpec.Args) > 0 {
			args = append(args, containerSpec.Args...)
		}
		specOpts = append(specOpts, oci.WithProcessArgs(args...))
	} else if len(containerSpec.Args) > 0 {
		specOpts = append(specOpts, oci.WithProcessArgs(containerSpec.Args...))
	}

	// Set working directory (OCI process.cwd). Empty leaves the image's
	// WORKDIR untouched — containerd's WithImageConfig populates Cwd from
	// the image at create time, and overwriting it with "" would erase that.
	if containerSpec.WorkingDir != "" {
		specOpts = append(specOpts, oci.WithProcessCwd(containerSpec.WorkingDir))
	}

	// Set environment variables. KUKEON_* identity vars (issue #351) are
	// merged with the user-supplied containerSpec.Env, with user entries
	// taking precedence on key collisions so an explicit override in a
	// CellProfile still wins.
	if env := kukeonContainerEnv(containerSpec); len(env) > 0 {
		specOpts = append(specOpts, oci.WithEnv(env))
	}

	// Set privileged mode if specified
	if containerSpec.Privileged {
		specOpts = append(specOpts, oci.WithPrivileged)
	}

	// Host network: drop the network LinuxNamespace entry from the OCI spec so
	// the container shares the host's netns. The runner separately skips CNI
	// attach for these containers (no per-container veth to wire up).
	if containerSpec.HostNetwork {
		specOpts = append(specOpts, oci.WithHostNamespace(runtimespec.NetworkNamespace))
	}

	// Host PID: drop the PID LinuxNamespace entry so the container shares the
	// host's PID namespace and /proc reflects host PIDs. Required for kukeond
	// because the CNI bridge plugin running inside it resolves netns paths
	// from host PIDs that containerd returns via task.Pid().
	if containerSpec.HostPID {
		specOpts = append(specOpts, oci.WithHostNamespace(runtimespec.PIDNamespace))
	}

	// Cgroup namespace: cgroup-ns is the inverse of HostNetwork/HostPID — the
	// OCI default leaves it shared with the parent, so the explicit branch
	// here *adds* the LinuxNamespace entry on the safer (private) path and
	// omits it on opt-in. Private cgroup-ns is what lets a nested runtime
	// (kuke init, dockerd, podman, an inner containerd) write its cgroup
	// tree under the cell instead of trampling the host's cgroup root, and
	// is what clears runc's "cgroup not empty" precheck on cgroup-v2 hosts
	// without systemd. See ContainerSpec.HostCgroup for the operator-facing
	// rationale.
	if !containerSpec.HostCgroup {
		specOpts = append(specOpts, oci.WithLinuxNamespace(runtimespec.LinuxNamespace{
			Type: runtimespec.CgroupNamespace,
		}))
	}

	// NestedCgroupRuntime: pair the private cgroup-ns with a cgroup2 mount
	// so an inner runtime (dockerd, podman, an inner containerd, systemd)
	// can read the controller list that #318's EnableCellAllSubtreeControllers
	// just delegated host-side. Without this, /sys/fs/cgroup is an empty
	// mountpoint inside the cell and dockerd's "Devices cgroup isn't
	// mounted" probe aborts (issue #322). Gated on !HostCgroup because a
	// HostCgroup cell already shares the host's cgroup hierarchy through
	// kukeond's own bind-mount path; emitting a private cgroup mount on
	// top would shadow it.
	if containerSpec.NestedCgroupRuntime && !containerSpec.HostCgroup {
		specOpts = append(specOpts, oci.WithMounts([]runtimespec.Mount{nestedCgroupMount()}))
	}

	if mounts := buildVolumeMounts(containerSpec.Volumes); len(mounts) > 0 {
		specOpts = append(specOpts, oci.WithMounts(mounts))
	}

	// Linux.CgroupsPath: place the container task inside the cell's cgroup
	// subtree so cell-level resource accounting and limits actually constrain
	// it. Without this, containerd's runc-shim default places the task under
	// /<containerd-namespace>/<id>/, leaving the kukeon cgroup hierarchy
	// decorative (issue #312).
	if cgPath := cellCgroupsPath(containerSpec.CellCgroupPath, containerdID); cgPath != "" {
		specOpts = append(specOpts, oci.WithCgroup(cgPath))
	}

	specOpts = append(specOpts, securitySpecOpts(containerSpec)...)

	// Daemon-default memory cap: applies only when the spec does not already
	// carry a positive Resources.MemoryLimitBytes, so an explicit per-
	// container limit always wins. Closes the "container admitted with
	// memory.max=max" gap on no-swap, no-userspace-OOM hosts (issue #531).
	if opts.defaultMemoryLimitBytes > 0 && !specHasMemoryLimit(containerSpec) {
		//nolint:gosec // bounded by the > 0 guard above and clamped to >= 0 in cmd/kukeond/serve.go
		specOpts = append(specOpts, oci.WithMemoryLimit(uint64(opts.defaultMemoryLimitBytes)))
	}

	// Attachable wrapping is appended last so the args-wrap runs after any
	// user-supplied WithProcessArgs above and after the image's ENTRYPOINT/CMD
	// resolution that containerd applies at container-create time.
	if containerSpec.Attachable {
		specOpts = append(
			specOpts,
			withAttachableMounts(opts.attachable),
			withAttachableArgsWrap(opts.attachable),
		)
	}

	return ContainerSpec{
		ID:            containerdID,
		Image:         containerSpec.Image,
		Labels:        labels,
		SpecOpts:      specOpts,
		CNIConfigPath: containerSpec.CNIConfigPath,
	}
}

// etcFileBindMounts returns OCI bind-mount entries for the per-cell
// /etc/hosts and /etc/hostname files, skipping each entry whose host source
// path is empty. Both files are bind-mounted read-only because containers
// have no business mutating identity files the runtime owns; the runner
// re-renders the source on every cell start so updated content propagates
// through the bind without remounting (issue #345).
func etcFileBindMounts(hostsPath, hostnamePath string) []runtimespec.Mount {
	var mounts []runtimespec.Mount
	if hostsPath != "" {
		mounts = append(mounts, runtimespec.Mount{
			Destination: "/etc/hosts",
			Source:      hostsPath,
			Type:        "bind",
			Options:     []string{"rbind", "ro"},
		})
	}
	if hostnamePath != "" {
		mounts = append(mounts, runtimespec.Mount{
			Destination: "/etc/hostname",
			Source:      hostnamePath,
			Type:        "bind",
			Options:     []string{"rbind", "ro"},
		})
	}
	return mounts
}

// nestedCgroupMount returns the OCI Mount entry that exposes the cell's
// delegated cgroup2 subtree at /sys/fs/cgroup inside the container. runc
// resolves a Type:"cgroup" entry under a private cgroup-ns to the calling
// process's cgroup root, which is exactly the scope #318's host-side
// subtree-controller delegation prepared. Options match what systemd and
// dockerd write for cgroup2 mounts.
func nestedCgroupMount() runtimespec.Mount {
	return runtimespec.Mount{
		Destination: "/sys/fs/cgroup",
		Source:      "cgroup",
		Type:        "cgroup",
		Options:     []string{"rw", "nosuid", "noexec", "nodev"},
	}
}

// kukeonContainerEnv builds the final container env: a set of KUKEON_*
// identity vars derived from the container spec's cell-context fields, plus
// the GIT_AUTHOR_* / GIT_COMMITTER_* / GIT_CONFIG_* block expanded from
// spec.Git (issue #618), all merged with the user-supplied spec.Env. User
// entries override defaults on key collisions; empty cell-context fields and
// an absent git block produce no entries. Order in the returned slice is:
// surviving defaults in canonical order, then user entries in their declared
// order. The merge is done here (rather than
// trusting oci.WithEnv) because containerd's replaceOrAppendEnvValues
// dedupes only against keys already present in spec.Process.Env when WithEnv
// runs, so two entries with the same key inside a single overrides slice
// would both end up in the final env. Issue #351.
func kukeonContainerEnv(spec intmodel.ContainerSpec) []string {
	defaults := kukeonDefaultEnv(spec)
	defaults = append(defaults, gitEnv(spec.Git)...)
	switch {
	case len(spec.Env) == 0:
		return defaults
	case len(defaults) == 0:
		return spec.Env
	}
	userKeys := make(map[string]struct{}, len(spec.Env))
	for _, kv := range spec.Env {
		k, _, _ := strings.Cut(kv, "=")
		userKeys[k] = struct{}{}
	}
	out := make([]string, 0, len(defaults)+len(spec.Env))
	for _, kv := range defaults {
		k, _, _ := strings.Cut(kv, "=")
		if _, ok := userKeys[k]; ok {
			continue
		}
		out = append(out, kv)
	}
	out = append(out, spec.Env...)
	return out
}

// kukeonDefaultEnv returns the KUKEON_* identity entries that describe the
// container's cell context. Each field contributes one entry only when set;
// the order is profile → cell → container ID → realm/space/stack →
// cgroup-path so `env | grep KUKEON_` reads top-down from the broadest
// identity to the narrowest. Issue #351.
func kukeonDefaultEnv(spec intmodel.ContainerSpec) []string {
	pairs := []struct{ key, value string }{
		{"KUKEON_CELL_PROFILE_NAME", spec.CellProfileName},
		{"KUKEON_CELL_NAME", spec.CellName},
		{"KUKEON_CONTAINER_ID", spec.ID},
		{"KUKEON_REALM", spec.RealmName},
		{"KUKEON_SPACE", spec.SpaceName},
		{"KUKEON_STACK", spec.StackName},
		{"KUKEON_CGROUP_PATH", spec.CellCgroupPath},
	}
	out := make([]string, 0, len(pairs))
	for _, p := range pairs {
		if p.value == "" {
			continue
		}
		out = append(out, p.key+"="+p.value)
	}
	return out
}

// gitEnv expands a container's git identity/signing sugar (ContainerSpec.Git)
// into the GIT_AUTHOR_* / GIT_COMMITTER_* / GIT_CONFIG_* env-var block git
// reads natively, replacing the hand-rolled block crew templates duplicate
// per cell today. Returns nil when git is unset. The GIT_CONFIG_* signing
// pairs are emitted in crew's canonical order — user.signingkey, gpg.format,
// commit.gpgsign, tag.gpgsign, gpg.ssh.allowedSignersFile — with
// GIT_CONFIG_COUNT tracking the live pair count, so a block that omits signing
// renders no GIT_CONFIG_* entries at all. gpg.format=ssh is implied by a
// non-empty signingKey (kukeon signs with SSH keys). The returned entries are
// merged with the user's explicit env by kukeonContainerEnv, where an explicit
// env: entry wins on key collision. Issue #618.
func gitEnv(git *intmodel.ContainerGit) []string {
	if git == nil {
		return nil
	}
	var out []string
	if git.Author != nil {
		out = append(out,
			"GIT_AUTHOR_NAME="+git.Author.Name,
			"GIT_AUTHOR_EMAIL="+git.Author.Email,
		)
	}
	if git.Committer != nil {
		out = append(out,
			"GIT_COMMITTER_NAME="+git.Committer.Name,
			"GIT_COMMITTER_EMAIL="+git.Committer.Email,
		)
	}

	type configPair struct{ key, value string }
	var pairs []configPair
	if git.SigningKey != "" {
		pairs = append(pairs,
			configPair{"user.signingkey", git.SigningKey},
			configPair{"gpg.format", "ssh"},
		)
	}
	for _, s := range git.Sign {
		switch s {
		case intmodel.GitSignCommits:
			pairs = append(pairs, configPair{"commit.gpgsign", "true"})
		case intmodel.GitSignTags:
			pairs = append(pairs, configPair{"tag.gpgsign", "true"})
		}
	}
	if git.AllowedSigners != "" {
		pairs = append(pairs, configPair{"gpg.ssh.allowedSignersFile", git.AllowedSigners})
	}
	if len(pairs) > 0 {
		out = append(out, "GIT_CONFIG_COUNT="+strconv.Itoa(len(pairs)))
		for i, p := range pairs {
			n := strconv.Itoa(i)
			out = append(out,
				"GIT_CONFIG_KEY_"+n+"="+p.key,
				"GIT_CONFIG_VALUE_"+n+"="+p.value,
			)
		}
	}
	return out
}

// cellCgroupsPath returns the absolute OCI Linux.CgroupsPath for a container
// nested under its cell's cgroup, or "" when either the cell cgroup path or
// the containerd id is missing (in which case the runc-shim default placement
// applies). The returned path is absolute so runc treats it as a path under
// the unified cgroup mount rather than as a systemd slice triple.
func cellCgroupsPath(cellCgroupPath, containerdID string) string {
	if cellCgroupPath == "" || containerdID == "" {
		return ""
	}
	return filepath.Join(cellCgroupPath, containerdID)
}

// buildVolumeMounts translates ContainerSpec.Volumes into OCI mounts. The
// VolumeMount.Kind discriminator selects the emitted mount type: empty or
// VolumeKindBind produces a host bind mount (Source → Target), VolumeKindTmpfs
// produces an in-memory tmpfs mount at Target with the runtime's standard
// tmpfs Source. Entries are expected to be already validated (absolute paths
// for the bind kind; absolute Target for the tmpfs kind). Unknown kinds are
// skipped silently — validation belongs upstream.
func buildVolumeMounts(volumes []intmodel.VolumeMount) []runtimespec.Mount {
	if len(volumes) == 0 {
		return nil
	}
	mounts := make([]runtimespec.Mount, 0, len(volumes))
	for _, v := range volumes {
		switch v.Kind {
		case intmodel.VolumeKindTmpfs:
			if m, ok := tmpfsVolumeMount(v); ok {
				mounts = append(mounts, m)
			}
		case "", intmodel.VolumeKindBind:
			if m, ok := bindVolumeMount(v); ok {
				mounts = append(mounts, m)
			}
		}
	}
	return mounts
}

// bindVolumeMount renders a bind-kind VolumeMount as an OCI Mount. Returns
// (zero, false) when Source or Target is empty so the caller can skip the
// entry — matching the historical buildBindMounts skip rule.
func bindVolumeMount(v intmodel.VolumeMount) (runtimespec.Mount, bool) {
	if v.Source == "" || v.Target == "" {
		return runtimespec.Mount{}, false
	}
	options := []string{"rbind"}
	if v.ReadOnly {
		options = append(options, "ro")
	} else {
		options = append(options, "rw")
	}
	return runtimespec.Mount{
		Destination: v.Target,
		Source:      v.Source,
		Type:        "bind",
		Options:     options,
	}, true
}

// tmpfsVolumeMount renders a tmpfs-kind VolumeMount as an OCI Mount. Returns
// (zero, false) when Target is empty. SizeBytes and Mode emit the standard
// tmpfs size= / mode= options when set; ReadOnly maps to ro/rw last in the
// option list.
func tmpfsVolumeMount(v intmodel.VolumeMount) (runtimespec.Mount, bool) {
	if v.Target == "" {
		return runtimespec.Mount{}, false
	}
	options := make([]string, 0)
	if v.SizeBytes > 0 {
		options = append(options, "size="+strconv.FormatInt(v.SizeBytes, 10))
	}
	if v.Mode != 0 {
		options = append(options, fmt.Sprintf("mode=%04o", v.Mode))
	}
	if v.ReadOnly {
		options = append(options, "ro")
	} else {
		options = append(options, "rw")
	}
	return runtimespec.Mount{
		Destination: v.Target,
		Source:      "tmpfs",
		Type:        "tmpfs",
		Options:     options,
	}, true
}

// securitySpecOpts translates the security/isolation fields on the internal
// ContainerSpec (user, readOnlyRootFilesystem, capabilities, securityOpts,
// tmpfs, resources) into OCI spec options.
func securitySpecOpts(spec intmodel.ContainerSpec) []oci.SpecOpts {
	var opts []oci.SpecOpts

	if spec.User != "" {
		opts = append(opts, oci.WithUser(spec.User))
	}

	if spec.ReadOnlyRootFilesystem {
		opts = append(opts, oci.WithRootFSReadonly())
	}

	if spec.Capabilities != nil {
		drop := normalizeCapabilities(spec.Capabilities.Drop)
		add := normalizeCapabilities(spec.Capabilities.Add)
		// "ALL" is not a real capability name — containerd's
		// WithDroppedCapabilities does a literal string-match removal, so
		// dropping "ALL" would leave the default cap set intact. Clear the
		// whole set instead, then layer the add list on top.
		if containsAllCaps(drop) {
			opts = append(opts, oci.WithCapabilities(nil))
		} else if len(drop) > 0 {
			opts = append(opts, oci.WithDroppedCapabilities(drop))
		}
		if len(add) > 0 {
			opts = append(opts, oci.WithAddedCapabilities(add))
		}
	}

	for _, entry := range spec.SecurityOpts {
		opts = append(opts, securityOptSpecOpt(entry))
	}

	if mounts := buildTmpfsMounts(spec.Tmpfs); len(mounts) > 0 {
		opts = append(opts, oci.WithMounts(mounts))
	}

	if spec.Resources != nil {
		if spec.Resources.MemoryLimitBytes != nil && *spec.Resources.MemoryLimitBytes > 0 {
			opts = append(opts, oci.WithMemoryLimit(uint64(*spec.Resources.MemoryLimitBytes)))
		}
		if spec.Resources.CPUShares != nil && *spec.Resources.CPUShares > 0 {
			opts = append(opts, oci.WithCPUShares(uint64(*spec.Resources.CPUShares)))
		}
		if spec.Resources.PidsLimit != nil && *spec.Resources.PidsLimit > 0 {
			opts = append(opts, oci.WithPidsLimit(*spec.Resources.PidsLimit))
		}
	}

	return opts
}

// containsAllCaps reports whether the normalized capability list names the
// "ALL" sentinel in any of its accepted spellings.
func containsAllCaps(caps []string) bool {
	for _, c := range caps {
		if c == "ALL" || c == "CAP_ALL" {
			return true
		}
	}
	return false
}

// normalizeCapabilities ensures each capability name has the "CAP_" prefix and
// is upper-case, which is what the OCI runtime spec expects. Callers are free
// to write "NET_ADMIN" or "cap_net_admin"; both normalize to "CAP_NET_ADMIN".
func normalizeCapabilities(in []string) []string {
	out := make([]string, 0, len(in))
	for _, c := range in {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		upper := strings.ToUpper(c)
		if upper == "ALL" || strings.HasPrefix(upper, "CAP_") {
			out = append(out, upper)
			continue
		}
		out = append(out, "CAP_"+upper)
	}
	return out
}

// buildTmpfsMounts returns OCI mounts for each declared tmpfs entry. Size is
// emitted as the standard tmpfs "size=N" option when set.
func buildTmpfsMounts(entries []intmodel.ContainerTmpfsMount) []runtimespec.Mount {
	if len(entries) == 0 {
		return nil
	}
	mounts := make([]runtimespec.Mount, 0, len(entries))
	for _, e := range entries {
		path := strings.TrimSpace(e.Path)
		if path == "" {
			continue
		}
		options := []string{"nosuid", "nodev"}
		if e.SizeBytes > 0 {
			options = append(options, "size="+strconv.FormatInt(e.SizeBytes, 10))
		}
		options = append(options, e.Options...)
		mounts = append(mounts, runtimespec.Mount{
			Destination: path,
			Source:      "tmpfs",
			Type:        "tmpfs",
			Options:     options,
		})
	}
	return mounts
}

// securityOptSpecOpt parses a single docker-style security option
// ("no-new-privileges", "seccomp=unconfined", "seccomp=/path/to/profile.json")
// and returns a SpecOpts that applies it. Unknown keys return an error at
// spec-apply time so callers see a clear failure rather than a silent pass.
func securityOptSpecOpt(raw string) oci.SpecOpts {
	entry := strings.TrimSpace(raw)
	key, value, hasValue := splitSecurityOpt(entry)

	switch strings.ToLower(key) {
	case "no-new-privileges":
		enabled := true
		if hasValue {
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return errorSpecOpt(fmt.Errorf("securityOpt %q: invalid bool: %w", raw, err))
			}
			enabled = parsed
		}
		return func(_ context.Context, _ oci.Client, _ *containers.Container, s *runtimespec.Spec) error {
			if s.Process == nil {
				s.Process = &runtimespec.Process{}
			}
			s.Process.NoNewPrivileges = enabled
			return nil
		}
	case "seccomp":
		return seccompSpecOpt(raw, value, hasValue)
	default:
		return errorSpecOpt(fmt.Errorf("unsupported securityOpt %q", raw))
	}
}

// splitSecurityOpt parses "key=value" and "key:value" forms. A bare "key"
// returns (key, "", false).
func splitSecurityOpt(entry string) (key, value string, ok bool) {
	if idx := strings.IndexAny(entry, "=:"); idx >= 0 {
		return strings.TrimSpace(entry[:idx]), strings.TrimSpace(entry[idx+1:]), true
	}
	return entry, "", false
}

// seccompSpecOpt handles seccomp=unconfined (strip profile) and
// seccomp=/path/to/profile.json (load + apply) forms.
func seccompSpecOpt(raw, value string, hasValue bool) oci.SpecOpts {
	if !hasValue || value == "" {
		return errorSpecOpt(fmt.Errorf("securityOpt %q: seccomp requires a value (unconfined or profile path)", raw))
	}
	if strings.EqualFold(value, "unconfined") {
		return func(_ context.Context, _ oci.Client, _ *containers.Container, s *runtimespec.Spec) error {
			if s.Linux != nil {
				s.Linux.Seccomp = nil
			}
			return nil
		}
	}
	return func(_ context.Context, _ oci.Client, _ *containers.Container, s *runtimespec.Spec) error {
		data, err := os.ReadFile(value)
		if err != nil {
			return fmt.Errorf("securityOpt %q: read seccomp profile: %w", raw, err)
		}
		profile := &runtimespec.LinuxSeccomp{}
		if err = json.Unmarshal(data, profile); err != nil {
			return fmt.Errorf("securityOpt %q: parse seccomp profile: %w", raw, err)
		}
		if s.Linux == nil {
			s.Linux = &runtimespec.Linux{}
		}
		s.Linux.Seccomp = profile
		return nil
	}
}

// errorSpecOpt returns a SpecOpts that always fails with the given error. Used
// to defer reporting of invalid user input until spec application so the call
// site still composes cleanly.
func errorSpecOpt(err error) oci.SpecOpts {
	return func(_ context.Context, _ oci.Client, _ *containers.Container, _ *runtimespec.Spec) error {
		return err
	}
}
