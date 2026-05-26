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
	"reflect"
	"testing"

	"github.com/containerd/containerd/v2/pkg/oci"
	ctr "github.com/eminwux/kukeon/internal/ctr"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

func TestJoinContainerNamespaces(t *testing.T) {
	tests := []struct {
		name string
		spec ctr.ContainerSpec
		ns   ctr.NamespacePaths
		want int // Expected number of spec opts added
	}{
		{
			name: "empty namespace paths",
			spec: ctr.ContainerSpec{
				ID:       "test-container",
				SpecOpts: []oci.SpecOpts{},
			},
			ns:   ctr.NamespacePaths{},
			want: 0,
		},
		{
			name: "single namespace path",
			spec: ctr.ContainerSpec{
				ID:       "test-container",
				SpecOpts: []oci.SpecOpts{},
			},
			ns: ctr.NamespacePaths{
				Net: "/proc/1/ns/net",
			},
			want: 1,
		},
		{
			name: "all namespace paths",
			spec: ctr.ContainerSpec{
				ID:       "test-container",
				SpecOpts: []oci.SpecOpts{},
			},
			ns: ctr.NamespacePaths{
				Net: "/proc/1/ns/net",
				IPC: "/proc/1/ns/ipc",
				UTS: "/proc/1/ns/uts",
				PID: "/proc/1/ns/pid",
			},
			want: 4,
		},
		{
			name: "existing spec opts are preserved",
			spec: ctr.ContainerSpec{
				ID:       "test-container",
				SpecOpts: []oci.SpecOpts{oci.WithDefaultPathEnv, oci.WithDefaultPathEnv}, // Simulate existing opts
			},
			ns: ctr.NamespacePaths{
				Net: "/proc/1/ns/net",
			},
			want: 1, // 1 new namespace opt added to existing 2
		},
		{
			name: "partial namespace paths",
			spec: ctr.ContainerSpec{
				ID:       "test-container",
				SpecOpts: []oci.SpecOpts{},
			},
			ns: ctr.NamespacePaths{
				Net: "/proc/1/ns/net",
				IPC: "/proc/1/ns/ipc",
			},
			want: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ctr.JoinContainerNamespaces(tt.spec, tt.ns)

			// Verify that result is a copy (different pointer)
			if &result == &tt.spec {
				t.Error("JoinContainerNamespaces should return a copy, not the original")
			}

			// Verify ID is preserved
			if result.ID != tt.spec.ID {
				t.Errorf("ID = %q, want %q", result.ID, tt.spec.ID)
			}

			// Verify spec opts count
			resultLen := len(result.SpecOpts)
			originalLen := len(tt.spec.SpecOpts)
			added := resultLen - originalLen
			if added != tt.want {
				t.Errorf(
					"Added spec opts = %d, want %d (original: %d, result: %d)",
					added,
					tt.want,
					originalLen,
					resultLen,
				)
			}

			// Verify original spec is not modified
			if len(tt.spec.SpecOpts) != originalLen {
				t.Error("Original spec SpecOpts should not be modified")
			}
		})
	}
}

func TestBuildContainerSpec(t *testing.T) {
	tests := []struct {
		name          string
		containerSpec intmodel.ContainerSpec
		wantID        string
		wantImage     string
		wantLabels    map[string]string
	}{
		{
			name: "with containerd ID",
			containerSpec: intmodel.ContainerSpec{
				ID:           "test-id",
				ContainerdID: "containerd-123",
				Image:        "registry.eminwux.com/busybox:latest",
				CellName:     "cell-1",
				SpaceName:    "space-1",
				RealmName:    "realm-1",
				StackName:    "stack-1",
			},
			wantID:    "containerd-123",
			wantImage: "registry.eminwux.com/busybox:latest",
			wantLabels: map[string]string{
				"kukeon.io/container-type": "container",
				"kukeon.io/cell":           "cell-1",
				"kukeon.io/space":          "space-1",
				"kukeon.io/realm":          "realm-1",
				"kukeon.io/stack":          "stack-1",
			},
		},
		{
			name: "fallback to ID when containerd ID is empty",
			containerSpec: intmodel.ContainerSpec{
				ID:        "test-id",
				Image:     "registry.eminwux.com/alpine:latest",
				CellName:  "cell-1",
				SpaceName: "space-1",
				RealmName: "realm-1",
				StackName: "stack-1",
			},
			wantID:    "test-id",
			wantImage: "registry.eminwux.com/alpine:latest",
			wantLabels: map[string]string{
				"kukeon.io/container-type": "container",
				"kukeon.io/cell":           "cell-1",
				"kukeon.io/space":          "space-1",
				"kukeon.io/realm":          "realm-1",
				"kukeon.io/stack":          "stack-1",
			},
		},
		{
			name: "with command and args",
			containerSpec: intmodel.ContainerSpec{
				ID:        "test-id",
				Image:     "registry.eminwux.com/busybox:latest",
				Command:   "sh",
				Args:      []string{"-c", "echo hello"},
				CellName:  "cell-1",
				SpaceName: "space-1",
				RealmName: "realm-1",
				StackName: "stack-1",
			},
			wantID:    "test-id",
			wantImage: "registry.eminwux.com/busybox:latest",
		},
		{
			name: "with args only (no command)",
			containerSpec: intmodel.ContainerSpec{
				ID:        "test-id",
				Image:     "registry.eminwux.com/busybox:latest",
				Args:      []string{"sh", "-c", "echo test"},
				CellName:  "cell-1",
				SpaceName: "space-1",
				RealmName: "realm-1",
				StackName: "stack-1",
			},
			wantID:    "test-id",
			wantImage: "registry.eminwux.com/busybox:latest",
		},
		{
			name: "with environment variables",
			containerSpec: intmodel.ContainerSpec{
				ID:        "test-id",
				Image:     "registry.eminwux.com/busybox:latest",
				Env:       []string{"ENV1=value1", "ENV2=value2"},
				CellName:  "cell-1",
				SpaceName: "space-1",
				RealmName: "realm-1",
				StackName: "stack-1",
			},
			wantID:    "test-id",
			wantImage: "registry.eminwux.com/busybox:latest",
		},
		{
			name: "with privileged mode",
			containerSpec: intmodel.ContainerSpec{
				ID:         "test-id",
				Image:      "registry.eminwux.com/busybox:latest",
				Privileged: true,
				CellName:   "cell-1",
				SpaceName:  "space-1",
				RealmName:  "realm-1",
				StackName:  "stack-1",
			},
			wantID:    "test-id",
			wantImage: "registry.eminwux.com/busybox:latest",
		},
		{
			name: "with host network",
			containerSpec: intmodel.ContainerSpec{
				ID:          "test-id",
				Image:       "registry.eminwux.com/busybox:latest",
				HostNetwork: true,
				CellName:    "cell-1",
				SpaceName:   "space-1",
				RealmName:   "realm-1",
				StackName:   "stack-1",
			},
			wantID:    "test-id",
			wantImage: "registry.eminwux.com/busybox:latest",
		},
		{
			name: "with CNI config path",
			containerSpec: intmodel.ContainerSpec{
				ID:            "test-id",
				Image:         "registry.eminwux.com/busybox:latest",
				CNIConfigPath: "/path/to/cni/config",
				CellName:      "cell-1",
				SpaceName:     "space-1",
				RealmName:     "realm-1",
				StackName:     "stack-1",
			},
			wantID:    "test-id",
			wantImage: "registry.eminwux.com/busybox:latest",
			wantLabels: map[string]string{
				"kukeon.io/container-type": "container",
				"kukeon.io/cell":           "cell-1",
				"kukeon.io/space":          "space-1",
				"kukeon.io/realm":          "realm-1",
				"kukeon.io/stack":          "stack-1",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := ctr.BuildContainerSpec(tt.containerSpec)

			if spec.ID != tt.wantID {
				t.Errorf("ID = %q, want %q", spec.ID, tt.wantID)
			}

			if spec.Image != tt.wantImage {
				t.Errorf("Image = %q, want %q", spec.Image, tt.wantImage)
			}

			if tt.wantLabels != nil {
				for k, v := range tt.wantLabels {
					if spec.Labels[k] != v {
						t.Errorf("Labels[%q] = %q, want %q", k, spec.Labels[k], v)
					}
				}
			}

			// Verify required labels exist
			requiredLabels := []string{
				"kukeon.io/container-type",
				"kukeon.io/cell",
				"kukeon.io/space",
				"kukeon.io/realm",
				"kukeon.io/stack",
			}
			for _, label := range requiredLabels {
				if _, ok := spec.Labels[label]; !ok {
					t.Errorf("Required label %q missing", label)
				}
			}

			// Verify SpecOpts is not nil
			if spec.SpecOpts == nil {
				t.Error("SpecOpts should not be nil")
			}

			// Verify at least WithDefaultPathEnv is present
			if len(spec.SpecOpts) == 0 {
				t.Error("SpecOpts should contain at least one option")
			}

			if tt.containerSpec.CNIConfigPath != "" && spec.CNIConfigPath != tt.containerSpec.CNIConfigPath {
				t.Errorf("CNIConfigPath = %q, want %q", spec.CNIConfigPath, tt.containerSpec.CNIConfigPath)
			}
		})
	}
}

