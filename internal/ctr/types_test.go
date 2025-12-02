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
	"time"

	ctr "github.com/eminwux/kukeon/internal/ctr"
)

func TestRegistryCredentials(t *testing.T) {
	creds := ctr.RegistryCredentials{
		Username:      "testuser",
		Password:      "testpass",
		ServerAddress: "docker.io",
	}

	if creds.Username != "testuser" {
		t.Errorf("Username = %q, want %q", creds.Username, "testuser")
	}
	if creds.Password != "testpass" {
		t.Errorf("Password = %q, want %q", creds.Password, "testpass")
	}
	if creds.ServerAddress != "docker.io" {
		t.Errorf("ServerAddress = %q, want %q", creds.ServerAddress, "docker.io")
	}
}

func TestContainerSpec(t *testing.T) {
	runtime := &ctr.ContainerRuntime{
		Name:    "io.containerd.runc.v2",
		Options: map[string]string{"key": "value"},
	}

	spec := ctr.ContainerSpec{
		ID:            "test-container",
		Image:         "docker.io/library/busybox:latest",
		SnapshotKey:   "snapshot-key",
		Snapshotter:   "overlayfs",
		Runtime:       runtime,
		Labels:        map[string]string{"label1": "value1"},
		CNIConfigPath: "/path/to/cni/config",
	}

	if spec.ID != "test-container" {
		t.Errorf("ID = %q, want %q", spec.ID, "test-container")
	}
	if spec.Image != "docker.io/library/busybox:latest" {
		t.Errorf("Image = %q, want %q", spec.Image, "docker.io/library/busybox:latest")
	}
	if spec.Runtime == nil || spec.Runtime.Name != "io.containerd.runc.v2" {
		t.Errorf("Runtime.Name = %q, want %q", spec.Runtime.Name, "io.containerd.runc.v2")
	}
}

func TestContainerRuntime(t *testing.T) {
	runtime := &ctr.ContainerRuntime{
		Name:    "io.containerd.runc.v2",
		Options: "test-options",
	}

	if runtime.Name != "io.containerd.runc.v2" {
		t.Errorf("Name = %q, want %q", runtime.Name, "io.containerd.runc.v2")
	}
	if runtime.Options != "test-options" {
		t.Errorf("Options = %v, want %q", runtime.Options, "test-options")
	}
}

func TestTaskSpec(t *testing.T) {
	taskIO := &ctr.TaskIO{
		Stdin:    "/dev/stdin",
		Stdout:   "/dev/stdout",
		Stderr:   "/dev/stderr",
		Terminal: true,
	}

	spec := ctr.TaskSpec{
		IO: taskIO,
	}

	if spec.IO == nil {
		t.Fatal("IO should not be nil")
	}
	if spec.IO.Stdin != "/dev/stdin" {
		t.Errorf("IO.Stdin = %q, want %q", spec.IO.Stdin, "/dev/stdin")
	}
	if spec.IO.Terminal != true {
		t.Errorf("IO.Terminal = %v, want %v", spec.IO.Terminal, true)
	}
}

func TestTaskIO(t *testing.T) {
	io := &ctr.TaskIO{
		Stdin:    "/dev/stdin",
		Stdout:   "/dev/stdout",
		Stderr:   "/dev/stderr",
		Terminal: false,
	}

	if io.Stdin != "/dev/stdin" {
		t.Errorf("Stdin = %q, want %q", io.Stdin, "/dev/stdin")
	}
	if io.Terminal != false {
		t.Errorf("Terminal = %v, want %v", io.Terminal, false)
	}
}

func TestContainerDeleteOptions(t *testing.T) {
	opts := ctr.ContainerDeleteOptions{
		SnapshotCleanup: true,
	}

	if opts.SnapshotCleanup != true {
		t.Errorf("SnapshotCleanup = %v, want %v", opts.SnapshotCleanup, true)
	}
}

func TestStopContainerOptions(t *testing.T) {
	timeout := 30 * time.Second
	opts := ctr.StopContainerOptions{
		Signal:  "SIGTERM",
		Timeout: &timeout,
		Force:   true,
	}

	if opts.Signal != "SIGTERM" {
		t.Errorf("Signal = %q, want %q", opts.Signal, "SIGTERM")
	}
	if opts.Timeout == nil || *opts.Timeout != timeout {
		t.Errorf("Timeout = %v, want %v", opts.Timeout, timeout)
	}
	if opts.Force != true {
		t.Errorf("Force = %v, want %v", opts.Force, true)
	}
}

func TestNamespacePaths(t *testing.T) {
	paths := ctr.NamespacePaths{
		Net: "/proc/1/ns/net",
		IPC: "/proc/1/ns/ipc",
		UTS: "/proc/1/ns/uts",
		PID: "/proc/1/ns/pid",
	}

	if paths.Net != "/proc/1/ns/net" {
		t.Errorf("Net = %q, want %q", paths.Net, "/proc/1/ns/net")
	}
	if paths.IPC != "/proc/1/ns/ipc" {
		t.Errorf("IPC = %q, want %q", paths.IPC, "/proc/1/ns/ipc")
	}
}

