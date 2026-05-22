//go:build !integration

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

//nolint:testpackage // exercises *Exec lifecycle locking against the in-package ctr.Client fake
package runner

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// TestCellLockManager_SameKeySerializes asserts the keyed mutex admits at most
// one holder per key: 50 goroutines contend on the same key and the observed
// concurrent-holder count never exceeds one.
func TestCellLockManager_SameKeySerializes(t *testing.T) {
	m := newCellLockManager()

	var active, maxActive int32
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rel := m.lock("same")
			defer rel()
			n := atomic.AddInt32(&active, 1)
			for {
				old := atomic.LoadInt32(&maxActive)
				if n <= old || atomic.CompareAndSwapInt32(&maxActive, old, n) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			atomic.AddInt32(&active, -1)
		}()
	}
	wg.Wait()

	if maxActive != 1 {
		t.Fatalf("max concurrent holders of one key = %d, want 1 (issue #714 keyed mutex must be exclusive per key)", maxActive)
	}
}

// TestCellLockManager_DifferentKeysDoNotSerialize asserts that holding one key
// never blocks acquisition of a different key — the lock must be per-cell, not
// global (issue #714 acceptance criterion).
func TestCellLockManager_DifferentKeysDoNotSerialize(t *testing.T) {
	m := newCellLockManager()

	relA := m.lock("a")
	defer relA()

	done := make(chan struct{})
	go func() {
		relB := m.lock("b")
		relB()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("acquiring a second key blocked while a different key was held; the lock must not serialize unrelated cells (issue #714)")
	}
}

// TestCellLockManager_ReclaimsIdleEntries asserts the entry map does not grow
// unbounded: an entry is dropped once its last holder releases.
func TestCellLockManager_ReclaimsIdleEntries(t *testing.T) {
	m := newCellLockManager()

	rel := m.lock("k")
	rel()

	m.mu.Lock()
	n := len(m.locks)
	m.mu.Unlock()
	if n != 0 {
		t.Fatalf("locks map size = %d after the only holder released, want 0 (entries must be reclaimed)", n)
	}
}

// TestCellLockKey_DistinguishesIdentity asserts the key folds the full
// realm/space/stack/cell identity: any single differing component yields a
// distinct key, and identical identities collide.
func TestCellLockKey_DistinguishesIdentity(t *testing.T) {
	base := func() intmodel.Cell {
		return intmodel.Cell{
			Metadata: intmodel.CellMetadata{Name: "cell"},
			Spec:     intmodel.CellSpec{RealmName: "realm", SpaceName: "space", StackName: "stack"},
		}
	}

	if cellLockKey(base()) != cellLockKey(base()) {
		t.Fatal("identical cell identities must produce the same lock key")
	}

	mutations := map[string]func(*intmodel.Cell){
		"realm": func(c *intmodel.Cell) { c.Spec.RealmName = "other" },
		"space": func(c *intmodel.Cell) { c.Spec.SpaceName = "other" },
		"stack": func(c *intmodel.Cell) { c.Spec.StackName = "other" },
		"name":  func(c *intmodel.Cell) { c.Metadata.Name = "other" },
	}
	baseKey := cellLockKey(base())
	for field, mutate := range mutations {
		c := base()
		mutate(&c)
		if cellLockKey(c) == baseKey {
			t.Errorf("changing %s did not change the lock key; distinct cells must not collide", field)
		}
	}
}

// gateExistsContainer builds an existsContainerFn that signals each gate entry
// on entered (non-blocking) and then parks the caller until release is closed.
// Because the first ExistsContainer call in a given lifecycle op parks here, a
// single op contributes exactly one entered signal before blocking — so the
// number of signals received equals the number of ops that ran their
// containerd-touching span concurrently.
func gateExistsContainer(entered chan struct{}, release chan struct{}) func(string, string) (bool, error) {
	return func(string, string) (bool, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		return false, nil
	}
}

