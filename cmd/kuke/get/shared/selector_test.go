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

package shared_test

import (
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/kuke/get/shared"
)

func TestParseLabelSelector_Grammar(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		labels  map[string]string
		want    bool
		wantErr bool
	}{
		{
			name:   "empty selector matches anything",
			input:  "",
			labels: map[string]string{"env": "prod"},
			want:   true,
		},
		{
			name:   "empty selector matches nil labels",
			input:  "",
			labels: nil,
			want:   true,
		},
		{
			name:   "equality match",
			input:  "env=prod",
			labels: map[string]string{"env": "prod"},
			want:   true,
		},
		{
			name:   "equality mismatch on value",
			input:  "env=prod",
			labels: map[string]string{"env": "dev"},
			want:   false,
		},
		{
			name:   "equality mismatch on missing key",
			input:  "env=prod",
			labels: map[string]string{"tier": "web"},
			want:   false,
		},
		{
			name:   "double-equals alias",
			input:  "env==prod",
			labels: map[string]string{"env": "prod"},
			want:   true,
		},
		{
			name:   "inequality matches when value differs",
			input:  "env!=prod",
			labels: map[string]string{"env": "dev"},
			want:   true,
		},
		{
			name:   "inequality matches when key absent",
			input:  "env!=prod",
			labels: map[string]string{"tier": "web"},
			want:   true,
		},
		{
			name:   "inequality fails when value equals",
			input:  "env!=prod",
			labels: map[string]string{"env": "prod"},
			want:   false,
		},
		{
			name:   "existence matches when key present (any value)",
			input:  "env",
			labels: map[string]string{"env": "dev"},
			want:   true,
		},
		{
			name:   "existence matches when value is empty string",
			input:  "env",
			labels: map[string]string{"env": ""},
			want:   true,
		},
		{
			name:   "existence fails when key absent",
			input:  "env",
			labels: map[string]string{"tier": "web"},
			want:   false,
		},
		{
			name:   "absence matches when key absent",
			input:  "!debug",
			labels: map[string]string{"env": "prod"},
			want:   true,
		},
		{
			name:   "absence matches against nil labels",
			input:  "!debug",
			labels: nil,
			want:   true,
		},
		{
			name:   "absence fails when key present",
			input:  "!debug",
			labels: map[string]string{"debug": "yes"},
			want:   false,
		},
		{
			name:   "AND-combination both clauses match",
			input:  "env=prod,tier=web",
			labels: map[string]string{"env": "prod", "tier": "web"},
			want:   true,
		},
		{
			name:   "AND-combination second clause fails",
			input:  "env=prod,tier=web",
			labels: map[string]string{"env": "prod", "tier": "db"},
			want:   false,
		},
		{
			name:   "AND-combination existence + inequality",
			input:  "env,tier!=db",
			labels: map[string]string{"env": "prod", "tier": "web"},
			want:   true,
		},
		{
			name:   "whitespace around clauses tolerated",
			input:  "  env = prod ,  tier != db  ",
			labels: map[string]string{"env": "prod", "tier": "web"},
			want:   true,
		},
		{
			name:    "malformed: empty key before =",
			input:   "=value",
			wantErr: true,
		},
		{
			name:    "malformed: empty value after =",
			input:   "key=",
			wantErr: true,
		},
		{
			name:    "malformed: empty value after !=",
			input:   "key!=",
			wantErr: true,
		},
		{
			name:    "malformed: bang only",
			input:   "!",
			wantErr: true,
		},
		{
			name:    "malformed: trailing comma",
			input:   "env=prod,",
			wantErr: true,
		},
		{
			name:    "malformed: leading comma",
			input:   ",env=prod",
			wantErr: true,
		},
		{
			name:    "malformed: double comma",
			input:   "env=prod,,tier=web",
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sel, err := shared.ParseLabelSelector(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseLabelSelector(%q): expected error, got nil", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseLabelSelector(%q): unexpected error: %v", tc.input, err)
			}
			if got := sel.Matches(tc.labels); got != tc.want {
				t.Fatalf("Matches(%v) for %q = %v, want %v", tc.labels, tc.input, got, tc.want)
			}
		})
	}
}

func TestLabelSelector_Empty(t *testing.T) {
	sel, err := shared.ParseLabelSelector("")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if !sel.Empty() {
		t.Errorf("empty selector should report Empty() == true")
	}
	sel2, err := shared.ParseLabelSelector("env=prod")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if sel2.Empty() {
		t.Errorf("non-empty selector should report Empty() == false")
	}
	var nilSel *shared.LabelSelector
	if !nilSel.Empty() {
		t.Errorf("nil selector should report Empty() == true")
	}
	if !nilSel.Matches(map[string]string{"any": "thing"}) {
		t.Errorf("nil selector should match every label set")
	}
}

func TestParseLabelSelector_ErrorMessage(t *testing.T) {
	_, err := shared.ParseLabelSelector("env=prod,,tier=web")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "empty clause") {
		t.Errorf("error message should mention 'empty clause'; got %v", err)
	}
}
