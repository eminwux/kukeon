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

package lifecycle_test

import (
	"context"
	"errors"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eminwux/kukeon/cmd/kuke/internal/lifecycle"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

// stubPingService mirrors the daemon's KukeonV1Service.Ping handler: a
// net/rpc receiver registered under kukeonv1.ServiceName whose Ping method
// returns OK. Served over the stdlib jsonrpc codec so PingKukeond's
// production client speaks the same wire protocol the daemon serves over,
// without standing up a full controller.
type stubPingService struct {
	calls atomic.Int32
}

func (s *stubPingService) Ping(_ *kukeonv1.PingArgs, reply *kukeonv1.PingReply) error {
	s.calls.Add(1)
	reply.OK = true
	reply.Version = "test"
	return nil
}

// servePingStub registers svc under kukeonv1.ServiceName on a unix socket
// inside a temp dir and serves connections with the stdlib jsonrpc server
// codec until the test ends. Returns the socket path the client should dial.
func servePingStub(t *testing.T, svc *stubPingService) string {
	t.Helper()
	srv := rpc.NewServer()
	if err := srv.RegisterName(kukeonv1.ServiceName, svc); err != nil {
		t.Fatalf("RegisterName: %v", err)
	}
	socketPath := filepath.Join(t.TempDir(), "kukeond.sock")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return // listener closed by cleanup
			}
			go srv.ServeCodec(jsonrpc.NewServerCodec(conn))
		}
	}()
	return socketPath
}

// serveAcceptAndClose returns a unix socket path whose listener accepts
// connections and immediately closes them. The raw dial in PingKukeond
// succeeds, but the subsequent JSON-RPC client.Ping call hits EOF — this
// pins the "dial succeeded, RPC handler not serving" branch the bring-up
// guard exists for.
func serveAcceptAndClose(t *testing.T) string {
	t.Helper()
	socketPath := filepath.Join(t.TempDir(), "kukeond.sock")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	return socketPath
}

// TestPingKukeond_Success confirms the production probe succeeds against a
// listener that registers the Ping method on the kukeonv1 service. This
// covers the "socket accepts AND handler answers" branch — the gate
// WaitForKukeondReady exists to wait for.
func TestPingKukeond_Success(t *testing.T) {
	stub := &stubPingService{}
	socketPath := servePingStub(t, stub)

	if err := lifecycle.PingKukeond(context.Background(), socketPath); err != nil {
		t.Fatalf("PingKukeond: %v", err)
	}
	if stub.calls.Load() == 0 {
		t.Fatal("expected the stub Ping handler to be called at least once")
	}
}

// TestPingKukeond_DialError confirms the dial gate surfaces with a "dial:"
// prefix when the socket file does not exist. The prefix is part of the
// contract callers (`kuke init`, `kuke daemon recreate`) rely on to
// disambiguate "the listener never bound" from "the handler isn't serving".
func TestPingKukeond_DialError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.sock")
	err := lifecycle.PingKukeond(context.Background(), missing)
	if err == nil {
		t.Fatal("PingKukeond returned nil against a missing socket")
	}
	if !strings.HasPrefix(err.Error(), "dial:") {
		t.Fatalf("want \"dial:\" prefix, got %q", err.Error())
	}
}

// TestPingKukeond_PingError confirms the ping gate surfaces with a "ping:"
// prefix when the socket accepts the dial but the JSON-RPC call fails.
// This is the exact window the WaitForKukeondReady guard exists for: the
// listener file appeared (so a raw dial succeeds) but the RPC handler is
// not installed yet, so the Ping call hits EOF / handler-missing.
func TestPingKukeond_PingError(t *testing.T) {
	socketPath := serveAcceptAndClose(t)
	err := lifecycle.PingKukeond(context.Background(), socketPath)
	if err == nil {
		t.Fatal("PingKukeond returned nil against an accept-and-close listener")
	}
	if !strings.HasPrefix(err.Error(), "ping:") {
		t.Fatalf("want \"ping:\" prefix, got %q", err.Error())
	}
}

