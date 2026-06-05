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
	"regexp"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/util/naming"
)

var generatedShape = regexp.MustCompile(`^(.+)-[0-9a-f]{6}$`)

func TestGenerateCellName_Shape(t *testing.T) {
	got, err := naming.GenerateCellName("prod")
	if err != nil {
		t.Fatalf("GenerateCellName: %v", err)
	}
	if !generatedShape.MatchString(got) {
		t.Errorf("GenerateCellName(%q) = %q, want <prefix>-<6hex>", "prod", got)
	}
	if !strings.HasPrefix(got, "prod-") {
		t.Errorf("GenerateCellName(%q) = %q, want prod- prefix", "prod", got)
	}
}

func TestGenerateCellName_TrimsPrefix(t *testing.T) {
	got, err := naming.GenerateCellName("  prod  ")
	if err != nil {
		t.Fatalf("GenerateCellName: %v", err)
	}
	if !strings.HasPrefix(got, "prod-") {
		t.Errorf("GenerateCellName(%q) = %q, want trimmed prod- prefix", "  prod  ", got)
	}
}

func TestGenerateCellName_Unique(t *testing.T) {
	const samples = 256
	seen := make(map[string]struct{}, samples)
	for i := 0; i < samples; i++ {
		got, err := naming.GenerateCellName("c")
		if err != nil {
			t.Fatalf("GenerateCellName: %v", err)
		}
		if _, dup := seen[got]; dup {
			t.Fatalf("GenerateCellName produced duplicate %q within %d samples", got, samples)
		}
		seen[got] = struct{}{}
	}
}

func TestAllocCellName_ExplicitVerbatim(t *testing.T) {
	calls := 0
	exists := func(string) (bool, error) { calls++; return false, nil }
	got, err := naming.AllocCellName("  pinned  ", "prod", exists)
	if err != nil {
		t.Fatalf("AllocCellName: %v", err)
	}
	if got != "pinned" {
		t.Errorf("AllocCellName explicit = %q, want trimmed verbatim %q", got, "pinned")
	}
	if calls != 0 {
		t.Errorf("AllocCellName probed exists %d times for an explicit name, want 0", calls)
	}
}

func TestAllocCellName_GeneratedNoCollision(t *testing.T) {
	got, err := naming.AllocCellName("", "prod", func(string) (bool, error) { return false, nil })
	if err != nil {
		t.Fatalf("AllocCellName: %v", err)
	}
	if !generatedShape.MatchString(got) || !strings.HasPrefix(got, "prod-") {
		t.Errorf("AllocCellName generated = %q, want prod-<6hex>", got)
	}
}

func TestAllocCellName_NilExistsSingleShot(t *testing.T) {
	got, err := naming.AllocCellName("", "prod", nil)
	if err != nil {
		t.Fatalf("AllocCellName: %v", err)
	}
	if !strings.HasPrefix(got, "prod-") {
		t.Errorf("AllocCellName(nil exists) = %q, want prod-<6hex>", got)
	}
}

func TestAllocCellName_RetriesPastCollisions(t *testing.T) {
	// First two candidates are "taken", third is free; AllocCellName must
	// return the third without surfacing an error.
	n := 0
	exists := func(string) (bool, error) {
		n++
		return n <= 2, nil
	}
	got, err := naming.AllocCellName("", "prod", exists)
	if err != nil {
		t.Fatalf("AllocCellName: %v", err)
	}
	if !strings.HasPrefix(got, "prod-") {
		t.Errorf("AllocCellName after retries = %q, want prod-<6hex>", got)
	}
	if n != 3 {
		t.Errorf("AllocCellName probed %d times, want 3 (two collisions then free)", n)
	}
}

func TestAllocCellName_ExhaustionErrors(t *testing.T) {
	exists := func(string) (bool, error) { return true, nil } // always taken
	_, err := naming.AllocCellName("", "prod", exists)
	if err == nil {
		t.Fatal("AllocCellName: want error on persistent collision, got nil")
	}
	if !strings.Contains(err.Error(), "persistent suffix collision") {
		t.Errorf("AllocCellName exhaustion error = %q, want it to mention persistent suffix collision", err)
	}
}

func TestAllocCellName_PropagatesExistsError(t *testing.T) {
	sentinel := errors.New("daemon unreachable")
	_, err := naming.AllocCellName("", "prod", func(string) (bool, error) { return false, sentinel })
	if !errors.Is(err, sentinel) {
		t.Errorf("AllocCellName error = %v, want it to wrap %v", err, sentinel)
	}
}
