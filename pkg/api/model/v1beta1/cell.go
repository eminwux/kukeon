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

type CellDoc struct {
	APIVersion Version      `json:"apiVersion" yaml:"apiVersion"`
	Kind       Kind         `json:"kind"       yaml:"kind"`
	Metadata   CellMetadata `json:"metadata"   yaml:"metadata"`
	Spec       CellSpec     `json:"spec"       yaml:"spec"`
	Status     CellStatus   `json:"status"     yaml:"status"`
}

type CellMetadata struct {
	Name   string            `json:"name"                 yaml:"name"`
	Labels map[string]string `json:"labels"               yaml:"labels"`
	// Generation is a monotonic counter bumped by a writer on each
	// spec-changing update. Defaults to zero; phase 3 wires the writers to
	// populate it. See ObservedGeneration on the status.
	Generation int64 `json:"generation,omitempty" yaml:"generation,omitempty"`
}

type CellSpec struct {
	ID              string          `json:"id"                            yaml:"id"`
	RealmID         string          `json:"realmId"                       yaml:"realmId"`
	SpaceID         string          `json:"spaceId"                       yaml:"spaceId"`
	StackID         string          `json:"stackId"                       yaml:"stackId"`
	RootContainerID string          `json:"rootContainerId,omitempty"     yaml:"rootContainerId,omitempty"`
	Tty             *CellTty        `json:"tty,omitempty"                 yaml:"tty,omitempty"`
	Containers      []ContainerSpec `json:"containers"                    yaml:"containers"`
	// AutoDelete asks kukeond to delete this cell best-effort after its root
	// container's task exits (any rc). Set by `kuke run --rm`. Cleanup is
	// scoped to the cell only — never cascades to stack/space/realm.
	// Cleanup is driven by kukeond's reconcile loop: the next pass that
	// observes the root task as Stopped/Failed runs KillCell+DeleteCell on
	// the cell. Latency is bounded by the reconcile interval, and the
	// trigger survives daemon restarts (no per-cell goroutine needs to be
	// re-installed on startup).
	AutoDelete bool `json:"autoDelete,omitempty"          yaml:"autoDelete,omitempty"`
	// NestedCgroupRuntime opts the cell into delegating the full
	// host-available cgroup-v2 controller set on its cgroup.subtree_control,
	// rather than the kukeon resource subset (cpu/memory/io/pids). This is
	// the knob a cell that hosts a nested cgroup runtime — an inner
	// containerd, runc, or systemd that places its own children in
	// sub-cgroups under the cell — needs so the inner runtime can in turn
	// delegate any controller it wants to its workloads. Default false
	// keeps the existing cell-as-leaf semantics (issue #312) untouched.
	NestedCgroupRuntime bool `json:"nestedCgroupRuntime,omitempty" yaml:"nestedCgroupRuntime,omitempty"`
	// RuntimeEnv carries CLI-injected env entries (KUKE_RUN's --env
	// KEY=VALUE) for the cell's attachable container, merged into the
	// container's OCI process env at cell start time. Entries collide-and-
	// replace against spec.containers[<attachable>].env (--env wins on
	// matching KEY). Set by `kuke run --env` from the CLI; never authored
	// in a YAML manifest, never read back off a daemon RPC response.
	//
	// Transport-only field with two boundary contracts:
	//
	//  1. The `yaml:"-"` tag keeps it out of any YAML-author surface.
	//  2. JSON-RPC carries it CLI → daemon (where the daemon's StartCell /
	//     CreateCell handler copies it onto the internalCell before the
	//     runner uses it for OCI build). The daemon → CLI direction in
	//     apischeme.BuildCellExternalFromInternal deliberately drops it,
	//     which simultaneously keeps metadata.json clean (the same builder
	//     produces the disk-write doc) and keeps the divergent-spec check
	//     on a subsequent `kuke run <config>` from tripping on the prior
	//     --env injection. Each invocation re-supplies its own RuntimeEnv.
	//
	// Issue #834.
	RuntimeEnv []string `json:"runtimeEnv,omitempty"          yaml:"-"`
	// Provenance records the materialization inputs this cell was stamped
	// from — the binding it was instantiated against (a Blueprint or a
	// Config), the scoped reference to that binding, and the scalar params /
	// env overrides supplied at materialization time. It is the persisted
	// record P4 re-runs to recompute the would-be desired spec for the
	// OutOfSync diff, and the binding reference P2's name generator reads for
	// the cell-name prefix (epic:cell-identity, umbrella #1020).
	//
	// Unlike RuntimeEnv (transport-only, yaml:"-"), Provenance IS persisted:
	// it carries no yaml:"-" so it survives both the JSON metadata.json write
	// and a `kuke get cell -o yaml` round-trip. It is identity/lineage data,
	// not a runtime spec field, so DiffCell deliberately does not compare it
	// (a provenance-only difference must never report a cell OutOfSync). A
	// hand-built cell that was never materialized from a binding carries a
	// nil Provenance. Issue #1021.
	Provenance *CellProvenance `json:"provenance,omitempty"          yaml:"provenance,omitempty"`
	// IgnoreDiskPressure bypasses kukeond's data-volume disk-pressure guard for
	// this cell's creation (issue #1035). Set by `kuke create cell` /
	// `kuke run --ignore-disk-pressure`. Transport-only with the same two
	// boundary contracts as RuntimeEnv: the `yaml:"-"` tag keeps it out of any
	// YAML-author surface, and JSON-RPC carries it CLI → daemon where the
	// CreateCell guard reads it. The daemon → CLI direction in
	// apischeme.BuildCellExternalFromInternal deliberately drops it so the
	// per-invocation override never persists into the stored cell spec; each
	// `kuke create`/`kuke run` re-supplies its own.
	IgnoreDiskPressure bool `json:"ignoreDiskPressure,omitempty"  yaml:"-"`
}

