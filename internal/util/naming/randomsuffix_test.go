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
	"regexp"
	"testing"

	"github.com/eminwux/kukeon/internal/util/naming"
)

func TestRandomHexSuffixLengthAndAlphabet(t *testing.T) {
	tests := []struct {
		name string
		n    int
	}{
		{name: "default cell-name width (3 bytes)", n: naming.DefaultCellNameSuffixBytes},
		{name: "one byte", n: 1},
		{name: "eight bytes", n: 8},
		{name: "zero bytes", n: 0},
	}

	re := regexp.MustCompile(`^[0-9a-f]*$`)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := naming.RandomHexSuffix(tt.n)
			if err != nil {
				t.Fatalf("RandomHexSuffix(%d) = _, %v, want nil err", tt.n, err)
			}
			if want := 2 * tt.n; len(got) != want {
				t.Errorf("RandomHexSuffix(%d) length = %d, want %d", tt.n, len(got), want)
			}
			if !re.MatchString(got) {
				t.Errorf("RandomHexSuffix(%d) = %q, want lowercase hex only", tt.n, got)
			}
		})
	}
}

func TestRandomHexSuffixUnique(t *testing.T) {
	const samples = 256
	seen := make(map[string]struct{}, samples)
	for i := range samples {
		s, err := naming.RandomHexSuffix(naming.DefaultCellNameSuffixBytes)
		if err != nil {
			t.Fatalf("RandomHexSuffix: %v", err)
		}
		if _, dup := seen[s]; dup {
			t.Fatalf("collision on iteration %d (%q); RNG should not repeat at this sample size", i, s)
		}
		seen[s] = struct{}{}
	}
}

func TestDefaultCellNameSuffixBytes(t *testing.T) {
	if naming.DefaultCellNameSuffixBytes != 3 {
		t.Errorf(
			"DefaultCellNameSuffixBytes = %d, want 3 (canonical width shared by -b/-p/-c -g)",
			naming.DefaultCellNameSuffixBytes,
		)
	}
}
