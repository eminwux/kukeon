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

package ctr_test

import (
	"errors"
	"testing"

	ctr "github.com/eminwux/kukeon/internal/ctr"
)

func TestErrdefs(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantMessage string
	}{
		{
			name:        "ErrEmptyGroupPath",
			err:         ctr.ErrEmptyGroupPath,
			wantMessage: "ctr: cgroup group path is required",
		},
		{
			name:        "ErrInvalidPID",
			err:         ctr.ErrInvalidPID,
			wantMessage: "ctr: pid must be greater than zero",
		},
		{
			name:        "ErrInvalidCPUWeight",
			err:         ctr.ErrInvalidCPUWeight,
			wantMessage: "ctr: cpu weight must be within [1, 10000]",
		},
		{
			name:        "ErrInvalidIOWeight",
			err:         ctr.ErrInvalidIOWeight,
			wantMessage: "ctr: io weight must be within [1, 1000]",
		},
		{
			name:        "ErrInvalidThrottle",
			err:         ctr.ErrInvalidThrottle,
			wantMessage: "ctr: io throttle entries require type, major, minor and rate",
		},
		{
			name:        "ErrEmptyContainerID",
			err:         ctr.ErrEmptyContainerID,
			wantMessage: "ctr: container id is required",
		},
		{
			name:        "ErrEmptyCellID",
			err:         ctr.ErrEmptyCellID,
			wantMessage: "ctr: cell id is required",
		},
		{
			name:        "ErrEmptySpaceID",
			err:         ctr.ErrEmptySpaceID,
			wantMessage: "ctr: space id is required",
		},
		{
			name:        "ErrEmptyRealmID",
			err:         ctr.ErrEmptyRealmID,
			wantMessage: "ctr: realm id is required",
		},
		{
			name:        "ErrEmptyStackID",
			err:         ctr.ErrEmptyStackID,
			wantMessage: "ctr: stack id is required",
		},
		{
			name:        "ErrContainerExists",
			err:         ctr.ErrContainerExists,
			wantMessage: "ctr: container already exists",
		},
		{
			name:        "ErrContainerNotFound",
			err:         ctr.ErrContainerNotFound,
			wantMessage: "ctr: container not found",
		},
		{
			name:        "ErrTaskNotFound",
			err:         ctr.ErrTaskNotFound,
			wantMessage: "ctr: task not found",
		},
		{
			name:        "ErrTaskNotRunning",
			err:         ctr.ErrTaskNotRunning,
			wantMessage: "ctr: task is not running",
		},
		{
			name:        "ErrInvalidImage",
			err:         ctr.ErrInvalidImage,
			wantMessage: "ctr: image reference is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err == nil {
				t.Fatal("error should not be nil")
			}

			if tt.err.Error() != tt.wantMessage {
				t.Errorf("error message = %q, want %q", tt.err.Error(), tt.wantMessage)
			}

			// Verify errors.Is works correctly
			if !errors.Is(tt.err, tt.err) {
				t.Errorf("errors.Is(%v, %v) = false, want true", tt.err, tt.err)
			}
		})
	}
}

func TestErrdefsAreDistinct(t *testing.T) {
	// Verify that different error variables are not equal
	errorPairs := []struct {
		name string
		err1 error
		err2 error
	}{
		{"ErrEmptyGroupPath vs ErrInvalidPID", ctr.ErrEmptyGroupPath, ctr.ErrInvalidPID},
		{"ErrEmptyContainerID vs ErrContainerNotFound", ctr.ErrEmptyContainerID, ctr.ErrContainerNotFound},
		{"ErrTaskNotFound vs ErrTaskNotRunning", ctr.ErrTaskNotFound, ctr.ErrTaskNotRunning},
		{"ErrEmptyRealmID vs ErrEmptySpaceID", ctr.ErrEmptyRealmID, ctr.ErrEmptySpaceID},
	}

	for _, tt := range errorPairs {
		t.Run(tt.name, func(t *testing.T) {
			if errors.Is(tt.err1, tt.err2) {
				t.Errorf("errors.Is(%v, %v) = true, want false (errors should be distinct)", tt.err1, tt.err2)
			}
		})
	}
}
