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

type SpaceDoc struct {
	APIVersion Version       `json:"apiVersion" yaml:"apiVersion"`
	Kind       Kind          `json:"kind"       yaml:"kind"`
	Metadata   SpaceMetadata `json:"metadata"   yaml:"metadata"`
	Spec       SpaceSpec     `json:"spec"       yaml:"spec"`
	Status     SpaceStatus   `json:"status"     yaml:"status"`
}

type SpaceMetadata struct {
	Name   string            `json:"name"   yaml:"name"`
	Labels map[string]string `json:"labels" yaml:"labels"`
}

type SpaceSpec struct {
	RealmID       string         `json:"realmId"                 yaml:"realmId"`
	CNIConfigPath string         `json:"cniConfigPath,omitempty" yaml:"cniConfigPath,omitempty"`
	Defaults      *SpaceDefaults `json:"defaults,omitempty"      yaml:"defaults,omitempty"`
}

// SpaceDefaults declares default values that Kukeon inherits into resources
// created inside the Space unless the resource's own spec overrides the field.
// It exists so the isolation envelope can be declared once on the Space and
// reused by every Container that lives in it.
type SpaceDefaults struct {
	Container *SpaceContainerDefaults `json:"container,omitempty" yaml:"container,omitempty"`
}

// SpaceContainerDefaults mirrors the isolation-related fields on ContainerSpec.
// Each field is applied to a Container only when the Container leaves it empty.
// Inheritance is shallow: a Container that sets Capabilities replaces the Space
// default outright — Drop and Add slices are not merged.
//
// ReadOnlyRootFilesystem is a *bool so the default can distinguish "not set"
// from an explicit "false"; Container.Spec.ReadOnlyRootFilesystem is still a
// plain bool, so a Container cannot opt out of a Space default that enables
// it.
type SpaceContainerDefaults struct {
	User                   string                 `json:"user,omitempty"                   yaml:"user,omitempty"`
	ReadOnlyRootFilesystem *bool                  `json:"readOnlyRootFilesystem,omitempty" yaml:"readOnlyRootFilesystem,omitempty"`
	Capabilities           *ContainerCapabilities `json:"capabilities,omitempty"           yaml:"capabilities,omitempty"`
	SecurityOpts           []string               `json:"securityOpts,omitempty"           yaml:"securityOpts,omitempty"`
	Tmpfs                  []ContainerTmpfsMount  `json:"tmpfs,omitempty"                  yaml:"tmpfs,omitempty"`
	Resources              *ContainerResources    `json:"resources,omitempty"              yaml:"resources,omitempty"`
}

type SpaceStatus struct {
	State      SpaceState `json:"state"                yaml:"state"`
	CgroupPath string     `json:"cgroupPath,omitempty" yaml:"cgroupPath,omitempty"`
}

type SpaceState int

const (
	SpaceStatePending SpaceState = iota
	SpaceStateReady
	SpaceStateFailed
	SpaceStateUnknown
)

func (s *SpaceState) String() string {
	switch *s {
	case SpaceStatePending:
		return StatePendingStr
	case SpaceStateReady:
		return StateReadyStr
	case SpaceStateFailed:
		return StateFailedStr
	case SpaceStateUnknown:
		return StateUnknownStr
	}
	return StateUnknownStr
}
