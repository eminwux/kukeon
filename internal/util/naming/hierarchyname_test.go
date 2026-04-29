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

package naming_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/util/naming"
)

func TestValidateHierarchyName(t *testing.T) {
	kinds := []string{"space", "stack", "cell", "container"}

	tests := []struct {
		name      string
		input     string
		wantErr   error
		wantValid bool
	}{
		{name: "alphanumeric legal", input: "alpha-1", wantValid: true},
		{name: "single char legal", input: "a", wantValid: true},
		{name: "dashes legal", input: "team-alpha-1", wantValid: true},
		{name: "empty rejected", input: "", wantErr: errdefs.ErrInvalidName},
		{name: "whitespace-only rejected", input: "   ", wantErr: errdefs.ErrInvalidName},
		{
			name:    "underscore rejected (containerd ID parser collision)",
			input:   "team_alpha",
			wantErr: errdefs.ErrInvalidName,
		},
		{
			name:    "underscore in middle rejected",
			input:   "a_b",
			wantErr: errdefs.ErrInvalidName,
		},
		{
			name:    "underscore alone rejected",
			input:   "_",
			wantErr: errdefs.ErrInvalidName,
		},
		{
			name:    "slash rejected (cgroup path injection)",
			input:   "team/alpha",
			wantErr: errdefs.ErrInvalidName,
		},
		{
			name:    "leading slash rejected",
			input:   "/team",
			wantErr: errdefs.ErrInvalidName,
		},
		{
			name:    "slash alone rejected",
			input:   "/",
			wantErr: errdefs.ErrInvalidName,
		},
	}

	for _, kind := range kinds {
		for _, tt := range tests {
			t.Run(kind+"/"+tt.name, func(t *testing.T) {
				err := naming.ValidateHierarchyName(kind, tt.input)
				if tt.wantValid {
					if err != nil {
						t.Errorf("ValidateHierarchyName(%q, %q) = %v, want nil", kind, tt.input, err)
					}
					return
				}
				if err == nil {
					t.Fatalf("ValidateHierarchyName(%q, %q) = nil, want error %v", kind, tt.input, tt.wantErr)
				}
				if !errors.Is(err, tt.wantErr) {
					t.Errorf(
						"ValidateHierarchyName(%q, %q) = %v, want errors.Is(_, %v)",
						kind,
						tt.input,
						err,
						tt.wantErr,
					)
				}
				// Error message must name the kind so the operator knows which
				// input was rejected.
				if !strings.Contains(err.Error(), kind) {
					t.Errorf("ValidateHierarchyName(%q, %q) = %q, want error containing kind %q",
						kind, tt.input, err.Error(), kind)
				}
			})
		}
	}
}

func TestValidateHierarchyName_RejectsEmptyKind(t *testing.T) {
	if err := naming.ValidateHierarchyName("", "foo"); err == nil {
		t.Fatal("ValidateHierarchyName(\"\", \"foo\") = nil, want error")
	}
	if err := naming.ValidateHierarchyName("   ", "foo"); err == nil {
		t.Fatal("ValidateHierarchyName(\"   \", \"foo\") = nil, want error")
	}
}
