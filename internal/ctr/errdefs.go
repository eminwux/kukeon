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

package ctr

import "errors"

var (
	// ErrEmptyGroupPath indicates that a cgroup group path is required.
	ErrEmptyGroupPath = errors.New("ctr: cgroup group path is required")
	// ErrInvalidPID indicates that a PID must be greater than zero.
	ErrInvalidPID = errors.New("ctr: pid must be greater than zero")
	// ErrInvalidCPUWeight indicates that CPU weight must be within [1, 10000].
	ErrInvalidCPUWeight = errors.New("ctr: cpu weight must be within [1, 10000]")
	// ErrInvalidIOWeight indicates that IO weight must be within [1, 1000].
	ErrInvalidIOWeight = errors.New("ctr: io weight must be within [1, 1000]")
	// ErrInvalidThrottle indicates that IO throttle entries require type, major, minor and rate.
	ErrInvalidThrottle = errors.New("ctr: io throttle entries require type, major, minor and rate")

	// ErrEmptyContainerID indicates that a container id is required.
	ErrEmptyContainerID = errors.New("ctr: container id is required")
	// ErrEmptyCellID indicates that a cell id is required.
	ErrEmptyCellID = errors.New("ctr: cell id is required")
	// ErrEmptySpaceID indicates that a space id is required.
	ErrEmptySpaceID = errors.New("ctr: space id is required")
	// ErrEmptyRealmID indicates that a realm id is required.
	ErrEmptyRealmID = errors.New("ctr: realm id is required")
	// ErrEmptyStackID indicates that a stack id is required.
	ErrEmptyStackID = errors.New("ctr: stack id is required")
	// ErrContainerExists indicates that a container already exists.
	ErrContainerExists = errors.New("ctr: container already exists")
	// ErrContainerNotFound indicates that a container was not found.
	ErrContainerNotFound = errors.New("ctr: container not found")
	// ErrTaskNotFound indicates that a task was not found.
	ErrTaskNotFound = errors.New("ctr: task not found")
	// ErrTaskNotRunning indicates that a task is not running.
	ErrTaskNotRunning = errors.New("ctr: task is not running")
	// ErrInvalidImage indicates that an image reference is required.
	ErrInvalidImage = errors.New("ctr: image reference is required")
)