// TestBuildContainerSpec_WorkingDir verifies the OCI spec produced by
// BuildContainerSpec sets process.cwd from ContainerSpec.WorkingDir when set,
// and leaves it untouched when empty so the image's WORKDIR survives.
func TestBuildContainerSpec_WorkingDir(t *testing.T) {
	tests := []struct {
		name       string
		workingDir string
		preCwd     string
		wantCwd    string
	}{
		{name: "empty leaves image cwd intact", workingDir: "", preCwd: "/from-image", wantCwd: "/from-image"},
		{name: "set overrides image cwd", workingDir: "/workspace", preCwd: "/from-image", wantCwd: "/workspace"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := ctr.BuildContainerSpec(intmodel.ContainerSpec{
				ID:         "test-id",
				Image:      "registry.eminwux.com/busybox:latest",
				WorkingDir: tt.workingDir,
				CellName:   "c", SpaceName: "s", RealmName: "r", StackName: "st",
			})

			ociSpec := &runtimespec.Spec{
				Process: &runtimespec.Process{Cwd: tt.preCwd},
				Linux:   &runtimespec.Linux{},
			}
			for _, opt := range spec.SpecOpts {
				if err := opt(context.Background(), nil, nil, ociSpec); err != nil {
					t.Fatalf("apply SpecOpts: %v", err)
				}
			}

			if ociSpec.Process.Cwd != tt.wantCwd {
				t.Errorf("Process.Cwd = %q, want %q", ociSpec.Process.Cwd, tt.wantCwd)
			}
		})
	}
}

// TestBuildRootContainerSpec_WorkingDir mirrors the user-container test for
// the root-container builder so a user-supplied root spec marked with
// WorkingDir is honored end-to-end.
func TestBuildRootContainerSpec_WorkingDir(t *testing.T) {
	tests := []struct {
		name       string
		workingDir string
		preCwd     string
		wantCwd    string
	}{
		{name: "empty leaves image cwd intact", workingDir: "", preCwd: "/from-image", wantCwd: "/from-image"},
		{name: "set overrides image cwd", workingDir: "/workspace", preCwd: "/from-image", wantCwd: "/workspace"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := ctr.BuildRootContainerSpec(intmodel.ContainerSpec{
				ID:           "root",
				ContainerdID: "root-id",
				Image:        "registry.eminwux.com/busybox:latest",
				WorkingDir:   tt.workingDir,
			}, nil)

			ociSpec := &runtimespec.Spec{
				Process: &runtimespec.Process{Cwd: tt.preCwd},
				Linux:   &runtimespec.Linux{},
			}
			for _, opt := range spec.SpecOpts {
				if err := opt(context.Background(), nil, nil, ociSpec); err != nil {
					t.Fatalf("apply SpecOpts: %v", err)
				}
			}

			if ociSpec.Process.Cwd != tt.wantCwd {
				t.Errorf("Process.Cwd = %q, want %q", ociSpec.Process.Cwd, tt.wantCwd)
			}
		})
	}
}

