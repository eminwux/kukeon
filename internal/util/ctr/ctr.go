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

package ctrutil

import (
	"strings"

	"github.com/containerd/containerd/v2/pkg/oci"
	internalctr "github.com/eminwux/kukeon/internal/ctr"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
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
func DefaultRootContainerSpec(
	containerID,
	cellID,
	realmID,
	spaceID,
	stackID,
	cniConfigPath string,
) *v1beta1.ContainerSpec {
	return &v1beta1.ContainerSpec{
		ID:            containerID,
		CellID:        cellID,
		RealmID:       realmID,
		SpaceID:       spaceID,
		StackID:       stackID,
		Root:          true,
		Image:         DefaultRootContainerImage,
		Command:       defaultRootContainerCmd,
		Args:          []string{defaultRootContainerArg},
		CNIConfigPath: cniConfigPath,
	}
}

// BuildRootContainerSpec converts the API-level root container spec into an
// internal ctr.ContainerSpec with the expected defaults applied.
func BuildRootContainerSpec(
	rootSpec *v1beta1.ContainerSpec,
	labels map[string]string,
) internalctr.ContainerSpec {
	if rootSpec == nil {
		return internalctr.ContainerSpec{}
	}

	image := rootSpec.Image
	if image == "" {
		image = DefaultRootContainerImage
	}

	specOpts := []oci.SpecOpts{
		oci.WithDefaultPathEnv,
	}

	if rootSpec.ID != "" {
		specOpts = append(specOpts, oci.WithHostname(rootSpec.ID))
	}

	if processArgs := buildRootProcessArgs(rootSpec); len(processArgs) > 0 {
		specOpts = append(specOpts, oci.WithProcessArgs(processArgs...))
	}

	if len(rootSpec.Env) > 0 {
		specOpts = append(specOpts, oci.WithEnv(rootSpec.Env))
	}

	if rootSpec.Privileged {
		specOpts = append(specOpts, oci.WithPrivileged)
	}

	rootLabels := copyLabels(labels)
	rootLabels[rootContainerLabelKey] = rootContainerLabelValue

	return internalctr.ContainerSpec{
		ID:            rootSpec.ID,
		Image:         image,
		Labels:        rootLabels,
		SpecOpts:      specOpts,
		CNIConfigPath: rootSpec.CNIConfigPath,
	}
}

func buildRootProcessArgs(rootSpec *v1beta1.ContainerSpec) []string {
	if rootSpec == nil {
		return []string{defaultRootContainerCmd, defaultRootContainerArg}
	}

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
