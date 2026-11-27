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

// uniqueSamples and maxUniqueDuplicates bound TestGenerateCellName_Unique.
// The suffix has only 2^24 values (DefaultCellNameSuffixBytes = 3), so a
// correct generator hits about 256·255/2 / 2^24 ≈ 0.002 colliding pairs per
// 256 draws: one duplicate happens in ~0.19% of runs. Tolerating up to two
// keeps the false-failure rate near 1e-9, while a constant or low-entropy
// generator still produces far more duplicates than that.
const (
	uniqueSamples       = 256
	maxUniqueDuplicates = 2
)

// countDuplicates draws samples names from gen and returns how many repeated
// an earlier draw.
func countDuplicates(t *testing.T, gen func() (string, error), samples int) int {
	t.Helper()
	seen := make(map[string]struct{}, samples)
	dups := 0
	for i := 0; i < samples; i++ {
		got, err := gen()
		if err != nil {
			t.Fatalf("generate name: %v", err)
		}
		if _, dup := seen[got]; dup {
			dups++
		}
		seen[got] = struct{}{}
	}
	return dups
}

func TestGenerateCellName_Unique(t *testing.T) {
	gen := func() (string, error) { return naming.GenerateCellName("c") }
	if dups := countDuplicates(t, gen, uniqueSamples); dups > maxUniqueDuplicates {
		t.Fatalf("GenerateCellName produced %d duplicates within %d samples, want at most %d",
			dups, uniqueSamples, maxUniqueDuplicates)
	}
}

// TestGenerateCellName_UniqueCatchesWeakGenerators pins that the tolerance in
// TestGenerateCellName_Unique still rejects broken generators.
func TestGenerateCellName_UniqueCatchesWeakGenerators(t *testing.T) {
	constant := func() (string, error) { return "c-000000", nil }
	if dups := countDuplicates(t, constant, uniqueSamples); dups <= maxUniqueDuplicates {
		t.Errorf("constant generator: %d duplicates, want more than %d", dups, maxUniqueDuplicates)
	}

	// One byte of entropy: 256 draws from 256 values repeat ~94 times.
	oneByte := func() (string, error) {
		s, err := naming.RandomHexSuffix(1)
		return "c-" + s, err
	}
	if dups := countDuplicates(t, oneByte, uniqueSamples); dups <= maxUniqueDuplicates {
		t.Errorf("1-byte generator: %d duplicates, want more than %d", dups, maxUniqueDuplicates)
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
