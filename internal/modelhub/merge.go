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

package modelhub

// ApplySpaceDefaultsToContainer fills isolation fields on container that are
// unset (zero value or nil) using the defaults declared on space. Precedence:
//
//	container spec > Space defaults > kukeon built-in defaults
//
// Inheritance is shallow — overriding a pointer/slice field replaces the Space
// default outright rather than deep-merging. For example, a Container that
// sets Capabilities.Drop=["CAP_NET_RAW"] replaces the Space default's Drop
// list entirely; it does not union with it.
//
// The merge is idempotent: calling it twice on the same container yields the
// same result as calling it once.
func ApplySpaceDefaultsToContainer(space Space, container *ContainerSpec) {
	if container == nil || space.Spec.Defaults == nil || space.Spec.Defaults.Container == nil {
		return
	}
	defaults := space.Spec.Defaults.Container

	if container.User == "" {
		container.User = defaults.User
	}
	if !container.ReadOnlyRootFilesystem && defaults.ReadOnlyRootFilesystem != nil {
		container.ReadOnlyRootFilesystem = *defaults.ReadOnlyRootFilesystem
	}
	if container.Capabilities == nil && defaults.Capabilities != nil {
		container.Capabilities = cloneCapabilities(defaults.Capabilities)
	}
	if len(container.SecurityOpts) == 0 && len(defaults.SecurityOpts) > 0 {
		container.SecurityOpts = append([]string(nil), defaults.SecurityOpts...)
	}
	if len(container.Tmpfs) == 0 && len(defaults.Tmpfs) > 0 {
		container.Tmpfs = cloneTmpfsMounts(defaults.Tmpfs)
	}
	if container.Resources == nil && defaults.Resources != nil {
		container.Resources = cloneResources(defaults.Resources)
	}
}

func cloneCapabilities(in *ContainerCapabilities) *ContainerCapabilities {
	if in == nil {
		return nil
	}
	out := &ContainerCapabilities{
		Drop: append([]string(nil), in.Drop...),
		Add:  append([]string(nil), in.Add...),
	}
	if len(out.Drop) == 0 {
		out.Drop = nil
	}
	if len(out.Add) == 0 {
		out.Add = nil
	}
	return out
}

func cloneTmpfsMounts(in []ContainerTmpfsMount) []ContainerTmpfsMount {
	if len(in) == 0 {
		return nil
	}
	out := make([]ContainerTmpfsMount, len(in))
	for i, m := range in {
		opts := append([]string(nil), m.Options...)
		if len(opts) == 0 {
			opts = nil
		}
		out[i] = ContainerTmpfsMount{
			Path:      m.Path,
			SizeBytes: m.SizeBytes,
			Options:   opts,
		}
	}
	return out
}

func cloneResources(in *ContainerResources) *ContainerResources {
	if in == nil {
		return nil
	}
	out := &ContainerResources{}
	if in.MemoryLimitBytes != nil {
		v := *in.MemoryLimitBytes
		out.MemoryLimitBytes = &v
	}
	if in.CPUShares != nil {
		v := *in.CPUShares
		out.CPUShares = &v
	}
	if in.PidsLimit != nil {
		v := *in.PidsLimit
		out.PidsLimit = &v
	}
	return out
}
