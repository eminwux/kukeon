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

type CellDoc struct {
	APIVersion Version      `json:"apiVersion" yaml:"apiVersion"`
	Kind       Kind         `json:"kind"       yaml:"kind"`
	Metadata   CellMetadata `json:"metadata"   yaml:"metadata"`
	Spec       CellSpec     `json:"spec"       yaml:"spec"`
	Status     CellStatus   `json:"status"     yaml:"status"`
}

type CellMetadata struct {
	Name   string            `json:"name"   yaml:"name"`
	Labels map[string]string `json:"labels" yaml:"labels"`
}

type CellSpec struct {
	ID              string          `json:"id"                        yaml:"id"`
	RealmID         string          `json:"realmId"                   yaml:"realmId"`
	SpaceID         string          `json:"spaceId"                   yaml:"spaceId"`
	StackID         string          `json:"stackId"                   yaml:"stackId"`
	RootContainerID string          `json:"rootContainerId,omitempty" yaml:"rootContainerId,omitempty"`
	Containers      []ContainerSpec `json:"containers"                yaml:"containers"`
}

type CellStatus struct {
	State      CellState         `json:"state"      yaml:"state"`
	CgroupPath string            `json:"cgroupPath" yaml:"cgroupPath"`
	Containers []ContainerStatus `json:"containers" yaml:"containers"`
}

type CellState int

const (
	CellStatePending CellState = iota
	CellStateReady
	CellStateStopped
	CellStateFailed
	CellStateUnknown
)

func (c *CellState) String() string {
	switch *c {
	case CellStatePending:
		return StatePendingStr
	case CellStateReady:
		return StateReadyStr
	case CellStateStopped:
		return StateStoppedStr
	case CellStateFailed:
		return StateFailedStr
	case CellStateUnknown:
		return StateUnknownStr
	}
	return StateUnknownStr
}

// NewCellDoc creates a CellDoc ensuring all nested structs are initialized.
func NewCellDoc(from *CellDoc) *CellDoc {
	if from == nil {
		return &CellDoc{
			APIVersion: "",
			Kind:       "",
			Metadata: CellMetadata{
				Name:   "",
				Labels: map[string]string{},
			},
			Spec: CellSpec{
				ID:              "",
				RealmID:         "",
				SpaceID:         "",
				StackID:         "",
				RootContainerID: "",
				Containers:      []ContainerSpec{},
			},
			Status: CellStatus{
				State:      CellStateUnknown,
				CgroupPath: "",
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

	if out.Spec.Containers == nil {
		out.Spec.Containers = []ContainerSpec{}
	} else {
		containers := make([]ContainerSpec, len(out.Spec.Containers))
		for i, container := range out.Spec.Containers {
			containers[i] = container
			containers[i].Args = cloneSlice(container.Args)
			containers[i].Env = cloneSlice(container.Env)
			containers[i].Ports = cloneSlice(container.Ports)
			containers[i].Volumes = cloneSlice(container.Volumes)
			containers[i].Networks = cloneSlice(container.Networks)
			containers[i].NetworksAliases = cloneSlice(container.NetworksAliases)
		}
		out.Spec.Containers = containers
	}

	return &out
}
