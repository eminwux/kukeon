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
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ctr "github.com/eminwux/kukeon/internal/ctr"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

// applyBuiltSpec composes the SpecOpts produced by BuildContainerSpec into an
// empty runtime spec so tests can assert on the resulting OCI fields without
// touching containerd.
func applyBuiltSpec(t *testing.T, in intmodel.ContainerSpec) *runtimespec.Spec {
	t.Helper()
	spec := &runtimespec.Spec{
		Process: &runtimespec.Process{},
		Linux:   &runtimespec.Linux{},
	}
	built := ctr.BuildContainerSpec(in)
	for _, opt := range built.SpecOpts {
		if err := opt(context.Background(), nil, nil, spec); err != nil {
			t.Fatalf("SpecOpts returned error: %v", err)
		}
	}
	return spec
}

func ptrInt64(v int64) *int64 { return &v }

func TestBuildContainerSpec_UserAndReadonlyRootfs(t *testing.T) {
	spec := applyBuiltSpec(t, intmodel.ContainerSpec{
		ID:                     "c1",
		Image:                  "registry.eminwux.com/busybox:latest",
		CellName:               "cell",
		SpaceName:              "space",
		RealmName:              "realm",
		StackName:              "stack",
		User:                   "1000:1000",
		ReadOnlyRootFilesystem: true,
	})

	if spec.Process == nil || spec.Process.User.UID != 1000 || spec.Process.User.GID != 1000 {
		t.Fatalf("Process.User = %+v, want UID=1000 GID=1000", spec.Process.User)
	}
	if spec.Root == nil || !spec.Root.Readonly {
		t.Fatalf("Root.Readonly = %+v, want readonly=true", spec.Root)
	}
}