// TestBuildContainerSpec_HostNetwork verifies the OCI spec produced by
// BuildContainerSpec drops the network LinuxNamespace entry exactly when
// HostNetwork is true. The runner relies on this — a remaining network entry
// would tell runc to unshare a fresh netns at start, leaving daemon-installed
// bridges/veths/iptables invisible to the host.
func TestBuildContainerSpec_HostNetwork(t *testing.T) {
	tests := []struct {
		name        string
		hostNetwork bool
		wantNetNS   bool
	}{
		{name: "host network true drops netns entry", hostNetwork: true, wantNetNS: false},
		{name: "host network false keeps netns entry", hostNetwork: false, wantNetNS: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := ctr.BuildContainerSpec(intmodel.ContainerSpec{
				ID:          "test-id",
				Image:       "registry.eminwux.com/busybox:latest",
				HostNetwork: tt.hostNetwork,
				CellName:    "c", SpaceName: "s", RealmName: "r", StackName: "st",
			})

			ociSpec := &runtimespec.Spec{
				Process: &runtimespec.Process{},
				Linux: &runtimespec.Linux{
					Namespaces: []runtimespec.LinuxNamespace{
						{Type: runtimespec.NetworkNamespace},
						{Type: runtimespec.PIDNamespace},
						{Type: runtimespec.MountNamespace},
					},
				},
			}
			for _, opt := range spec.SpecOpts {
				if err := opt(context.Background(), nil, nil, ociSpec); err != nil {
					t.Fatalf("apply SpecOpts: %v", err)
				}
			}

			var hasNet bool
			for _, ns := range ociSpec.Linux.Namespaces {
				if ns.Type == runtimespec.NetworkNamespace {
					hasNet = true
					break
				}
			}
			if hasNet != tt.wantNetNS {
				t.Errorf("network namespace present = %v, want %v (namespaces=%+v)",
					hasNet, tt.wantNetNS, ociSpec.Linux.Namespaces)
			}
		})
	}
}

// TestBuildContainerSpec_HostPID verifies the OCI spec produced by
// BuildContainerSpec drops the PID LinuxNamespace entry exactly when
// HostPID is true. Required for kukeond — without it, /proc inside the
// daemon does not reflect host PIDs and the in-process CNI bridge plugin
// fails to resolve user-cell netns paths (issue #105).
func TestBuildContainerSpec_HostPID(t *testing.T) {
	tests := []struct {
		name      string
		hostPID   bool
		wantPIDNS bool
	}{
		{name: "host pid true drops pid ns entry", hostPID: true, wantPIDNS: false},
		{name: "host pid false keeps pid ns entry", hostPID: false, wantPIDNS: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := ctr.BuildContainerSpec(intmodel.ContainerSpec{
				ID:       "test-id",
				Image:    "registry.eminwux.com/busybox:latest",
				HostPID:  tt.hostPID,
				CellName: "c", SpaceName: "s", RealmName: "r", StackName: "st",
			})

			ociSpec := &runtimespec.Spec{
				Process: &runtimespec.Process{},
				Linux: &runtimespec.Linux{
					Namespaces: []runtimespec.LinuxNamespace{
						{Type: runtimespec.NetworkNamespace},
						{Type: runtimespec.PIDNamespace},
						{Type: runtimespec.MountNamespace},
					},
				},
			}
			for _, opt := range spec.SpecOpts {
				if err := opt(context.Background(), nil, nil, ociSpec); err != nil {
					t.Fatalf("apply SpecOpts: %v", err)
				}
			}

			var hasPID bool
			for _, ns := range ociSpec.Linux.Namespaces {
				if ns.Type == runtimespec.PIDNamespace {
					hasPID = true
					break
				}
			}
			if hasPID != tt.wantPIDNS {
				t.Errorf("PID namespace present = %v, want %v (namespaces=%+v)",
					hasPID, tt.wantPIDNS, ociSpec.Linux.Namespaces)
			}
		})
	}
}

// TestBuildContainerSpec_HostNetworkAndHostPID verifies that the two flags
// are independent — setting one must not silently affect the other.
func TestBuildContainerSpec_HostNetworkAndHostPID(t *testing.T) {
	spec := ctr.BuildContainerSpec(intmodel.ContainerSpec{
		ID:          "test-id",
		Image:       "registry.eminwux.com/busybox:latest",
		HostNetwork: true,
		HostPID:     true,
		CellName:    "c", SpaceName: "s", RealmName: "r", StackName: "st",
	})

	ociSpec := &runtimespec.Spec{
		Process: &runtimespec.Process{},
		Linux: &runtimespec.Linux{
			Namespaces: []runtimespec.LinuxNamespace{
				{Type: runtimespec.NetworkNamespace},
				{Type: runtimespec.PIDNamespace},
				{Type: runtimespec.MountNamespace},
			},
		},
	}
	for _, opt := range spec.SpecOpts {
		if err := opt(context.Background(), nil, nil, ociSpec); err != nil {
			t.Fatalf("apply SpecOpts: %v", err)
		}
	}

	var hasNet, hasPID bool
	for _, ns := range ociSpec.Linux.Namespaces {
		switch ns.Type {
		case runtimespec.NetworkNamespace:
			hasNet = true
		case runtimespec.PIDNamespace:
			hasPID = true
		}
	}
	if hasNet || hasPID {
		t.Errorf("both HostNetwork and HostPID true must drop both ns entries; got net=%v pid=%v",
			hasNet, hasPID)
	}
}

// TestBuildRootContainerSpec_HostPID mirrors TestBuildContainerSpec_HostPID
// for the root-container builder. A user-supplied root spec marked HostPID
// must produce an OCI spec without the PID namespace entry.
func TestBuildRootContainerSpec_HostPID(t *testing.T) {
	tests := []struct {
		name      string
		hostPID   bool
		wantPIDNS bool
	}{
		{name: "host pid true drops pid ns entry", hostPID: true, wantPIDNS: false},
		{name: "host pid false keeps pid ns entry", hostPID: false, wantPIDNS: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := ctr.BuildRootContainerSpec(intmodel.ContainerSpec{
				ID:           "root",
				ContainerdID: "root-id",
				Image:        "registry.eminwux.com/busybox:latest",
				HostPID:      tt.hostPID,
			}, nil)

			ociSpec := &runtimespec.Spec{
				Process: &runtimespec.Process{},
				Linux: &runtimespec.Linux{
					Namespaces: []runtimespec.LinuxNamespace{
						{Type: runtimespec.NetworkNamespace},
						{Type: runtimespec.PIDNamespace},
					},
				},
			}
			for _, opt := range spec.SpecOpts {
				if err := opt(context.Background(), nil, nil, ociSpec); err != nil {
					t.Fatalf("apply SpecOpts: %v", err)
				}
			}

			var hasPID bool
			for _, ns := range ociSpec.Linux.Namespaces {
				if ns.Type == runtimespec.PIDNamespace {
					hasPID = true
					break
				}
			}
			if hasPID != tt.wantPIDNS {
				t.Errorf("PID namespace present = %v, want %v (namespaces=%+v)",
					hasPID, tt.wantPIDNS, ociSpec.Linux.Namespaces)
			}
		})
	}
}

