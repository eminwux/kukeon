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

package runner

import (
	"strings"
	"sync"

	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// cellLockManager hands out a per-cell mutex keyed by the cell's
// realm/space/stack/name identity. It serializes the containerd+CNI
// side-effect span of the lifecycle ops (Start/Stop/Kill/Delete/Recreate/
// Create/Purge/Reconcile) against each other on the same cell, closing the
// window where a gRPC handler and the reconcile-loop tick could run teardown
// concurrently against the same root container ID and IPAM file (issue #714).
//
// The metadata flock + generation-token CAS guard only the on-disk document,
// not the containerd/CNI mutations; this lock covers the mutation span. Keys
// are independent, so unrelated cells never serialize against one another.
type cellLockManager struct {
	mu    sync.Mutex
	locks map[string]*cellLockEntry
}

// cellLockEntry is the per-key mutex plus a reference count of goroutines
// currently holding or waiting on it. The entry is dropped from the map once
// the count returns to zero so the map cannot grow unbounded over the daemon's
// lifetime.
type cellLockEntry struct {
	mu      sync.Mutex
	waiters int
}

func newCellLockManager() *cellLockManager {
	return &cellLockManager{locks: make(map[string]*cellLockEntry)}
}

// lock acquires the per-key mutex and returns a release func. The release func
// unlocks the mutex and drops the entry when no other goroutine is waiting on
// it. Call the returned func exactly once (typically via defer).
func (m *cellLockManager) lock(key string) func() {
	m.mu.Lock()
	e := m.locks[key]
	if e == nil {
		e = &cellLockEntry{}
		m.locks[key] = e
	}
	e.waiters++
	m.mu.Unlock()

	e.mu.Lock()

	return func() {
		e.mu.Unlock()
		m.mu.Lock()
		e.waiters--
		if e.waiters == 0 {
			delete(m.locks, key)
		}
		m.mu.Unlock()
	}
}

// cellLockKey derives the per-cell lock key from the realm/space/stack/cell
// identity carried on every lifecycle request. The NUL separator cannot appear
// in a validated hierarchy name, so distinct identities never collide.
func cellLockKey(cell intmodel.Cell) string {
	return strings.Join([]string{
		strings.TrimSpace(cell.Spec.RealmName),
		strings.TrimSpace(cell.Spec.SpaceName),
		strings.TrimSpace(cell.Spec.StackName),
		strings.TrimSpace(cell.Metadata.Name),
	}, "\x00")
}

// lockCell acquires the per-cell lifecycle lock for the given cell and returns
// the release func. The manager is lazily initialized so *Exec values built
// directly in tests (rather than via NewRunner) participate in the same
// serialization without extra fixture wiring.
func (r *Exec) lockCell(cell intmodel.Cell) func() {
	r.cellLocksOnce.Do(func() {
		if r.cellLocks == nil {
			r.cellLocks = newCellLockManager()
		}
	})
	return r.cellLocks.lock(cellLockKey(cell))
}
