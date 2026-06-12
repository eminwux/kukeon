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
	"os"
	"testing"

	ctr "github.com/eminwux/kukeon/internal/ctr"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

// TestBuildContainerSpec_DevicesShortForm asserts that a short-form devices[]
// entry on a non-privileged container materialises both an OCI Linux.Devices
// entry (node visible at the same path inside the container) and a matching
// Linux.Resources.Devices allow rule (open() not denied by the default
// deny-all device cgroup). Uses /dev/null — always present, a char device —
// so the host stat at create time resolves on any Linux test host. Issue #1252.
func TestBuildContainerSpec_DevicesShortForm(t *testing.T) {
	const dev = "/dev/null"
	if _, err := os.Stat(dev); err != nil {
		t.Skipf("%s not present on test host: %v", dev, err)
	}

	spec := applyBuiltSpec(t, intmodel.ContainerSpec{
		ID:         "c1",
		Image:      "registry.eminwux.com/busybox:latest",
		CellName:   "cell",
		SpaceName:  "space",
		RealmName:  "realm",
		StackName:  "stack",
		Privileged: false,
		Devices:    []string{dev},
	})

	if spec.Linux == nil {
		t.Fatalf("Linux section is nil")
	}

	// Node replicated into the container at the same path.
	var node *runtimespec.LinuxDevice
	for i := range spec.Linux.Devices {
		if spec.Linux.Devices[i].Path == dev {
			node = &spec.Linux.Devices[i]
			break
		}
	}
	if node == nil {
		t.Fatalf("Linux.Devices missing entry for %s: %+v", dev, spec.Linux.Devices)
	}
	if node.Type != "c" {
		t.Errorf("device %s Type = %q, want %q (char device)", dev, node.Type, "c")
	}

	// Device-cgroup allow rule matching the node's major/minor with rwm access.
	if spec.Linux.Resources == nil {
		t.Fatalf("Linux.Resources is nil — no device-cgroup allow rule emitted")
	}
	var allowed bool
	for _, r := range spec.Linux.Resources.Devices {
		if r.Allow && r.Type == "c" &&
			r.Major != nil && *r.Major == node.Major &&
			r.Minor != nil && *r.Minor == node.Minor &&
			r.Access == "rwm" {
			allowed = true
			break
		}
	}
	if !allowed {
		t.Errorf("Linux.Resources.Devices missing rwm allow rule for %s (%d:%d): %+v",
			dev, node.Major, node.Minor, spec.Linux.Resources.Devices)
	}
}

// TestBuildContainerSpec_DevicesMissingNodeErrors asserts that a devices[]
// entry naming a host node that does not exist fails at container-create time
// (when the SpecOpts are applied) with a clear error, rather than silently
// producing a container without the device. Issue #1252.
func TestBuildContainerSpec_DevicesMissingNodeErrors(t *testing.T) {
	const missing = "/dev/kukeon-nonexistent-device-1252"
	if _, err := os.Stat(missing); err == nil {
		t.Skipf("%s unexpectedly exists on test host", missing)
	}

	spec := &runtimespec.Spec{Process: &runtimespec.Process{}, Linux: &runtimespec.Linux{}}
	built := ctr.BuildContainerSpec(intmodel.ContainerSpec{
		ID:        "c1",
		Image:     "registry.eminwux.com/busybox:latest",
		CellName:  "cell",
		SpaceName: "space",
		RealmName: "realm",
		StackName: "stack",
		Devices:   []string{missing},
	})

	var sawError bool
	for _, opt := range built.SpecOpts {
		if err := opt(context.Background(), nil, nil, spec); err != nil {
			sawError = true
		}
	}
	if !sawError {
		t.Fatalf("expected a create-time error for missing device %s, got none", missing)
	}
}

// TestBuildContainerSpec_DevicesEmptySkipped asserts that an empty devices[]
// (and blank entries) emit no Linux.Devices entries and no device-cgroup
// rules, so existing specs are unaffected. Issue #1252.
func TestBuildContainerSpec_DevicesEmptySkipped(t *testing.T) {
	spec := applyBuiltSpec(t, intmodel.ContainerSpec{
		ID:        "c1",
		Image:     "registry.eminwux.com/busybox:latest",
		CellName:  "cell",
		SpaceName: "space",
		RealmName: "realm",
		StackName: "stack",
		Devices:   []string{"", "   "},
	})
	if spec.Linux != nil && len(spec.Linux.Devices) != 0 {
		t.Errorf("expected no Linux.Devices for blank-only devices[], got %+v", spec.Linux.Devices)
	}
}
