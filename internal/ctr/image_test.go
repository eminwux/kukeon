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

	"github.com/eminwux/kukeon/internal/errdefs"
)

// Note: The functions in image.go are unexported (internal implementation).
// They are tested indirectly through exported functions that use them
// (e.g., CreateContainer which calls pullImage and ensureImageUnpacked).
//
// Full testing would require:
// 1. Mocking containerd.Image interface (IsUnpacked, Unpack, Name methods)
// 2. Mocking containerd.Client for GetImage and Pull operations
// 3. Mocking remotes.Resolver for image pulling
//
// These tests are better suited for integration tests that exercise the
// full image pull/unpack flow through exported container creation methods.

func TestImageErrorHandling(t *testing.T) {
	// Verify that image-related errors are properly defined
	if errdefs.ErrInvalidImage == nil {
		t.Error("ErrInvalidImage should not be nil")
	}
	if errdefs.ErrInvalidImage.Error() == "" {
		t.Error("ErrInvalidImage should have a non-empty error message")
	}
}
