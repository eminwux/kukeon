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

type RealmDoc struct {
	APIVersion Version       `json:"apiVersion" yaml:"apiVersion"`
	Kind       Kind          `json:"kind"       yaml:"kind"`
	Metadata   RealmMetadata `json:"metadata"   yaml:"metadata"`
	Spec       RealmSpec     `json:"spec"       yaml:"spec"`
	Status     RealmStatus   `json:"status"     yaml:"status"`
}

type RealmMetadata struct {
	Name   string            `json:"name"   yaml:"name"`
	Labels map[string]string `json:"labels" yaml:"labels"`
}

type RealmSpec struct {
	Namespace           string                `json:"namespace"                     yaml:"namespace"`
	RegistryCredentials []RegistryCredentials `json:"registryCredentials,omitempty" yaml:"registryCredentials,omitempty"`
}

// RegistryCredentials contains authentication information for a container registry.
type RegistryCredentials struct {
	// Username is the registry username.
	Username string `json:"username"                yaml:"username"`
	// Password is the registry password or token.
	Password string `json:"password"                yaml:"password"`
	// ServerAddress is the registry server address (e.g., "docker.io", "registry.example.com").
	// If empty, credentials apply to the registry extracted from the image reference.
	ServerAddress string `json:"serverAddress,omitempty" yaml:"serverAddress,omitempty"`
}

type RealmStatus struct {
	State      RealmState `json:"state"`
	CgroupPath string     `json:"cgroupPath,omitempty" yaml:"cgroupPath,omitempty"`
}

type RealmState int

const (
	RealmStatePending RealmState = iota
	RealmStateCreating
	RealmStateReady
	RealmStateDeleting
	RealmStateFailed
	RealmStateUnknown
)

func (r *RealmState) String() string {
	switch *r {
	case RealmStatePending:
		return StatePendingStr
	case RealmStateCreating:
		return StateCreatingStr
	case RealmStateReady:
		return StateReadyStr
	case RealmStateDeleting:
		return StateDeletingStr
	case RealmStateFailed:
		return StateFailedStr
	case RealmStateUnknown:
		return StateUnknownStr
	}
	return StateUnknownStr
}
