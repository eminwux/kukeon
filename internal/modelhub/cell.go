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

type Cell struct {
	Metadata CellMetadata
	Spec     CellSpec
	Status   CellStatus
}

type CellMetadata struct {
	Name   string
	Labels map[string]string
}

type CellSpec struct {
	ID              string
	RealmName       string
	SpaceName       string
	StackName       string
	RootContainerID string
	Tty             *CellTty
	Containers      []ContainerSpec
}

// CellTty mirrors the v1beta1 CellTty payload. See the v1beta1 type for
// field semantics.
type CellTty struct {
	Default string
}

type CellStatus struct {
	State      CellState
	CgroupPath string
	Network    CellNetworkStatus
	Containers []ContainerStatus
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