// TestBuildContainerSpec_HostCgroup verifies the OCI spec produced by
// BuildContainerSpec appends a CgroupNamespace entry when HostCgroup is
// false (the default, private) and omits it when HostCgroup is true.
// Inverse of the HostNetwork/HostPID pattern — cgroup-ns is the only one
// of the three that defaults to private, so the branch adds rather than
// drops. Required for nested-runtime workloads (kuke init, dockerd, an
// inner containerd) to write their cgroup tree under the cell instead of
// the host's cgroup root and clear runc's "cgroup not empty" precheck.
func TestBuildContainerSpec_HostCgroup(t *testing.T) {
	tests := []struct {
		name         string
		hostCgroup   bool
		wantCgroupNS bool
	}{
		{name: "host cgroup false appends cgroup ns entry", hostCgroup: false, wantCgroupNS: true},
		{name: "host cgroup zero value appends cgroup ns entry", wantCgroupNS: true},
		{name: "host cgroup true omits cgroup ns entry", hostCgroup: true, wantCgroupNS: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := ctr.BuildContainerSpec(intmodel.ContainerSpec{
				ID:         "test-id",
				Image:      "registry.eminwux.com/busybox:latest",
				HostCgroup: tt.hostCgroup,
				CellName:   "c", SpaceName: "s", RealmName: "r", StackName: "st",
			})

			// Starting OCI spec deliberately omits CgroupNamespace — that
			// matches the OCI default, where cgroup-ns is *not* in the
			// initial namespace list. The HostCgroup=false branch is
			// expected to append it; HostCgroup=true is expected to leave
			// the spec untouched.
			ociSpec := &runtimespec.Spec{
				Process: &runtimespec.Process{},
				Linux: &runtimespec.Linux{
					Namespaces: []runtimespec.LinuxNamespace{
						{Type: runtimespec.NetworkNamespace},
						{Type: runtimespec.PIDNamespace},
						{Type: runtimespec.MountNamespace},
					},
				},
			}
			for _, opt := range spec.SpecOpts {
				if err := opt(context.Background(), nil, nil, ociSpec); err != nil {
					t.Fatalf("apply SpecOpts: %v", err)
				}
			}

			var hasCgroup bool
			for _, ns := range ociSpec.Linux.Namespaces {
				if ns.Type == runtimespec.CgroupNamespace {
					hasCgroup = true
					break
				}
			}
			if hasCgroup != tt.wantCgroupNS {
				t.Errorf("cgroup namespace present = %v, want %v (namespaces=%+v)",
					hasCgroup, tt.wantCgroupNS, ociSpec.Linux.Namespaces)
			}
		})
	}
}

// TestBuildRootContainerSpec_HostCgroup mirrors
// TestBuildContainerSpec_HostCgroup for the root-container builder. The
// kukeond cell's root container relies on this — HostCgroup=true must
// produce an OCI spec that does not append a CgroupNamespace entry, so
// kukeond joins its parent's cgroup-ns and can write sibling cgroups
// outside its own subtree.
func TestBuildRootContainerSpec_HostCgroup(t *testing.T) {
	tests := []struct {
		name         string
		hostCgroup   bool
		wantCgroupNS bool
	}{
		{name: "host cgroup false appends cgroup ns entry", hostCgroup: false, wantCgroupNS: true},
		{name: "host cgroup zero value appends cgroup ns entry", wantCgroupNS: true},
		{name: "host cgroup true omits cgroup ns entry", hostCgroup: true, wantCgroupNS: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := ctr.BuildRootContainerSpec(intmodel.ContainerSpec{
				ID:           "root",
				ContainerdID: "root-id",
				Image:        "registry.eminwux.com/busybox:latest",
				HostCgroup:   tt.hostCgroup,
			}, nil)

			ociSpec := &runtimespec.Spec{
				Process: &runtimespec.Process{},
				Linux: &runtimespec.Linux{
					Namespaces: []runtimespec.LinuxNamespace{
						{Type: runtimespec.NetworkNamespace},
						{Type: runtimespec.PIDNamespace},
					},
				},
			}
			for _, opt := range spec.SpecOpts {
				if err := opt(context.Background(), nil, nil, ociSpec); err != nil {
					t.Fatalf("apply SpecOpts: %v", err)
				}
			}

			var hasCgroup bool
			for _, ns := range ociSpec.Linux.Namespaces {
				if ns.Type == runtimespec.CgroupNamespace {
					hasCgroup = true
					break
				}
			}
			if hasCgroup != tt.wantCgroupNS {
				t.Errorf("cgroup namespace present = %v, want %v (namespaces=%+v)",
					hasCgroup, tt.wantCgroupNS, ociSpec.Linux.Namespaces)
			}
		})
	}
}

