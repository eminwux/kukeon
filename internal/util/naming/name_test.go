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

package naming_test

import (
	"testing"

	"github.com/eminwux/kukeon/internal/util/naming"
	"github.com/eminwux/kukeon/pkg/team/config"
)

// TestIsValidName pins the shared rule (issue #1719) and that the team layer's
// config.ValidName is the same rule.
func TestIsValidName(t *testing.T) {
	valid := []string{
		"a", "0", "default", "kuke-system", "kukeon", "kukeond", "web-ui-admin",
		"a.b", "a..b", "a-b-c", "gh-token", "v1.2.3",
	}
	invalid := []string{
		"", ".", "..", ".x", "x.", "-x", "x-", "A", "aB", "a b", " a", "a ", `a\b`,
		"a_b", "_", "a/b", "/", "../x", "a\x00b", "a\tb", "a\nb", ".types", ".meta",
		".secret-x.tmp", "é",
	}
	for _, name := range valid {
		if !naming.IsValidName(name) {
			t.Errorf("IsValidName(%q) = false, want true", name)
		}
		if !config.ValidName(name) {
			t.Errorf("config.ValidName(%q) = false, want true", name)
		}
	}
	for _, name := range invalid {
		if naming.IsValidName(name) {
			t.Errorf("IsValidName(%q) = true, want false", name)
		}
		if config.ValidName(name) {
			t.Errorf("config.ValidName(%q) = true, want false", name)
		}
	}
}

func TestIsValidSegment(t *testing.T) {
	if !naming.IsValidSegment("") {
		t.Error(`IsValidSegment("") = false, want true (unset coordinate)`)
	}
	if !naming.IsValidSegment("alpha") {
		t.Error(`IsValidSegment("alpha") = false, want true`)
	}
	for _, seg := range []string{".", "..", "a/b", "A"} {
		if naming.IsValidSegment(seg) {
			t.Errorf("IsValidSegment(%q) = true, want false", seg)
		}
	}
}
