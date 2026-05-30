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

package lifecycle

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

// KukeondReadyTimeout is the bootstrap budget callers grant kukeond to bind
// its listener and start serving after the cell-creation path returns.
// Shared by `kuke init` and `kuke daemon recreate` so the two bring-up paths
// cannot drift on how long they wait before declaring failure.
const KukeondReadyTimeout = 30 * time.Second

// KukeondReadyTick is the poll interval between Ping attempts inside
// WaitForKukeondReady, and the per-attempt dial/ping budget. 200ms keeps
// the spin rate low (~150 attempts over the 30s window) while staying short
// enough that a healthy daemon answers on the first or second tick.
const KukeondReadyTick = 200 * time.Millisecond

// WaitForKukeondReady polls the kukeond socket with PingKukeond until it
// responds or the timeout expires. Used by `kuke init` and `kuke daemon
// recreate` after their cell-creation phase to gate "kukeond is ready"
// output on the RPC handler actually serving — the socket file appears as
// soon as the listener binds, which is strictly earlier than the gRPC
// handler being installed.
func WaitForKukeondReady(ctx context.Context, socketPath string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		if time.Now().After(deadline) {
			if lastErr != nil {
				return fmt.Errorf("timed out after %s: %w", timeout, lastErr)
			}
			return fmt.Errorf("timed out after %s", timeout)
		}

		attemptCtx, cancel := context.WithTimeout(ctx, KukeondReadyTick)
		err := PingKukeond(attemptCtx, socketPath)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(KukeondReadyTick):
		}
	}
}

// PingKukeond performs a single bring-up readiness check on socketPath: a
// bounded raw unix dial followed by a kukeonv1 Ping. The dial guards against
// the window where the socket file has appeared but the gRPC handler is not
// yet installed; the Ping confirms the handler answers. Errors are wrapped
// with a "dial:" or "ping:" prefix so callers can disambiguate which gate
// failed.
func PingKukeond(ctx context.Context, socketPath string) error {
	d := net.Dialer{Timeout: KukeondReadyTick}
	conn, err := d.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	_ = conn.Close()

	client := kukeonv1.NewUnixClient(socketPath, kukeonv1.WithDialTimeout(KukeondReadyTick))
	defer func() { _ = client.Close() }()

	if err = client.Ping(ctx); err != nil {
		return fmt.Errorf("ping: %w", err)
	}
	return nil
}
