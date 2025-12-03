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

// BuildContainerSpec converts an internal ContainerSpec to ctr.ContainerSpec
// with the expected defaults applied.
// Uses ContainerdID if available, otherwise falls back to ID.
func BuildContainerSpec(
	containerSpec intmodel.ContainerSpec,
) ContainerSpec {
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

	return ContainerSpec{
		ID:            containerdID,
		Image:         containerSpec.Image,
		Labels:        labels,
		SpecOpts:      specOpts,
		CNIConfigPath: containerSpec.CNIConfigPath,
	}
}
