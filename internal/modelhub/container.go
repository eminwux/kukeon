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
	// Repos mirrors the v1beta1 ContainerSpec.Repos payload — git
	// repositories kuketty clones/fetches in its pre-Serve step. See the
	// v1beta1 type for field semantics. Issue #617.
	Repos []ContainerRepo
	// Git mirrors the v1beta1 ContainerSpec.Git payload — declarative git
	// identity/signing sugar that BuildContainerSpec / BuildRootContainerSpec
	// expand into the GIT_AUTHOR_* / GIT_COMMITTER_* / GIT_CONFIG_* env block.
	// See the v1beta1 type for field semantics. Issue #618.
	Git           *ContainerGit
	CNIConfigPath string
	RestartPolicy string
	Attachable    bool
	Tty           *ContainerTty
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
	// CellProfileName is the legacy metadata.name of the CellProfile this
	// container's cell was materialized from (mirrors cell.Metadata.Labels
	// ["kukeon.io/profile"], stamped by the runner from the cell label). When
	// non-empty, BuildContainerSpec / BuildRootContainerSpec emit it as
	// KUKEON_CELL_PROFILE_NAME on the container's OCI Process.Env. The
	// CellProfile kind itself was removed in #626, so no fresh cell sets this
	// field; cells persisted before the cleanup still carry the label and have
	// it propagated for the duration of their lifetime. Populated by the
	// runner at container-create time; not part of the persisted document.
	// Issue #351.
	CellProfileName string
}

// ContainerTty mirrors the v1beta1 ContainerTty payload. See the v1beta1
// type for field semantics.
type ContainerTty struct {
	Prompt   string
	OnInit   []TtyStage
	LogFile  string
	LogLevel string
}

// TtyStage mirrors the v1beta1 TtyStage payload. See the v1beta1 type for
// field semantics. RunOn is empty/"start" (forward to sbsh's Stages.OnInit) or
// "create" (kuketty pre-Serve executor). Issue #635.
type TtyStage struct {
	Script string
	RunOn  string
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
	if t.LogLevel != "" {
		return false
	}
	for _, s := range t.OnInit {
		if s.Script != "" || s.RunOn != "" {
			return false
		}
	}
	return true
}

// ContainerSecret references a credential resolved by the daemon at apply
// time. Only the reference is persisted in the hub; the resolved value lives
// only in the OCI runtime spec (for env injection) or in the staged secret
// file (for mount mode). Exactly one source must be set: FromFile, FromEnv, or
// SecretRef (a daemon-managed kind: Secret, issue #623).
type ContainerSecret struct {
	Name      string
	FromFile  string
	FromEnv   string
	SecretRef *ContainerSecretRef
	MountPath string
}

// ContainerSecretRef mirrors the v1beta1 ContainerSecretRef payload — a name +
// scope pointing at a daemon-managed kind: Secret (issue #619). See the
// v1beta1 type for the scope-coordinate contract. Issue #623.
type ContainerSecretRef struct {
	Name  string
	Realm string
	Space string
	Stack string
	Cell  string
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

// ContainerRepo mirrors the v1beta1 ContainerRepo payload — a git repository
// the container depends on, cloned/fetched by kuketty pre-Serve. See the
// v1beta1 type for field semantics. Issue #617.
type ContainerRepo struct {
	Name     string
	Target   string
	Branch   string
	URL      string
	Required bool
}

// GitSignTarget enumerates the artefacts ContainerGit.Sign can enable signing
// for. Mirrors the v1beta1 constants.
const (
	GitSignCommits = "commits"
	GitSignTags    = "tags"
)

// ContainerGit mirrors the v1beta1 ContainerGit payload — declarative sugar
// over the GIT_AUTHOR_* / GIT_COMMITTER_* / GIT_CONFIG_* env-var protocol. See
// the v1beta1 type for field semantics. Issue #618.
type ContainerGit struct {
	Author         *GitIdentity
	Committer      *GitIdentity
	SigningKey     string
	Sign           []string
	AllowedSigners string
}

// GitIdentity mirrors the v1beta1 GitIdentity payload.
type GitIdentity struct {
	Name  string
	Email string
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
	Name string // Container name/ID
	ID   string // Container ID (same as Name)
	// CreatedAt is the wall-clock time of the first time the controller
	// observed this container in cell.Spec.Containers. Stamped on the
	// first populateCellContainerStatuses pass and preserved across
	// reconciliations. Sources the AGE column on `kuke get container`.
	CreatedAt    time.Time
	State        ContainerState
	RestartCount int
	RestartTime  time.Time
	StartTime    time.Time
	FinishTime   time.Time
	ExitCode     int
	ExitSignal   string
	// Repos reports the per-repo outcome of kuketty's pre-Serve clone/fetch
	// step. Mirrors the v1beta1 ContainerStatus.Repos payload. Issue #617.
	Repos []RepoStatus
	// Stages reports the per-stage outcome of kuketty's pre-Serve execution of
	// the container's runOn: create stages. Mirrors the v1beta1
	// ContainerStatus.Stages payload; schema only this phase, populated in
	// phase B (#689). Issue #635.
	Stages []StageStatus
}

// RepoStatus mirrors the v1beta1 RepoStatus payload. Issue #617.
type RepoStatus struct {
	Name   string
	Target string
	State  string
	Commit string
	Error  string
}

// StageStatus mirrors the v1beta1 StageStatus payload. Phase C1 (#690) adds
// the Hash key the controller-side merge uses to carry done records across
// stop/start. Issue #635.
type StageStatus struct {
	Index int
	State string
	Error string
	// Hash is the content hash of the stage at record time — the run-once
	// "done" key. See the v1beta1 type for the merge contract.
	Hash string
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
	// ContainerStateNotCreated marks a container with no containerd record at
	// all — distinct from Stopped (a record that exists but whose task is
	// gone). Appended last to keep the ordinals in lockstep with the v1beta1
	// ContainerState enum, which scheme.go converts by direct int cast.
	ContainerStateNotCreated
)
