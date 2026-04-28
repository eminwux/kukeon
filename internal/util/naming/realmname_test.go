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
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/util/naming"
)

func TestValidateRealmName(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantErr   error
		wantValid bool
	}{
		{name: "default realm legal", input: "default", wantValid: true},
		{name: "kuke-system legal", input: "kuke-system", wantValid: true},
		{name: "alphanumeric and dash legal", input: "team-alpha-1", wantValid: true},
		{name: "empty rejected", input: "", wantErr: errdefs.ErrRealmNameRequired},
		{name: "whitespace-only rejected", input: "   ", wantErr: errdefs.ErrRealmNameRequired},
		{
			name:    "underscore rejected (containerd ID parser collision)",
			input:   "team_alpha",
			wantErr: errdefs.ErrInvalidRealmName,
		},
		{
			name:    "underscore in middle rejected",
			input:   "a_b",
			wantErr: errdefs.ErrInvalidRealmName,
		},
		{
			name:    "slash rejected (cgroup path injection)",
			input:   "team/alpha",
			wantErr: errdefs.ErrInvalidRealmName,
		},
		{
			name:    "leading slash rejected",
			input:   "/team",
			wantErr: errdefs.ErrInvalidRealmName,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := naming.ValidateRealmName(tt.input)
			if tt.wantValid {
				if err != nil {
					t.Errorf("ValidateRealmName(%q) = %v, want nil", tt.input, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateRealmName(%q) = nil, want error %v", tt.input, tt.wantErr)
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("ValidateRealmName(%q) = %v, want errors.Is(_, %v)", tt.input, err, tt.wantErr)
			}
		})
	}
}
