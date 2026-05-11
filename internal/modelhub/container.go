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

package modelhub

import "time"

type Container struct {
	Metadata ContainerMetadata
	Spec     ContainerSpec
	Status   ContainerStatus
}

type ContainerMetadata struct {
	Name   string
	Labels map[string]string
}

type ContainerSpec struct {
	ID              string
	ContainerdID    string
	RealmName       string
	SpaceName       string
	StackName       string
	CellName        string
	Root            bool
	Image           string
	Command         string
	Args            []string
	WorkingDir      string
	Env             []string
	Ports           []string
	Volumes         []VolumeMount
	Networks        []string
	NetworksAliases []string
	Privileged      bool
	HostNetwork     bool
	HostPID         bool
	HostCgroup      bool
	// NestedCgroupRuntime mirrors the parent cell's
	// CellSpec.NestedCgroupRuntime opt-in (issue #314). When true and
	// !HostCgroup, BuildContainerSpec/BuildRootContainerSpec append a
	// cgroup2 mount at /sys/fs/cgroup so an inner runtime (dockerd,
	// podman, an inner containerd) can read the controller set that
	// the controller delegated host-side via
	// EnableCellAllSubtreeControllers (#318). Propagated by the runner
	// from cell.Spec.NestedCgroupRuntime at every BuildContainerSpec
	// call site; not part of the persisted container document.
	NestedCgroupRuntime    bool
	User                   string
	ReadOnlyRootFilesystem bool
	Capabilities           *ContainerCapabilities
	SecurityOpts           []string
	Tmpfs                  []ContainerTmpfsMount
	Resources              *ContainerResources
	Secrets                []ContainerSecret
	CNIConfigPath          string
	RestartPolicy          string
	Attachable             bool
	Tty                    *ContainerTty
	// CellCgroupPath is the absolute cgroup path of the parent cell (mirrors
	// Cell.Status.CgroupPath). When set, BuildContainerSpec emits an OCI
	// Linux.CgroupsPath rooted at <CellCgroupPath>/<containerd-id> so the
	// container task lands inside the cell's cgroup subtree instead of
	// containerd's runc-shim default placement. Populated by the runner at
	// container-create time; not part of the persisted cell document.
	CellCgroupPath string
	// EtcHostsPath is the host-side path of a kukeond-rendered /etc/hosts file
	// to bind-mount at /etc/hosts inside the container. Empty disables the
	// bind-mount, leaving the image's /etc/hosts in place. Mirrors Docker's
	// per-container hosts pattern; the source file lives under the cell's
	// metadata directory so cell teardown cleans it up. Populated by the
	// runner at container-create time; not part of the persisted document.
	EtcHostsPath string
	// EtcHostnamePath is the host-side path of a kukeond-rendered /etc/hostname
	// file (cell name) to bind-mount at /etc/hostname inside the container.
	// Empty disables the bind-mount. Same lifecycle and storage location as
	// EtcHostsPath; not part of the persisted document.
	EtcHostnamePath string
	// CellProfileName is the metadata.name of the CellProfile this container's
	// cell was materialized from (mirrors cell.Metadata.Labels
	// [cellprofile.LabelProfile]). When non-empty, BuildContainerSpec /
	// BuildRootContainerSpec emit it as KUKEON_CELL_PROFILE_NAME on the
	// container's OCI Process.Env so workloads can read their own profile
	// identity without relying on profile authors to hardcode it. Empty when
	// the cell was created from a plain CellDoc rather than a CellProfile.
	// Populated by the runner at container-create time; not part of the
	// persisted document. Issue #351.
	CellProfileName string
}

// ContainerTty mirrors the v1beta1 ContainerTty payload. See the v1beta1
// type for field semantics.
type ContainerTty struct {
	Prompt  string
	OnInit  []TtyStage
	LogFile string
}

// TtyStage mirrors the v1beta1 TtyStage payload.
type TtyStage struct {
	Script string
}

// IsEmpty reports whether the tty block carries no user-supplied config.
func (t *ContainerTty) IsEmpty() bool {
	if t == nil {
		return true
	}
	if t.Prompt != "" {
		return false
	}
	if t.LogFile != "" {
		return false
	}
	for _, s := range t.OnInit {
		if s.Script != "" {
			return false
		}
	}
	return true
}

// ContainerSecret references a credential resolved by the daemon at apply
// time. Only the reference is persisted in the hub; the resolved value lives
// only in the OCI runtime spec (for env injection) or in the staged secret
// file (for mount mode).
type ContainerSecret struct {
	Name      string
	FromFile  string
	FromEnv   string
	MountPath string
}

// VolumeKind discriminates between the supported VolumeMount kinds. An empty
// value is treated as VolumeKindBind so existing call sites that build a
// VolumeMount without a Kind keep their bind-mount semantics.
type VolumeKind string

const (
	// VolumeKindBind is a host bind mount. Source and Target are required.
	VolumeKindBind VolumeKind = "bind"
	// VolumeKindTmpfs is an in-memory tmpfs mount. Only Target is required;
	// Source is implicit ("tmpfs"). SizeBytes and Mode tune the standard
	// tmpfs size= and mode= options when non-zero.
	VolumeKindTmpfs VolumeKind = "tmpfs"
)

// VolumeMount is a mount entry attached to a container. The Kind discriminator
// selects the OCI mount type the runtime emits: bind (host path → container
// path) or tmpfs (in-memory directory). Empty Kind means bind for back-compat
// with call sites that predate the discriminator.
type VolumeMount struct {
	Kind     VolumeKind
	Source   string
	Target   string
	ReadOnly bool
	// SizeBytes is the tmpfs size= option in bytes. Only honored when
	// Kind == VolumeKindTmpfs; zero leaves the kernel default.
	SizeBytes int64
	// Mode is the tmpfs mode= option as a 4-digit octal value (e.g. 0755).
	// Only honored when Kind == VolumeKindTmpfs; zero leaves the kernel
	// default (01777).
	Mode uint32
}

// ContainerCapabilities groups Linux capability deltas applied relative to the
// image default set.
type ContainerCapabilities struct {
	Drop []string
	Add  []string
}

// ContainerTmpfsMount declares a tmpfs mount inside the container.
type ContainerTmpfsMount struct {
	Path      string
	SizeBytes int64
	Options   []string
}

// ContainerResources exposes the cgroup v2 knobs supported per container.
type ContainerResources struct {
	MemoryLimitBytes *int64
	CPUShares        *int64
	PidsLimit        *int64
}

type ContainerStatus struct {
	Name         string // Container name/ID
	ID           string // Container ID (same as Name)
	State        ContainerState
	RestartCount int
	RestartTime  time.Time
	StartTime    time.Time
	FinishTime   time.Time
	ExitCode     int
	ExitSignal   string
}

type ContainerState int

const (
	ContainerStatePending ContainerState = iota
	ContainerStateReady
	ContainerStateStopped
	ContainerStatePaused
	ContainerStatePausing
	ContainerStateFailed
	ContainerStateUnknown
)
