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

import (
	"errors"
	"fmt"
	"testing"

	"github.com/containerd/errdefs"
)

func TestSnapshotterAbsent(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "unregistered snapshotter (ErrNotFound)",
			err:  errdefs.ErrNotFound,
			want: true,
		},
		{
			name: "wrapped ErrNotFound",
			err:  fmt.Errorf("snapshotter btrfs: %w", errdefs.ErrNotFound),
			want: true,
		},
		{
			// containerd v2 shape for a configured-known but
			// not-loaded snapshotter — "snapshotter not loaded:
			// btrfs: invalid argument" — which is an
			// ErrInvalidArgument-class error.
			name: "not-loaded snapshotter (ErrInvalidArgument)",
			err:  fmt.Errorf("snapshotter not loaded: btrfs: %w", errdefs.ErrInvalidArgument),
			want: true,
		},
		{
			name: "bare ErrInvalidArgument",
			err:  errdefs.ErrInvalidArgument,
			want: true,
		},
		{
			// A genuine probe failure (e.g. boltdb corruption) is
			// neither class and must surface, not be skipped.
			name: "genuine probe failure (boltdb corruption)",
			err:  fmt.Errorf("snapshots/overlayfs: %w", errdefs.ErrFailedPrecondition),
			want: false,
		},
		{
			name: "opaque I/O error",
			err:  errors.New("read /var/lib/containerd: input/output error"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := snapshotterAbsent(tt.err); got != tt.want {
				t.Errorf("snapshotterAbsent(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
