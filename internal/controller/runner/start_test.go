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
	"testing"

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
