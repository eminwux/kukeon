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

package modelhub_test

import (
	"reflect"
	"testing"

	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

func ptrBool(v bool) *bool   { return &v }
func ptrInt64(v int64) *int64 { return &v }

func spaceWithContainerDefaults(d *intmodel.SpaceContainerDefaults) intmodel.Space {
	return intmodel.Space{
		Spec: intmodel.SpaceSpec{
			Defaults: &intmodel.SpaceDefaults{Container: d},
		},
	}
}

// TestApplySpaceDefaultsToContainer_FillsEmptyFields covers the common case:
// a Container that declares nothing beyond its image inherits the full Space
// envelope (user, readOnly, caps, securityOpts, tmpfs, resources).
func TestApplySpaceDefaultsToContainer_FillsEmptyFields(t *testing.T) {
	space := spaceWithContainerDefaults(&intmodel.SpaceContainerDefaults{
		User:                   "1000:1000",
		ReadOnlyRootFilesystem: ptrBool(true),
		Capabilities: &intmodel.ContainerCapabilities{
			Drop: []string{"ALL"},
			Add:  []string{"NET_BIND_SERVICE"},
		},
		SecurityOpts: []string{"no-new-privileges"},
		Tmpfs: []intmodel.ContainerTmpfsMount{
			{Path: "/tmp", SizeBytes: 64 * 1024 * 1024},
		},
		Resources: &intmodel.ContainerResources{
			MemoryLimitBytes: ptrInt64(4 * 1024 * 1024 * 1024),
			PidsLimit:        ptrInt64(512),
		},
	})

	container := intmodel.ContainerSpec{ID: "c1", Image: "busybox:latest"}
	intmodel.ApplySpaceDefaultsToContainer(space, &container)

	if container.User != "1000:1000" {
		t.Errorf("User = %q, want 1000:1000", container.User)
	}
	if !container.ReadOnlyRootFilesystem {
		t.Errorf("ReadOnlyRootFilesystem = false, want true")
	}
	if container.Capabilities == nil ||
		!reflect.DeepEqual(container.Capabilities.Drop, []string{"ALL"}) ||
		!reflect.DeepEqual(container.Capabilities.Add, []string{"NET_BIND_SERVICE"}) {
		t.Errorf("Capabilities = %+v, want Drop=[ALL] Add=[NET_BIND_SERVICE]", container.Capabilities)
	}
	if !reflect.DeepEqual(container.SecurityOpts, []string{"no-new-privileges"}) {
		t.Errorf("SecurityOpts = %v, want [no-new-privileges]", container.SecurityOpts)
	}
	if len(container.Tmpfs) != 1 || container.Tmpfs[0].Path != "/tmp" ||
		container.Tmpfs[0].SizeBytes != 64*1024*1024 {
		t.Errorf("Tmpfs = %+v, want single /tmp mount of 64MiB", container.Tmpfs)
	}
	if container.Resources == nil ||
		container.Resources.MemoryLimitBytes == nil || *container.Resources.MemoryLimitBytes != 4*1024*1024*1024 ||
		container.Resources.PidsLimit == nil || *container.Resources.PidsLimit != 512 {
		t.Errorf("Resources = %+v, want MemoryLimit=4GiB PidsLimit=512", container.Resources)
	}
}

// TestApplySpaceDefaultsToContainer_ContainerValuesWin covers precedence:
// the Container's explicit values take precedence over Space defaults.
func TestApplySpaceDefaultsToContainer_ContainerValuesWin(t *testing.T) {
	space := spaceWithContainerDefaults(&intmodel.SpaceContainerDefaults{
		User:         "1000:1000",
		SecurityOpts: []string{"no-new-privileges"},
		Resources: &intmodel.ContainerResources{
			MemoryLimitBytes: ptrInt64(4 * 1024 * 1024 * 1024),
		},
	})

	container := intmodel.ContainerSpec{
		ID:           "c1",
		User:         "2000:2000",
		SecurityOpts: []string{"seccomp=unconfined"},
		Resources: &intmodel.ContainerResources{
			MemoryLimitBytes: ptrInt64(1 * 1024 * 1024 * 1024),
		},
	}
	intmodel.ApplySpaceDefaultsToContainer(space, &container)

	if container.User != "2000:2000" {
		t.Errorf("User = %q, container-spec value should win", container.User)
	}
	if !reflect.DeepEqual(container.SecurityOpts, []string{"seccomp=unconfined"}) {
		t.Errorf("SecurityOpts = %v, container-spec value should win", container.SecurityOpts)
	}
	if container.Resources == nil || container.Resources.MemoryLimitBytes == nil ||
		*container.Resources.MemoryLimitBytes != 1*1024*1024*1024 {
		t.Errorf("Resources.MemoryLimitBytes = %+v, container-spec 1GiB should win", container.Resources)
	}
}

// TestApplySpaceDefaultsToContainer_OverrideReplacesShallow covers the
// shallow-merge semantic on pointer / slice fields: a Container that sets
// Capabilities replaces the Space default entirely — Drop/Add are not
// merged.
func TestApplySpaceDefaultsToContainer_OverrideReplacesShallow(t *testing.T) {
	space := spaceWithContainerDefaults(&intmodel.SpaceContainerDefaults{
		Capabilities: &intmodel.ContainerCapabilities{
			Drop: []string{"ALL"},
			Add:  []string{"NET_BIND_SERVICE"},
		},
		Tmpfs: []intmodel.ContainerTmpfsMount{
			{Path: "/tmp", SizeBytes: 64 * 1024 * 1024},
			{Path: "/run", SizeBytes: 16 * 1024 * 1024},
		},
	})

	container := intmodel.ContainerSpec{
		ID: "c1",
		Capabilities: &intmodel.ContainerCapabilities{
			Drop: []string{"CAP_NET_RAW"},
		},
		Tmpfs: []intmodel.ContainerTmpfsMount{
			{Path: "/var/tmp", SizeBytes: 8 * 1024 * 1024},
		},
	}
	intmodel.ApplySpaceDefaultsToContainer(space, &container)

	// Capabilities replaced — Add from Space must NOT appear.
	if container.Capabilities == nil {
		t.Fatalf("Capabilities lost during merge")
	}
	if !reflect.DeepEqual(container.Capabilities.Drop, []string{"CAP_NET_RAW"}) {
		t.Errorf("Capabilities.Drop = %v, container-spec value should replace defaults", container.Capabilities.Drop)
	}
	if len(container.Capabilities.Add) != 0 {
		t.Errorf("Capabilities.Add = %v, expected empty — shallow override must not leak default Add", container.Capabilities.Add)
	}

	// Tmpfs replaced — the single container-spec entry, not the Space pair.
	if len(container.Tmpfs) != 1 || container.Tmpfs[0].Path != "/var/tmp" {
		t.Errorf("Tmpfs = %+v, container-spec value should replace defaults (no append)", container.Tmpfs)
	}
}

// TestApplySpaceDefaultsToContainer_NoDefaults covers the nil-defaults case:
// a Space with no defaults block leaves the container untouched.
func TestApplySpaceDefaultsToContainer_NoDefaults(t *testing.T) {
	cases := []struct {
		name  string
		space intmodel.Space
	}{
		{name: "nil-defaults", space: intmodel.Space{Spec: intmodel.SpaceSpec{Defaults: nil}}},
		{name: "nil-container", space: intmodel.Space{Spec: intmodel.SpaceSpec{Defaults: &intmodel.SpaceDefaults{}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			container := intmodel.ContainerSpec{ID: "c1", Image: "busybox"}
			orig := container
			intmodel.ApplySpaceDefaultsToContainer(tc.space, &container)
			if !reflect.DeepEqual(container, orig) {
				t.Errorf("container mutated: got=%+v orig=%+v", container, orig)
			}
		})
	}
}

// TestApplySpaceDefaultsToContainer_Idempotent verifies calling the merge
// twice yields the same result as calling it once — important because both
// CreateContainer and UpdateContainer now invoke the merge, and an update
// that happens to retrigger create must not compound defaults.
func TestApplySpaceDefaultsToContainer_Idempotent(t *testing.T) {
	space := spaceWithContainerDefaults(&intmodel.SpaceContainerDefaults{
		User: "1000:1000",
		Capabilities: &intmodel.ContainerCapabilities{
			Drop: []string{"ALL"},
			Add:  []string{"NET_BIND_SERVICE"},
		},
		SecurityOpts: []string{"no-new-privileges"},
		Tmpfs: []intmodel.ContainerTmpfsMount{
			{Path: "/tmp", SizeBytes: 64 * 1024 * 1024},
		},
	})

	once := intmodel.ContainerSpec{ID: "c1"}
	intmodel.ApplySpaceDefaultsToContainer(space, &once)

	twice := once
	intmodel.ApplySpaceDefaultsToContainer(space, &twice)

	if !reflect.DeepEqual(once, twice) {
		t.Errorf("merge is not idempotent:\n once=%+v\n twice=%+v", once, twice)
	}
}

// TestApplySpaceDefaultsToContainer_NilContainer is a sanity check that a
// nil container pointer is a no-op rather than a panic.
func TestApplySpaceDefaultsToContainer_NilContainer(t *testing.T) {
	space := spaceWithContainerDefaults(&intmodel.SpaceContainerDefaults{User: "1000:1000"})
	intmodel.ApplySpaceDefaultsToContainer(space, nil)
}

// TestApplySpaceDefaultsToContainer_DefaultsDeepCopied guards against
// aliasing — mutating the merged container later must not reach back into
// the Space defaults object.
func TestApplySpaceDefaultsToContainer_DefaultsDeepCopied(t *testing.T) {
	defaults := &intmodel.SpaceContainerDefaults{
		Capabilities: &intmodel.ContainerCapabilities{Drop: []string{"ALL"}},
		SecurityOpts: []string{"no-new-privileges"},
		Tmpfs: []intmodel.ContainerTmpfsMount{
			{Path: "/tmp", SizeBytes: 64 * 1024 * 1024, Options: []string{"mode=1777"}},
		},
		Resources: &intmodel.ContainerResources{MemoryLimitBytes: ptrInt64(1 << 30)},
	}
	space := spaceWithContainerDefaults(defaults)

	container := intmodel.ContainerSpec{ID: "c1"}
	intmodel.ApplySpaceDefaultsToContainer(space, &container)

	// Mutate the merged container's fields.
	container.Capabilities.Drop[0] = "CAP_NET_RAW"
	container.SecurityOpts[0] = "seccomp=unconfined"
	container.Tmpfs[0].Options[0] = "mode=0700"
	*container.Resources.MemoryLimitBytes = 2 << 30

	// Space defaults should remain unchanged.
	if defaults.Capabilities.Drop[0] != "ALL" {
		t.Errorf("Capabilities.Drop leaked: got %q, want ALL", defaults.Capabilities.Drop[0])
	}
	if defaults.SecurityOpts[0] != "no-new-privileges" {
		t.Errorf("SecurityOpts leaked: got %q", defaults.SecurityOpts[0])
	}
	if defaults.Tmpfs[0].Options[0] != "mode=1777" {
		t.Errorf("Tmpfs.Options leaked: got %q", defaults.Tmpfs[0].Options[0])
	}
	if *defaults.Resources.MemoryLimitBytes != 1<<30 {
		t.Errorf("Resources.MemoryLimitBytes leaked: got %d", *defaults.Resources.MemoryLimitBytes)
	}
}
