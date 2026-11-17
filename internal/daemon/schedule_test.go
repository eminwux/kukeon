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

package daemon

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/controller"
)

// schedulePasses records the (prev, now) pair of every scheduler pass.
type schedulePasses struct {
	mu    sync.Mutex
	calls atomic.Int32
	prevs []time.Time
	nows  []time.Time
}

func (p *schedulePasses) record(prev, now time.Time) {
	p.mu.Lock()
	p.prevs = append(p.prevs, prev)
	p.nows = append(p.nows, now)
	p.mu.Unlock()
	p.calls.Add(1)
}

func (p *schedulePasses) snapshot() ([]time.Time, []time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]time.Time(nil), p.prevs...), append([]time.Time(nil), p.nows...)
}

// TestServer_ScheduleLoopRunsWithReconcileDisabled is the headline AC of
// #1503's scheduler: it runs on its own timer, so an operator who turns the
// reconcile loop off with `--reconcile-interval 0` still gets scheduled
// Configs fired.
func TestServer_ScheduleLoopRunsWithReconcileDisabled(t *testing.T) {
	srv := newTestServer(t, 0)
	srv.scheduleInterval = 20 * time.Millisecond
	var reconciles atomic.Int32
	srv.reconcileFn = func() (controller.ReconcileResult, error) {
		reconciles.Add(1)
		return controller.ReconcileResult{}, nil
	}
	var passes schedulePasses
	srv.scheduleFn = func(prev, now time.Time) (controller.ScheduleResult, error) {
		passes.record(prev, now)
		return controller.ScheduleResult{Scheduled: 1}, nil
	}

	startServer(t, srv)
	waitForCalls(t, &passes.calls, 3, 5*time.Second)
	if got := reconciles.Load(); got != 0 {
		t.Errorf("reconcileFn calls with ReconcileInterval=0: got %d, want 0", got)
	}
}

// TestServer_SchedulePassesChainPrevToNow pins what the scheduler's missed-fire
// logic relies on: the first pass after a start has no previous pass (the zero
// time), and every later pass is handed the time of the one before it.
func TestServer_SchedulePassesChainPrevToNow(t *testing.T) {
	srv := newTestServer(t, 0)
	srv.scheduleInterval = 20 * time.Millisecond
	var passes schedulePasses
	srv.scheduleFn = func(prev, now time.Time) (controller.ScheduleResult, error) {
		passes.record(prev, now)
		return controller.ScheduleResult{}, nil
	}

	startServer(t, srv)
	waitForCalls(t, &passes.calls, 3, 5*time.Second)

	prevs, nows := passes.snapshot()
	if !prevs[0].IsZero() {
		t.Errorf("first pass prev = %s, want the zero time", prevs[0])
	}
	for i := 1; i < 3; i++ {
		if !prevs[i].Equal(nows[i-1]) {
			t.Errorf("pass %d prev = %s, want the previous pass's now %s", i, prevs[i], nows[i-1])
		}
		if !nows[i].After(nows[i-1]) {
			t.Errorf("pass %d now = %s is not after the previous pass's %s", i, nows[i], nows[i-1])
		}
	}
}

// TestServer_ScheduleRunsEagerlyOnStartup: the first pass runs at once rather
// than at the next minute boundary — it is the one that sees fires missed
// while the daemon was down. The default one-minute interval is kept, so a
// call inside the short deadline can only be the eager pass.
func TestServer_ScheduleRunsEagerlyOnStartup(t *testing.T) {
	srv := newTestServer(t, 0)
	var passes schedulePasses
	srv.scheduleFn = func(prev, now time.Time) (controller.ScheduleResult, error) {
		passes.record(prev, now)
		return controller.ScheduleResult{}, nil
	}

	startServer(t, srv)
	waitForCalls(t, &passes.calls, 1, 2*time.Second)
}

// TestServer_ScheduleLoopSurvivesErrorsAndPanics: nothing a pass does — an
// error, per-Config errors, or a panic — may wedge the loop or take the daemon
// down.
func TestServer_ScheduleLoopSurvivesErrorsAndPanics(t *testing.T) {
	srv := newTestServer(t, 0)
	srv.scheduleInterval = 20 * time.Millisecond
	var calls atomic.Int32
	srv.scheduleFn = func(_, _ time.Time) (controller.ScheduleResult, error) {
		switch calls.Add(1) {
		case 1:
			return controller.ScheduleResult{}, errors.New("synthetic failure")
		case 2:
			panic("synthetic panic")
		case 3:
			return controller.ScheduleResult{Failed: 1, Errors: []string{"config x: boom"}}, nil
		default:
			return controller.ScheduleResult{Fired: 1}, nil
		}
	}

	startServer(t, srv)
	waitForCalls(t, &calls, 5, 5*time.Second)
}

// TestServer_ScheduleLoopStopsOnStop: Stop ends the loop and waits for it, so
// no pass runs against a closed controller.
func TestServer_ScheduleLoopStopsOnStop(t *testing.T) {
	srv := newTestServer(t, 0)
	srv.scheduleInterval = 20 * time.Millisecond
	var calls atomic.Int32
	srv.scheduleFn = func(_, _ time.Time) (controller.ScheduleResult, error) {
		calls.Add(1)
		return controller.ScheduleResult{}, nil
	}

	startServer(t, srv)
	waitForCalls(t, &calls, 1, 5*time.Second)
	if err := srv.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	after := calls.Load()
	time.Sleep(150 * time.Millisecond)
	if got := calls.Load(); got != after {
		t.Errorf("scheduleFn ran %d more times after Stop", got-after)
	}
}

// TestUntilNextScheduleTick: the scheduler wakes just past the next whole
// minute, never before it.
func TestUntilNextScheduleTick(t *testing.T) {
	for _, tc := range []struct {
		now  string
		want time.Duration
	}{
		{"2026-11-17T08:22:19Z", 41*time.Second + scheduleTickSlack},
		{"2026-11-17T08:22:59.5Z", 500*time.Millisecond + scheduleTickSlack},
		// Exactly on the minute: this minute's pass has just run, wait a whole one.
		{"2026-11-17T08:23:00Z", time.Minute + scheduleTickSlack},
	} {
		now, err := time.Parse(time.RFC3339Nano, tc.now)
		if err != nil {
			t.Fatalf("parse %q: %v", tc.now, err)
		}
		if got := untilNextScheduleTick(now, time.Minute); got != tc.want {
			t.Errorf("untilNextScheduleTick(%s) = %s, want %s", tc.now, got, tc.want)
		}
	}
}
