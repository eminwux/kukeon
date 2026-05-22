//go:build !integration

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

//nolint:testpackage // this package is used to test the private functions in the helpers package
package runner

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/errdefs"
)

func TestIsValidationError(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		want    bool
		wantErr string
	}{
		{
			name: "nil error returns false",
			err:  nil,
			want: false,
		},
		{
			name: "ErrCellNameRequired returns true",
			err:  errdefs.ErrCellNameRequired,
			want: true,
		},
		{
			name: "ErrCellIDRequired returns true",
			err:  errdefs.ErrCellIDRequired,
			want: true,
		},
		{
			name: "ErrRealmNameRequired returns true",
			err:  errdefs.ErrRealmNameRequired,
			want: true,
		},
		{
			name: "ErrSpaceNameRequired returns true",
			err:  errdefs.ErrSpaceNameRequired,
			want: true,
		},
		{
			name: "ErrStackNameRequired returns true",
			err:  errdefs.ErrStackNameRequired,
			want: true,
		},
		{
			name: "ErrContainerNameRequired returns true",
			err:  errdefs.ErrContainerNameRequired,
			want: true,
		},
		{
			name: "wrapped validation error returns true",
			err:  fmt.Errorf("context: %w", errdefs.ErrCellNameRequired),
			want: true,
		},
		{
			name: "non-validation error returns false",
			err:  errors.New("some other error"),
			want: false,
		},
		{
			name: "wrapped non-validation error returns false",
			err:  fmt.Errorf("context: %w", errors.New("some other error")),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidationError(tt.err)
			if got != tt.want {
				t.Errorf("isValidationError() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestResolveRootCNINetworkName_EmptyWhenSpaceMissing pins the best-effort
// contract the issue #630 teardown sites rely on: when the space metadata
// can't be loaded, resolveRootCNINetworkName returns "" rather than panicking,
// which makes the downstream purgeCNIForContainer safety net a no-op instead
// of dereferencing a nil network name.
func TestResolveRootCNINetworkName_EmptyWhenSpaceMissing(t *testing.T) {
	r := newMetadataTestExec(t, t.TempDir(), time.Now())
	if got := r.resolveRootCNINetworkName("default", "nonexistent-space"); got != "" {
		t.Errorf("resolveRootCNINetworkName() = %q, want \"\" when space metadata is absent", got)
	}
}

// TestBuildRootCNINetworkName_DeterministicWhenSpaceMissing is the regression
// guard for issue #685. The stop/kill/delete teardown sites used to resolve the
// purge-target network name via GetSpace+getSpaceNetworkName and swallow the
// GetSpace error, so when space metadata was gone or corrupt networkName stayed
// "" and the post-delete CNI/IPAM purge (purgeCNIForContainer) was gated out —
// leaking the root container's IPAM reservation at /var/lib/cni/networks/<net>/<ip>.
// buildRootCNINetworkName derives the name deterministically from (realm, space)
// so the purge gate is satisfied even with no space metadata on disk. Contrast
// TestResolveRootCNINetworkName_EmptyWhenSpaceMissing above: the re-ADD helper
// deliberately still returns "" so attach is skipped for a vanished space.
func TestBuildRootCNINetworkName_DeterministicWhenSpaceMissing(t *testing.T) {
	r := newMetadataTestExec(t, t.TempDir(), time.Now())
	// No space metadata is seeded under the tmpdir RunPath, so GetSpace would
	// fail here — the exact condition that used to leave networkName empty.
	got := r.buildRootCNINetworkName("default", "nonexistent-space")
	want := "default-nonexistent-space"
	if got != want {
		t.Errorf("buildRootCNINetworkName() = %q, want %q when space metadata is absent", got, want)
	}
}
