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

package ctr_test

import (
	"context"
	"testing"

	ctr "github.com/eminwux/kukeon/internal/ctr"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

// applyWithBuildOpts composes the SpecOpts produced by BuildContainerSpec
// (with explicit BuildOptions) into an empty runtime spec.
func applyWithBuildOpts(
	t *testing.T,
	in intmodel.ContainerSpec,
	options ...ctr.BuildOption,
) *runtimespec.Spec {
	t.Helper()
	spec := &runtimespec.Spec{
		Process: &runtimespec.Process{},
		Linux:   &runtimespec.Linux{},
	}
	built := ctr.BuildContainerSpec(in, options...)
	for _, opt := range built.SpecOpts {
		if err := opt(context.Background(), nil, nil, spec); err != nil {
			t.Fatalf("SpecOpts returned error: %v", err)
		}
	}
	return spec
}

// applyRootWithBuildOpts is the BuildRootContainerSpec counterpart of
// applyWithBuildOpts.
func applyRootWithBuildOpts(
	t *testing.T,
	in intmodel.ContainerSpec,
	options ...ctr.BuildOption,
) *runtimespec.Spec {
	t.Helper()
	spec := &runtimespec.Spec{
		Process: &runtimespec.Process{},
		Linux:   &runtimespec.Linux{},
	}
	built := ctr.BuildRootContainerSpec(in, nil, options...)
	for _, opt := range built.SpecOpts {
		if err := opt(context.Background(), nil, nil, spec); err != nil {
			t.Fatalf("SpecOpts returned error: %v", err)
		}
	}
	return spec
}

func baseContainerSpec() intmodel.ContainerSpec {
	return intmodel.ContainerSpec{
		ID:        "c1",
		Image:     "registry.eminwux.com/busybox:latest",
		CellName:  "cell",
		SpaceName: "space",
		RealmName: "realm",
		StackName: "stack",
	}
}

// TestBuildContainerSpec_DefaultMemoryLimit_AppliedWhenSpecHasNone pins the
// admission-time fallback: a spec with no Resources at all gets memory.max
// written from the daemon default (#531).
func TestBuildContainerSpec_DefaultMemoryLimit_AppliedWhenSpecHasNone(t *testing.T) {
	const defaultBytes int64 = 2 * 1024 * 1024 * 1024
	spec := applyWithBuildOpts(t, baseContainerSpec(), ctr.WithDefaultMemoryLimit(defaultBytes))

	if spec.Linux == nil || spec.Linux.Resources == nil {
		t.Fatalf("Linux.Resources is nil; daemon-default cap was not applied")
	}
	if spec.Linux.Resources.Memory == nil ||
		spec.Linux.Resources.Memory.Limit == nil ||
		*spec.Linux.Resources.Memory.Limit != defaultBytes {
		t.Errorf("Memory.Limit = %+v, want %d (daemon default)",
			spec.Linux.Resources.Memory, defaultBytes)
	}
}

// TestBuildContainerSpec_DefaultMemoryLimit_AppliedWhenResourcesNilMember
// covers the variant where Resources is non-nil but MemoryLimitBytes is unset.
func TestBuildContainerSpec_DefaultMemoryLimit_AppliedWhenResourcesNilMember(t *testing.T) {
	const defaultBytes int64 = 3 * 1024 * 1024 * 1024
	cpu := int64(512)
	in := baseContainerSpec()
	in.Resources = &intmodel.ContainerResources{CPUShares: &cpu} // no MemoryLimitBytes

	spec := applyWithBuildOpts(t, in, ctr.WithDefaultMemoryLimit(defaultBytes))

	if spec.Linux.Resources.Memory == nil ||
		spec.Linux.Resources.Memory.Limit == nil ||
		*spec.Linux.Resources.Memory.Limit != defaultBytes {
		t.Errorf("Memory.Limit = %+v, want %d (daemon default)",
			spec.Linux.Resources.Memory, defaultBytes)
	}
}

// TestBuildContainerSpec_DefaultMemoryLimit_AppliedWhenSpecValueIsZero
// covers Resources.MemoryLimitBytes set to *int64(0): the existing
// securitySpecOpts branch already skips a zero limit, so the daemon default
// must still apply.
func TestBuildContainerSpec_DefaultMemoryLimit_AppliedWhenSpecValueIsZero(t *testing.T) {
	const defaultBytes int64 = 4 * 1024 * 1024 * 1024
	zero := int64(0)
	in := baseContainerSpec()
	in.Resources = &intmodel.ContainerResources{MemoryLimitBytes: &zero}

	spec := applyWithBuildOpts(t, in, ctr.WithDefaultMemoryLimit(defaultBytes))

	if spec.Linux.Resources.Memory == nil ||
		spec.Linux.Resources.Memory.Limit == nil ||
		*spec.Linux.Resources.Memory.Limit != defaultBytes {
		t.Errorf("Memory.Limit = %+v, want %d (daemon default)",
			spec.Linux.Resources.Memory, defaultBytes)
	}
}

// TestBuildContainerSpec_DefaultMemoryLimit_ExplicitWins pins the rule the
// AC names: an explicit per-container limit always overrides the daemon
// default. The explicit value is the one written, not the default.
func TestBuildContainerSpec_DefaultMemoryLimit_ExplicitWins(t *testing.T) {
	const defaultBytes int64 = 2 * 1024 * 1024 * 1024
	explicit := int64(8 * 1024 * 1024 * 1024)
	in := baseContainerSpec()
	in.Resources = &intmodel.ContainerResources{MemoryLimitBytes: &explicit}

	spec := applyWithBuildOpts(t, in, ctr.WithDefaultMemoryLimit(defaultBytes))

	if spec.Linux.Resources.Memory == nil ||
		spec.Linux.Resources.Memory.Limit == nil ||
		*spec.Linux.Resources.Memory.Limit != explicit {
		t.Errorf("Memory.Limit = %+v, want explicit %d (not default %d)",
			spec.Linux.Resources.Memory, explicit, defaultBytes)
	}
}

// TestBuildContainerSpec_DefaultMemoryLimit_ZeroIsNoop guards the
// current-behavior preserving contract: passing WithDefaultMemoryLimit(0) (or
// not passing the option at all) leaves the spec with no memory limit.
func TestBuildContainerSpec_DefaultMemoryLimit_ZeroIsNoop(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []ctr.BuildOption
	}{
		{name: "no-option", opts: nil},
		{name: "zero-default", opts: []ctr.BuildOption{ctr.WithDefaultMemoryLimit(0)}},
		{name: "negative-default", opts: []ctr.BuildOption{ctr.WithDefaultMemoryLimit(-1)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := applyWithBuildOpts(t, baseContainerSpec(), tc.opts...)
			if spec.Linux != nil && spec.Linux.Resources != nil &&
				spec.Linux.Resources.Memory != nil && spec.Linux.Resources.Memory.Limit != nil {
				t.Errorf("Memory.Limit = %+v, want unset", spec.Linux.Resources.Memory)
			}
		})
	}
}

