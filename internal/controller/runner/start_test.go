//go:build !integration

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

package runner

import (
	"errors"
	"fmt"
	"testing"

	containerd "github.com/containerd/containerd/v2/client"
	internalerrdefs "github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// TestRootContainerWantsCNI is the gate StartCell uses before any CNI work
// (NewManager, LoadNetworkConfigList, AddContainerToNetwork). The negative
// case is the kukeond invariant from issue #96 — host-netns containers must
// not be CNI-attached, otherwise the bridge plugin runs in the daemon's own
// netns and the host loses visibility of the cell's veths and iptables rules.
func TestRootContainerWantsCNI(t *testing.T) {
	tests := []struct {
		name string
		spec intmodel.ContainerSpec
		want bool
	}{
		{
			name: "default container goes through CNI attach",
			spec: intmodel.ContainerSpec{ID: "c1"},
			want: true,
		},
		{
			name: "privileged-only container still goes through CNI",
			spec: intmodel.ContainerSpec{ID: "c2", Privileged: true},
			want: true,
		},
		{
			name: "host network container skips CNI attach",
			spec: intmodel.ContainerSpec{ID: "kukeond", HostNetwork: true},
			want: false,
		},
		{
			name: "host network + privileged skips CNI attach",
			spec: intmodel.ContainerSpec{ID: "kukeond", HostNetwork: true, Privileged: true},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rootContainerWantsCNI(tt.spec); got != tt.want {
				t.Errorf("rootContainerWantsCNI(%+v) = %v, want %v", tt.spec, got, tt.want)
			}
		})
	}
}

