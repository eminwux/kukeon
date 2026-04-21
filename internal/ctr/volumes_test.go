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
	"testing"

	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

func TestBuildBindMounts(t *testing.T) {
	tests := []struct {
		name    string
		in      []intmodel.VolumeMount
		wantLen int
		wantRO  []bool // per-mount; "ro" option expected
	}{
		{name: "empty", in: nil, wantLen: 0},
		{
			name:    "rw",
			in:      []intmodel.VolumeMount{{Source: "/a", Target: "/b"}},
			wantLen: 1,
			wantRO:  []bool{false},
		},
		{
			name:    "ro",
			in:      []intmodel.VolumeMount{{Source: "/a", Target: "/b", ReadOnly: true}},
			wantLen: 1,
			wantRO:  []bool{true},
		},
		{
			name: "mixed",
			in: []intmodel.VolumeMount{
				{Source: "/a", Target: "/a"},
				{Source: "/b", Target: "/b", ReadOnly: true},
			},
			wantLen: 2,
			wantRO:  []bool{false, true},
		},
		{
			name:    "skip empty source",
			in:      []intmodel.VolumeMount{{Source: "", Target: "/b"}},
			wantLen: 0,
		},
		{
			name:    "skip empty target",
			in:      []intmodel.VolumeMount{{Source: "/a", Target: ""}},
			wantLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildBindMounts(tt.in)
			if len(got) != tt.wantLen {
				t.Fatalf("len = %d, want %d", len(got), tt.wantLen)
			}
			for i, m := range got {
				if m.Type != "bind" {
					t.Errorf("[%d] Type = %q, want \"bind\"", i, m.Type)
				}
				if m.Source != tt.in[i].Source || m.Destination != tt.in[i].Target {
					t.Errorf("[%d] Source/Destination = %q/%q, want %q/%q",
						i, m.Source, m.Destination, tt.in[i].Source, tt.in[i].Target)
				}
				hasRO := false
				hasRW := false
				hasRbind := false
				for _, o := range m.Options {
					switch o {
					case "ro":
						hasRO = true
					case "rw":
						hasRW = true
					case "rbind":
						hasRbind = true
					}
				}
				if !hasRbind {
					t.Errorf("[%d] Options %v missing rbind", i, m.Options)
				}
				if tt.wantRO[i] {
					if !hasRO || hasRW {
						t.Errorf("[%d] Options %v want ro", i, m.Options)
					}
				} else {
					if hasRO || !hasRW {
						t.Errorf("[%d] Options %v want rw", i, m.Options)
					}
				}
			}
		})
	}
}
