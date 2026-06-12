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

// hasAllowAllDeviceRule reports whether resources carries the wildcard
// allow-all device-cgroup rule oci.WithAllDevicesAllowed emits (Allow, no
// type/major/minor, rwm access).
func hasAllowAllDeviceRule(resources *runtimespec.LinuxResources) bool {
	if resources == nil {
		return false
	}
	for _, r := range resources.Devices {
		if r.Allow && r.Type == "" && r.Major == nil && r.Minor == nil && r.Access == "rwm" {
			return true
		}
	}
	return false
}

// TestBuildContainerSpec_PrivilegedHostDevices asserts that a privileged
// container gets the allow-all device cgroup rule and host device nodes
// replicated into Linux.Devices, restoring `docker run --privileged` device
// parity. The enumeration runs against the un-containerized test host's /dev
// (the host-root containerized path is e2e-only). Issue #1261.
func TestBuildContainerSpec_PrivilegedHostDevices(t *testing.T) {
	spec := applyBuiltSpec(t, intmodel.ContainerSpec{
		ID:         "c1",
		Image:      "registry.eminwux.com/busybox:latest",
		CellName:   "cell",
		SpaceName:  "space",
		RealmName:  "realm",
		StackName:  "stack",
		Privileged: true,
	})

	if spec.Linux == nil {
		t.Fatalf("Linux section is nil")
	}
	if !hasAllowAllDeviceRule(spec.Linux.Resources) {
		t.Errorf("privileged spec missing allow-all device cgroup rule: %+v", spec.Linux.Resources)
	}
	if len(spec.Linux.Devices) == 0 {
		t.Errorf("privileged spec replicated no host devices into Linux.Devices")
	}
}

// TestBuildRootContainerSpec_PrivilegedHostDevices asserts the root-container
// path gets the same allow-all rule + host-device replication. Issue #1261.
func TestBuildRootContainerSpec_PrivilegedHostDevices(t *testing.T) {
	built := ctr.BuildRootContainerSpec(intmodel.ContainerSpec{
		ID:         "root",
		Root:       true,
		Image:      "registry.eminwux.com/busybox:latest",
		CellName:   "cell",
		SpaceName:  "space",
		RealmName:  "realm",
		StackName:  "stack",
		Privileged: true,
	}, nil)

	spec := &runtimespec.Spec{Process: &runtimespec.Process{}, Linux: &runtimespec.Linux{}}
	for _, opt := range built.SpecOpts {
		if err := opt(context.Background(), nil, nil, spec); err != nil {
			t.Fatalf("SpecOpts returned error: %v", err)
		}
	}

	if !hasAllowAllDeviceRule(spec.Linux.Resources) {
		t.Errorf("privileged root spec missing allow-all device cgroup rule: %+v", spec.Linux.Resources)
	}
	if len(spec.Linux.Devices) == 0 {
		t.Errorf("privileged root spec replicated no host devices into Linux.Devices")
	}
}

// TestBuildContainerSpec_NonPrivilegedNoAllowAll asserts a non-privileged
// container without devices[] gets neither the allow-all rule nor host-device
// replication, so the privileged change does not leak into ordinary cells.
// Issue #1261.
func TestBuildContainerSpec_NonPrivilegedNoAllowAll(t *testing.T) {
	spec := applyBuiltSpec(t, intmodel.ContainerSpec{
		ID:        "c1",
		Image:     "registry.eminwux.com/busybox:latest",
		CellName:  "cell",
		SpaceName: "space",
		RealmName: "realm",
		StackName: "stack",
	})

	if spec.Linux != nil && hasAllowAllDeviceRule(spec.Linux.Resources) {
		t.Errorf("non-privileged spec unexpectedly carries allow-all device rule: %+v", spec.Linux.Resources)
	}
}