// TestCellWantsHostNetworkRoot covers the propagation rule that makes the
// auto-default root container host-network whenever any container in the
// cell asked for HostNetwork=true. Without this, the kukeond container
// would join the netns of a busybox sleep root that has its own
// per-container netns — exactly the divergence issue #96 fixes.
func TestCellWantsHostNetworkRoot(t *testing.T) {
	tests := []struct {
		name string
		cell intmodel.Cell
		want bool
	}{
		{
			name: "empty containers list",
			cell: intmodel.Cell{},
			want: false,
		},
		{
			name: "all containers default network",
			cell: intmodel.Cell{Spec: intmodel.CellSpec{Containers: []intmodel.ContainerSpec{
				{ID: "a"}, {ID: "b"},
			}}},
			want: false,
		},
		{
			name: "one container wants host network",
			cell: intmodel.Cell{Spec: intmodel.CellSpec{Containers: []intmodel.ContainerSpec{
				{ID: "a"}, {ID: "kukeond", HostNetwork: true},
			}}},
			want: true,
		},
		{
			name: "single host-network container",
			cell: intmodel.Cell{Spec: intmodel.CellSpec{Containers: []intmodel.ContainerSpec{
				{ID: "kukeond", HostNetwork: true},
			}}},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cellWantsHostNetworkRoot(tt.cell); got != tt.want {
				t.Errorf("cellWantsHostNetworkRoot() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestValidateExplicitRootHostNetwork covers the explicit-root branch's
// host-network alignment guard from issue #103. Without it, a peer with
// HostNetwork=true alongside an explicit non-host root would silently lose
// its host-network intent — peers join the root's netns via
// JoinContainerNamespaces, so the root owns the decision.
func TestValidateExplicitRootHostNetwork(t *testing.T) {
	tests := []struct {
		name     string
		cell     intmodel.Cell
		rootSpec intmodel.ContainerSpec
		wantErr  bool
	}{
		{
			name: "explicit root, no peer wants host-network",
			cell: intmodel.Cell{Spec: intmodel.CellSpec{
				RootContainerID: "c2",
				Containers: []intmodel.ContainerSpec{
					{ID: "c1"}, {ID: "c2"},
				},
			}},
			rootSpec: intmodel.ContainerSpec{ID: "c2"},
			wantErr:  false,
		},
		{
			name: "explicit root host-network, peers default — fine, peers join host netns",
			cell: intmodel.Cell{Spec: intmodel.CellSpec{
				RootContainerID: "c2",
				Containers: []intmodel.ContainerSpec{
					{ID: "c1"}, {ID: "c2", HostNetwork: true},
				},
			}},
			rootSpec: intmodel.ContainerSpec{ID: "c2", HostNetwork: true},
			wantErr:  false,
		},
		{
			name: "explicit root host-network, peer also host-network — aligned",
			cell: intmodel.Cell{Spec: intmodel.CellSpec{
				RootContainerID: "c2",
				Containers: []intmodel.ContainerSpec{
					{ID: "c1", HostNetwork: true}, {ID: "c2", HostNetwork: true},
				},
			}},
			rootSpec: intmodel.ContainerSpec{ID: "c2", HostNetwork: true},
			wantErr:  false,
		},
		{
			name: "peer wants host-network but explicit root does not — reject",
			cell: intmodel.Cell{Spec: intmodel.CellSpec{
				RootContainerID: "c2",
				Containers: []intmodel.ContainerSpec{
					{ID: "c1", HostNetwork: true}, {ID: "c2"},
				},
			}},
			rootSpec: intmodel.ContainerSpec{ID: "c2"},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateExplicitRootHostNetwork(tt.cell, tt.rootSpec)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateExplicitRootHostNetwork() err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && !errors.Is(err, internalerrdefs.ErrExplicitRootHostNetworkMismatch) {
				t.Errorf("err = %v, want wrapped ErrExplicitRootHostNetworkMismatch", err)
			}
		})
	}
}

// TestCellTasksAllRunningFn pins the StartCell idempotency guard from
// issue #149: only every-task-running yields a no-op skip. Anything else
// — root not running, root status error, a non-root in any non-Running
// state, a non-root with an empty ContainerdID — has to fall through to
// the destructive teardown-and-recreate path so a wedged cell can still
// recover.
func TestCellTasksAllRunningFn(t *testing.T) {
	const rootID = "space_stack_cell_root"

	statusOf := func(states map[string]containerd.ProcessStatus) func(string) (containerd.Status, error) {
		return func(id string) (containerd.Status, error) {
			s, ok := states[id]
			if !ok {
				return containerd.Status{}, fmt.Errorf("no task for %q", id)
			}
			return containerd.Status{Status: s}, nil
		}
	}

	cellWithPeers := func(peers ...intmodel.ContainerSpec) intmodel.Cell {
		return intmodel.Cell{Spec: intmodel.CellSpec{Containers: peers}}
	}

	tests := []struct {
		name string
		cell intmodel.Cell
		fn   func(string) (containerd.Status, error)
		want bool
	}{
		{
			name: "root running, no non-root containers — already up",
			cell: intmodel.Cell{},
			fn:   statusOf(map[string]containerd.ProcessStatus{rootID: containerd.Running}),
			want: true,
		},
		{
			name: "root running and all non-roots running — already up",
			cell: cellWithPeers(
				intmodel.ContainerSpec{ID: "a", ContainerdID: "cid_a"},
				intmodel.ContainerSpec{ID: "b", ContainerdID: "cid_b"},
			),
			fn: statusOf(map[string]containerd.ProcessStatus{
				rootID:  containerd.Running,
				"cid_a": containerd.Running,
				"cid_b": containerd.Running,
			}),
			want: true,
		},
		{
			name: "explicit-root entry in Containers list is skipped",
			cell: cellWithPeers(
				intmodel.ContainerSpec{ID: "root", Root: true, ContainerdID: "ignored"},
				intmodel.ContainerSpec{ID: "a", ContainerdID: "cid_a"},
			),
			fn: statusOf(map[string]containerd.ProcessStatus{
				rootID:  containerd.Running,
				"cid_a": containerd.Running,
			}),
			want: true,
		},
		{
			name: "root TaskStatus errors — not up",
			cell: intmodel.Cell{},
			fn:   statusOf(map[string]containerd.ProcessStatus{}),
			want: false,
		},
		{
			name: "root stopped — not up",
			cell: intmodel.Cell{},
			fn:   statusOf(map[string]containerd.ProcessStatus{rootID: containerd.Stopped}),
			want: false,
		},
		{
			name: "root running, one non-root stopped — not up",
			cell: cellWithPeers(
				intmodel.ContainerSpec{ID: "a", ContainerdID: "cid_a"},
				intmodel.ContainerSpec{ID: "b", ContainerdID: "cid_b"},
			),
			fn: statusOf(map[string]containerd.ProcessStatus{
				rootID:  containerd.Running,
				"cid_a": containerd.Running,
				"cid_b": containerd.Stopped,
			}),
			want: false,
		},
		{
			name: "non-root has empty ContainerdID — not up",
			cell: cellWithPeers(
				intmodel.ContainerSpec{ID: "a", ContainerdID: ""},
			),
			fn:   statusOf(map[string]containerd.ProcessStatus{rootID: containerd.Running}),
			want: false,
		},
		{
			name: "non-root TaskStatus errors — not up",
			cell: cellWithPeers(
				intmodel.ContainerSpec{ID: "a", ContainerdID: "cid_a"},
			),
			fn:   statusOf(map[string]containerd.ProcessStatus{rootID: containerd.Running}),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cellTasksAllRunningFn(tt.cell, rootID, tt.fn); got != tt.want {
				t.Errorf("cellTasksAllRunningFn() = %v, want %v", got, tt.want)
			}
		})
	}
}
