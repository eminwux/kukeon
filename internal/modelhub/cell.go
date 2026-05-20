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

type Cell struct {
	Metadata CellMetadata
	Spec     CellSpec
	Status   CellStatus
}

type CellMetadata struct {
	Name   string
	Labels map[string]string
	// Generation is a monotonic counter bumped by a writer on each
	// spec-changing update. Defaults to zero; phase 3 wires the writers to
	// populate it. See ObservedGeneration on the status.
	Generation int64
}

type CellSpec struct {
	ID              string
	RealmName       string
	SpaceName       string
	StackName       string
	RootContainerID string
	Tty             *CellTty
	Containers      []ContainerSpec
	// AutoDelete mirrors v1beta1.CellSpec.AutoDelete. See that type for
	// semantics; the field is round-tripped through cell metadata so the
	// daemon can re-derive the auto-delete intent after a restart.
	AutoDelete bool
	// NestedCgroupRuntime mirrors v1beta1.CellSpec.NestedCgroupRuntime. See
	// that type for semantics; the field is round-tripped through cell
	// metadata so the daemon can re-toggle the full subtree controller set
	// on the ensure-pass after a restart.
	NestedCgroupRuntime bool
}

// CellTty mirrors the v1beta1 CellTty payload. See the v1beta1 type for
// field semantics.
type CellTty struct {
	Default string
}

type CellStatus struct {
	State      CellState
	CgroupPath string
	// SubtreeControllers records the cgroup-v2 controllers actually
	// delegated on this cell's own cgroup.subtree_control after the
	// effective filter against the host root's cgroup.controllers
	// (issue #328). For NestedCgroupRuntime cells this carries the full
	// host-available set; for ordinary cells it carries the kukeon
	// resource subset (cgroupcheck.CellResourceControllers).
	SubtreeControllers []string
	Network            CellNetworkStatus
	Containers         []ContainerStatus
	// ReadyObserved is a one-way latch set the first time the cell has
	// been observed Ready by ReconcileCell — either via the freshly
	// derived state or via a persisted Ready state from a prior
	// observation (or a synchronous Start that wrote Ready before the
	// reconciler got there). The latch gates Spec.AutoDelete cleanup so
	// a cell that has never been Ready (e.g. mid-creation, between
	// cgroup setup and root-container registration, where
	// GetContainerState reports Stopped for a not-yet-existing
	// container) cannot be reaped by the reconciler.
	ReadyObserved bool
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

// CellNetworkStatus records the network endpoints the cell is attached to.
// BridgeName is the host-side Linux bridge derived via cni.SafeBridgeName
// from the cell's space network — persisting it lets `kuke describe`/
// `kuke get cell -o yaml` recover the human→iface mapping without
// recomputing the hash.
type CellNetworkStatus struct {
	BridgeName string
}

type CellState int

const (
	CellStatePending CellState = iota
	CellStateReady
	CellStateStopped
	CellStateFailed
	CellStateUnknown
)
