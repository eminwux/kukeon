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
	"os"
	"strings"
	"testing"

	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

// pinDeviceResolution overrides the host-root prefix and the node resolver for
// the duration of a test, restoring both on cleanup. It lets the host-root
// rewrite be exercised deterministically without a containerized daemon or the
// root privilege a real mknod would need — the gap the issue notes unit tests
// "structurally cannot catch" is only the live containerized stat, not this
// path-rewrite logic.
func pinDeviceResolution(t *testing.T, root string, resolve func(string) (*runtimespec.LinuxDevice, error)) {
	t.Helper()
	origRoot, origResolve := deviceHostRoot, deviceFromPath
	t.Cleanup(func() { deviceHostRoot, deviceFromPath = origRoot, origResolve })
	deviceHostRoot = func() string { return root }
	deviceFromPath = resolve
}

// TestHostLinuxDeviceOpt_ResolvesViaHostRoot asserts that when kukeond is
// containerized (deviceHostRoot == /proc/1/root) a devices[] entry is stat'd
// through the host-root prefix yet exposed in the container at its original,
// un-prefixed path, with a matching device-cgroup allow rule. Issue #1261.
func TestHostLinuxDeviceOpt_ResolvesViaHostRoot(t *testing.T) {
	mode := os.FileMode(0o660)
	var statted string
	pinDeviceResolution(t, hostRootViaPID1, func(path string) (*runtimespec.LinuxDevice, error) {
		statted = path
		if path != "/proc/1/root/dev/kvm" {
			return nil, os.ErrNotExist
		}
		return &runtimespec.LinuxDevice{Type: "c", Path: path, Major: 10, Minor: 232, FileMode: &mode}, nil
	})

	spec := &runtimespec.Spec{Linux: &runtimespec.Linux{}}
	if err := hostLinuxDeviceOpt("/dev/kvm", deviceAccessRWM)(context.Background(), nil, nil, spec); err != nil {
		t.Fatalf("hostLinuxDeviceOpt returned error: %v", err)
	}

	if statted != "/proc/1/root/dev/kvm" {
		t.Errorf("node stat'd at %q, want host-root-prefixed /proc/1/root/dev/kvm", statted)
	}
	if len(spec.Linux.Devices) != 1 || spec.Linux.Devices[0].Path != "/dev/kvm" {
		t.Fatalf("Linux.Devices = %+v, want a single entry exposed at /dev/kvm", spec.Linux.Devices)
	}
	if spec.Linux.Resources == nil || len(spec.Linux.Resources.Devices) != 1 {
		t.Fatalf("expected one device-cgroup allow rule, got %+v", spec.Linux.Resources)
	}
	r := spec.Linux.Resources.Devices[0]
	if !r.Allow || r.Type != "c" || r.Major == nil || *r.Major != 10 || r.Minor == nil || *r.Minor != 232 || r.Access != "rwm" {
		t.Errorf("device-cgroup rule = %+v, want allow c 10:232 rwm", r)
	}
}

// TestHostLinuxDeviceOpt_MissingNodeNamesPath asserts that a node missing on
// the host fails container create with an error that names the requested device
// path — the "clear error for a missing node" the issue calls out as unmet by
// the path-less ENOENT the old in-daemon stat produced. Issue #1261.
func TestHostLinuxDeviceOpt_MissingNodeNamesPath(t *testing.T) {
	pinDeviceResolution(t, hostRootViaPID1, func(string) (*runtimespec.LinuxDevice, error) {
		return nil, os.ErrNotExist
	})

	err := hostLinuxDeviceOpt("/dev/kvm", deviceAccessRWM)(context.Background(), nil, nil, &runtimespec.Spec{Linux: &runtimespec.Linux{}})
	if err == nil {
		t.Fatal("expected a create-time error for a missing host node, got nil")
	}
	if !strings.Contains(err.Error(), "/dev/kvm") {
		t.Errorf("error %q does not name the device path /dev/kvm", err)
	}
}

// TestEnumerateHostDevices_RewritesContainerPath exercises the privileged-path
// walker against the test host's real /dev (standing in for a host-root /dev),
// asserting every returned node is rooted at /dev with the prefix stripped.
// Skips when the test host's /dev has no device nodes. Issue #1261.
func TestEnumerateHostDevices_RewritesContainerPath(t *testing.T) {
	devices, err := enumerateHostDevices("/dev")
	if err != nil {
		t.Fatalf("enumerateHostDevices(/dev): %v", err)
	}
	if len(devices) == 0 {
		t.Skip("no device nodes under /dev on the test host")
	}
	for _, d := range devices {
		if !strings.HasPrefix(d.Path, "/dev/") {
			t.Errorf("device exposed at %q, want a /dev/... container path", d.Path)
		}
		if d.Type == "p" {
			t.Errorf("fifo %q should have been skipped", d.Path)
		}
	}
}

// TestResolveDeviceHostRoot_UnContainerized asserts that on a host where the
// process shares PID 1's root (the unit-test environment), resolution targets
// "/" — preserving the historical in-daemon stat behavior so a privileged or
// devices[] spec built off the host behaves exactly as before. Issue #1261.
func TestResolveDeviceHostRoot_UnContainerized(t *testing.T) {
	if daemonInPrivateMountNS() {
		t.Skip("test host is in a private mount namespace relative to PID 1")
	}
	if got := resolveDeviceHostRoot(); got != "/" {
		t.Errorf("resolveDeviceHostRoot() = %q, want \"/\" when un-containerized", got)
	}
}
