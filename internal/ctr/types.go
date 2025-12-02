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
	"time"

	cgroup2 "github.com/containerd/cgroups/v2/cgroup2"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/oci"
)

const (
	// cniConfigAnnotation is the annotation key for CNI configuration path.
	cniConfigAnnotation = "io.kukeon.cni.config"
)

// RegistryCredentials contains authentication information for a container registry.
// This type matches the modelhub RegistryCredentials structure for use in the ctr package.
type RegistryCredentials struct {
	// Username is the registry username.
	Username string
	// Password is the registry password or token.
	Password string
	// ServerAddress is the registry server address (e.g., "docker.io", "registry.example.com").
	// If empty, credentials apply to the registry extracted from the image reference.
	ServerAddress string
}

// ContainerSpec describes how to create a new container.
type ContainerSpec struct {
	// ID is the unique identifier for the container.
	ID string
	// Image is the image reference to use for the container.
	Image string
	// SnapshotKey is the key for the snapshot. If empty, defaults to ID.
	SnapshotKey string
	// Snapshotter is the snapshotter to use. If empty, uses default.
	Snapshotter string
	// Runtime is the runtime configuration.
	Runtime *ContainerRuntime
	// SpecOpts are OCI spec options to apply.
	SpecOpts []oci.SpecOpts
	// Labels are key-value pairs to attach to the container.
	Labels map[string]string
	// CNIConfigPath is the path to the CNI configuration to use for this container.
	CNIConfigPath string
}

// ContainerRuntime describes the runtime configuration.
type ContainerRuntime struct {
	// Name is the runtime name (e.g., "io.containerd.runc.v2").
	Name string
	// Options are runtime-specific options.
	Options interface{}
}

// TaskSpec describes how to create a new task.
type TaskSpec struct {
	// IO is the IO configuration for the task.
	IO *TaskIO
	// Options are task creation options.
	Options []containerd.NewTaskOpts
}

// TaskIO describes the IO configuration for a task.
type TaskIO struct {
	// Stdin is the path to stdin (if any).
	Stdin string
	// Stdout is the path to stdout (if any).
	Stdout string
	// Stderr is the path to stderr (if any).
	Stderr string
	// Terminal indicates if the task should have a TTY.
	Terminal bool
}

// ContainerDeleteOptions describes options for deleting a container.
type ContainerDeleteOptions struct {
	// SnapshotCleanup indicates whether to clean up snapshots.
	SnapshotCleanup bool
}

// StopContainerOptions describes options for stopping a container.
type StopContainerOptions struct {
	// Signal is the signal to send (defaults to SIGTERM).
	Signal string
	// Timeout is the timeout for graceful shutdown.
	Timeout *time.Duration
	// Force indicates whether to force kill if timeout is exceeded.
	Force bool
}

// NamespacePaths describes the namespace file paths a container should join.
type NamespacePaths struct {
	Net string
	IPC string
	UTS string
	PID string
}

// CgroupSpec describes how to create a new cgroup.
type CgroupSpec struct {
	// Group is the target cgroup path, e.g. /kukeon/workloads/runner.
	Group string
	// Mountpoint overrides the default cgroup mount (/sys/fs/cgroup) when non-empty.
	Mountpoint string
	// Resources defines the controller knobs that should be configured for the cgroup.
	Resources CgroupResources
}

// CgroupResources represents the subset of controllers we expose.
type CgroupResources struct {
	CPU    *CPUResources
	Memory *MemoryResources
	IO     *IOResources
}

// CPUResources maps to cpu*, cpuset* controllers.
type CPUResources struct {
	Weight *uint64
	Quota  *int64
	Period *uint64
	Cpus   string
	Mems   string
}

// MemoryResources maps to memory controller knobs.
type MemoryResources struct {
	Min  *int64
	Max  *int64
	Low  *int64
	High *int64
	Swap *int64
}

// IOResources exposes IO weight + throttling.
type IOResources struct {
	Weight   uint16
	Throttle []IOThrottleEntry
}

// IOThrottleType identifies the throttle file to target.
type IOThrottleType string

const (
	IOTypeReadBPS   IOThrottleType = IOThrottleType(cgroup2.ReadBPS)
	IOTypeWriteBPS  IOThrottleType = IOThrottleType(cgroup2.WriteBPS)
	IOTypeReadIOPS  IOThrottleType = IOThrottleType(cgroup2.ReadIOPS)
	IOTypeWriteIOPS IOThrottleType = IOThrottleType(cgroup2.WriteIOPS)
)

// IOThrottleEntry represents a single io.max entry.
type IOThrottleEntry struct {
	Type  IOThrottleType
	Major int64
	Minor int64
	Rate  uint64
}