func TestBuildContainerSpec_Capabilities(t *testing.T) {
	// Pre-populate a realistic default cap set so drop-ALL has something to
	// clear. containerd's populateDefaultUnixSpec seeds this set at
	// container-create time in production; applyBuiltSpec starts from an
	// empty spec, so the test must seed it itself or drop-ALL passes
	// vacuously.
	defaults := []string{
		"CAP_CHOWN",
		"CAP_DAC_OVERRIDE",
		"CAP_FSETID",
		"CAP_FOWNER",
		"CAP_MKNOD",
		"CAP_NET_RAW",
		"CAP_SETGID",
		"CAP_SETUID",
		"CAP_SETFCAP",
		"CAP_SETPCAP",
		"CAP_NET_BIND_SERVICE",
		"CAP_SYS_CHROOT",
		"CAP_KILL",
		"CAP_AUDIT_WRITE",
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
	built := ctr.BuildContainerSpec(intmodel.ContainerSpec{
		ID:        "c1",
		Image:     "registry.eminwux.com/busybox:latest",
		CellName:  "cell",
		SpaceName: "space",
		RealmName: "realm",
		StackName: "stack",
		Capabilities: &intmodel.ContainerCapabilities{
			Drop: []string{"ALL"},
			Add:  []string{"NET_ADMIN"},
		},
	})
	for _, opt := range built.SpecOpts {
		if err := opt(context.Background(), nil, nil, spec); err != nil {
			t.Fatalf("SpecOpts returned error: %v", err)
		}
	}

	if spec.Process == nil || spec.Process.Capabilities == nil {
		t.Fatalf("Process.Capabilities is nil")
	}
	// After drop ALL + add NET_ADMIN, each cap set should contain exactly
	// CAP_NET_ADMIN and nothing else — the defaults seeded above must be
	// cleared.
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

func TestBuildContainerSpec_TmpfsMounts(t *testing.T) {
	spec := applyBuiltSpec(t, intmodel.ContainerSpec{
		ID:        "c1",
		Image:     "registry.eminwux.com/busybox:latest",
		CellName:  "cell",
		SpaceName: "space",
		RealmName: "realm",
		StackName: "stack",
		Tmpfs: []intmodel.ContainerTmpfsMount{
			{Path: "/tmp", SizeBytes: 64 * 1024 * 1024, Options: []string{"mode=1777"}},
		},
	})

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

func TestBuildContainerSpec_Resources(t *testing.T) {
	spec := applyBuiltSpec(t, intmodel.ContainerSpec{
		ID:        "c1",
		Image:     "registry.eminwux.com/busybox:latest",
		CellName:  "cell",
		SpaceName: "space",
		RealmName: "realm",
		StackName: "stack",
		Resources: &intmodel.ContainerResources{
			MemoryLimitBytes: ptrInt64(4 * 1024 * 1024 * 1024),
			CPUShares:        ptrInt64(512),
			PidsLimit:        ptrInt64(256),
		},
	})

	if spec.Linux == nil || spec.Linux.Resources == nil {
		t.Fatalf("Linux.Resources is nil")
	}
	if spec.Linux.Resources.Memory == nil || spec.Linux.Resources.Memory.Limit == nil ||
		*spec.Linux.Resources.Memory.Limit != 4*1024*1024*1024 {
		t.Errorf("Memory.Limit = %+v, want 4GiB", spec.Linux.Resources.Memory)
	}
	if spec.Linux.Resources.CPU == nil || spec.Linux.Resources.CPU.Shares == nil ||
		*spec.Linux.Resources.CPU.Shares != 512 {
		t.Errorf("CPU.Shares = %+v, want 512", spec.Linux.Resources.CPU)
	}
	if spec.Linux.Resources.Pids == nil || spec.Linux.Resources.Pids.Limit != 256 {
		t.Errorf("Pids.Limit = %+v, want 256", spec.Linux.Resources.Pids)
	}
}

func TestBuildContainerSpec_SecurityOptsNoNewPrivileges(t *testing.T) {
	spec := applyBuiltSpec(t, intmodel.ContainerSpec{
		ID:           "c1",
		Image:        "registry.eminwux.com/busybox:latest",
		CellName:     "cell",
		SpaceName:    "space",
		RealmName:    "realm",
		StackName:    "stack",
		SecurityOpts: []string{"no-new-privileges"},
	})
	if !spec.Process.NoNewPrivileges {
		t.Fatalf("Process.NoNewPrivileges = false, want true")
	}
}

func TestBuildContainerSpec_SecurityOptsSeccompUnconfined(t *testing.T) {
	// Pre-populate Linux.Seccomp so we can observe it being cleared.
	spec := &runtimespec.Spec{
		Process: &runtimespec.Process{},
		Linux: &runtimespec.Linux{
			Seccomp: &runtimespec.LinuxSeccomp{DefaultAction: runtimespec.ActErrno},
		},
	}
	built := ctr.BuildContainerSpec(intmodel.ContainerSpec{
		ID:           "c1",
		Image:        "registry.eminwux.com/busybox:latest",
		CellName:     "cell",
		SpaceName:    "space",
		RealmName:    "realm",
		StackName:    "stack",
		SecurityOpts: []string{"seccomp=unconfined"},
	})
	for _, opt := range built.SpecOpts {
		if err := opt(context.Background(), nil, nil, spec); err != nil {
			t.Fatalf("SpecOpts returned error: %v", err)
		}
	}
	if spec.Linux.Seccomp != nil {
		t.Fatalf("Linux.Seccomp = %+v, want nil after seccomp=unconfined", spec.Linux.Seccomp)
	}
}

func TestBuildContainerSpec_SecurityOptsSeccompProfile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profile.json")
	profile := runtimespec.LinuxSeccomp{
		DefaultAction: runtimespec.ActErrno,
	}
	data, err := json.Marshal(profile)
	if err != nil {
		t.Fatalf("marshal profile: %v", err)
	}
	if err = os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write profile: %v", err)
	}

	spec := applyBuiltSpec(t, intmodel.ContainerSpec{
		ID:           "c1",
		Image:        "registry.eminwux.com/busybox:latest",
		CellName:     "cell",
		SpaceName:    "space",
		RealmName:    "realm",
		StackName:    "stack",
		SecurityOpts: []string{"seccomp=" + path},
	})
	if spec.Linux.Seccomp == nil {
		t.Fatalf("Linux.Seccomp is nil, want profile loaded")
	}
	if spec.Linux.Seccomp.DefaultAction != runtimespec.ActErrno {
		t.Errorf("DefaultAction = %q, want %q", spec.Linux.Seccomp.DefaultAction, runtimespec.ActErrno)
	}
}

func TestBuildContainerSpec_SecurityOptsUnknownErrors(t *testing.T) {
	spec := &runtimespec.Spec{Process: &runtimespec.Process{}, Linux: &runtimespec.Linux{}}
	built := ctr.BuildContainerSpec(intmodel.ContainerSpec{
		ID:           "c1",
		Image:        "registry.eminwux.com/busybox:latest",
		CellName:     "cell",
		SpaceName:    "space",
		RealmName:    "realm",
		StackName:    "stack",
		SecurityOpts: []string{"apparmor=my-profile"},
	})
	var sawError bool
	for _, opt := range built.SpecOpts {
		if err := opt(context.Background(), nil, nil, spec); err != nil {
			if !strings.Contains(err.Error(), "unsupported securityOpt") {
				t.Fatalf("unexpected error: %v", err)
			}
			sawError = true
		}
	}
	if !sawError {
		t.Fatalf("expected unsupported securityOpt error, got none")
	}
}

func containsString(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func containsOnly(xs []string, want string) bool {
	return len(xs) == 1 && xs[0] == want
}