// TestBuildRootContainerSpec_DefaultMemoryLimit_AppliedWhenSpecHasNone
// mirrors the BuildContainerSpec parity test for root containers — a root
// spec with no Resources gets the daemon default written (#531).
func TestBuildRootContainerSpec_DefaultMemoryLimit_AppliedWhenSpecHasNone(t *testing.T) {
	const defaultBytes int64 = 2 * 1024 * 1024 * 1024
	in := ctr.DefaultRootContainerSpec("containerd-root", "cell", "realm", "space", "stack", "")

	spec := applyRootWithBuildOpts(t, in, ctr.WithDefaultMemoryLimit(defaultBytes))

	if spec.Linux == nil || spec.Linux.Resources == nil ||
		spec.Linux.Resources.Memory == nil ||
		spec.Linux.Resources.Memory.Limit == nil ||
		*spec.Linux.Resources.Memory.Limit != defaultBytes {
		t.Errorf("Memory.Limit = %+v, want %d (daemon default)",
			spec.Linux.Resources.Memory, defaultBytes)
	}
}

// TestBuildRootContainerSpec_DefaultMemoryLimit_ExplicitWins mirrors the
// BuildContainerSpec parity test: an explicit root-spec limit wins over the
// daemon default.
func TestBuildRootContainerSpec_DefaultMemoryLimit_ExplicitWins(t *testing.T) {
	const defaultBytes int64 = 2 * 1024 * 1024 * 1024
	explicit := int64(8 * 1024 * 1024 * 1024)
	in := ctr.DefaultRootContainerSpec("containerd-root", "cell", "realm", "space", "stack", "")
	in.Resources = &intmodel.ContainerResources{MemoryLimitBytes: &explicit}

	spec := applyRootWithBuildOpts(t, in, ctr.WithDefaultMemoryLimit(defaultBytes))

	if spec.Linux.Resources.Memory == nil ||
		spec.Linux.Resources.Memory.Limit == nil ||
		*spec.Linux.Resources.Memory.Limit != explicit {
		t.Errorf("Memory.Limit = %+v, want explicit %d (not default %d)",
			spec.Linux.Resources.Memory, explicit, defaultBytes)
	}
}

// TestBuildRootContainerSpec_DefaultMemoryLimit_ZeroIsNoop locks the current-
// behavior preserving contract for root containers — no option, zero, or
// negative value all leave memory.max unwritten.
func TestBuildRootContainerSpec_DefaultMemoryLimit_ZeroIsNoop(t *testing.T) {
	in := ctr.DefaultRootContainerSpec("containerd-root", "cell", "realm", "space", "stack", "")
	for _, tc := range []struct {
		name string
		opts []ctr.BuildOption
	}{
		{name: "no-option", opts: nil},
		{name: "zero-default", opts: []ctr.BuildOption{ctr.WithDefaultMemoryLimit(0)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := applyRootWithBuildOpts(t, in, tc.opts...)
			if spec.Linux != nil && spec.Linux.Resources != nil &&
				spec.Linux.Resources.Memory != nil && spec.Linux.Resources.Memory.Limit != nil {
				t.Errorf("Memory.Limit = %+v, want unset", spec.Linux.Resources.Memory)
			}
		})
	}
}