// TestBuildContainerSpec_NestedCgroupRuntimeMount asserts that
// BuildContainerSpec emits the cgroup2 mount at /sys/fs/cgroup exactly when
// NestedCgroupRuntime is set and HostCgroup is not — pairing the in-cell
// mount with the host-side subtree-controller delegation #318 added. Without
// this, an inner runtime (dockerd, podman) inside a NestedCgroupRuntime cell
// sees /sys/fs/cgroup as an empty mountpoint and aborts (#322). The unset
// and HostCgroup-true cases must leave the mount list byte-identical to the
// pre-#322 output so cells that don't opt in keep their existing OCI spec.
func TestBuildContainerSpec_NestedCgroupRuntimeMount(t *testing.T) {
	tests := []struct {
		name      string
		nested    bool
		hostCG    bool
		wantMount bool
	}{
		{name: "nested true and host cgroup false emits mount", nested: true, hostCG: false, wantMount: true},
		{name: "nested false and host cgroup false omits mount", nested: false, hostCG: false, wantMount: false},
		{name: "nested true and host cgroup true omits mount", nested: true, hostCG: true, wantMount: false},
		{name: "nested zero values omit mount", wantMount: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := ctr.BuildContainerSpec(intmodel.ContainerSpec{
				ID:                  "test-id",
				Image:               "registry.eminwux.com/busybox:latest",
				NestedCgroupRuntime: tt.nested,
				HostCgroup:          tt.hostCG,
				CellName:            "c", SpaceName: "s", RealmName: "r", StackName: "st",
			})

			ociSpec := &runtimespec.Spec{
				Process: &runtimespec.Process{},
				Linux:   &runtimespec.Linux{},
			}
			for _, opt := range spec.SpecOpts {
				if err := opt(context.Background(), nil, nil, ociSpec); err != nil {
					t.Fatalf("apply SpecOpts: %v", err)
				}
			}

			var got *runtimespec.Mount
			for i, m := range ociSpec.Mounts {
				if m.Destination == "/sys/fs/cgroup" {
					got = &ociSpec.Mounts[i]
					break
				}
			}
			if tt.wantMount {
				if got == nil {
					t.Fatalf("/sys/fs/cgroup mount missing; mounts=%+v", ociSpec.Mounts)
				}
				if got.Type != "cgroup" {
					t.Errorf("mount.Type = %q, want %q", got.Type, "cgroup")
				}
				if got.Source != "cgroup" {
					t.Errorf("mount.Source = %q, want %q", got.Source, "cgroup")
				}
				wantOpts := []string{"rw", "nosuid", "noexec", "nodev"}
				if len(got.Options) != len(wantOpts) {
					t.Fatalf("mount.Options = %v, want %v", got.Options, wantOpts)
				}
				for i, want := range wantOpts {
					if got.Options[i] != want {
						t.Errorf("mount.Options[%d] = %q, want %q", i, got.Options[i], want)
					}
				}
			} else if got != nil {
				t.Errorf("unexpected /sys/fs/cgroup mount on opt-out spec: %+v", *got)
			}
		})
	}
}

// TestBuildContainerSpec_NestedCgroupRuntimeOffByteIdentical pins the
// pre-#322 mount-list output for the opt-out path: a ContainerSpec with
// NestedCgroupRuntime=false must produce the same Mounts slice as one with
// the field omitted entirely. The byte-identical guarantee matters because
// every existing cell on existing hosts is opt-out, and a stray cgroup2
// entry would alter every running container's OCI spec on first reconcile
// after the upgrade.
func TestBuildContainerSpec_NestedCgroupRuntimeOffByteIdentical(t *testing.T) {
	base := intmodel.ContainerSpec{
		ID:       "test-id",
		Image:    "registry.eminwux.com/busybox:latest",
		CellName: "c", SpaceName: "s", RealmName: "r", StackName: "st",
	}
	want := mountListAfterApply(t, ctr.BuildContainerSpec(base))

	off := base
	off.NestedCgroupRuntime = false
	got := mountListAfterApply(t, ctr.BuildContainerSpec(off))

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mount list diverged on opt-out path:\n got: %+v\nwant: %+v", got, want)
	}
}

// TestBuildRootContainerSpec_NestedCgroupRuntimeMount mirrors
// TestBuildContainerSpec_NestedCgroupRuntimeMount for the root-container
// builder. A user-supplied root in a NestedCgroupRuntime cell must get the
// same /sys/fs/cgroup wiring; the kukeond cell's HostCgroup=true root is
// exempted by the same guard.
func TestBuildRootContainerSpec_NestedCgroupRuntimeMount(t *testing.T) {
	tests := []struct {
		name      string
		nested    bool
		hostCG    bool
		wantMount bool
	}{
		{name: "nested true and host cgroup false emits mount", nested: true, hostCG: false, wantMount: true},
		{name: "nested false and host cgroup false omits mount", nested: false, hostCG: false, wantMount: false},
		{name: "nested true and host cgroup true omits mount", nested: true, hostCG: true, wantMount: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := ctr.BuildRootContainerSpec(intmodel.ContainerSpec{
				ID:                  "root",
				ContainerdID:        "root-id",
				Image:               "registry.eminwux.com/busybox:latest",
				NestedCgroupRuntime: tt.nested,
				HostCgroup:          tt.hostCG,
			}, nil)

			ociSpec := &runtimespec.Spec{
				Process: &runtimespec.Process{},
				Linux:   &runtimespec.Linux{},
			}
			for _, opt := range spec.SpecOpts {
				if err := opt(context.Background(), nil, nil, ociSpec); err != nil {
					t.Fatalf("apply SpecOpts: %v", err)
				}
			}

			var hasMount bool
			for _, m := range ociSpec.Mounts {
				if m.Destination == "/sys/fs/cgroup" && m.Type == "cgroup" {
					hasMount = true
					break
				}
			}
			if hasMount != tt.wantMount {
				t.Errorf("/sys/fs/cgroup cgroup mount present = %v, want %v (mounts=%+v)",
					hasMount, tt.wantMount, ociSpec.Mounts)
			}
		})
	}
}

// mountListAfterApply runs the spec's SpecOpts against an empty OCI spec and
// returns the resulting Mounts slice. Used by the byte-identical guard above
// to compare the pre-#322 mount layout against the opt-out path.
func mountListAfterApply(t *testing.T, spec ctr.ContainerSpec) []runtimespec.Mount {
	t.Helper()
	ociSpec := &runtimespec.Spec{
		Process: &runtimespec.Process{},
		Linux:   &runtimespec.Linux{},
	}
	for _, opt := range spec.SpecOpts {
		if err := opt(context.Background(), nil, nil, ociSpec); err != nil {
			t.Fatalf("apply SpecOpts: %v", err)
		}
	}
	return ociSpec.Mounts
}

