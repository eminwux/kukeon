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

// applyRootBuiltSpec composes the SpecOpts produced by BuildRootContainerSpec
// against an empty runtime spec so tests can assert on the resulting OCI fields
// without touching containerd. Mirrors applyBuiltSpec in spec_security_test.go.
func applyRootBuiltSpec(
	t *testing.T,
	in intmodel.ContainerSpec,
	labels map[string]string,
) *runtimespec.Spec {
	t.Helper()
	spec := &runtimespec.Spec{
		Process: &runtimespec.Process{},
		Linux:   &runtimespec.Linux{},
	}
	built := ctr.BuildRootContainerSpec(in, labels)
	for _, opt := range built.SpecOpts {
		if err := opt(context.Background(), nil, nil, spec); err != nil {
			t.Fatalf("SpecOpts returned error: %v", err)
		}
	}
	return spec
}

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
			wantImage:     ctr.DefaultRootContainerImage,
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
			wantImage:     ctr.DefaultRootContainerImage,
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
			wantImage:    ctr.DefaultRootContainerImage,
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

func TestBuildRootContainerSpec_Volumes(t *testing.T) {
	spec := applyRootBuiltSpec(t, intmodel.ContainerSpec{
		ID:           "root",
		ContainerdID: "containerd-root",
		Image:        "registry.eminwux.com/busybox:latest",
		Volumes: []intmodel.VolumeMount{
			{Source: "/run/kukeon", Target: "/run/kukeon", ReadOnly: false},
			{Source: "/opt/kukeon", Target: "/opt/kukeon", ReadOnly: true},
		},
	}, nil)

	wantBindCount := 2
	bindCount := 0
	for _, m := range spec.Mounts {
		if m.Type == "bind" {
			bindCount++
		}
	}
	if bindCount != wantBindCount {
		t.Fatalf("bind mount count = %d, want %d, mounts=%+v", bindCount, wantBindCount, spec.Mounts)
	}

	var runMount, optMount *runtimespec.Mount
	for i := range spec.Mounts {
		switch spec.Mounts[i].Destination {
		case "/run/kukeon":
			runMount = &spec.Mounts[i]
		case "/opt/kukeon":
			optMount = &spec.Mounts[i]
		}
	}
	if runMount == nil {
		t.Fatalf("bind mount for /run/kukeon not found")
	}
	if runMount.Source != "/run/kukeon" {
		t.Errorf("/run/kukeon source = %q, want /run/kukeon", runMount.Source)
	}
	if !containsString(runMount.Options, "rw") {
		t.Errorf("/run/kukeon options = %v, want contains rw", runMount.Options)
	}
	if optMount == nil {
		t.Fatalf("bind mount for /opt/kukeon not found")
	}
	if !containsString(optMount.Options, "ro") {
		t.Errorf("/opt/kukeon options = %v, want contains ro", optMount.Options)
	}
}

func TestBuildRootContainerSpec_UserAndReadonlyRootfs(t *testing.T) {
	spec := applyRootBuiltSpec(t, intmodel.ContainerSpec{
		ID:                     "root",
		ContainerdID:           "containerd-root",
		Image:                  "registry.eminwux.com/busybox:latest",
		User:                   "1000:1000",
		ReadOnlyRootFilesystem: true,
	}, nil)

	if spec.Process == nil || spec.Process.User.UID != 1000 || spec.Process.User.GID != 1000 {
		t.Fatalf("Process.User = %+v, want UID=1000 GID=1000", spec.Process.User)
	}
	if spec.Root == nil || !spec.Root.Readonly {
		t.Fatalf("Root.Readonly = %+v, want readonly=true", spec.Root)
	}
}

func TestBuildRootContainerSpec_Capabilities(t *testing.T) {
	defaults := []string{
		"CAP_CHOWN",
		"CAP_DAC_OVERRIDE",
		"CAP_NET_RAW",
		"CAP_SETGID",
		"CAP_SETUID",
	}
	spec := &runtimespec.Spec{
		Process: &runtimespec.Process{
			Capabilities: &runtimespec.LinuxCapabilities{
				Bounding:  append([]string(nil), defaults...),
				Permitted: append([]string(nil), defaults...),
				Effective: append([]string(nil), defaults...),
			},
		},
		Linux: &runtimespec.Linux{},
	}
	built := ctr.BuildRootContainerSpec(intmodel.ContainerSpec{
		ID:           "root",
		ContainerdID: "containerd-root",
		Image:        "registry.eminwux.com/busybox:latest",
		Capabilities: &intmodel.ContainerCapabilities{
			Drop: []string{"ALL"},
			Add:  []string{"NET_ADMIN"},
		},
	}, nil)
	for _, opt := range built.SpecOpts {
		if err := opt(context.Background(), nil, nil, spec); err != nil {
			t.Fatalf("SpecOpts returned error: %v", err)
		}
	}

	if spec.Process == nil || spec.Process.Capabilities == nil {
		t.Fatalf("Process.Capabilities is nil")
	}
	if !containsOnly(spec.Process.Capabilities.Effective, "CAP_NET_ADMIN") {
		t.Errorf("Effective caps = %v, want only CAP_NET_ADMIN", spec.Process.Capabilities.Effective)
	}
	if !containsOnly(spec.Process.Capabilities.Bounding, "CAP_NET_ADMIN") {
		t.Errorf("Bounding caps = %v, want only CAP_NET_ADMIN", spec.Process.Capabilities.Bounding)
	}
	if !containsOnly(spec.Process.Capabilities.Permitted, "CAP_NET_ADMIN") {
		t.Errorf("Permitted caps = %v, want only CAP_NET_ADMIN", spec.Process.Capabilities.Permitted)
	}
}

