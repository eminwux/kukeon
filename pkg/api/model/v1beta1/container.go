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
	ID           string   `json:"id"                               yaml:"id"`
	ContainerdID string   `json:"containerdId,omitempty"           yaml:"containerdId,omitempty"`
	RealmID      string   `json:"realmId"                          yaml:"realmId"`
	SpaceID      string   `json:"spaceId"                          yaml:"spaceId"`
	StackID      string   `json:"stackId"                          yaml:"stackId"`
	CellID       string   `json:"cellId"                           yaml:"cellId"`
	Root         bool     `json:"root,omitempty"                   yaml:"root,omitempty"`
	Image        string   `json:"image"                            yaml:"image"`
	Command      string   `json:"command"                          yaml:"command"`
	Args         []string `json:"args"                             yaml:"args"`
	// WorkingDir sets the cwd of the spawned container process — OCI
	// process.cwd, Docker WORKDIR, K8s Container.workingDir. Empty falls
	// back to the image's WORKDIR (no behavior change for existing specs).
	WorkingDir      string        `json:"workingDir,omitempty"             yaml:"workingDir,omitempty"`
	Env             []string      `json:"env"                              yaml:"env"`
	Ports           []string      `json:"ports"                            yaml:"ports"`
	Volumes         []VolumeMount `json:"volumes"                          yaml:"volumes"`
	Networks        []string      `json:"networks"                         yaml:"networks"`
	NetworksAliases []string      `json:"networksAliases"                  yaml:"networksAliases"`
	Privileged      bool          `json:"privileged"                       yaml:"privileged"`
	// HostNetwork opts the container into the host's network namespace.
	// When true, the runner omits the network LinuxNamespace from the OCI
	// spec (containerd's WithHostNamespace) and does not invoke CNI attach,
	// since a host-netns container has no per-container veth to wire up.
	// Used by the kukeond bootstrap so daemon-installed bridges, veths, and
	// iptables rules land in host scope where kubelet-style CNI plumbing
	// belongs. Default false — no behavior change for existing specs.
	HostNetwork bool `json:"hostNetwork,omitempty"            yaml:"hostNetwork,omitempty"`
	// HostPID opts the container into the host's PID namespace. When true,
	// the runner omits the PID LinuxNamespace from the OCI spec so /proc
	// inside the container reflects host PIDs. Used by the kukeond bootstrap
	// so the CNI bridge plugin running inside the daemon can resolve the
	// host PIDs containerd returns from task.Pid() — without this, attaching
	// user cells to a network fails with `Statfs /proc/<host-pid>/ns/net:
	// no such file or directory`. Default false — no behavior change for
	// existing specs.
	HostPID                bool                   `json:"hostPID,omitempty"                yaml:"hostPID,omitempty"`
	User                   string                 `json:"user,omitempty"                   yaml:"user,omitempty"`
	ReadOnlyRootFilesystem bool                   `json:"readOnlyRootFilesystem,omitempty" yaml:"readOnlyRootFilesystem,omitempty"`
	Capabilities           *ContainerCapabilities `json:"capabilities,omitempty"           yaml:"capabilities,omitempty"`
	SecurityOpts           []string               `json:"securityOpts,omitempty"           yaml:"securityOpts,omitempty"`
	Tmpfs                  []ContainerTmpfsMount  `json:"tmpfs,omitempty"                  yaml:"tmpfs,omitempty"`
	Resources              *ContainerResources    `json:"resources,omitempty"              yaml:"resources,omitempty"`
	Secrets                []ContainerSecret      `json:"secrets,omitempty"                yaml:"secrets,omitempty"`
	CNIConfigPath          string                 `json:"cniConfigPath,omitempty"          yaml:"cniConfigPath,omitempty"`
	RestartPolicy          string                 `json:"restartPolicy"                    yaml:"restartPolicy"`
	// Attachable opts the container into sbsh-wrapper injection. When true,
	// the daemon prepends `sbsh terminal --run-path /run/kukeon/tty …` to
	// process.args, bind-mounts the sbsh binary read-only at /.kukeon/bin/sbsh,
	// and bind-mounts a per-container tty directory at /run/kukeon/tty (sbsh
	// owns its socket, capture, and log files inside it). The host-visible
	// peer of that directory lives in the per-container metadata dir and its
	// `socket` entry is what `kuke attach` connects to. Default false — no
	// behavior change for existing specs.
	Attachable bool `json:"attachable,omitempty"             yaml:"attachable,omitempty"`
	// Tty configures shell-UX (prompt, init scripts) for the sbsh wrapper
	// when Attachable=true. The container model already owns command, args,
	// workingDir, and env, so Tty intentionally only adds layers the
	// container model can't express. Setting any tty field with
	// Attachable=false is a validation error.
	Tty *ContainerTty `json:"tty,omitempty"                    yaml:"tty,omitempty"`
}

// ContainerTty carries per-attach shell-UX config that the daemon threads
// into sbsh terminal on first attach. Has no effect unless Attachable=true.
type ContainerTty struct {
	// Prompt is the literal prompt expression sbsh sets in the wrapped
	// shell, in the same form sbsh's TerminalProfile spec.shell.prompt
	// accepts (e.g. a quoted PS1-style string).
	Prompt string `json:"prompt,omitempty" yaml:"prompt,omitempty"`
	// OnInit are scripts run once when the wrapped shell starts, in order.
	OnInit []TtyStage `json:"onInit,omitempty" yaml:"onInit,omitempty"`
}

// TtyStage is a single onInit script entry. Wrapped in a struct rather than
// a bare string so future stage knobs (timeout, runOn, etc.) can land
// without breaking the YAML shape.
type TtyStage struct {
	Script string `json:"script,omitempty" yaml:"script,omitempty"`
}

// IsEmpty reports whether the tty block carries no user-supplied config —
// i.e. equivalent to omitting the block entirely. Used by validation to
// distinguish "explicitly empty" from "any field set".
func (t *ContainerTty) IsEmpty() bool {
	if t == nil {
		return true
	}
	if t.Prompt != "" {
		return false
	}
	for _, s := range t.OnInit {
		if s.Script != "" {
			return false
		}
	}
	return true
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
				HostNetwork:     false,
				HostPID:         false,
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
