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
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/daemon/internal/lifecycle"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestIsCellRunning(t *testing.T) {
	cases := []struct {
		name string
		cell v1beta1.CellDoc
		want bool
	}{
		{
			name: "container ready means running even when cell state lags",
			cell: v1beta1.CellDoc{Status: v1beta1.CellStatus{
				State: v1beta1.CellStateStopped,
				Containers: []v1beta1.ContainerStatus{
					{State: v1beta1.ContainerStateReady},
				},
			}},
			want: true,
		},
		{
			name: "cell state ready means running even with no containers",
			cell: v1beta1.CellDoc{Status: v1beta1.CellStatus{State: v1beta1.CellStateReady}},
			want: true,
		},
		{
			name: "stopped cell with no containers is not running",
			cell: v1beta1.CellDoc{Status: v1beta1.CellStatus{State: v1beta1.CellStateStopped}},
			want: false,
		},
		{
			name: "stopped cell with stopped containers is not running",
			cell: v1beta1.CellDoc{Status: v1beta1.CellStatus{
				State: v1beta1.CellStateStopped,
				Containers: []v1beta1.ContainerStatus{
					{State: v1beta1.ContainerStateStopped},
				},
			}},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := lifecycle.IsCellRunning(tc.cell); got != tc.want {
				t.Fatalf("IsCellRunning: want %v, got %v", tc.want, got)
			}
		})
	}
}

func TestStopPhase_GracefulSuccess(t *testing.T) {
	fc := &fakeClient{
		stopCellFn: func(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.StopCellResult, error) {
			return kukeonv1.StopCellResult{Cell: doc, Stopped: true}, nil
		},
		killCellFn: func(_ context.Context, _ v1beta1.CellDoc) (kukeonv1.KillCellResult, error) {
			t.Fatal("KillCell must not run when StopCell succeeds within the grace period")
			return kukeonv1.KillCellResult{}, nil
		},
	}
	cmd := newCmdWithContext(context.Background(), t)
	if err := lifecycle.StopPhase(cmd, fc, v1beta1.CellDoc{}, time.Second); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStopPhase_GracefulNoChangeWrapsSentinel(t *testing.T) {
	fc := &fakeClient{
		stopCellFn: func(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.StopCellResult, error) {
			return kukeonv1.StopCellResult{Cell: doc, Stopped: false}, nil
		},
	}
	cmd := newCmdWithContext(context.Background(), t)
	err := lifecycle.StopPhase(cmd, fc, v1beta1.CellDoc{}, time.Second)
	if err == nil {
		t.Fatal("want error from StopPhase no-change path, got nil")
	}
	if !errors.Is(err, errdefs.ErrControllerNoChange) {
		t.Fatalf("want errors.Is(..., ErrControllerNoChange) true, got err=%v", err)
	}
	if !strings.Contains(err.Error(), "stop kukeond cell:") {
		t.Fatalf("want wrap prefix \"stop kukeond cell:\", got %q", err.Error())
	}
}

func TestStopPhase_TimeoutEscalatesAndCancelsOrphan(t *testing.T) {
	// The fix verified here: when StopPhase's grace period expires, the
	// derived stopCtx is cancelled *before* KillCell runs. The in-flight
	// StopCell goroutine therefore sees ctx.Done() instead of continuing
	// against an unmodified parent context.
	stopReturned := make(chan struct{})
	seen := &ctxCapture{}
	killCalled := atomicBool{}
	fc := &fakeClient{
		stopCellFn: func(ctx context.Context, _ v1beta1.CellDoc) (kukeonv1.StopCellResult, error) {
			<-ctx.Done()
			seen.set(ctx)
			close(stopReturned)
			return kukeonv1.StopCellResult{}, ctx.Err()
		},
		killCellFn: func(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.KillCellResult, error) {
			killCalled.Store(true)
			// Wait for the StopCell goroutine to observe cancellation so the
			// race between cancel + KillCell is deterministic in the test.
			select {
			case <-stopReturned:
			case <-time.After(time.Second):
				t.Fatal("StopCell did not observe ctx cancellation before KillCell returned")
			}
			return kukeonv1.KillCellResult{Cell: doc, Killed: true}, nil
		},
	}
	cmd := newCmdWithContext(context.Background(), t)
	if err := lifecycle.StopPhase(cmd, fc, v1beta1.CellDoc{}, 20*time.Millisecond); err != nil {
		t.Fatalf("unexpected error from timeout-escalation path: %v", err)
	}
	if !killCalled.Load() {
		t.Fatal("KillCell must run after the grace period expires")
	}
	got := seen.get()
	if got == nil || got.Err() == nil {
		t.Fatal("StopCell goroutine must have seen ctx cancellation before returning")
	}
}

func TestStopPhase_KillNoChangeWrapsSentinel(t *testing.T) {
	fc := &fakeClient{
		stopCellFn: func(ctx context.Context, _ v1beta1.CellDoc) (kukeonv1.StopCellResult, error) {
			<-ctx.Done()
			return kukeonv1.StopCellResult{}, ctx.Err()
		},
		killCellFn: func(_ context.Context, _ v1beta1.CellDoc) (kukeonv1.KillCellResult, error) {
			return kukeonv1.KillCellResult{Killed: false}, nil
		},
	}
	cmd := newCmdWithContext(context.Background(), t)
	err := lifecycle.StopPhase(cmd, fc, v1beta1.CellDoc{}, 20*time.Millisecond)
	if err == nil {
		t.Fatal("want error from KillCell no-change path, got nil")
	}
	if !errors.Is(err, errdefs.ErrControllerNoChange) {
		t.Fatalf("want errors.Is(..., ErrControllerNoChange) true, got err=%v", err)
	}
	if !strings.Contains(err.Error(), "kill kukeond cell:") {
		t.Fatalf("want wrap prefix \"kill kukeond cell:\", got %q", err.Error())
	}
}

// TestIsDaemonReachable_Listening verifies the production probe answers true
// against a real unix-socket listener. Pairs with the not-listening case to
// pin both branches of the dial result.
func TestIsDaemonReachable_Listening(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "kukeond.sock")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	if !lifecycle.IsDaemonReachable(context.Background(), socketPath, 500*time.Millisecond) {
		t.Fatal("IsDaemonReachable returned false against a real listener")
	}
}

