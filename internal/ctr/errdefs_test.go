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

	"github.com/eminwux/kukeon/internal/errdefs"
)

func TestErrdefs(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantMessage string
	}{
		{
			name:        "ErrEmptyGroupPath",
			err:         errdefs.ErrEmptyGroupPath,
			wantMessage: "cgroup group path is required",
		},
		{
			name:        "ErrInvalidPID",
			err:         errdefs.ErrInvalidPID,
			wantMessage: "pid must be greater than zero",
		},
		{
			name:        "ErrInvalidCPUWeight",
			err:         errdefs.ErrInvalidCPUWeight,
			wantMessage: "cpu weight must be within [1, 10000]",
		},
		{
			name:        "ErrInvalidIOWeight",
			err:         errdefs.ErrInvalidIOWeight,
			wantMessage: "io weight must be within [1, 1000]",
		},
		{
			name:        "ErrInvalidThrottle",
			err:         errdefs.ErrInvalidThrottle,
			wantMessage: "io throttle entries require type, major, minor and rate",
		},
		{
			name:        "ErrEmptyContainerID",
			err:         errdefs.ErrEmptyContainerID,
			wantMessage: "container id is required",
		},
		{
			name:        "ErrEmptyCellID",
			err:         errdefs.ErrEmptyCellID,
			wantMessage: "cell id is required",
		},
		{
			name:        "ErrEmptySpaceID",
			err:         errdefs.ErrEmptySpaceID,
			wantMessage: "space id is required",
		},
		{
			name:        "ErrEmptyRealmID",
			err:         errdefs.ErrEmptyRealmID,
			wantMessage: "realm id is required",
		},
		{
			name:        "ErrEmptyStackID",
			err:         errdefs.ErrEmptyStackID,
			wantMessage: "stack id is required",
		},
		{
			name:        "ErrContainerExists",
			err:         errdefs.ErrContainerExists,
			wantMessage: "container already exists",
		},
		{
			name:        "ErrContainerNotFound",
			err:         errdefs.ErrContainerNotFound,
			wantMessage: "container not found",
		},
		{
			name:        "ErrTaskNotFound",
			err:         errdefs.ErrTaskNotFound,
			wantMessage: "task not found",
		},
		{
			name:        "ErrTaskNotRunning",
			err:         errdefs.ErrTaskNotRunning,
			wantMessage: "task is not running",
		},
		{
			name:        "ErrInvalidImage",
			err:         errdefs.ErrInvalidImage,
			wantMessage: "invalid image reference",
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
		{"ErrEmptyGroupPath vs ErrInvalidPID", errdefs.ErrEmptyGroupPath, errdefs.ErrInvalidPID},
		{"ErrEmptyContainerID vs ErrContainerNotFound", errdefs.ErrEmptyContainerID, errdefs.ErrContainerNotFound},
		{"ErrTaskNotFound vs ErrTaskNotRunning", errdefs.ErrTaskNotFound, errdefs.ErrTaskNotRunning},
		{"ErrEmptyRealmID vs ErrEmptySpaceID", errdefs.ErrEmptyRealmID, errdefs.ErrEmptySpaceID},
	}

	for _, tt := range errorPairs {
		t.Run(tt.name, func(t *testing.T) {
			if errors.Is(tt.err1, tt.err2) {
				t.Errorf("errors.Is(%v, %v) = true, want false (errors should be distinct)", tt.err1, tt.err2)
			}
		})
	}
}