func TestBuildRootContainerSpec_SecurityOpts(t *testing.T) {
	spec := applyRootBuiltSpec(t, intmodel.ContainerSpec{
		ID:           "root",
		ContainerdID: "containerd-root",
		Image:        "registry.eminwux.com/busybox:latest",
		SecurityOpts: []string{"no-new-privileges"},
	}, nil)
	if !spec.Process.NoNewPrivileges {
		t.Fatalf("Process.NoNewPrivileges = false, want true")
	}
}

func TestBuildRootContainerSpec_TmpfsMounts(t *testing.T) {
	spec := applyRootBuiltSpec(t, intmodel.ContainerSpec{
		ID:           "root",
		ContainerdID: "containerd-root",
		Image:        "registry.eminwux.com/busybox:latest",
		Tmpfs: []intmodel.ContainerTmpfsMount{
			{Path: "/tmp", SizeBytes: 64 * 1024 * 1024, Options: []string{"mode=1777"}},
		},
	}, nil)

	var found *runtimespec.Mount
	for i := range spec.Mounts {
		if spec.Mounts[i].Destination == "/tmp" {
			found = &spec.Mounts[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("tmpfs mount for /tmp not found, mounts=%+v", spec.Mounts)
	}
	if found.Type != "tmpfs" {
		t.Errorf("mount type = %q, want %q", found.Type, "tmpfs")
	}
	if !containsString(found.Options, "size=67108864") {
		t.Errorf("tmpfs options = %v, want size=67108864", found.Options)
	}
	if !containsString(found.Options, "mode=1777") {
		t.Errorf("tmpfs options = %v, want mode=1777", found.Options)
	}
}

func TestBuildRootContainerSpec_Resources(t *testing.T) {
	mem := int64(4 * 1024 * 1024 * 1024)
	cpu := int64(512)
	pids := int64(256)
	spec := applyRootBuiltSpec(t, intmodel.ContainerSpec{
		ID:           "root",
		ContainerdID: "containerd-root",
		Image:        "registry.eminwux.com/busybox:latest",
		Resources: &intmodel.ContainerResources{
			MemoryLimitBytes: &mem,
			CPUShares:        &cpu,
			PidsLimit:        &pids,
		},
	}, nil)

	if spec.Linux == nil || spec.Linux.Resources == nil {
		t.Fatalf("Linux.Resources is nil")
	}
	if spec.Linux.Resources.Memory == nil || spec.Linux.Resources.Memory.Limit == nil ||
		*spec.Linux.Resources.Memory.Limit != mem {
		t.Errorf("Memory.Limit = %+v, want %d", spec.Linux.Resources.Memory, mem)
	}
	if spec.Linux.Resources.CPU == nil || spec.Linux.Resources.CPU.Shares == nil ||
		*spec.Linux.Resources.CPU.Shares != 512 {
		t.Errorf("CPU.Shares = %+v, want 512", spec.Linux.Resources.CPU)
	}
	if spec.Linux.Resources.Pids == nil || spec.Linux.Resources.Pids.Limit != 256 {
		t.Errorf("Pids.Limit = %+v, want 256", spec.Linux.Resources.Pids)
	}
}

// TestBuildRootContainerSpec_DefaultsUnaffected guards that the auto-default
// root container path (no Volumes, no security fields) keeps its existing
// minimal SpecOpts shape after the parity fix.
func TestBuildRootContainerSpec_DefaultsUnaffected(t *testing.T) {
	spec := applyRootBuiltSpec(t, ctr.DefaultRootContainerSpec(
		"containerd-root",
		"cell",
		"realm",
		"space",
		"stack",
		"",
	), nil)

	for _, m := range spec.Mounts {
		if m.Type == "bind" {
			t.Errorf("default root container produced bind mount %+v, want none", m)
		}
		if m.Type == "tmpfs" {
			t.Errorf("default root container produced tmpfs mount %+v, want none", m)
		}
	}
	if spec.Linux != nil && spec.Linux.Resources != nil {
		t.Errorf("default root container set Linux.Resources = %+v, want nil", spec.Linux.Resources)
	}
	if spec.Process != nil && spec.Process.NoNewPrivileges {
		t.Errorf("default root container set NoNewPrivileges = true, want false")
	}
}
