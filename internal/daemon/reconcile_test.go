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
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/controller"
)

// TestServer_ReconcileLoopFires confirms the daemon's background ticker
// invokes ReconcileCells repeatedly when ReconcileInterval > 0 — the
// minimum signal that #161 AC #1 is live (kukeond runs a background ticker
// that, on every tick, reconciles each cell).
func TestServer_ReconcileLoopFires(t *testing.T) {
	srv := newTestServer(t, 20*time.Millisecond)
	var calls atomic.Int32
	srv.reconcileFn = func() (controller.ReconcileResult, error) {
		calls.Add(1)
		return controller.ReconcileResult{CellsScanned: 3}, nil
	}

	startServer(t, srv)
	waitForCalls(t, &calls, 2, 2*time.Second)
}

// TestServer_ReconcileLoopContinuesOnError is AC #5: errors during a pass
// must be logged and the loop must keep ticking. A single failing pass
// cannot wedge the loop or take the daemon down.
func TestServer_ReconcileLoopContinuesOnError(t *testing.T) {
	srv := newTestServer(t, 20*time.Millisecond)
	var calls atomic.Int32
	srv.reconcileFn = func() (controller.ReconcileResult, error) {
		calls.Add(1)
		return controller.ReconcileResult{}, errors.New("synthetic failure")
	}

	startServer(t, srv)
	waitForCalls(t, &calls, 3, 2*time.Second)
}

// TestServer_ReconcileLoopDisabledWhenIntervalZero is the explicit opt-out
// path: a 0 (or negative) ReconcileInterval must keep the loop off so
// tests, embedded callers, and operators who set --reconcile-interval 0
// see no background activity.
func TestServer_ReconcileLoopDisabledWhenIntervalZero(t *testing.T) {
	srv := newTestServer(t, 0)
	var calls atomic.Int32
	srv.reconcileFn = func() (controller.ReconcileResult, error) {
		calls.Add(1)
		return controller.ReconcileResult{}, nil
	}

	startServer(t, srv)
	time.Sleep(150 * time.Millisecond)
	if got := calls.Load(); got != 0 {
		t.Errorf("reconcileFn calls with ReconcileInterval=0: got %d, want 0", got)
	}
}

// TestServer_ReconcileLoopStopsWithContext confirms cancellation of the
// daemon context terminates the loop — required to keep daemon shutdowns
// fast and to honor the no-leader-election single-instance invariant.
func TestServer_ReconcileLoopStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	srv := newTestServerWithCtx(t, ctx, 20*time.Millisecond)
	var calls atomic.Int32
	srv.reconcileFn = func() (controller.ReconcileResult, error) {
		calls.Add(1)
		return controller.ReconcileResult{}, nil
	}
	startServer(t, srv)
	waitForCalls(t, &calls, 1, 2*time.Second)

	cancel()
	preStop := calls.Load()
	time.Sleep(150 * time.Millisecond)
	postStop := calls.Load()
	// Allow up to one in-flight tick after cancel — we require the loop to
	// settle, not to be perfectly synchronous.
	if postStop-preStop > 1 {
		t.Errorf("loop kept ticking after ctx cancel: pre=%d post=%d", preStop, postStop)
	}
}

func newTestServer(t *testing.T, interval time.Duration) *Server {
	t.Helper()
	return newTestServerWithCtx(t, context.Background(), interval)
}

func newTestServerWithCtx(t *testing.T, ctx context.Context, interval time.Duration) *Server {
	t.Helper()
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "kukeond.sock")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewServer(ctx, logger, Options{
		SocketPath:        socketPath,
		SocketMode:        0o600,
		ReconcileInterval: interval,
	})
}

func startServer(t *testing.T, srv *Server) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- srv.Serve() }()
	t.Cleanup(func() {
		_ = srv.Stop()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("Serve did not return after Stop")
		}
	})
}

func waitForCalls(t *testing.T, counter *atomic.Int32, want int32, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if counter.Load() >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("reconcileFn calls: got %d, want >=%d within %s", counter.Load(), want, deadline)
}
