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
	Name   string            `json:"name"   yaml:"name"`
	Labels map[string]string `json:"labels" yaml:"labels"`
}

type CellSpec struct {
	ID              string          `json:"id"                        yaml:"id"`
	RealmID         string          `json:"realmId"                   yaml:"realmId"`
	SpaceID         string          `json:"spaceId"                   yaml:"spaceId"`
	StackID         string          `json:"stackId"                   yaml:"stackId"`
	RootContainerID string          `json:"rootContainerId,omitempty" yaml:"rootContainerId,omitempty"`
	Tty             *CellTty        `json:"tty,omitempty"             yaml:"tty,omitempty"`
	Containers      []ContainerSpec `json:"containers"                yaml:"containers"`
	// AutoDelete asks kukeond to delete this cell best-effort after its root
	// container's task exits (any rc). Set by `kuke run --rm`. Cleanup is
	// scoped to the cell only — never cascades to stack/space/realm.
	// Cleanup is driven by kukeond's reconcile loop: the next pass that
	// observes the root task as Stopped/Failed runs KillCell+DeleteCell on
	// the cell. Latency is bounded by the reconcile interval, and the
	// trigger survives daemon restarts (no per-cell goroutine needs to be
	// re-installed on startup).
	AutoDelete bool `json:"autoDelete,omitempty"      yaml:"autoDelete,omitempty"`
	// NestedCgroupRuntime opts the cell into delegating the full
	// host-available cgroup-v2 controller set on its cgroup.subtree_control,
	// rather than the kukeon resource subset (cpu/memory/io/pids). This is
	// the knob a cell that hosts a nested cgroup runtime — an inner
	// containerd, runc, or systemd that places its own children in
	// sub-cgroups under the cell — needs so the inner runtime can in turn
	// delegate any controller it wants to its workloads. Default false
	// keeps the existing cell-as-leaf semantics (issue #312) untouched.
	NestedCgroupRuntime bool `json:"nestedCgroupRuntime,omitempty" yaml:"nestedCgroupRuntime,omitempty"`
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
	ReadyObserved bool `json:"readyObserved,omitempty" yaml:"readyObserved,omitempty"`
	// Lifecycle and runtime-health fields — see RealmStatus for the
	// per-field contract; the semantics carry across all four kinds.
	CreatedAt   time.Time `json:"createdAt,omitempty"     yaml:"createdAt,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt,omitempty"     yaml:"updatedAt,omitempty"`
	ReadyAt     time.Time `json:"readyAt,omitempty"       yaml:"readyAt,omitempty"`
	Reason      string    `json:"reason,omitempty"        yaml:"reason,omitempty"`
	Message     string    `json:"message,omitempty"       yaml:"message,omitempty"`
	CgroupReady bool      `json:"cgroupReady,omitempty"   yaml:"cgroupReady,omitempty"`
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

	return &out
}
