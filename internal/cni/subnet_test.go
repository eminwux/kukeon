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

package cni_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	cni "github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/errdefs"
)

func TestSubnetAllocator_AllocateAssignsLowestFree(t *testing.T) {
	runPath := t.TempDir()
	a, err := cni.NewSubnetAllocator(runPath, "10.88.0.0/16", 24)
	if err != nil {
		t.Fatalf("NewSubnetAllocator: %v", err)
	}

	first, err := a.Allocate("default", "alpha")
	if err != nil {
		t.Fatalf("Allocate alpha: %v", err)
	}
	if first != "10.88.0.0/24" {
		t.Errorf("first allocation = %q, want 10.88.0.0/24", first)
	}

	second, err := a.Allocate("default", "beta")
	if err != nil {
		t.Fatalf("Allocate beta: %v", err)
	}
	if second != "10.88.1.0/24" {
		t.Errorf("second allocation = %q, want 10.88.1.0/24", second)
	}

	if first == second {
		t.Errorf("two spaces collided on the same subnet %q", first)
	}
}

func TestSubnetAllocator_AllocateIsIdempotentForSameSpace(t *testing.T) {
	runPath := t.TempDir()
	a, err := cni.NewSubnetAllocator(runPath, "10.88.0.0/16", 24)
	if err != nil {
		t.Fatalf("NewSubnetAllocator: %v", err)
	}

	first, err := a.Allocate("default", "alpha")
	if err != nil {
		t.Fatalf("Allocate first: %v", err)
	}
	again, err := a.Allocate("default", "alpha")
	if err != nil {
		t.Fatalf("Allocate again: %v", err)
	}
	if first != again {
		t.Errorf("repeat Allocate returned %q, want %q (must reuse persisted subnet)", again, first)
	}
}

func TestSubnetAllocator_AllocatePersistsToDisk(t *testing.T) {
	runPath := t.TempDir()
	a, err := cni.NewSubnetAllocator(runPath, "10.88.0.0/16", 24)
	if err != nil {
		t.Fatalf("NewSubnetAllocator: %v", err)
	}

	subnet, err := a.Allocate("kuke-system", "kukeon")
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}

	statePath := filepath.Join(runPath, "kuke-system", "kukeon", cni.SubnetStateFileName)
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}
	var st cni.SubnetState
	if uErr := json.Unmarshal(data, &st); uErr != nil {
		t.Fatalf("unmarshal state: %v", uErr)
	}
	if st.Version != cni.SubnetStateVersion {
		t.Errorf("state.Version = %q, want %q", st.Version, cni.SubnetStateVersion)
	}
	if st.SubnetCIDR != subnet {
		t.Errorf("state.SubnetCIDR = %q, want %q", st.SubnetCIDR, subnet)
	}
}

func TestSubnetAllocator_LoadAssignedReturnsEmptyWhenMissing(t *testing.T) {
	runPath := t.TempDir()
	a, err := cni.NewSubnetAllocator(runPath, "10.88.0.0/16", 24)
	if err != nil {
		t.Fatalf("NewSubnetAllocator: %v", err)
	}

	got, err := a.LoadAssigned("default", "absent")
	if err != nil {
		t.Fatalf("LoadAssigned: %v", err)
	}
	if got != "" {
		t.Errorf("LoadAssigned for absent space = %q, want \"\"", got)
	}
}