// Binding-kind discriminants for CellProvenance.BindingKind. A cell is
// materialized either from a Config (`kuke run <config>`) or directly from a
// Blueprint (`kuke run -b`); the kind tells P4 which binding channel to
// re-resolve against.
const (
	BindingKindConfig    = "config"
	BindingKindBlueprint = "blueprint"
)

// CellProvenance is the typed record of the inputs a cell was materialized
// from. See CellSpec.Provenance for the lifecycle contract. Issue #1021.
type CellProvenance struct {
	// BindingKind is "config" or "blueprint" (see BindingKind* constants).
	BindingKind string `json:"bindingKind"            yaml:"bindingKind"`
	// BindingRef is the scoped name of the Config or Blueprint this cell was
	// materialized from — the lineage back-reference P4 re-resolves against.
	BindingRef CellBindingRef `json:"bindingRef"            yaml:"bindingRef"`
	// Params are the scalar values resolved into the binding at
	// materialization time (a Config's spec.values, or a Blueprint's
	// resolved --param map). Persisted verbatim so re-resolution does not
	// depend on re-reading transient CLI state.
	Params map[string]string `json:"params,omitempty"      yaml:"params,omitempty"`
	// EnvOverrides are the `--env KEY=VALUE` entries supplied at run time.
	// Mirrors the values RuntimeEnv carries transiently, but persisted here
	// so a later re-resolution sees the same overrides the operator chose.
	EnvOverrides []string `json:"envOverrides,omitempty" yaml:"envOverrides,omitempty"`
}

// CellBindingRef is a scoped reference to the Config or Blueprint a cell was
// materialized from. The scope coordinates follow the same realm/space/stack
// contract every scoped kind uses (realm always set; a deeper coordinate
// requires every shallower one). Issue #1021.
type CellBindingRef struct {
	Name  string `json:"name"            yaml:"name"`
	Realm string `json:"realm"           yaml:"realm"`
	Space string `json:"space,omitempty" yaml:"space,omitempty"`
	Stack string `json:"stack,omitempty" yaml:"stack,omitempty"`
}

// CloneCellProvenance deep-copies a *CellProvenance so mutations on a
// materialized cell (or a converted copy) cannot leak back into a shared
// source. Returns nil for a nil input. Issue #1021.
func CloneCellProvenance(in *CellProvenance) *CellProvenance {
	if in == nil {
		return nil
	}
	out := &CellProvenance{
		BindingKind:  in.BindingKind,
		BindingRef:   in.BindingRef,
		EnvOverrides: cloneSlice(in.EnvOverrides),
	}
	if in.Params != nil {
		params := make(map[string]string, len(in.Params))
		for k, v := range in.Params {
			params[k] = v
		}
		out.Params = params
	}
	return out
}

