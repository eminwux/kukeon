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

type Realm struct {
	Metadata RealmMetadata
	Spec     RealmSpec
	Status   RealmStatus
}

type RealmMetadata struct {
	Name   string
	Labels map[string]string
	// Generation is a monotonic counter bumped by a writer on each
	// spec-changing update. Defaults to zero; phase 3 (issue #596 follow-up)
	// wires the writers to populate it. See ObservedGeneration on the status.
	Generation int64
}

type RealmSpec struct {
	Namespace           string
	RegistryCredentials []RegistryCredentials
}

// RegistryCredentials contains authentication information for a container registry.
type RegistryCredentials struct {
	// Username is the registry username.
	Username string
	// Password is the registry password or token.
	Password string
	// ServerAddress is the registry server address (e.g., "docker.io", "registry.example.com").
	// If empty, credentials apply to the registry extracted from the image reference.
	ServerAddress string
}

type RealmStatus struct {
	State      RealmState
	CgroupPath string
	// SubtreeControllers records the cgroup-v2 controllers actually
	// delegated on this realm's own cgroup.subtree_control after the
	// effective filter against the host root's cgroup.controllers (issue
	// #328, surfacing the result of the helper landed by issue #327).
	SubtreeControllers []string
	// CreatedAt is the wall-clock time of the first persist. Stamped
	// only when zero so the value never moves once set (issue #166).
	CreatedAt time.Time
	// UpdatedAt is the wall-clock time of the most recent persist.
	UpdatedAt time.Time
	// ReadyAt is the wall-clock time of the first State==Ready persist.
	// Set-once: never overwritten by subsequent Ready transitions.
	ReadyAt time.Time
	// Reason is a short reason code summarizing why State is in its
	// current value. Empty when no reason has been recorded.
	Reason string
	// Message is the human-readable detail backing Reason.
	Message string
	// CgroupReady reports whether CgroupPath was observed to exist on
	// the host filesystem as of the last status write.
	CgroupReady bool
	// ContainerdNamespaceReady reports whether the realm's containerd
	// namespace was observed to exist as of the last status write.
	ContainerdNamespaceReady bool
	// ObservedGeneration is the Metadata.Generation the reconciler last
	// acted on. Defaults to zero; phase 3 wires the reconciler to compare
	// it against Generation to skip stale work.
	ObservedGeneration int64
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
