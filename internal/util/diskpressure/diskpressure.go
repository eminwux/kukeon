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

// Package diskpressure samples filesystem usage for the volume backing a given
// path and rate-limits the warnings kukeond emits when a data volume crosses a
// high-water mark. It is intentionally observation-only: nothing in this
// package deletes, reaps, or mutates on-disk state. The daemon uses it to make
// disk pressure visible (reconcile-loop WARN) and to refuse to dig the hole
// deeper (a CreateCell guard) without ever removing operator data (issue
// #1035).
package diskpressure

import (
	"fmt"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// percentScale converts a [0,1] fraction to a percentage.
const percentScale = 100

// Usage is a point-in-time snapshot of a filesystem's capacity, derived from a
// single statfs(2) call against a path on that filesystem.
type Usage struct {
	// TotalBytes is the filesystem's total capacity (f_blocks * f_bsize).
	TotalBytes uint64
	// AvailBytes is the space available to unprivileged writers
	// (f_bavail * f_bsize) — the headroom that matters for "will the next
	// write succeed".
	AvailBytes uint64
	// UsedBytes is TotalBytes minus the free space (f_bfree * f_bsize).
	UsedBytes uint64
	// UsedPercent is the fraction of capacity consumed, expressed as used /
	// (used + available) * 100 — matching df(1)'s Use% so the number lines up
	// with what an operator sees. Reserved-for-root blocks (f_bfree - f_bavail)
	// are excluded from the denominator, so a volume an unprivileged process
	// can no longer write to reports 100% even though root has a sliver left.
	UsedPercent float64
}

// Sample runs statfs(2) on path and returns the backing filesystem's usage.
// The path need only exist; it resolves to whatever filesystem it lives on.
func Sample(path string) (Usage, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return Usage{}, fmt.Errorf("statfs %s: %w", path, err)
	}

	bsize := uint64(st.Bsize) //nolint:gosec // f_bsize is non-negative; uint64 is the arithmetic type
	total := st.Blocks * bsize
	avail := st.Bavail * bsize
	used := (st.Blocks - st.Bfree) * bsize

	var pct float64
	if denom := used + avail; denom > 0 {
		pct = float64(used) / float64(denom) * percentScale
	}

	return Usage{
		TotalBytes:  total,
		AvailBytes:  avail,
		UsedBytes:   used,
		UsedPercent: pct,
	}, nil
}

// Warner rate-limits warnings per key (e.g. per realm) so a daemon whose
// reconcile interval is short does not emit a WARN every tick while a volume
// stays over the high-water mark. ShouldWarn returns true at most once per
// window per key. The zero value is not usable; construct with NewWarner.
type Warner struct {
	mu     sync.Mutex
	last   map[string]time.Time
	window time.Duration
	nowFn  func() time.Time
}

// NewWarner returns a Warner that lets a given key fire at most once per
// window. A non-positive window disables rate-limiting (every ShouldWarn call
// returns true).
func NewWarner(window time.Duration) *Warner {
	return &Warner{
		last:   make(map[string]time.Time),
		window: window,
	}
}

// now returns the Warner's clock, defaulting to time.Now when no test hook is
// installed.
func (w *Warner) now() time.Time {
	if w.nowFn != nil {
		return w.nowFn()
	}
	return time.Now()
}

// ShouldWarn reports whether a warning for key should be emitted now, recording
// the decision so a subsequent call within the window returns false. Safe for
// concurrent use.
func (w *Warner) ShouldWarn(key string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.window <= 0 {
		return true
	}

	now := w.now()
	if last, ok := w.last[key]; ok && now.Sub(last) < w.window {
		return false
	}
	w.last[key] = now
	return true
}