// CellTty is cell-level tty/attach config. Kept intentionally minimal: only
// fields the container or container-level tty cannot express belong here.
type CellTty struct {
	// Default names the attachable container the post-start attach
	// (`kuke run`'s default mode) selects when no --container flag is
	// given. Must reference an existing container in this cell whose
	// Attachable=true (or be empty).
	Default string `json:"default,omitempty" yaml:"default,omitempty"`
}

type CellStatus struct {
	State      CellState `json:"state"                        yaml:"state"`
	CgroupPath string    `json:"cgroupPath"                   yaml:"cgroupPath"`
	// SubtreeControllers is the cgroup-v2 controller set actually
	// delegated on this cell's own cgroup.subtree_control after the
	// host-root filter (issue #328). For NestedCgroupRuntime cells this
	// is the full host-available set; for ordinary cells it's the
	// kukeon resource subset (cpu/memory/io/pids).
	SubtreeControllers []string          `json:"subtreeControllers,omitempty" yaml:"subtreeControllers,omitempty"`
	Network            CellNetworkStatus `json:"network,omitempty"            yaml:"network,omitempty"`
	Containers         []ContainerStatus `json:"containers"                   yaml:"containers"`
	// ReadyObserved is the persisted form of the one-way latch the
	// reconciler uses to gate Spec.AutoDelete cleanup. Once a cell has
	// been observed Ready it stays true across daemon restarts so that
	// cleanup of a `kuke run --rm` cell that was already Ready at
	// shutdown still fires on the next tick after restart.
	ReadyObserved bool `json:"readyObserved,omitempty"      yaml:"readyObserved,omitempty"`
	// Lifecycle and runtime-health fields — see RealmStatus for the
	// per-field contract; the semantics carry across all four kinds.
	CreatedAt   time.Time `json:"createdAt,omitempty"          yaml:"createdAt,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt,omitempty"          yaml:"updatedAt,omitempty"`
	ReadyAt     time.Time `json:"readyAt,omitempty"            yaml:"readyAt,omitempty"`
	Reason      string    `json:"reason,omitempty"             yaml:"reason,omitempty"`
	Message     string    `json:"message,omitempty"            yaml:"message,omitempty"`
	CgroupReady bool      `json:"cgroupReady,omitempty"        yaml:"cgroupReady,omitempty"`
	// ObservedGeneration is the Metadata.Generation the reconciler last
	// acted on. Defaults to zero; phase 3 wires the reconciler to compare
	// it against Generation to skip stale work.
	ObservedGeneration int64 `json:"observedGeneration,omitempty" yaml:"observedGeneration,omitempty"`
	// OutOfSync is true when the daemon's reconciler detects that this
	// cell's live spec has diverged from what its lineage Config would
	// materialize (issue #820, foundation phase of #819's umbrella). Only
	// set on cells carrying the kukeon.io/config lineage label; cells
	// without that label leave it false. OutOfSyncReason carries a short
	// human-readable summary when true. OutOfSyncError carries a distinct
	// failure surface when the reconciler could not compute divergence at
	// all (referenced Blueprint missing, materialization error) — when
	// non-empty, OutOfSync stays false because divergence is undecidable.
	OutOfSync       bool   `json:"outOfSync,omitempty"          yaml:"outOfSync,omitempty"`
	OutOfSyncReason string `json:"outOfSyncReason,omitempty"    yaml:"outOfSyncReason,omitempty"`
	OutOfSyncError  string `json:"outOfSyncError,omitempty"     yaml:"outOfSyncError,omitempty"`
}

// CellNetworkStatus exposes the host-side bridge a cell is attached to.
// Populated by the runner during cell provisioning so describe/get -o yaml
// surfaces the iface name without recomputing the hash. Always emitted in
// the canonical k-{8hex} form (see cni.SafeBridgeName).
type CellNetworkStatus struct {
	BridgeName string `json:"bridgeName,omitempty" yaml:"bridgeName,omitempty"`
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
				Network:    CellNetworkStatus{},
				Containers: []ContainerStatus{},
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
			containers[i].Volumes = cloneVolumeMounts(container.Volumes)
			containers[i].Networks = cloneSlice(container.Networks)
			containers[i].NetworksAliases = cloneSlice(container.NetworksAliases)
		}
		out.Spec.Containers = containers
	}

	out.Spec.Provenance = CloneCellProvenance(out.Spec.Provenance)

	return &out
}
