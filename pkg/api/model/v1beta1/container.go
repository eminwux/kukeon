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
	ID              string   `json:"id"                      yaml:"id"`
	RealmID         string   `json:"realmId"                 yaml:"realmId"`
	SpaceID         string   `json:"spaceId"                 yaml:"spaceId"`
	StackID         string   `json:"stackId"                 yaml:"stackId"`
	CellID          string   `json:"cellId"                  yaml:"cellId"`
	Root            bool     `json:"root,omitempty"          yaml:"root,omitempty"`
	Image           string   `json:"image"                   yaml:"image"`
	Command         string   `json:"command"                 yaml:"command"`
	Args            []string `json:"args"                    yaml:"args"`
	Env             []string `json:"env"                     yaml:"env"`
	Ports           []string `json:"ports"                   yaml:"ports"`
	Volumes         []string `json:"volumes"                 yaml:"volumes"`
	Networks        []string `json:"networks"                yaml:"networks"`
	NetworksAliases []string `json:"networksAliases"         yaml:"networksAliases"`
	Privileged      bool     `json:"privileged"              yaml:"privileged"`
	CNIConfigPath   string   `json:"cniConfigPath,omitempty" yaml:"cniConfigPath,omitempty"`
	RestartPolicy   string   `json:"restartPolicy"           yaml:"restartPolicy"`
}

type ContainerStatus struct {
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
	ContainerStateFailed
	ContainerStateUnknown
)

func (c *ContainerState) String() string {
	switch *c {
	case ContainerStatePending:
		return StatePendingStr
	case ContainerStateReady:
		return StateReadyStr
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
				Volumes:         []string{},
				Networks:        []string{},
				NetworksAliases: []string{},
				Privileged:      false,
				CNIConfigPath:   "",
				RestartPolicy:   "",
			},
			Status: ContainerStatus{
				State:        ContainerStateUnknown,
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
	out.Spec.Volumes = cloneSlice(out.Spec.Volumes)
	out.Spec.Networks = cloneSlice(out.Spec.Networks)
	out.Spec.NetworksAliases = cloneSlice(out.Spec.NetworksAliases)

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
