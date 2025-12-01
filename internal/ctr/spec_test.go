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
	"testing"

	"github.com/containerd/containerd/v2/pkg/oci"
	ctr "github.com/eminwux/kukeon/internal/ctr"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
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
				Image:        "docker.io/library/busybox:latest",
				CellName:     "cell-1",
				SpaceName:    "space-1",
				RealmName:    "realm-1",
				StackName:    "stack-1",
			},
			wantID:    "containerd-123",
			wantImage: "docker.io/library/busybox:latest",
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
				Image:     "docker.io/library/alpine:latest",
				CellName:  "cell-1",
				SpaceName: "space-1",
				RealmName: "realm-1",
				StackName: "stack-1",
			},
			wantID:    "test-id",
			wantImage: "docker.io/library/alpine:latest",
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
				Image:     "docker.io/library/busybox:latest",
				Command:   "sh",
				Args:      []string{"-c", "echo hello"},
				CellName:  "cell-1",
				SpaceName: "space-1",
				RealmName: "realm-1",
				StackName: "stack-1",
			},
			wantID:    "test-id",
			wantImage: "docker.io/library/busybox:latest",
		},
		{
			name: "with args only (no command)",
			containerSpec: intmodel.ContainerSpec{
				ID:        "test-id",
				Image:     "docker.io/library/busybox:latest",
				Args:      []string{"sh", "-c", "echo test"},
				CellName:  "cell-1",
				SpaceName: "space-1",
				RealmName: "realm-1",
				StackName: "stack-1",
			},
			wantID:    "test-id",
			wantImage: "docker.io/library/busybox:latest",
		},
		{
			name: "with environment variables",
			containerSpec: intmodel.ContainerSpec{
				ID:        "test-id",
				Image:     "docker.io/library/busybox:latest",
				Env:       []string{"ENV1=value1", "ENV2=value2"},
				CellName:  "cell-1",
				SpaceName: "space-1",
				RealmName: "realm-1",
				StackName: "stack-1",
			},
			wantID:    "test-id",
			wantImage: "docker.io/library/busybox:latest",
		},
		{
			name: "with privileged mode",
			containerSpec: intmodel.ContainerSpec{
				ID:         "test-id",
				Image:      "docker.io/library/busybox:latest",
				Privileged: true,
				CellName:   "cell-1",
				SpaceName:  "space-1",
				RealmName:  "realm-1",
				StackName:  "stack-1",
			},
			wantID:    "test-id",
			wantImage: "docker.io/library/busybox:latest",
		},
		{
			name: "with CNI config path",
			containerSpec: intmodel.ContainerSpec{
				ID:            "test-id",
				Image:         "docker.io/library/busybox:latest",
				CNIConfigPath: "/path/to/cni/config",
				CellName:      "cell-1",
				SpaceName:     "space-1",
				RealmName:     "realm-1",
				StackName:     "stack-1",
			},
			wantID:    "test-id",
			wantImage: "docker.io/library/busybox:latest",
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