func oneContainerCell(realm, space, stack, name string) intmodel.Cell {
	return intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: name},
		Spec: intmodel.CellSpec{
			ID:        name,
			RealmName: realm,
			SpaceName: space,
			StackName: stack,
			Containers: []intmodel.ContainerSpec{
				{ID: "workload", ContainerdID: space + "_" + stack + "_" + name + "_workload", Root: false},
			},
		},
	}
}

// TestCellLifecycleLock_StartCellWaitsForReconcile drives a concurrent
// ReconcileCell + StartCell on the same cell and asserts they cannot interleave
// their containerd/CNI side-effect spans: while ReconcileCell holds the per-cell
// lock (parked in ExistsContainer), StartCell must block at lock acquisition and
// only proceed once ReconcileCell releases. This is the headline #714 guard —
// without the lock the two ops race teardown against the same root container ID
// and IPAM file.
func TestCellLifecycleLock_StartCellWaitsForReconcile(t *testing.T) {
	realm, space, stack, cellName := "default", "kukeon", "kukeon", "web"

	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	fake := &deleteCellFakeClient{existsContainerFn: gateExistsContainer(entered, release)}
	r := newDeleteCellTestExec(t, fake)
	seedDeleteCellRealm(t, r, realm)
	// The cell metadata is deliberately NOT seeded: ReconcileCell reaches the
	// gated ExistsContainer off the in-memory cell arg (it needs only the
	// realm), while StartCell fails fast at its early GetCell (a filesystem
	// read, not gated) the moment it acquires the lock. That makes "StartCell
	// returned" a clean signal that it got past lock acquisition — without the
	// lock it would return near-instantly rather than waiting on ReconcileCell.

	cell := oneContainerCell(realm, space, stack, cellName)

	// G1: ReconcileCell acquires the per-cell lock, then parks in the gated
	// ExistsContainer while still holding it.
	recDone := make(chan struct{})
	go func() {
		_, _, _ = r.ReconcileCell(cell)
		close(recDone)
	}()

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("ReconcileCell never reached the ExistsContainer gate; cannot exercise the lock")
	}

	// G2: StartCell on the same cell must block on the per-cell lock G1 holds.
	startDone := make(chan struct{})
	go func() {
		_, _ = r.StartCell(cell)
		close(startDone)
	}()

	select {
	case <-startDone:
		t.Fatal("StartCell returned while ReconcileCell held the per-cell lifecycle lock; same-cell lifecycle ops were not serialized (issue #714)")
	case <-time.After(300 * time.Millisecond):
		// Expected: StartCell is parked at lock acquisition.
	}

	close(release) // ReconcileCell finishes and drops the lock.

	select {
	case <-startDone:
	case <-time.After(3 * time.Second):
		t.Fatal("StartCell did not proceed after ReconcileCell released the per-cell lock; the lock was not handed off")
	}
	<-recDone
}

// TestCellLifecycleLock_DifferentCellsRunConcurrently is the per-cell-keying
// counterpart: two ReconcileCell ops on distinct cells must run their
// side-effect spans concurrently. Both reach the gate while neither has
// released, so two entry signals arrive — a global lock would admit only one.
func TestCellLifecycleLock_DifferentCellsRunConcurrently(t *testing.T) {
	realm, space, stack := "default", "kukeon", "kukeon"

	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	fake := &deleteCellFakeClient{existsContainerFn: gateExistsContainer(entered, release)}
	r := newDeleteCellTestExec(t, fake)
	seedDeleteCellRealm(t, r, realm)
	seedDeleteCellCell(t, r, realm, space, stack, "web-a")
	seedDeleteCellCell(t, r, realm, space, stack, "web-b")

	cellA := oneContainerCell(realm, space, stack, "web-a")
	cellB := oneContainerCell(realm, space, stack, "web-b")

	doneA := make(chan struct{})
	doneB := make(chan struct{})
	go func() { _, _, _ = r.ReconcileCell(cellA); close(doneA) }()
	go func() { _, _, _ = r.ReconcileCell(cellB); close(doneB) }()

	for range 2 {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("expected both ReconcileCell ops on distinct cells to enter their side-effect span concurrently; the per-cell lock must not serialize unrelated cells (issue #714)")
		}
	}

	close(release)
	<-doneA
	<-doneB
}
