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

//nolint:testpackage // exercises *Exec.ensureClientConnected against in-package state
package runner

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/ctr"
)

// TestEnsureClientConnected_ConcurrentFirstUseSingleClient pins issue #684:
// two concurrent first-use goroutines must not each construct a ctr.Client and
// overwrite the other's assignment to r.ctrClient. The sync.Once guard
// serializes construction, so every goroutine observes the same client
// pointer. Run under `go test -race` to also catch the unguarded
// nil-check-then-assign data race the fix removes.
//
// The socket points at a nonexistent path and the runner context carries a
// short deadline, so the constructed client's Connect fails fast without a
// real containerd; the error is irrelevant here — the assertion is on the
// single, stable client pointer.
func TestEnsureClientConnected_ConcurrentFirstUseSingleClient(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	r := &Exec{
		ctx:    ctx,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		opts:   Options{ContainerdSocket: "/nonexistent/kukeon-test-containerd.sock"},
	}

	const goroutines = 32
	observed := make([]ctr.Client, goroutines)
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(goroutines)
	for i := range observed {
		go func(idx int) {
			defer wg.Done()
			<-start
			_ = r.ensureClientConnected()
			observed[idx] = r.ctrClient
		}(i)
	}
	close(start)
	wg.Wait()

	if r.ctrClient == nil {
		t.Fatal("ctrClient is nil after concurrent first-use")
	}
	for idx, c := range observed {
		if c != r.ctrClient {
			t.Fatalf("goroutine %d observed a different client pointer (%p) than the final one (%p): two clients were constructed", idx, c, r.ctrClient)
		}
	}
}

// connectCountingClient is a ctr.Client whose only behavior is to count
// Connect calls. It embeds deleteCellFakeClient (defined in delete_cell_test.go)
// so it satisfies the full interface with zero-value methods, overriding only
// Connect.
type connectCountingClient struct {
	deleteCellFakeClient
	connects int64
	mu       sync.Mutex
}

func (c *connectCountingClient) Connect() error {
	c.mu.Lock()
	c.connects++
	c.mu.Unlock()
	return nil
}

// TestEnsureClientConnected_PreservesInjectedClient guards the nil-check inside
// the sync.Once: a test-injected fake (constructed via &Exec{ctrClient: fake})
// must never be overwritten by a lazily-built real client. Without the nil
// guard the Once would clobber the fake on first use.
func TestEnsureClientConnected_PreservesInjectedClient(t *testing.T) {
	fake := &connectCountingClient{}
	r := &Exec{
		ctx:       context.Background(),
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		opts:      Options{ContainerdSocket: "/nonexistent/kukeon-test-containerd.sock"},
		ctrClient: fake,
	}

	const goroutines = 16
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			<-start
			if err := r.ensureClientConnected(); err != nil {
				t.Errorf("ensureClientConnected: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if r.ctrClient != ctr.Client(fake) {
		t.Fatalf("injected fake was overwritten: got %p, want %p", r.ctrClient, fake)
	}
	if fake.connects != goroutines {
		t.Fatalf("Connect called %d times, want %d (one per ensureClientConnected)", fake.connects, goroutines)
	}
}