func TestCgroupSpec(t *testing.T) {
	resources := ctr.CgroupResources{
		CPU:    &ctr.CPUResources{Weight: uint64Ptr(100)},
		Memory: &ctr.MemoryResources{Max: int64Ptr(1024)},
		IO:     &ctr.IOResources{Weight: 500},
	}

	spec := ctr.CgroupSpec{
		Group:      "/kukeon/workloads/test",
		Mountpoint: "/sys/fs/cgroup",
		Resources:  resources,
	}

	if spec.Group != "/kukeon/workloads/test" {
		t.Errorf("Group = %q, want %q", spec.Group, "/kukeon/workloads/test")
	}
	if spec.Mountpoint != "/sys/fs/cgroup" {
		t.Errorf("Mountpoint = %q, want %q", spec.Mountpoint, "/sys/fs/cgroup")
	}
	if spec.Resources.CPU == nil || *spec.Resources.CPU.Weight != 100 {
		t.Errorf("Resources.CPU.Weight = %v, want %d", spec.Resources.CPU.Weight, 100)
	}
}

func TestCgroupResources(t *testing.T) {
	resources := ctr.CgroupResources{
		CPU:    &ctr.CPUResources{Weight: uint64Ptr(100)},
		Memory: &ctr.MemoryResources{Max: int64Ptr(2048)},
		IO:     &ctr.IOResources{Weight: 600},
	}

	if resources.CPU == nil {
		t.Fatal("CPU should not be nil")
	}
	if resources.Memory == nil {
		t.Fatal("Memory should not be nil")
	}
	if resources.IO.Weight != 600 {
		t.Errorf("IO.Weight = %d, want %d", resources.IO.Weight, 600)
	}
}

func TestCPUResources(t *testing.T) {
	cpu := &ctr.CPUResources{
		Weight: uint64Ptr(100),
		Quota:  int64Ptr(50000),
		Period: uint64Ptr(100000),
		Cpus:   "0-3",
		Mems:   "0",
	}

	if *cpu.Weight != 100 {
		t.Errorf("Weight = %d, want %d", *cpu.Weight, 100)
	}
	if *cpu.Quota != 50000 {
		t.Errorf("Quota = %d, want %d", *cpu.Quota, 50000)
	}
	if cpu.Cpus != "0-3" {
		t.Errorf("Cpus = %q, want %q", cpu.Cpus, "0-3")
	}
}

func TestMemoryResources(t *testing.T) {
	mem := &ctr.MemoryResources{
		Min:  int64Ptr(512),
		Max:  int64Ptr(2048),
		Low:  int64Ptr(1024),
		High: int64Ptr(1536),
		Swap: int64Ptr(4096),
	}

	if *mem.Min != 512 {
		t.Errorf("Min = %d, want %d", *mem.Min, 512)
	}
	if *mem.Max != 2048 {
		t.Errorf("Max = %d, want %d", *mem.Max, 2048)
	}
	if *mem.Swap != 4096 {
		t.Errorf("Swap = %d, want %d", *mem.Swap, 4096)
	}
}

func TestIOResources(t *testing.T) {
	io := &ctr.IOResources{
		Weight: 500,
		Throttle: []ctr.IOThrottleEntry{
			{
				Type:  ctr.IOTypeReadBPS,
				Major: 8,
				Minor: 0,
				Rate:  1048576,
			},
		},
	}

	if io.Weight != 500 {
		t.Errorf("Weight = %d, want %d", io.Weight, 500)
	}
	if len(io.Throttle) != 1 {
		t.Fatalf("Throttle length = %d, want %d", len(io.Throttle), 1)
	}
	if io.Throttle[0].Type != ctr.IOTypeReadBPS {
		t.Errorf("Throttle[0].Type = %q, want %q", io.Throttle[0].Type, ctr.IOTypeReadBPS)
	}
}

func TestIOThrottleTypeConstants(t *testing.T) {
	if ctr.IOTypeReadBPS == "" {
		t.Error("IOTypeReadBPS should not be empty")
	}
	if ctr.IOTypeWriteBPS == "" {
		t.Error("IOTypeWriteBPS should not be empty")
	}
	if ctr.IOTypeReadIOPS == "" {
		t.Error("IOTypeReadIOPS should not be empty")
	}
	if ctr.IOTypeWriteIOPS == "" {
		t.Error("IOTypeWriteIOPS should not be empty")
	}

	// Verify constants are distinct
	if ctr.IOTypeReadBPS == ctr.IOTypeWriteBPS {
		t.Error("IOTypeReadBPS and IOTypeWriteBPS should be distinct")
	}
	if ctr.IOTypeReadBPS == ctr.IOTypeReadIOPS {
		t.Error("IOTypeReadBPS and IOTypeReadIOPS should be distinct")
	}
}

func TestIOThrottleEntry(t *testing.T) {
	entry := ctr.IOThrottleEntry{
		Type:  ctr.IOTypeWriteIOPS,
		Major: 8,
		Minor: 1,
		Rate:  1000,
	}

	if entry.Type != ctr.IOTypeWriteIOPS {
		t.Errorf("Type = %q, want %q", entry.Type, ctr.IOTypeWriteIOPS)
	}
	if entry.Major != 8 {
		t.Errorf("Major = %d, want %d", entry.Major, 8)
	}
	if entry.Minor != 1 {
		t.Errorf("Minor = %d, want %d", entry.Minor, 1)
	}
	if entry.Rate != 1000 {
		t.Errorf("Rate = %d, want %d", entry.Rate, 1000)
	}
}

// Helper functions.
func uint64Ptr(v uint64) *uint64 {
	return &v
}

func int64Ptr(v int64) *int64 {
	return &v
}
