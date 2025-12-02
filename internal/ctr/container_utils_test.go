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

	ctr "github.com/eminwux/kukeon/internal/ctr"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

func TestDefaultRootContainerSpec(t *testing.T) {
	tests := []struct {
		name          string
		containerdID  string
		cellID        string
		realmID       string
		spaceID       string
		stackID       string
		cniConfigPath string
		wantID        string
		wantImage     string
		wantCommand   string
		wantRoot      bool
	}{
		{
			name:          "all parameters provided",
			containerdID:  "containerd-123",
			cellID:        "cell-1",
			realmID:       "realm-1",
			spaceID:       "space-1",
			stackID:       "stack-1",
			cniConfigPath: "/path/to/cni/config",
			wantID:        "root",
			wantImage:     "docker.io/library/busybox:latest",
			wantCommand:   "sleep",
			wantRoot:      true,
		},
		{
			name:          "empty parameters",
			containerdID:  "",
			cellID:        "",
			realmID:       "",
			spaceID:       "",
			stackID:       "",
			cniConfigPath: "",
			wantID:        "root",
			wantImage:     "docker.io/library/busybox:latest",
			wantCommand:   "sleep",
			wantRoot:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := ctr.DefaultRootContainerSpec(
				tt.containerdID,
				tt.cellID,
				tt.realmID,
				tt.spaceID,
				tt.stackID,
				tt.cniConfigPath,
			)

			if spec.ID != tt.wantID {
				t.Errorf("ID = %q, want %q", spec.ID, tt.wantID)
			}
			if spec.ContainerdID != tt.containerdID {
				t.Errorf("ContainerdID = %q, want %q", spec.ContainerdID, tt.containerdID)
			}
			if spec.CellName != tt.cellID {
				t.Errorf("CellName = %q, want %q", spec.CellName, tt.cellID)
			}
			if spec.RealmName != tt.realmID {
				t.Errorf("RealmName = %q, want %q", spec.RealmName, tt.realmID)
			}
			if spec.Image != tt.wantImage {
				t.Errorf("Image = %q, want %q", spec.Image, tt.wantImage)
			}
			if spec.Command != tt.wantCommand {
				t.Errorf("Command = %q, want %q", spec.Command, tt.wantCommand)
			}
			if spec.Root != tt.wantRoot {
				t.Errorf("Root = %v, want %v", spec.Root, tt.wantRoot)
			}
			if len(spec.Args) != 1 || spec.Args[0] != "infinity" {
				t.Errorf("Args = %v, want [infinity]", spec.Args)
			}
			if spec.CNIConfigPath != tt.cniConfigPath {
				t.Errorf("CNIConfigPath = %q, want %q", spec.CNIConfigPath, tt.cniConfigPath)
			}
		})
	}
}

