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

type Realm struct {
	Metadata RealmMetadata
	Spec     RealmSpec
	Status   RealmStatus
}

type RealmMetadata struct {
	Name   string
	Labels map[string]string
}

type RealmSpec struct {
	Namespace           string
	RegistryCredentials []RegistryCredentials
}

// RegistryCredentials contains authentication information for a container registry.
type RegistryCredentials struct {
	// Username is the registry username.
	Username string
	// Password is the registry password or token.
	Password string
	// ServerAddress is the registry server address (e.g., "docker.io", "registry.example.com").
	// If empty, credentials apply to the registry extracted from the image reference.
	ServerAddress string
}

type RealmStatus struct {
	State      RealmState
	CgroupPath string
}

type RealmState int

const (
	RealmStatePending RealmState = iota
	RealmStateCreating
	RealmStateReady
	RealmStateDeleting
	RealmStateFailed
	RealmStateUnknown
)
