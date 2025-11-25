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

type Space struct {
	Metadata SpaceMetadata
	Spec     SpaceSpec
	Status   SpaceStatus
}

type SpaceMetadata struct {
	Name   string
	Labels map[string]string
}

type SpaceSpec struct {
	RealmName     string
	CNIConfigPath string
}

type SpaceStatus struct {
	State      SpaceState
	CgroupPath string
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