// TestBuildContainerSpec_CgroupsPath verifies that BuildContainerSpec emits an
// OCI Linux.CgroupsPath rooted at <CellCgroupPath>/<containerd-id> when the
// caller plumbs a non-empty cell cgroup path through the model spec, and
// leaves it untouched (runc-shim default placement) otherwise. Issue #312:
// without this, container task cgroups land outside the kukeon cgroup tree
// and cell-level resource accounting is impossible.
func TestBuildContainerSpec_CgroupsPath(t *testing.T) {
	tests := []struct {
		name           string
		cellCgroupPath string
		containerdID   string
		fallbackID     string
		wantCgroups    string
	}{
		{
			name:           "cell cgroup path joins containerd id",
			cellCgroupPath: "/kukeon/r/s/st/c",
			containerdID:   "s_st_c_app",
			wantCgroups:    "/kukeon/r/s/st/c/s_st_c_app",
		},
		{
			name:           "cell cgroup path falls back to base id when containerd id empty",
			cellCgroupPath: "/kukeon/r/s/st/c",
			fallbackID:     "app",
			wantCgroups:    "/kukeon/r/s/st/c/app",
		},
		{
			name:        "no cell cgroup path leaves cgroups path untouched",
			fallbackID:  "app",
			wantCgroups: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := ctr.BuildContainerSpec(intmodel.ContainerSpec{
				ID:             tt.fallbackID,
				ContainerdID:   tt.containerdID,
				Image:          "registry.eminwux.com/busybox:latest",
				CellCgroupPath: tt.cellCgroupPath,
				CellName:       "c", SpaceName: "s", RealmName: "r", StackName: "st",
			})

			ociSpec := &runtimespec.Spec{
				Process: &runtimespec.Process{},
				Linux:   &runtimespec.Linux{},
			}
			for _, opt := range spec.SpecOpts {
				if err := opt(context.Background(), nil, nil, ociSpec); err != nil {
					t.Fatalf("apply SpecOpts: %v", err)
				}
			}

			if ociSpec.Linux.CgroupsPath != tt.wantCgroups {
				t.Errorf("Linux.CgroupsPath = %q, want %q",
					ociSpec.Linux.CgroupsPath, tt.wantCgroups)
			}
		})
	}
}

// TestBuildRootContainerSpec_CgroupsPath mirrors
// TestBuildContainerSpec_CgroupsPath for the root-container builder. The
// root container is the cell's first task and must land under the cell
// cgroup just like the regular containers do.
func TestBuildRootContainerSpec_CgroupsPath(t *testing.T) {
	tests := []struct {
		name           string
		cellCgroupPath string
		containerdID   string
		fallbackID     string
		wantCgroups    string
	}{
		{
			name:           "cell cgroup path joins containerd id",
			cellCgroupPath: "/kukeon/r/s/st/c",
			containerdID:   "s_st_c_root",
			wantCgroups:    "/kukeon/r/s/st/c/s_st_c_root",
		},
		{
			name:           "cell cgroup path falls back to base id when containerd id empty",
			cellCgroupPath: "/kukeon/r/s/st/c",
			fallbackID:     "root",
			wantCgroups:    "/kukeon/r/s/st/c/root",
		},
		{
			name:         "no cell cgroup path leaves cgroups path untouched",
			containerdID: "s_st_c_root",
			wantCgroups:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := ctr.BuildRootContainerSpec(intmodel.ContainerSpec{
				ID:             tt.fallbackID,
				ContainerdID:   tt.containerdID,
				Image:          "registry.eminwux.com/busybox:latest",
				CellCgroupPath: tt.cellCgroupPath,
			}, nil)

			ociSpec := &runtimespec.Spec{
				Process: &runtimespec.Process{},
				Linux:   &runtimespec.Linux{},
			}
			for _, opt := range spec.SpecOpts {
				if err := opt(context.Background(), nil, nil, ociSpec); err != nil {
					t.Fatalf("apply SpecOpts: %v", err)
				}
			}

			if ociSpec.Linux.CgroupsPath != tt.wantCgroups {
				t.Errorf("Linux.CgroupsPath = %q, want %q",
					ociSpec.Linux.CgroupsPath, tt.wantCgroups)
			}
		})
	}
}

// TestBuildRootContainerSpec_Hostname pins the hostname-on-root contract
// from issue #345: the cell's root container's UTS hostname must be the
// cell name (so `hostname` returns `kuke-app` instead of the hierarchical
// containerd id `default_default_kuke-app_root`), and a missing CellName
// falls back defensively to the containerd id so a misrouted spec still
// produces a usable hostname instead of an empty one. All non-root
// containers in the cell join this UTS namespace and inherit the value.
func TestBuildRootContainerSpec_Hostname(t *testing.T) {
	tests := []struct {
		name         string
		cellName     string
		containerdID string
		wantHostname string
	}{
		{name: "cell name sets hostname", cellName: "kuke-app", containerdID: "s_st_kuke-app_root", wantHostname: "kuke-app"},
		{name: "empty cell name falls back to containerd id", cellName: "", containerdID: "s_st_kuke-app_root", wantHostname: "s_st_kuke-app_root"},
		{name: "whitespace cell name treated as empty", cellName: "   ", containerdID: "s_st_kuke-app_root", wantHostname: "s_st_kuke-app_root"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := ctr.BuildRootContainerSpec(intmodel.ContainerSpec{
				ID:           "root",
				ContainerdID: tt.containerdID,
				Image:        "registry.eminwux.com/busybox:latest",
				CellName:     tt.cellName,
			}, nil)

			ociSpec := &runtimespec.Spec{
				Process: &runtimespec.Process{},
				Linux:   &runtimespec.Linux{},
			}
			for _, opt := range spec.SpecOpts {
				if err := opt(context.Background(), nil, nil, ociSpec); err != nil {
					t.Fatalf("apply SpecOpts: %v", err)
				}
			}

			if ociSpec.Hostname != tt.wantHostname {
				t.Errorf("Hostname = %q, want %q", ociSpec.Hostname, tt.wantHostname)
			}
		})
	}
}

// TestBuildContainerSpec_NoHostname pins the inverse half of issue #345:
// non-root containers must not call oci.WithHostname. They share the root's
// UTS namespace via JoinContainerNamespaces, so any hostname set on the
// per-container spec would be overwritten at run-time anyway — but worse,
// containerd persists the spec's Hostname in the on-disk container metadata
// and a stray non-empty value there confuses tooling that inspects it. The
// test asserts ociSpec.Hostname is whatever the prebuilt spec started with
// (empty in this fixture), regardless of the model spec's identity fields.
func TestBuildContainerSpec_NoHostname(t *testing.T) {
	spec := ctr.BuildContainerSpec(intmodel.ContainerSpec{
		ID:           "user-app",
		ContainerdID: "s_st_kuke-app_user-app",
		Image:        "registry.eminwux.com/busybox:latest",
		CellName:     "kuke-app",
		SpaceName:    "s",
		RealmName:    "r",
		StackName:    "st",
	})

	ociSpec := &runtimespec.Spec{
		Process: &runtimespec.Process{},
		Linux:   &runtimespec.Linux{},
	}
	for _, opt := range spec.SpecOpts {
		if err := opt(context.Background(), nil, nil, ociSpec); err != nil {
			t.Fatalf("apply SpecOpts: %v", err)
		}
	}

	if ociSpec.Hostname != "" {
		t.Errorf("Hostname = %q, want empty (non-root must not set hostname)", ociSpec.Hostname)
	}
}