func TestSubnetAllocator_LoadAssignedReturnsCorruptError(t *testing.T) {
	runPath := t.TempDir()
	statePath := filepath.Join(runPath, "default", "broken", cni.SubnetStateFileName)
	if err := os.MkdirAll(filepath.Dir(statePath), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(statePath, []byte("not-json"), 0o600); err != nil {
		t.Fatalf("write garbage: %v", err)
	}

	a, err := cni.NewSubnetAllocator(runPath, "10.88.0.0/16", 24)
	if err != nil {
		t.Fatalf("NewSubnetAllocator: %v", err)
	}
	if _, lerr := a.LoadAssigned("default", "broken"); !errors.Is(lerr, errdefs.ErrSubnetStateCorrupt) {
		t.Errorf("LoadAssigned on garbage = %v, want ErrSubnetStateCorrupt", lerr)
	}
}

func TestSubnetAllocator_ReleaseFreesSubnetForReuse(t *testing.T) {
	runPath := t.TempDir()
	a, err := cni.NewSubnetAllocator(runPath, "10.88.0.0/16", 24)
	if err != nil {
		t.Fatalf("NewSubnetAllocator: %v", err)
	}

	first, err := a.Allocate("default", "alpha")
	if err != nil {
		t.Fatalf("Allocate alpha: %v", err)
	}
	if relErr := a.Release("default", "alpha"); relErr != nil {
		t.Fatalf("Release alpha: %v", relErr)
	}
	statePath := filepath.Join(runPath, "default", "alpha", cni.SubnetStateFileName)
	if _, statErr := os.Stat(statePath); !os.IsNotExist(statErr) {
		t.Errorf("Release left state file behind: stat err = %v", statErr)
	}

	again, err := a.Allocate("default", "gamma")
	if err != nil {
		t.Fatalf("Allocate gamma: %v", err)
	}
	if again != first {
		t.Errorf(
			"second allocator pass picked %q, want lowest free %q (released subnet should be reusable)",
			again, first,
		)
	}
}

func TestSubnetAllocator_ReleaseIsIdempotent(t *testing.T) {
	runPath := t.TempDir()
	a, err := cni.NewSubnetAllocator(runPath, "10.88.0.0/16", 24)
	if err != nil {
		t.Fatalf("NewSubnetAllocator: %v", err)
	}

	if relErr := a.Release("default", "never-allocated"); relErr != nil {
		t.Errorf("Release on never-allocated = %v, want nil", relErr)
	}
	if relErr := a.Release("", ""); relErr != nil {
		t.Errorf("Release with empty names = %v, want nil", relErr)
	}
}

// TestSubnetAllocator_AllocateSkipsCorruptStateFiles is the regression guard
// for the fail-fast DoS surface flagged on PR #134 review: a single corrupt
// network.json under one space must not block allocation in any other space.
// The reviewer's concern was that one bad file taking down host-wide space
// creates is far worse than continuing past it and warning the operator, so
// the scan is required to keep going.
func TestSubnetAllocator_AllocateSkipsCorruptStateFiles(t *testing.T) {
	runPath := t.TempDir()

	// Plant a garbage network.json under one realm/space pair.
	corruptPath := filepath.Join(runPath, "default", "broken", cni.SubnetStateFileName)
	if err := os.MkdirAll(filepath.Dir(corruptPath), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(corruptPath, []byte("not-json"), 0o600); err != nil {
		t.Fatalf("write garbage: %v", err)
	}

	a, err := cni.NewSubnetAllocator(runPath, "10.88.0.0/16", 24)
	if err != nil {
		t.Fatalf("NewSubnetAllocator: %v", err)
	}

	// Allocating a different space must succeed despite the corrupt sibling.
	got, err := a.Allocate("default", "alpha")
	if err != nil {
		t.Fatalf("Allocate alpha skipped past corrupt sibling but failed: %v", err)
	}
	// The corrupt file contributes no entries to the used set, so the
	// allocator hands out the lowest /24 in the parent.
	if got != "10.88.0.0/24" {
		t.Errorf("Allocate = %q, want 10.88.0.0/24 (lowest free; corrupt sibling must not occupy)", got)
	}

	// Direct LoadAssigned on the corrupt space still surfaces the error so
	// callers that need the specific entry can see it. The skip-and-continue
	// behaviour is scoped to scan-time, not point lookups.
	if _, lerr := a.LoadAssigned("default", "broken"); !errors.Is(lerr, errdefs.ErrSubnetStateCorrupt) {
		t.Errorf("LoadAssigned on corrupt space = %v, want ErrSubnetStateCorrupt", lerr)
	}
}

func TestSubnetAllocator_AllocateScansAllRealms(t *testing.T) {
	runPath := t.TempDir()
	// Pre-seed a network.json under a different realm to prove the scanner
	// is global, not per-realm.
	preSeeded := filepath.Join(runPath, "kuke-system", "kukeon", cni.SubnetStateFileName)
	if err := os.MkdirAll(filepath.Dir(preSeeded), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	seed, err := json.Marshal(cni.SubnetState{Version: cni.SubnetStateVersion, SubnetCIDR: "10.88.0.0/24"})
	if err != nil {
		t.Fatalf("marshal seed: %v", err)
	}
	if writeErr := os.WriteFile(preSeeded, seed, 0o600); writeErr != nil {
		t.Fatalf("write seed: %v", writeErr)
	}

	a, err := cni.NewSubnetAllocator(runPath, "10.88.0.0/16", 24)
	if err != nil {
		t.Fatalf("NewSubnetAllocator: %v", err)
	}
	got, err := a.Allocate("default", "alpha")
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if got == "10.88.0.0/24" {
		t.Errorf("Allocate handed out %q which is already in use under kuke-system/kukeon", got)
	}
	if got != "10.88.1.0/24" {
		t.Errorf("Allocate = %q, want 10.88.1.0/24 (lowest free after pre-seeded /24)", got)
	}
}

func TestSubnetAllocator_AllocateReturnsExhaustedWhenFull(t *testing.T) {
	runPath := t.TempDir()
	// Use a tiny parent (/30 with /32 leaves room for 4 subnets) to make
	// exhaustion observable without writing 65k files.
	a, err := cni.NewSubnetAllocator(runPath, "10.0.0.0/30", 32)
	if err != nil {
		t.Fatalf("NewSubnetAllocator: %v", err)
	}

	for i := range 4 {
		spaceName := "s" + string(rune('a'+i))
		if _, allocErr := a.Allocate("default", spaceName); allocErr != nil {
			t.Fatalf("Allocate %s: %v", spaceName, allocErr)
		}
	}
	if _, allocErr := a.Allocate("default", "overflow"); !errors.Is(allocErr, errdefs.ErrSubnetExhausted) {
		t.Errorf("expected ErrSubnetExhausted, got %v", allocErr)
	}
}

func TestNewSubnetAllocator_RejectsBadInput(t *testing.T) {
	cases := []struct {
		name      string
		parent    string
		prefixLen int
	}{
		{name: "garbage parent", parent: "not-a-cidr", prefixLen: 24},
		{name: "ipv6 parent", parent: "fd00::/64", prefixLen: 96},
		{name: "prefix shorter than parent", parent: "10.88.0.0/16", prefixLen: 8},
		{name: "prefix equal to parent", parent: "10.88.0.0/16", prefixLen: 16},
		{name: "prefix beyond /32", parent: "10.88.0.0/16", prefixLen: 33},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := cni.NewSubnetAllocator(t.TempDir(), tc.parent, tc.prefixLen); !errors.Is(
				err,
				errdefs.ErrInvalidSubnetCIDR,
			) {
				t.Errorf("expected ErrInvalidSubnetCIDR, got %v", err)
			}
		})
	}
}

func TestNewDefaultSubnetAllocator_UsesDefault10880016(t *testing.T) {
	a := cni.NewDefaultSubnetAllocator(t.TempDir())
	if got := a.ParentCIDR(); got != "10.88.0.0/16" {
		t.Errorf("default ParentCIDR = %q, want 10.88.0.0/16", got)
	}
	if got := a.PrefixLen(); got != 24 {
		t.Errorf("default PrefixLen = %d, want 24", got)
	}
}
