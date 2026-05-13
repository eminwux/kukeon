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

package runner

import (
	"reflect"
	"testing"
)

func TestMergeManagedLabels(t *testing.T) {
	tests := []struct {
		name     string
		existing map[string]string
		desired  map[string]string
		want     map[string]string
	}{
		{
			// AC #1: canonical *.kukeon.io labels survive an Update* call
			// against a desired doc that authors one user label.
			name: "preserves canonical labels when desired omits them",
			existing: map[string]string{
				"realm.kukeon.io": "default",
				"space.kukeon.io": "default",
				"stack.kukeon.io": "default",
				"cell.kukeon.io":  "hello-world",
			},
			desired: map[string]string{
				"env": "prod",
			},
			want: map[string]string{
				"realm.kukeon.io": "default",
				"space.kukeon.io": "default",
				"stack.kukeon.io": "default",
				"cell.kukeon.io":  "hello-world",
				"env":             "prod",
			},
		},
		{
			// AC #2: user-authored labels still follow the existing
			// "desired is authoritative" rule. An existing non-managed key
			// the user dropped from `desired` is removed.
			name: "user-authored labels follow desired (removal still works)",
			existing: map[string]string{
				"realm.kukeon.io": "default",
				"old":             "value",
			},
			desired: map[string]string{
				"new": "value",
			},
			want: map[string]string{
				"realm.kukeon.io": "default",
				"new":             "value",
			},
		},
		{
			// AC #3: an explicit user-authored *.kukeon.io key wins, mirroring
			// the create-time "if not exists" semantics in create_cell.go.
			name: "explicit kukeon.io key in desired wins over existing",
			existing: map[string]string{
				"cell.kukeon.io": "from-create",
			},
			desired: map[string]string{
				"cell.kukeon.io": "from-user",
			},
			want: map[string]string{
				"cell.kukeon.io": "from-user",
			},
		},
		{
			name:     "both empty returns desired (nil)",
			existing: nil,
			desired:  nil,
			want:     nil,
		},
		{
			name:     "empty existing returns desired unchanged",
			existing: nil,
			desired:  map[string]string{"env": "prod"},
			want:     map[string]string{"env": "prod"},
		},
		{
			// Non-`.kukeon.io` keys in `existing` are dropped even when
			// `desired` is empty — only managed keys survive an omission.
			name: "non-managed existing keys do not leak when desired is empty",
			existing: map[string]string{
				"realm.kukeon.io": "default",
				"stale":           "value",
			},
			desired: map[string]string{},
			want: map[string]string{
				"realm.kukeon.io": "default",
			},
		},
		{
			// Keys that contain `.kukeon.io` mid-string but do not end with
			// it should not be preserved. The suffix match is intentional
			// (mirrors the apply-side `filterManagedLabels`).
			name: "non-suffix kukeon.io substring is not preserved",
			existing: map[string]string{
				".kukeon.io.suffix": "not-managed",
				"realm.kukeon.io":   "default",
			},
			desired: map[string]string{},
			want: map[string]string{
				"realm.kukeon.io": "default",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeManagedLabels(tt.existing, tt.desired)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("mergeManagedLabels() = %v, want %v", got, tt.want)
			}
		})
	}
}
