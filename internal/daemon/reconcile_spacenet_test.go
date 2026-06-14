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
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/firewall"
)

// TestServer_SpaceNetworkReconcileLoopFires is #1074 AC #1/#4: the daemon
// ticker drives a Space network reconcile pass — re-asserting FORWARD
// admission and the per-space CNI/egress state — on every tick, sibling to
// the cell pass. Continuous enforcement is what re-creates a removed
// KUKEON-EGRESS chain on the next tick.
func TestServer_SpaceNetworkReconcileLoopFires(t *testing.T) {
	srv := newTestServer(t, 20*time.Millisecond)
	var fwdCalls, spaceCalls atomic.Int32
	srv.reconcileFn = func() (controller.ReconcileResult, error) {
		return controller.ReconcileResult{}, nil
	}
	srv.forwardAdmissionFn = func() error {
		fwdCalls.Add(1)
		return nil
	}
	srv.spaceNetReconcileFn = func() (controller.SpaceNetReconcileResult, error) {
		spaceCalls.Add(1)
		return controller.SpaceNetReconcileResult{SpacesScanned: 2}, nil
	}

	startServer(t, srv)
	waitForCalls(t, &spaceCalls, 2, 2*time.Second)
	// FORWARD admission is re-asserted in the same pass, so it tracks the
	// space pass tick-for-tick.
	if got := fwdCalls.Load(); got < 2 {
		t.Errorf("forwardAdmissionFn calls: got %d, want >=2", got)
	}
}

// TestServer_SpaceNetworkReconcileRunsOnStartupBeforeLoop is #1074 AC #3: the
// pass is invoked once on Serve startup before the loop begins. A large
// reconcile interval is used so a call landing inside the short deadline can
// only be the eager startup pass, not a ticker tick — this is the reboot-heal
// path (#1074) that re-installs the wiped chains immediately.
func TestServer_SpaceNetworkReconcileRunsOnStartupBeforeLoop(t *testing.T) {
	srv := newTestServer(t, 10*time.Second)
	var fwdCalls, spaceCalls atomic.Int32
	srv.reconcileFn = func() (controller.ReconcileResult, error) {
		return controller.ReconcileResult{}, nil
	}
	srv.forwardAdmissionFn = func() error {
		fwdCalls.Add(1)
		return nil
	}
	srv.spaceNetReconcileFn = func() (controller.SpaceNetReconcileResult, error) {
		spaceCalls.Add(1)
		return controller.SpaceNetReconcileResult{}, nil
	}

	startServer(t, srv)
	// Far shorter than the 10s interval: a call here can only be the startup
	// pass that runs before startReconcileLoop.
	waitForCalls(t, &spaceCalls, 1, 1*time.Second)
	if got := fwdCalls.Load(); got < 1 {
		t.Errorf("forwardAdmissionFn calls on startup: got %d, want >=1", got)
	}
}

// TestServer_SpaceNetworkReconcileContinuesOnError confirms a failing Space
// network pass (or a failing FORWARD re-assert) is logged and the loop keeps
// ticking — a transient iptables failure must never wedge the loop or take
// the daemon down (#1074).
func TestServer_SpaceNetworkReconcileContinuesOnError(t *testing.T) {
	srv := newTestServer(t, 20*time.Millisecond)
	var spaceCalls atomic.Int32
	srv.reconcileFn = func() (controller.ReconcileResult, error) {
		return controller.ReconcileResult{}, nil
	}
	srv.forwardAdmissionFn = func() error {
		return context.DeadlineExceeded
	}
	srv.spaceNetReconcileFn = func() (controller.SpaceNetReconcileResult, error) {
		spaceCalls.Add(1)
		return controller.SpaceNetReconcileResult{
			SpacesScanned: 1,
			SpacesErrored: 1,
			Errors:        []string{"synthetic egress failure"},
		}, nil
	}

	startServer(t, srv)
	waitForCalls(t, &spaceCalls, 3, 2*time.Second)
}

// recordingIptables is a firewall.CommandRunner that simulates a host whose
// KUKEON-FORWARD chain has been wiped: every existence probe (-L / -C) fails,
// forcing Install down its create path (-N / -A / -I). It records the
// mutating invocations so a test can assert the chain was actually
// re-installed.
type recordingIptables struct {
	mu   sync.Mutex
	args [][]string
}

func (r *recordingIptables) Run(_ context.Context, args ...string) ([]byte, error) {
	r.mu.Lock()
	r.args = append(r.args, append([]string(nil), args...))
	r.mu.Unlock()
	// Read-only probes report "absent" so Install always takes the create
	// path; mutating commands succeed.
	switch args[0] {
	case "-L", "-C", "-S":
		return nil, context.Canceled // any non-nil error == "not present"
	default:
		return nil, nil
	}
}

func (r *recordingIptables) ran(prefix ...string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, got := range r.args {
		if len(got) < len(prefix) {
			continue
		}
		match := true
		for i := range prefix {
			if got[i] != prefix[i] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// TestServer_SpaceNetworkReconcileReinstallsWipedForwardChain is #1074 AC #5:
// simulate a wiped FORWARD admission chain and assert the reconcile pass
// re-installs it. The pass drives a real firewall.Installer over a fake
// iptables runner whose existence probes all report "absent" (the
// post-reboot state); the test asserts the create path ran — the chain was
// declared (-N KUKEON-FORWARD) and the FORWARD jump re-inserted.
func TestServer_SpaceNetworkReconcileReinstallsWipedForwardChain(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "kukeond.sock")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	fake := &recordingIptables{}
	installer := firewall.NewInstallerWithRunner(logger, fake)

	srv := NewServer(context.Background(), logger, Options{
		SocketPath:        socketPath,
		SocketMode:        0o600,
		ReconcileInterval: 10 * time.Second,
	})
	srv.reconcileFn = func() (controller.ReconcileResult, error) {
		return controller.ReconcileResult{}, nil
	}
	srv.spaceNetReconcileFn = func() (controller.SpaceNetReconcileResult, error) {
		return controller.SpaceNetReconcileResult{}, nil
	}
	// Wire the FORWARD re-assert to the real Installer over the fake runner so
	// the startup pass exercises the genuine re-install code path.
	srv.forwardAdmissionFn = func() error {
		return installer.Install(context.Background())
	}

	startServer(t, srv)

	// The startup pass runs before the loop; poll briefly for it to land.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fake.ran("-N", firewall.ForwardChainName) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !fake.ran("-N", firewall.ForwardChainName) {
		t.Errorf("wiped FORWARD chain not re-created: expected `-N %s` invocation, got %v",
			firewall.ForwardChainName, fake.args)
	}
	if !fake.ran("-I", "FORWARD") {
		t.Errorf("FORWARD jump not re-inserted: expected `-I FORWARD ...` invocation, got %v", fake.args)
	}
	// The admission rules carry the kukeon-forward comment tag; confirm at
	// least one tagged rule was appended on the re-install.
	if !ranContains(fake, "kukeon-forward") {
		t.Errorf("admission rules not re-appended: no kukeon-forward-tagged rule in %v", fake.args)
	}
}

func ranContains(r *recordingIptables, needle string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, got := range r.args {
		if strings.Contains(strings.Join(got, " "), needle) {
			return true
		}
	}
	return false
}
