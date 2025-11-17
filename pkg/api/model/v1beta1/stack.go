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

type StackDoc struct {
	APIVersion Version       `json:"apiVersion" yaml:"apiVersion"`
	Kind       Kind          `json:"kind"       yaml:"kind"`
	Metadata   StackMetadata `json:"metadata"   yaml:"metadata"`
	Spec       StackSpec     `json:"spec"       yaml:"spec"`
	Status     StackStatus   `json:"status"     yaml:"status"`
}

type StackMetadata struct {
	Name   string            `json:"name"   yaml:"name"`
	Labels map[string]string `json:"labels" yaml:"labels"`
}

type StackSpec struct {
	ID      string `json:"id"      yaml:"id"`
	RealmID string `json:"realmId" yaml:"realmId"`
	SpaceID string `json:"spaceId" yaml:"spaceId"`
}

type StackStatus struct {
	State      StackState `json:"state"      yaml:"state"`
	CgroupPath string     `json:"cgroupPath" yaml:"cgroupPath"`
}

type StackState int

const (
	StackStatePending StackState = iota
	StackStateReady
	StackStateFailed
	StackStateUnknown
)

func (s *StackState) String() string {
	switch *s {
	case StackStatePending:
		return StatePendingStr
	case StackStateReady:
		return StateReadyStr
	case StackStateFailed:
		return StateFailedStr
	case StackStateUnknown:
		return StateUnknownStr
	}
	return StateUnknownStr
}
