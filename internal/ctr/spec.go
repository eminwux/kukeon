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

func (c *client) applySpecOpts(container containerd.Container, opts []oci.SpecOpts) error {
	if len(opts) == 0 {
		return nil
	}

	nsCtx := c.namespaceCtx()
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

	for _, opt := range opts {
		if err = opt(nsCtx, c.cClient, nil, ociSpec); err != nil {
			return fmt.Errorf("failed to apply spec option: %w", err)
		}
	}

	if err = container.Update(nsCtx, withUpdatedSpec(ociSpec)); err != nil {
		return fmt.Errorf("failed to persist updated spec: %w", err)
	}
	return nil
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
// just the host-side paths required when ContainerSpec.Attachable is true.
type BuildOption func(*buildOpts)

type buildOpts struct {
	attachable AttachableInjection
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

	// Set hostname to containerd ID if not empty
	if containerdID != "" {
		specOpts = append(specOpts, oci.WithHostname(containerdID))
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

	// Set environment variables
	if len(containerSpec.Env) > 0 {
		specOpts = append(specOpts, oci.WithEnv(containerSpec.Env))
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

	if mounts := buildBindMounts(containerSpec.Volumes); len(mounts) > 0 {
		specOpts = append(specOpts, oci.WithMounts(mounts))
	}

	specOpts = append(specOpts, securitySpecOpts(containerSpec)...)

	// Attachable wrapping is appended last so the args-wrap runs after any
	// user-supplied WithProcessArgs above and after the image's ENTRYPOINT/CMD
	// resolution that containerd applies at container-create time.
	if containerSpec.Attachable {
		specOpts = append(
			specOpts,
			withAttachableMounts(opts.attachable),
			withAttachableArgsWrap(),
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

// buildBindMounts translates ContainerSpec.Volumes into OCI bind mounts.
// Entries are expected to be already validated (absolute source/target).
func buildBindMounts(volumes []intmodel.VolumeMount) []runtimespec.Mount {
	if len(volumes) == 0 {
		return nil
	}
	mounts := make([]runtimespec.Mount, 0, len(volumes))
	for _, v := range volumes {
		if v.Source == "" || v.Target == "" {
			continue
		}
		options := []string{"rbind"}
		if v.ReadOnly {
			options = append(options, "ro")
		} else {
			options = append(options, "rw")
		}
		mounts = append(mounts, runtimespec.Mount{
			Destination: v.Target,
			Source:      v.Source,
			Type:        "bind",
			Options:     options,
		})
	}
	return mounts
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
