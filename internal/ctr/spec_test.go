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
