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
	ID                     string
	ContainerdID           string
	RealmName              string
	SpaceName              string
	StackName              string
	CellName               string
	Root                   bool
	Image                  string
	Command                string
	Args                   []string
	Env                    []string
	Ports                  []string
	Volumes                []VolumeMount
	Networks               []string
	NetworksAliases        []string
	Privileged             bool
	HostNetwork            bool
	HostPID                bool
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

// VolumeMount is a bind mount of a host path into a container.
type VolumeMount struct {
	Source   string
	Target   string
	ReadOnly bool
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
