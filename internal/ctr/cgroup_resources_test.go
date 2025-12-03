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
	"testing"

	ctr "github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
)

// Note: The functions in cgroup_resources.go are unexported (internal implementation).
// They are tested indirectly through exported functions that use them (e.g., NewCgroup).
// Direct testing would require package-level testing (package ctr instead of ctr_test),
// but we follow the external testing pattern for consistency.

// Test that CgroupResources types can be constructed and used.
// Validation and conversion logic is tested through NewCgroup integration tests.
func TestCgroupResourcesTypes(t *testing.T) {
	tests := []struct {
		name string
		test func(t *testing.T)
	}{
		{
			name: "CPUResources with valid weight",
			test: func(t *testing.T) {
				weight := uint64(100)
				cpu := &ctr.CPUResources{
					Weight: &weight,
					Cpus:   "0-3",
					Mems:   "0",
				}
				if cpu.Weight == nil || *cpu.Weight != 100 {
					t.Errorf("Weight = %v, want 100", cpu.Weight)
				}
				if cpu.Cpus != "0-3" {
					t.Errorf("Cpus = %q, want %q", cpu.Cpus, "0-3")
				}
			},
		},
		{
			name: "MemoryResources with limits",
			test: func(t *testing.T) {
				max := int64(1024 * 1024 * 1024) // 1GB
				mem := &ctr.MemoryResources{
					Max: &max,
				}
				if mem.Max == nil || *mem.Max != max {
					t.Errorf("Max = %v, want %d", mem.Max, max)
				}
			},
		},
		{
			name: "IOResources with throttle entries",
			test: func(t *testing.T) {
				io := &ctr.IOResources{
					Weight: 500,
					Throttle: []ctr.IOThrottleEntry{
						{
							Type:  ctr.IOTypeReadBPS,
							Major: 8,
							Minor: 0,
							Rate:  1048576, // 1MB/s
						},
					},
				}
				if io.Weight != 500 {
					t.Errorf("Weight = %d, want %d", io.Weight, 500)
				}
				if len(io.Throttle) != 1 {
					t.Errorf("Throttle length = %d, want %d", len(io.Throttle), 1)
				}
			},
		},
		{
			name: "CgroupResources with all resource types",
			test: func(t *testing.T) {
				weight := uint64(100)
				max := int64(2048 * 1024 * 1024)
				resources := ctr.CgroupResources{
					CPU: &ctr.CPUResources{
						Weight: &weight,
					},
					Memory: &ctr.MemoryResources{
						Max: &max,
					},
					IO: &ctr.IOResources{
						Weight: 600,
					},
				}
				if resources.CPU == nil {
					t.Error("CPU should not be nil")
				}
				if resources.Memory == nil {
					t.Error("Memory should not be nil")
				}
				if resources.IO.Weight != 600 {
					t.Errorf("IO.Weight = %d, want %d", resources.IO.Weight, 600)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.test)
	}
}

// Test that resource validation errors are properly defined.
// These errors are returned by unexported validation functions.
func TestCgroupResourceErrorTypes(t *testing.T) {
	// Verify that error types are exported and can be checked
	if errdefs.ErrInvalidCPUWeight == nil {
		t.Error("ErrInvalidCPUWeight should not be nil")
	}
	if errdefs.ErrInvalidIOWeight == nil {
		t.Error("ErrInvalidIOWeight should not be nil")
	}
	if errdefs.ErrInvalidThrottle == nil {
		t.Error("ErrInvalidThrottle should not be nil")
	}

	// Verify error messages
	if errdefs.ErrInvalidCPUWeight.Error() == "" {
		t.Error("ErrInvalidCPUWeight should have a non-empty error message")
	}
	if errdefs.ErrInvalidIOWeight.Error() == "" {
		t.Error("ErrInvalidIOWeight should have a non-empty error message")
	}
	if errdefs.ErrInvalidThrottle.Error() == "" {
		t.Error("ErrInvalidThrottle should have a non-empty error message")
	}
}
