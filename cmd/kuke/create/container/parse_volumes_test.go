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

package container

import (
	"testing"

	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func TestParseVolumeFlags(t *testing.T) {
	tests := []struct {
		name    string
		entries []string
		want    []v1beta1.VolumeMount
		wantErr bool
	}{
		{name: "empty", entries: nil, want: nil},
		{
			name:    "src and dst",
			entries: []string{"/host/src:/container/dst"},
			want:    []v1beta1.VolumeMount{{Source: "/host/src", Target: "/container/dst"}},
		},
		{
			name:    "readonly",
			entries: []string{"/host/src:/container/dst:ro"},
			want: []v1beta1.VolumeMount{
				{Source: "/host/src", Target: "/container/dst", ReadOnly: true},
			},
		},
		{
			name:    "explicit rw",
			entries: []string{"/host/src:/container/dst:rw"},
			want: []v1beta1.VolumeMount{
				{Source: "/host/src", Target: "/container/dst", ReadOnly: false},
			},
		},
		{
			name: "multiple",
			entries: []string{
				"/a:/a",
				"/b:/b:ro",
			},
			want: []v1beta1.VolumeMount{
				{Source: "/a", Target: "/a"},
				{Source: "/b", Target: "/b", ReadOnly: true},
			},
		},
		{name: "single segment rejected", entries: []string{"/only"}, wantErr: true},
		{name: "four segments rejected", entries: []string{"/a:/b:ro:extra"}, wantErr: true},
		{name: "empty source rejected", entries: []string{":/dst"}, wantErr: true},
		{name: "empty target rejected", entries: []string{"/src:"}, wantErr: true},
		{name: "unknown mode rejected", entries: []string{"/a:/b:zz"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseVolumeFlags(tt.entries)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseVolumeFlags(%v) = %v, want error", tt.entries, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseVolumeFlags(%v) unexpected error: %v", tt.entries, err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d (%+v)", len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("[%d] = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}
