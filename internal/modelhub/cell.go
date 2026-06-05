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
	// RuntimeEnv mirrors v1beta1.CellSpec.RuntimeEnv. The wire side carries
	// `kuke run --env KEY=VALUE` from the CLI; the daemon merges these
	// entries into the attachable container's OCI process env at create /
	// start time. NOT persisted (v1beta1 has yaml:"-") — the runtime cell
	// metadata.yaml never carries it, so the disk-read paths
	// (validateAndGetCell, runner.GetCell) return cells with an empty
	// RuntimeEnv. Callers that drive a fresh start/recreate from a CLI
	// request preserve the field by copying cell.Spec.RuntimeEnv from the
	// inbound RPC cell onto the disk-read cell before the OCI build. See
	// runner.startCellLocked and Exec.createCellInternal. Issue #834.
	RuntimeEnv []string
	// Provenance mirrors v1beta1.CellSpec.Provenance: the persisted record of
	// the binding (Config or Blueprint) and the scalar params / env overrides
	// this cell was materialized from. Unlike RuntimeEnv it round-trips
	// through cell metadata so the reconciler can re-resolve the binding after
	// a restart. Nil for hand-built cells. DiffCell ignores it (lineage data,
	// not a runtime spec field). Issue #1021.
	Provenance *CellProvenance
	// IgnoreDiskPressure mirrors v1beta1.CellSpec.IgnoreDiskPressure. The wire
	// side carries `kuke create cell`/`kuke run --ignore-disk-pressure` from
	// the CLI; the runner's CreateCell guard reads it to bypass the data-volume
	// disk-pressure block. NOT persisted (v1beta1 has yaml:"-") — the override
	// is per-invocation, so the disk-read paths return cells with it false.
	// Issue #1035.
	IgnoreDiskPressure bool
}

// CellProvenance mirrors v1beta1.CellProvenance. See that type for the
// field-by-field contract. Issue #1021.
type CellProvenance struct {
	BindingKind  string
	BindingRef   CellBindingRef
	Params       map[string]string
	EnvOverrides []string
}

// CellBindingRef mirrors v1beta1.CellBindingRef. Issue #1021.
type CellBindingRef struct {
	Name  string
	Realm string
	Space string
	Stack string
}

// CloneCellProvenance deep-copies a *CellProvenance, returning nil for a nil
// input. Issue #1021.
func CloneCellProvenance(in *CellProvenance) *CellProvenance {
	if in == nil {
		return nil
	}
	out := &CellProvenance{
		BindingKind: in.BindingKind,
		BindingRef:  in.BindingRef,
	}
	if in.Params != nil {
		params := make(map[string]string, len(in.Params))
		for k, v := range in.Params {
			params[k] = v
		}
		out.Params = params
	}
	if in.EnvOverrides != nil {
		out.EnvOverrides = append([]string(nil), in.EnvOverrides...)
	}
	return out
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
	// OutOfSync is true when the daemon's reconciler detects that this
	// cell's live spec has diverged from what its lineage Config would
	// materialize (issue #820, foundation phase of #819's umbrella). Only
	// set on cells carrying the kukeon.io/config lineage label; cells
	// without that label leave it false. OutOfSyncReason carries a short
	// human-readable summary when true. OutOfSyncError carries a distinct
	// failure surface when the reconciler could not compute divergence at
	// all (referenced Blueprint missing, materialization error) — when
	// non-empty, OutOfSync stays false because divergence is undecidable.
	OutOfSync       bool
	OutOfSyncReason string
	OutOfSyncError  string
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
