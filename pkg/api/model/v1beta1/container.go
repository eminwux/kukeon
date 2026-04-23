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

package v1beta1

import "time"

type ContainerDoc struct {
	APIVersion Version           `json:"apiVersion" yaml:"apiVersion"`
	Kind       Kind              `json:"kind"       yaml:"kind"`
	Metadata   ContainerMetadata `json:"metadata"   yaml:"metadata"`
	Spec       ContainerSpec     `json:"spec"       yaml:"spec"`
	Status     ContainerStatus   `json:"status"     yaml:"status"`
}

type ContainerMetadata struct {
	Name   string            `json:"name"   yaml:"name"`
	Labels map[string]string `json:"labels" yaml:"labels"`
}

type ContainerSpec struct {
	ID                     string                 `json:"id"                               yaml:"id"`
	ContainerdID           string                 `json:"containerdId,omitempty"           yaml:"containerdId,omitempty"`
	RealmID                string                 `json:"realmId"                          yaml:"realmId"`
	SpaceID                string                 `json:"spaceId"                          yaml:"spaceId"`
	StackID                string                 `json:"stackId"                          yaml:"stackId"`
	CellID                 string                 `json:"cellId"                           yaml:"cellId"`
	Root                   bool                   `json:"root,omitempty"                   yaml:"root,omitempty"`
	Image                  string                 `json:"image"                            yaml:"image"`
	Command                string                 `json:"command"                          yaml:"command"`
	Args                   []string               `json:"args"                             yaml:"args"`
	Env                    []string               `json:"env"                              yaml:"env"`
	Ports                  []string               `json:"ports"                            yaml:"ports"`
	Volumes                []VolumeMount          `json:"volumes"                          yaml:"volumes"`
	Networks               []string               `json:"networks"                         yaml:"networks"`
	NetworksAliases        []string               `json:"networksAliases"                  yaml:"networksAliases"`
	Privileged             bool                   `json:"privileged"                       yaml:"privileged"`
	User                   string                 `json:"user,omitempty"                   yaml:"user,omitempty"`
	ReadOnlyRootFilesystem bool                   `json:"readOnlyRootFilesystem,omitempty" yaml:"readOnlyRootFilesystem,omitempty"`
	Capabilities           *ContainerCapabilities `json:"capabilities,omitempty"           yaml:"capabilities,omitempty"`
	SecurityOpts           []string               `json:"securityOpts,omitempty"           yaml:"securityOpts,omitempty"`
	Tmpfs                  []ContainerTmpfsMount  `json:"tmpfs,omitempty"                  yaml:"tmpfs,omitempty"`
	Resources              *ContainerResources    `json:"resources,omitempty"              yaml:"resources,omitempty"`
	Secrets                []ContainerSecret      `json:"secrets,omitempty"                yaml:"secrets,omitempty"`
	CNIConfigPath          string                 `json:"cniConfigPath,omitempty"          yaml:"cniConfigPath,omitempty"`
	RestartPolicy          string                 `json:"restartPolicy"                    yaml:"restartPolicy"`
}

// ContainerSecret references a credential that the daemon resolves at apply
// time and injects into the container — either as an environment variable
// (default) or as a read-only file when MountPath is set. Only the reference
// is persisted; the resolved value is never written to status, metadata, or
// logs.
type ContainerSecret struct {
	Name      string `json:"name"                yaml:"name"`
	FromFile  string `json:"fromFile,omitempty"  yaml:"fromFile,omitempty"`
	FromEnv   string `json:"fromEnv,omitempty"   yaml:"fromEnv,omitempty"`
	MountPath string `json:"mountPath,omitempty" yaml:"mountPath,omitempty"`
}

// VolumeMount is a bind mount of a host path into a container.
type VolumeMount struct {
	Source   string `json:"source"             yaml:"source"`
	Target   string `json:"target"             yaml:"target"`
	ReadOnly bool   `json:"readOnly,omitempty" yaml:"readOnly,omitempty"`
}

// ContainerCapabilities groups Linux capability deltas applied to the
// container process relative to the image default set.
type ContainerCapabilities struct {
	Drop []string `json:"drop,omitempty" yaml:"drop,omitempty"`
	Add  []string `json:"add,omitempty"  yaml:"add,omitempty"`
}

// ContainerTmpfsMount declares a tmpfs mount inside the container.
type ContainerTmpfsMount struct {
	Path      string   `json:"path"                yaml:"path"`
	SizeBytes int64    `json:"sizeBytes,omitempty" yaml:"sizeBytes,omitempty"`
	Options   []string `json:"options,omitempty"   yaml:"options,omitempty"`
}

// ContainerResources exposes the cgroup v2 knobs the orchestrator supports for
// per-container resource limits.
type ContainerResources struct {
	MemoryLimitBytes *int64 `json:"memoryLimitBytes,omitempty" yaml:"memoryLimitBytes,omitempty"`
	CPUShares        *int64 `json:"cpuShares,omitempty"        yaml:"cpuShares,omitempty"`
	PidsLimit        *int64 `json:"pidsLimit,omitempty"        yaml:"pidsLimit,omitempty"`
}