// TestIsDaemonReachable_NotListening verifies the production probe answers
// false when the socket path does not exist. Empty paths must also resolve
// to false rather than dialing the empty string and surfacing a confusing
// "invalid address" error.
func TestIsDaemonReachable_NotListening(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.sock")
	if lifecycle.IsDaemonReachable(context.Background(), missing, 100*time.Millisecond) {
		t.Fatal("IsDaemonReachable returned true against a missing socket")
	}
	if lifecycle.IsDaemonReachable(context.Background(), "", 100*time.Millisecond) {
		t.Fatal("IsDaemonReachable returned true for an empty socket path")
	}
}

// TestResolveReachableProbe_DefaultAndInjected confirms the indirection
// returns the production probe by default and honours a context-injected
// fake — the contract every lifecycle verb relies on for unit testing.
func TestResolveReachableProbe_DefaultAndInjected(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	if probe := lifecycle.ResolveReachableProbe(cmd); probe == nil {
		t.Fatal("default probe must not be nil")
	}

	injected := func(_ context.Context, _ string, _ time.Duration) bool { return true }
	cmd.SetContext(
		context.WithValue(context.Background(), lifecycle.ReachableProbeKey{}, lifecycle.ReachableProbe(injected)),
	)
	probe := lifecycle.ResolveReachableProbe(cmd)
	if !probe(context.Background(), "ignored", time.Millisecond) {
		t.Fatal("ResolveReachableProbe did not return the injected fake")
	}
}

