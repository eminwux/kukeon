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

type Stack struct {
	Metadata StackMetadata
	Spec     StackSpec
	Status   StackStatus
}

type StackMetadata struct {
	Name   string
	Labels map[string]string
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
}

type StackState int

const (
	StackStatePending StackState = iota
	StackStateReady
	StackStateFailed
	StackStateUnknown
)
