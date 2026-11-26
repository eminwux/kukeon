// Copyright 2026 Emiliano Spinella (eminwux)
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

package v1beta1_test

import (
	"errors"
	"strings"
	"testing"

	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func TestNormalizeDisplayName(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"  Reviewer  ", "Reviewer", true},
		{"", "", true},
		{"   ", "", true},
		{"Docs bot", "Docs bot", true},
		{"Équipe ✓", "Équipe ✓", true},
		{strings.Repeat("a", v1beta1.MaxDisplayNameLength), strings.Repeat("a", v1beta1.MaxDisplayNameLength), true},
		{strings.Repeat("é", v1beta1.MaxDisplayNameLength), strings.Repeat("é", v1beta1.MaxDisplayNameLength), true},
		{strings.Repeat("a", v1beta1.MaxDisplayNameLength+1), "", false},
		{"two\nlines", "", false},
		{"tab\there", "", false},
		{"bad \xff byte", "", false},
	}
	for _, tc := range cases {
		got, err := v1beta1.NormalizeDisplayName(tc.in)
		if tc.ok {
			if err != nil || got != tc.want {
				t.Errorf("NormalizeDisplayName(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
			}
			continue
		}
		if !errors.Is(err, v1beta1.ErrInvalidDisplayName) {
			t.Errorf("NormalizeDisplayName(%q) error = %v; want ErrInvalidDisplayName", tc.in, err)
		}
	}
}