// TestResolveSocketPath honours both an explicit viper override and the
// registered default. Centralising the lookup means every lifecycle verb
// agrees on which socket counts as the source of truth.
func TestResolveSocketPath(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	if got := lifecycle.ResolveSocketPath(); got != config.KUKEOND_SOCKET.Default {
		t.Fatalf("default path: got %q, want %q", got, config.KUKEOND_SOCKET.Default)
	}

	viper.Set(config.KUKEOND_SOCKET.ViperKey, "/run/kukeon-dev/kukeond.sock")
	if got := lifecycle.ResolveSocketPath(); got != "/run/kukeon-dev/kukeond.sock" {
		t.Fatalf("override path: got %q, want %q", got, "/run/kukeon-dev/kukeond.sock")
	}
}

// TestErrHostNotInitialized_IsBoundary proves errors.Is identity survives
// the verb-callsite wrap pattern. The hoisted sentinel must compare equal
// when returned bare *and* when wrapped via fmt.Errorf("...: %w", err) — the
// shape every `kuke daemon` read/write verb uses for "host not initialized.".
func TestErrHostNotInitialized_IsBoundary(t *testing.T) {
	bare := errdefs.ErrHostNotInitialized
	if !errors.Is(bare, errdefs.ErrHostNotInitialized) {
		t.Fatal("bare sentinel must satisfy errors.Is identity")
	}
	wrapped := fmt.Errorf("inspect kukeond cell: %w", errdefs.ErrHostNotInitialized)
	if !errors.Is(wrapped, errdefs.ErrHostNotInitialized) {
		t.Fatalf("wrapped sentinel must satisfy errors.Is identity; got %v", wrapped)
	}
}

// TestErrControllerNoChange_IsBoundary proves errors.Is identity survives
// the canonical wrap pattern callers use ("<verb> kukeond cell: %w"). The
// lifecycle StopPhase helper and every verb that handles a !Started /
// !Stopped / !Killed / !MetadataDeleted result must produce errors that
// match this sentinel.
func TestErrControllerNoChange_IsBoundary(t *testing.T) {
	wrapped := fmt.Errorf("stop kukeond cell: %w", errdefs.ErrControllerNoChange)
	if !errors.Is(wrapped, errdefs.ErrControllerNoChange) {
		t.Fatalf("wrapped sentinel must satisfy errors.Is identity; got %v", wrapped)
	}
	doubleWrapped := fmt.Errorf("orchestrator: %w", wrapped)
	if !errors.Is(doubleWrapped, errdefs.ErrControllerNoChange) {
		t.Fatalf("double-wrapped sentinel must satisfy errors.Is identity; got %v", doubleWrapped)
	}
}

func newCmdWithContext(ctx context.Context, t *testing.T) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetContext(ctx)
	return cmd
}

type ctxCapture struct {
	mu  sync.Mutex
	ctx context.Context //nolint:containedctx // test fake; captures cancelled ctx for assertion
}

func (c *ctxCapture) set(ctx context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ctx = ctx
}

func (c *ctxCapture) get() context.Context {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ctx
}

type atomicBool struct {
	mu sync.Mutex
	v  bool
}

func (a *atomicBool) Store(v bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.v = v
}

func (a *atomicBool) Load() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.v
}

type fakeClient struct {
	kukeonv1.FakeClient

	stopCellFn func(context.Context, v1beta1.CellDoc) (kukeonv1.StopCellResult, error)
	killCellFn func(context.Context, v1beta1.CellDoc) (kukeonv1.KillCellResult, error)
}

func (f *fakeClient) StopCell(ctx context.Context, doc v1beta1.CellDoc) (kukeonv1.StopCellResult, error) {
	if f.stopCellFn == nil {
		return kukeonv1.StopCellResult{}, errors.New("unexpected StopCell call")
	}
	return f.stopCellFn(ctx, doc)
}

func (f *fakeClient) KillCell(ctx context.Context, doc v1beta1.CellDoc) (kukeonv1.KillCellResult, error) {
	if f.killCellFn == nil {
		return kukeonv1.KillCellResult{}, errors.New("unexpected KillCell call")
	}
	return f.killCellFn(ctx, doc)
}
