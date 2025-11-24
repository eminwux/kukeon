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

type Container struct {
	Metadata ContainerMetadata
	Spec     ContainerSpec
	Status   ContainerStatus
}

type ContainerMetadata struct {
	Name   string
	Labels map[string]string
}

type ContainerSpec struct {
	ID              string
	RealmName       string
	SpaceName       string
	StackName       string
	CellName        string
	Root            bool
	Image           string
	Command         string
	Args            []string
	Env             []string
	Ports           []string
	Volumes         []string
	Networks        []string
	NetworksAliases []string
	Privileged      bool
	CNIConfigPath   string
	RestartPolicy   string
}

type ContainerStatus struct {
	State        ContainerState
	RestartCount int
	RestartTime  time.Time
	StartTime    time.Time
	FinishTime   time.Time
	ExitCode     int
	ExitSignal   string
}

type ContainerState int

const (
	ContainerStatePending ContainerState = iota
	ContainerStateReady
	ContainerStateFailed
	ContainerStateUnknown
)