type ContainerStatus struct {
	Name         string         `json:"name"         yaml:"name"`
	ID           string         `json:"id"           yaml:"id"`
	State        ContainerState `json:"state"        yaml:"state"`
	RestartCount int            `json:"restartCount" yaml:"restartCount"`
	RestartTime  time.Time      `json:"restartTime"  yaml:"restartTime"`
	StartTime    time.Time      `json:"startTime"    yaml:"startTime"`
	FinishTime   time.Time      `json:"finishTime"   yaml:"finishTime"`
	ExitCode     int            `json:"exitCode"     yaml:"exitCode"`
	ExitSignal   string         `json:"exitSignal"   yaml:"exitSignal"`
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

func (c *ContainerState) String() string {
	switch *c {
	case ContainerStatePending:
		return StatePendingStr
	case ContainerStateReady:
		return StateReadyStr
	case ContainerStateStopped:
		return StateStoppedStr
	case ContainerStatePaused:
		return StatePausedStr
	case ContainerStatePausing:
		return StatePausingStr
	case ContainerStateFailed:
		return StateFailedStr
	case ContainerStateUnknown:
		return StateUnknownStr
	}
	return StateUnknownStr
}

// NewContainerDoc creates a ContainerDoc ensuring all nested structs are initialized.
func NewContainerDoc(from *ContainerDoc) *ContainerDoc {
	if from == nil {
		return &ContainerDoc{
			APIVersion: "",
			Kind:       "",
			Metadata: ContainerMetadata{
				Name:   "",
				Labels: map[string]string{},
			},
			Spec: ContainerSpec{
				ID:              "",
				ContainerdID:    "",
				RealmID:         "",
				SpaceID:         "",
				StackID:         "",
				CellID:          "",
				Root:            false,
				Image:           "",
				Command:         "",
				Args:            []string{},
				Env:             []string{},
				Ports:           []string{},
				Volumes:         []VolumeMount{},
				Networks:        []string{},
				NetworksAliases: []string{},
				Privileged:      false,
				CNIConfigPath:   "",
				RestartPolicy:   "",
			},
			Status: ContainerStatus{
				Name:         "",
				ID:           "",
				State:        ContainerStatePending,
				RestartCount: 0,
				RestartTime:  time.Time{},
				StartTime:    time.Time{},
				FinishTime:   time.Time{},
				ExitCode:     0,
				ExitSignal:   "",
			},
		}
	}

	out := *from

	if out.Metadata.Labels == nil {
		out.Metadata.Labels = map[string]string{}
	} else {
		labels := make(map[string]string, len(out.Metadata.Labels))
		for k, v := range out.Metadata.Labels {
			labels[k] = v
		}
		out.Metadata.Labels = labels
	}

	out.Spec.Args = cloneSlice(out.Spec.Args)
	out.Spec.Env = cloneSlice(out.Spec.Env)
	out.Spec.Ports = cloneSlice(out.Spec.Ports)
	out.Spec.Volumes = cloneVolumeMounts(out.Spec.Volumes)
	out.Spec.Networks = cloneSlice(out.Spec.Networks)
	out.Spec.NetworksAliases = cloneSlice(out.Spec.NetworksAliases)
	out.Spec.SecurityOpts = cloneSlice(out.Spec.SecurityOpts)
	out.Spec.Secrets = cloneSecrets(out.Spec.Secrets)

	if out.Spec.Capabilities != nil {
		caps := *out.Spec.Capabilities
		caps.Drop = cloneSlice(caps.Drop)
		caps.Add = cloneSlice(caps.Add)
		out.Spec.Capabilities = &caps
	}

	if len(out.Spec.Tmpfs) > 0 {
		mounts := make([]ContainerTmpfsMount, len(out.Spec.Tmpfs))
		for i, m := range out.Spec.Tmpfs {
			m.Options = cloneSlice(m.Options)
			mounts[i] = m
		}
		out.Spec.Tmpfs = mounts
	}

	if out.Spec.Resources != nil {
		res := *out.Spec.Resources
		out.Spec.Resources = &res
	}

	return &out
}

func cloneSlice(in []string) []string {
	if in == nil {
		return []string{}
	}

	out := make([]string, len(in))
	copy(out, in)
	return out
}

func cloneVolumeMounts(in []VolumeMount) []VolumeMount {
	if in == nil {
		return []VolumeMount{}
	}

	out := make([]VolumeMount, len(in))
	copy(out, in)
	return out
}

func cloneSecrets(in []ContainerSecret) []ContainerSecret {
	if in == nil {
		return nil
	}

	out := make([]ContainerSecret, len(in))
	copy(out, in)
	return out
}
