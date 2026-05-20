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

type Space struct {
	Metadata SpaceMetadata
	Spec     SpaceSpec
	Status   SpaceStatus
}

type SpaceMetadata struct {
	Name   string
	Labels map[string]string
	// Generation is a monotonic counter bumped by a writer on each
	// spec-changing update. Defaults to zero; phase 3 wires the writers to
	// populate it. See ObservedGeneration on the status.
	Generation int64
}

type SpaceSpec struct {
	RealmName     string
	CNIConfigPath string
	Network       *SpaceNetwork
	Defaults      *SpaceDefaults
}

// SpaceNetwork groups network-scoped policy applied to the space bridge.
type SpaceNetwork struct {
	Egress *EgressPolicy
}

// EgressPolicy constrains outbound traffic leaving the space bridge. nil
// means unconstrained; EgressDefaultAllow with no allow rules matches the
// same unconstrained behavior.
type EgressPolicy struct {
	Default EgressDefault
	Allow   []EgressAllowRule
}

// EgressDefault is the fallthrough action when no allowlist rule matches.
type EgressDefault string

const (
	EgressDefaultAllow EgressDefault = "allow"
	EgressDefaultDeny  EgressDefault = "deny"
)

// EgressAllowRule describes a single permitted destination. Exactly one of
// Host or CIDR must be set. Empty Ports means "any port on this destination".
type EgressAllowRule struct {
	Host  string
	CIDR  string
	Ports []int
}

// SpaceDefaults declares default values inherited by resources inside the
// Space unless the resource's own spec overrides the field. See the external
// v1beta1.SpaceDefaults type for user-facing documentation.
type SpaceDefaults struct {
	Container *SpaceContainerDefaults
}

// SpaceContainerDefaults mirrors the isolation fields on ContainerSpec.
type SpaceContainerDefaults struct {
	User                   string
	ReadOnlyRootFilesystem *bool
	Capabilities           *ContainerCapabilities
	SecurityOpts           []string
	Tmpfs                  []ContainerTmpfsMount
	Resources              *ContainerResources
}

type SpaceStatus struct {
	State      SpaceState
	CgroupPath string
	// SubtreeControllers records the cgroup-v2 controllers actually
	// delegated on this space's own cgroup.subtree_control after the
	// effective filter against the host root's cgroup.controllers (issue
	// #328).
	SubtreeControllers []string
	// Lifecycle and runtime-health fields (issue #166). See
	// RealmStatus for the per-field contract.
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ReadyAt     time.Time
	Reason      string
	Message     string
	CgroupReady bool
	// ObservedGeneration is the Metadata.Generation the reconciler last
	// acted on. Defaults to zero; phase 3 wires the reconciler to compare
	// it against Generation to skip stale work.
	ObservedGeneration int64
}

type SpaceState int

const (
	SpaceStatePending SpaceState = iota
	SpaceStateCreating
	SpaceStateReady
	SpaceStateDeleting
	SpaceStateFailed
	SpaceStateUnknown
)