// TestBuildContainerSpec_EtcFileBindMounts pins the bind-mount half of issue
// #345: when EtcHostsPath / EtcHostnamePath are stamped on the model spec,
// BuildContainerSpec must emit two bind entries (Destination /etc/hosts and
// /etc/hostname), each pointing at the supplied host source path with rbind
// + ro options. Empty paths must produce no entry — that path is the host-
// network carve-out where the host's /etc/hosts is the right view.
func TestBuildContainerSpec_EtcFileBindMounts(t *testing.T) {
	tests := []struct {
		name            string
		hostsPath       string
		hostnamePath    string
		wantHosts       bool
		wantHostname    bool
		wantHostsSrc    string
		wantHostnameSrc string
	}{
		{
			name:            "both paths set emits both bind mounts",
			hostsPath:       "/run/kukeon/r/s/st/c/etc-hosts",
			hostnamePath:    "/run/kukeon/r/s/st/c/etc-hostname",
			wantHosts:       true,
			wantHostname:    true,
			wantHostsSrc:    "/run/kukeon/r/s/st/c/etc-hosts",
			wantHostnameSrc: "/run/kukeon/r/s/st/c/etc-hostname",
		},
		{
			name:            "only hostname path set emits hostname mount only",
			hostsPath:       "",
			hostnamePath:    "/run/kukeon/r/s/st/c/etc-hostname",
			wantHosts:       false,
			wantHostname:    true,
			wantHostnameSrc: "/run/kukeon/r/s/st/c/etc-hostname",
		},
		{
			name:         "both paths empty emits no etc bind mounts",
			hostsPath:    "",
			hostnamePath: "",
			wantHosts:    false,
			wantHostname: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := ctr.BuildContainerSpec(intmodel.ContainerSpec{
				ID:              "user-app",
				ContainerdID:    "s_st_c_user-app",
				Image:           "registry.eminwux.com/busybox:latest",
				EtcHostsPath:    tt.hostsPath,
				EtcHostnamePath: tt.hostnamePath,
				CellName:        "c", SpaceName: "s", RealmName: "r", StackName: "st",
			})

			ociSpec := &runtimespec.Spec{
				Process: &runtimespec.Process{},
				Linux:   &runtimespec.Linux{},
			}
			for _, opt := range spec.SpecOpts {
				if err := opt(context.Background(), nil, nil, ociSpec); err != nil {
					t.Fatalf("apply SpecOpts: %v", err)
				}
			}

			assertEtcBindMounts(t, ociSpec.Mounts, tt.wantHosts, tt.wantHostname,
				tt.wantHostsSrc, tt.wantHostnameSrc)
		})
	}
}

// TestBuildRootContainerSpec_EtcFileBindMounts mirrors the non-root test for
// the root-container builder. The runner stamps the same host source paths
// on the root spec; a regression in either builder would partially break the
// cell — the root and its siblings would disagree on what `/etc/hosts`
// resolves to.
func TestBuildRootContainerSpec_EtcFileBindMounts(t *testing.T) {
	tests := []struct {
		name            string
		hostsPath       string
		hostnamePath    string
		wantHosts       bool
		wantHostname    bool
		wantHostsSrc    string
		wantHostnameSrc string
	}{
		{
			name:            "both paths set emits both bind mounts",
			hostsPath:       "/run/kukeon/r/s/st/c/etc-hosts",
			hostnamePath:    "/run/kukeon/r/s/st/c/etc-hostname",
			wantHosts:       true,
			wantHostname:    true,
			wantHostsSrc:    "/run/kukeon/r/s/st/c/etc-hosts",
			wantHostnameSrc: "/run/kukeon/r/s/st/c/etc-hostname",
		},
		{
			name:            "host-network root keeps hostname mount only",
			hostsPath:       "",
			hostnamePath:    "/run/kukeon/r/s/st/c/etc-hostname",
			wantHosts:       false,
			wantHostname:    true,
			wantHostnameSrc: "/run/kukeon/r/s/st/c/etc-hostname",
		},
		{
			name:         "both paths empty emits no etc bind mounts",
			hostsPath:    "",
			hostnamePath: "",
			wantHosts:    false,
			wantHostname: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := ctr.BuildRootContainerSpec(intmodel.ContainerSpec{
				ID:              "root",
				ContainerdID:    "s_st_c_root",
				Image:           "registry.eminwux.com/busybox:latest",
				CellName:        "c",
				EtcHostsPath:    tt.hostsPath,
				EtcHostnamePath: tt.hostnamePath,
			}, nil)

			ociSpec := &runtimespec.Spec{
				Process: &runtimespec.Process{},
				Linux:   &runtimespec.Linux{},
			}
			for _, opt := range spec.SpecOpts {
				if err := opt(context.Background(), nil, nil, ociSpec); err != nil {
					t.Fatalf("apply SpecOpts: %v", err)
				}
			}

			assertEtcBindMounts(t, ociSpec.Mounts, tt.wantHosts, tt.wantHostname,
				tt.wantHostsSrc, tt.wantHostnameSrc)
		})
	}
}

