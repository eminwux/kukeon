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

type Stack struct {
	Metadata StackMetadata
	Spec     StackSpec
	Status   StackStatus
}

type StackMetadata struct {
	Name   string
	Labels map[string]string
	// Generation is a monotonic counter bumped by a writer on each
	// spec-changing update. Defaults to zero; phase 3 wires the writers to
	// populate it. See ObservedGeneration on the status.
	Generation int64
}

type StackSpec struct {
	ID        string
	RealmName string
	SpaceName string
}

type StackStatus struct {
	State      StackState
	CgroupPath string
	// SubtreeControllers records the cgroup-v2 controllers actually
	// delegated on this stack's own cgroup.subtree_control after the
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

type StackState int

const (
	StackStatePending StackState = iota
	StackStateReady
	StackStateFailed
	StackStateUnknown
)