// TestWaitForKukeondReady_Success confirms the poll returns nil promptly
// once the Ping handler answers. With a healthy server the first tick
// succeeds, so the call must return well inside the bootstrap budget.
func TestWaitForKukeondReady_Success(t *testing.T) {
	stub := &stubPingService{}
	socketPath := servePingStub(t, stub)

	start := time.Now()
	if err := lifecycle.WaitForKukeondReady(context.Background(), socketPath, lifecycle.KukeondReadyTimeout); err != nil {
		t.Fatalf("WaitForKukeondReady: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("first-tick success took %s; healthy daemon should answer immediately", elapsed)
	}
}

// TestWaitForKukeondReady_BecomesReady covers the realistic bring-up race:
// the listener appears partway through the polling window. The poll must
// observe the readiness and return nil without timing out — the whole
// reason WaitForKukeondReady exists rather than a one-shot Ping.
func TestWaitForKukeondReady_BecomesReady(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "kukeond.sock")

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Delay short enough to land inside the 2s test budget but long
		// enough that at least one Ping attempt fails first.
		time.Sleep(2 * lifecycle.KukeondReadyTick)

		srv := rpc.NewServer()
		if err := srv.RegisterName(kukeonv1.ServiceName, &stubPingService{}); err != nil {
			return
		}
		ln, err := net.Listen("unix", socketPath)
		if err != nil {
			return
		}
		t.Cleanup(func() { _ = ln.Close() })
		go func() {
			for {
				conn, acceptErr := ln.Accept()
				if acceptErr != nil {
					return
				}
				go srv.ServeCodec(jsonrpc.NewServerCodec(conn))
			}
		}()
	}()

	if err := lifecycle.WaitForKukeondReady(context.Background(), socketPath, 2*time.Second); err != nil {
		t.Fatalf("WaitForKukeondReady: %v", err)
	}
	wg.Wait()
}

// TestWaitForKukeondReady_Timeout confirms the bootstrap budget is honoured
// and the last attempt's error is preserved in the wrap. The "timed out
// after" prefix is the shape the verb-level error messages depend on.
func TestWaitForKukeondReady_Timeout(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.sock")

	start := time.Now()
	err := lifecycle.WaitForKukeondReady(context.Background(), missing, 100*time.Millisecond)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("WaitForKukeondReady returned nil against a missing socket")
	}
	if !strings.Contains(err.Error(), "timed out after") {
		t.Errorf("want \"timed out after\" in error, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "dial:") {
		t.Errorf("want wrapped last attempt error (\"dial:\"), got %q", err.Error())
	}
	// Loose upper bound — the poll sleeps KukeondReadyTick between attempts,
	// so a 100ms budget realistically resolves within a single tick window.
	if elapsed > lifecycle.KukeondReadyTimeout {
		t.Errorf("timeout path took %s; expected well under %s", elapsed, lifecycle.KukeondReadyTimeout)
	}
}

// TestWaitForKukeondReady_ContextCanceled confirms the poll honours a
// cancelled parent context — callers ctrl-C'ing `kuke init` must not be
// forced to wait out the full bootstrap budget.
func TestWaitForKukeondReady_ContextCanceled(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.sock")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := lifecycle.WaitForKukeondReady(ctx, missing, lifecycle.KukeondReadyTimeout)
	if err == nil {
		t.Fatal("WaitForKukeondReady returned nil against a cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want errors.Is(..., context.Canceled), got %v", err)
	}
}

// TestKukeondReadyConstants pins the two exported tunables. `kuke init` and
// `kuke daemon recreate` both import them, and the values were chosen to
// balance "answer fast on a healthy daemon" against "don't spin the CPU
// while the listener is binding" — drifting them silently would break that
// trade-off.
func TestKukeondReadyConstants(t *testing.T) {
	if lifecycle.KukeondReadyTimeout != 30*time.Second {
		t.Errorf("KukeondReadyTimeout = %s, want 30s", lifecycle.KukeondReadyTimeout)
	}
	if lifecycle.KukeondReadyTick != 200*time.Millisecond {
		t.Errorf("KukeondReadyTick = %s, want 200ms", lifecycle.KukeondReadyTick)
	}
}