func TestBuildRootContainerSpec(t *testing.T) {
	tests := []struct {
		name         string
		rootSpec     intmodel.ContainerSpec
		labels       map[string]string
		wantID       string
		wantImage    string
		wantLabelKey string
		wantLabelVal string
		wantSpecOpts bool
		wantCNIPath  string
	}{
		{
			name: "with containerd ID",
			rootSpec: intmodel.ContainerSpec{
				ID:            "root",
				ContainerdID:  "containerd-123",
				Image:         "custom-image:tag",
				Command:       "custom-cmd",
				Args:          []string{"arg1", "arg2"},
				Env:           []string{"ENV1=value1"},
				Privileged:    true,
				CNIConfigPath: "/path/to/cni",
			},
			labels: map[string]string{
				"custom": "label",
			},
			wantID:       "containerd-123",
			wantImage:    "custom-image:tag",
			wantLabelKey: "kukeon.io/container-type",
			wantLabelVal: "root",
			wantSpecOpts: true,
			wantCNIPath:  "/path/to/cni",
		},
		{
			name: "fallback to ID when containerd ID is empty",
			rootSpec: intmodel.ContainerSpec{
				ID:            "root",
				ContainerdID:  "",
				Image:         "",
				CNIConfigPath: "",
			},
			labels:       nil,
			wantID:       "root",
			wantImage:    "docker.io/library/busybox:latest",
			wantLabelKey: "kukeon.io/container-type",
			wantLabelVal: "root",
			wantSpecOpts: true,
		},
		{
			name: "empty labels",
			rootSpec: intmodel.ContainerSpec{
				ID:           "root",
				ContainerdID: "test-id",
			},
			labels:       map[string]string{},
			wantID:       "test-id",
			wantLabelKey: "kukeon.io/container-type",
			wantLabelVal: "root",
		},
		{
			name: "with command and args",
			rootSpec: intmodel.ContainerSpec{
				ID:           "root",
				ContainerdID: "test-id",
				Command:      "echo",
				Args:         []string{"hello", "world"},
			},
			labels:       nil,
			wantID:       "test-id",
			wantSpecOpts: true,
		},
		{
			name: "with args only (no command)",
			rootSpec: intmodel.ContainerSpec{
				ID:           "root",
				ContainerdID: "test-id",
				Args:         []string{"sh", "-c", "echo test"},
			},
			labels:       nil,
			wantID:       "test-id",
			wantSpecOpts: true,
		},
		{
			name: "privileged container",
			rootSpec: intmodel.ContainerSpec{
				ID:           "root",
				ContainerdID: "test-id",
				Privileged:   true,
			},
			labels:       nil,
			wantID:       "test-id",
			wantSpecOpts: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := ctr.BuildRootContainerSpec(tt.rootSpec, tt.labels)

			if spec.ID != tt.wantID {
				t.Errorf("ID = %q, want %q", spec.ID, tt.wantID)
			}
			if spec.Image != tt.wantImage && tt.wantImage != "" {
				t.Errorf("Image = %q, want %q", spec.Image, tt.wantImage)
			}
			if tt.wantLabelKey != "" {
				if spec.Labels == nil {
					t.Fatal("Labels should not be nil")
				}
				if val, ok := spec.Labels[tt.wantLabelKey]; !ok {
					t.Errorf("Labels[%q] not found", tt.wantLabelKey)
				} else if val != tt.wantLabelVal {
					t.Errorf("Labels[%q] = %q, want %q", tt.wantLabelKey, val, tt.wantLabelVal)
				}
			}
			if tt.wantSpecOpts && len(spec.SpecOpts) == 0 {
				t.Error("SpecOpts should not be empty")
			}
			if tt.wantCNIPath != "" && spec.CNIConfigPath != tt.wantCNIPath {
				t.Errorf("CNIConfigPath = %q, want %q", spec.CNIConfigPath, tt.wantCNIPath)
			}

			// Verify custom labels are preserved
			if tt.labels != nil && len(tt.labels) > 0 {
				for k, v := range tt.labels {
					if spec.Labels[k] != v {
						t.Errorf("Labels[%q] = %q, want %q", k, spec.Labels[k], v)
					}
				}
			}
		})
	}
}

func TestNormalizeImageReference(t *testing.T) {
	tests := []struct {
		name  string
		image string
		want  string
	}{
		{
			name:  "empty string",
			image: "",
			want:  "",
		},
		{
			name:  "library image with tag",
			image: "debian:latest",
			want:  "docker.io/library/debian:latest",
		},
		{
			name:  "library image without tag",
			image: "alpine",
			want:  "docker.io/library/alpine:latest",
		},
		{
			name:  "user image with tag",
			image: "user/image:tag",
			want:  "docker.io/user/image:tag",
		},
		{
			name:  "user image without tag",
			image: "user/image",
			want:  "docker.io/user/image",
		},
		{
			name:  "already fully qualified docker.io",
			image: "docker.io/library/debian:latest",
			want:  "docker.io/library/debian:latest",
		},
		{
			name:  "custom registry with port",
			image: "registry.example.com:5000/image:tag",
			want:  "registry.example.com:5000/image:tag",
		},
		{
			name:  "custom registry without port",
			image: "registry.example.com/image:tag",
			want:  "registry.example.com/image:tag",
		},
		{
			name:  "registry with dot before slash",
			image: "registry.example.com/myimage:tag",
			want:  "registry.example.com/myimage:tag",
		},
		{
			name:  "image with protocol (unchanged)",
			image: "https://registry.example.com/image:tag",
			want:  "https://registry.example.com/image:tag",
		},
		{
			name:  "image with http protocol (unchanged)",
			image: "http://registry.example.com/image:tag",
			want:  "http://registry.example.com/image:tag",
		},
		{
			name:  "image with colon in path",
			image: "registry.example.com:5000/namespace/image:v1.0.0",
			want:  "registry.example.com:5000/namespace/image:v1.0.0",
		},
		{
			name:  "docker.io namespace image",
			image: "docker.io/user/image:tag",
			want:  "docker.io/user/image:tag",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ctr.NormalizeImageReference(tt.image)
			if got != tt.want {
				t.Errorf("NormalizeImageReference(%q) = %q, want %q", tt.image, got, tt.want)
			}
		})
	}
}