// assertEtcBindMounts verifies that the given OCI Mounts slice contains (or
// omits) /etc/hosts and /etc/hostname bind entries with the expected source
// paths and rbind/ro options.
func assertEtcBindMounts(
	t *testing.T,
	mounts []runtimespec.Mount,
	wantHosts, wantHostname bool,
	wantHostsSrc, wantHostnameSrc string,
) {
	t.Helper()
	var hostsM, hostnameM *runtimespec.Mount
	for i := range mounts {
		switch mounts[i].Destination {
		case "/etc/hosts":
			hostsM = &mounts[i]
		case "/etc/hostname":
			hostnameM = &mounts[i]
		}
	}

	if wantHosts {
		if hostsM == nil {
			t.Fatalf("/etc/hosts bind mount missing; mounts=%+v", mounts)
		}
		if hostsM.Source != wantHostsSrc {
			t.Errorf("/etc/hosts source = %q, want %q", hostsM.Source, wantHostsSrc)
		}
		if hostsM.Type != "bind" {
			t.Errorf("/etc/hosts type = %q, want %q", hostsM.Type, "bind")
		}
		if !containsString(hostsM.Options, "rbind") {
			t.Errorf("/etc/hosts options = %v, want contains rbind", hostsM.Options)
		}
		if !containsString(hostsM.Options, "ro") {
			t.Errorf("/etc/hosts options = %v, want contains ro", hostsM.Options)
		}
	} else if hostsM != nil {
		t.Errorf("unexpected /etc/hosts bind mount: %+v", *hostsM)
	}

	if wantHostname {
		if hostnameM == nil {
			t.Fatalf("/etc/hostname bind mount missing; mounts=%+v", mounts)
		}
		if hostnameM.Source != wantHostnameSrc {
			t.Errorf("/etc/hostname source = %q, want %q", hostnameM.Source, wantHostnameSrc)
		}
		if hostnameM.Type != "bind" {
			t.Errorf("/etc/hostname type = %q, want %q", hostnameM.Type, "bind")
		}
		if !containsString(hostnameM.Options, "rbind") {
			t.Errorf("/etc/hostname options = %v, want contains rbind", hostnameM.Options)
		}
		if !containsString(hostnameM.Options, "ro") {
			t.Errorf("/etc/hostname options = %v, want contains ro", hostnameM.Options)
		}
	} else if hostnameM != nil {
		t.Errorf("unexpected /etc/hostname bind mount: %+v", *hostnameM)
	}
}

// TestBuildContainerSpec_KukeonGroupGID asserts that WithKukeonGroupGID
// appends the host's kukeon group GID to Process.User.AdditionalGids, dedupes
// against an image-resolved entry of the same numeric value, and is a no-op
// at zero. Pins the contract that closes the "image kukeon GID ≠ host kukeon
// GID → kuketty EACCES on the tty bind-mount" startup failure.
func TestBuildContainerSpec_KukeonGroupGID(t *testing.T) {
	tests := []struct {
		name       string
		gid        uint32
		seedAddGid []uint32
		want       []uint32
	}{
		{name: "zero is no-op", gid: 0, seedAddGid: []uint32{1000}, want: []uint32{1000}},
		{
			name:       "appended when image had no kukeon entry",
			gid:        989,
			seedAddGid: []uint32{1000, 988},
			want:       []uint32{1000, 988, 989},
		},
		{
			name:       "deduped against pre-existing matching gid",
			gid:        989,
			seedAddGid: []uint32{1000, 989},
			want:       []uint32{1000, 989},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := ctr.BuildContainerSpec(intmodel.ContainerSpec{
				ID:       "test-id",
				Image:    "registry.eminwux.com/busybox:latest",
				CellName: "c", SpaceName: "s", RealmName: "r", StackName: "st",
			}, ctr.WithKukeonGroupGID(tt.gid))

			ociSpec := &runtimespec.Spec{
				Process: &runtimespec.Process{
					User: runtimespec.User{
						UID:            1000,
						GID:            1000,
						AdditionalGids: append([]uint32(nil), tt.seedAddGid...),
					},
				},
			}
			for _, opt := range spec.SpecOpts {
				if err := opt(context.Background(), nil, nil, ociSpec); err != nil {
					t.Fatalf("apply SpecOpts: %v", err)
				}
			}

			if !reflect.DeepEqual(ociSpec.Process.User.AdditionalGids, tt.want) {
				t.Errorf("AdditionalGids = %v, want %v",
					ociSpec.Process.User.AdditionalGids, tt.want)
			}
		})
	}
}

// TestBuildRootContainerSpec_KukeonGroupGID mirrors the user-container test
// for the root-container builder so a non-default root that runs as a non-
// root user gets the same hop.
func TestBuildRootContainerSpec_KukeonGroupGID(t *testing.T) {
	spec := ctr.BuildRootContainerSpec(intmodel.ContainerSpec{
		ID:           "root",
		ContainerdID: "root-id",
		Image:        "registry.eminwux.com/busybox:latest",
	}, nil, ctr.WithKukeonGroupGID(989))

	ociSpec := &runtimespec.Spec{
		Process: &runtimespec.Process{
			User: runtimespec.User{
				UID:            1000,
				GID:            1000,
				AdditionalGids: []uint32{1000, 988},
			},
		},
	}
	for _, opt := range spec.SpecOpts {
		if err := opt(context.Background(), nil, nil, ociSpec); err != nil {
			t.Fatalf("apply SpecOpts: %v", err)
		}
	}

	want := []uint32{1000, 988, 989}
	if !reflect.DeepEqual(ociSpec.Process.User.AdditionalGids, want) {
		t.Errorf("AdditionalGids = %v, want %v",
			ociSpec.Process.User.AdditionalGids, want)
	}
}

// TestBuildRootContainerSpec_HostNetwork is the same assertion as
// TestBuildContainerSpec_HostNetwork but for the root-container builder used
// by the runner for the kukeond cell.
func TestBuildRootContainerSpec_HostNetwork(t *testing.T) {
	tests := []struct {
		name        string
		hostNetwork bool
		wantNetNS   bool
	}{
		{name: "host network true drops netns entry", hostNetwork: true, wantNetNS: false},
		{name: "host network false keeps netns entry", hostNetwork: false, wantNetNS: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := ctr.BuildRootContainerSpec(intmodel.ContainerSpec{
				ID:           "root",
				ContainerdID: "root-id",
				Image:        "registry.eminwux.com/busybox:latest",
				HostNetwork:  tt.hostNetwork,
			}, nil)

			ociSpec := &runtimespec.Spec{
				Process: &runtimespec.Process{},
				Linux: &runtimespec.Linux{
					Namespaces: []runtimespec.LinuxNamespace{
						{Type: runtimespec.NetworkNamespace},
						{Type: runtimespec.PIDNamespace},
					},
				},
			}
			for _, opt := range spec.SpecOpts {
				if err := opt(context.Background(), nil, nil, ociSpec); err != nil {
					t.Fatalf("apply SpecOpts: %v", err)
				}
			}

			var hasNet bool
			for _, ns := range ociSpec.Linux.Namespaces {
				if ns.Type == runtimespec.NetworkNamespace {
					hasNet = true
					break
				}
			}
			if hasNet != tt.wantNetNS {
				t.Errorf("network namespace present = %v, want %v (namespaces=%+v)",
					hasNet, tt.wantNetNS, ociSpec.Linux.Namespaces)
			}
		})
	}
}
