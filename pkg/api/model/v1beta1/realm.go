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
	CgroupPath string     `json:"cgroupPath,omitempty"         yaml:"cgroupPath,omitempty"`
	// SubtreeControllers is the cgroup-v2 controller set actually
	// delegated on this realm's own cgroup.subtree_control after the
	// host-root filter (issue #328).
	SubtreeControllers []string `json:"subtreeControllers,omitempty" yaml:"subtreeControllers,omitempty"`
	// CreatedAt is the wall-clock time of the first persist for this
	// realm. Bumped only when zero so the value never moves once set.
	CreatedAt time.Time `json:"createdAt,omitempty"          yaml:"createdAt,omitempty"`
	// UpdatedAt is the wall-clock time of the most recent persist.
	UpdatedAt time.Time `json:"updatedAt,omitempty"          yaml:"updatedAt,omitempty"`
	// ReadyAt is the wall-clock time of the first State==Ready persist.
	// Set-once: never overwritten by subsequent Ready transitions or
	// state flaps, so it serves as an immutable "first reached Ready"
	// marker.
	ReadyAt time.Time `json:"readyAt,omitempty"            yaml:"readyAt,omitempty"`
	// Reason is a short reason code summarizing why State is in its
	// current value. Empty when no reason has been recorded.
	Reason string `json:"reason,omitempty"             yaml:"reason,omitempty"`
	// Message is the human-readable detail backing Reason; especially
	// valuable on State==Failed where it captures the immediate cause.
	Message string `json:"message,omitempty"            yaml:"message,omitempty"`
	// CgroupReady reports whether CgroupPath actually exists on the
	// host filesystem as of the last status write. The CgroupPath
	// field records the intent (the path where the cgroup should
	// live); this re-verifies presence so callers can distinguish
	// "configured" from "still mounted".
	CgroupReady bool `json:"cgroupReady,omitempty"        yaml:"cgroupReady,omitempty"`
	// ContainerdNamespaceReady reports whether the containerd
	// namespace recorded in Spec.Namespace was actually present as of
	// the last status write. Like CgroupReady, this separates intent
	// from observation.
	ContainerdNamespaceReady bool `json:"containerdNamespaceReady,omitempty" yaml:"containerdNamespaceReady,omitempty"`
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
