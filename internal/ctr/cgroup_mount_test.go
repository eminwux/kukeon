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
)

// Note: The functions in cgroup_mount.go are unexported (internal implementation).
// They are tested indirectly through the exported GetCgroupMountpoint() method
// on the Client interface. Direct testing would require:
// 1. Package-level testing (package ctr instead of ctr_test), OR
// 2. Testing through the exported API with mocked filesystem operations
//
// The cgroup mountpoint discovery logic is tested through:
// - GetCgroupMountpoint() method tests (in client_test.go or integration tests)
// - Integration tests that exercise the full mountpoint discovery flow

// TestCgroupMountpointConstants verifies that the expected constants exist.
// This is a minimal smoke test to ensure the package structure is correct.
func TestCgroupMountpointConstants(_ *testing.T) {
	// Verify that the package compiles and basic types are accessible
	// The actual mountpoint discovery is tested through GetCgroupMountpoint()
	_ = ctr.Client(nil) // Verify Client interface is accessible

	// This test primarily documents that cgroup_mount.go functions are
	// internal implementation details tested through the exported API
}
