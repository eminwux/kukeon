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
	"reflect"
	"testing"
)

func TestParseCapabilities(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		label string
		want  []string
	}{
		{name: "empty", label: "", want: nil},
		{name: "only separators", label: " , ,\t\n", want: nil},
		{name: "single", label: "git", want: []string{"git"}},
		{name: "comma separated", label: "git,gh,claude", want: []string{"claude", "gh", "git"}},
		{name: "comma and space", label: "git, gh ,  ssh-agent", want: []string{"gh", "git", "ssh-agent"}},
		{name: "whitespace separated", label: "git gh\tnode\npython", want: []string{"gh", "git", "node", "python"}},
		{name: "deduplicated and sorted", label: "gh,git,gh,claude,git", want: []string{"claude", "gh", "git"}},
		{name: "trailing comma", label: "git,gh,", want: []string{"gh", "git"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ParseCapabilities(tt.label); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseCapabilities(%q) = %v, want %v", tt.label, got, tt.want)
			}
		})
	}
}

func TestMissingCapabilities(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		have     []string
		required []string
		want     []string
	}{
		{name: "nothing required", have: []string{"git"}, required: nil, want: nil},
		{name: "all present", have: []string{"claude", "gh", "git"}, required: []string{"git", "gh"}, want: nil},
		{name: "one missing", have: []string{"git"}, required: []string{"git", "gh"}, want: []string{"gh"}},
		{
			// Missing entries keep the order the cell declared them in, so the
			// refusal reads the same as the blueprint's requires list.
			name: "several missing keep required order", have: []string{"git"},
			required: []string{"ssh-agent", "git", "gh"}, want: []string{"ssh-agent", "gh"},
		},
		{name: "image declares nothing", have: nil, required: []string{"git"}, want: []string{"git"}},
		{name: "no prefix match", have: []string{"github"}, required: []string{"git"}, want: []string{"git"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := MissingCapabilities(tt.have, tt.required); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("MissingCapabilities(%v, %v) = %v, want %v", tt.have, tt.required, got, tt.want)
			}
		})
	}
}
