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

package shared

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/sbsh/pkg/attach"
	rpcterminal "github.com/eminwux/sbsh/pkg/rpcclient/terminal"
)

// TerminalDialer supplies sbsh's pkg/attach with control-socket connections
// obtained through kukeonv1.Client.ConnectContainerTerminal, so `kuke
// attach` / `kuke run` (and, via TerminalClient, `kuke screenshot` / `kuke
// write`) reach a container's terminal through kukeond instead
// of dialing the socket path themselves (a cell with only the kukeond socket
// mounted cannot see that path).
//
// sbsh dials once per control RPC (Ping, State, Attach, Resize, Detach).
// Preflight runs the first ConnectContainerTerminal up front, so gate
// refusals (not attachable, task not running, boot-race socket classes)
// fail fast with kukeon's own error instead of sbsh's dial retries, and
// parks the connection for sbsh's first dial so the preflight costs no
// extra round trip.
type TerminalDialer struct {
	client kukeonv1.Client
	doc    v1beta1.ContainerDoc

	mu     sync.Mutex
	parked *net.UnixConn
}

// NewTerminalDialer returns a dialer for the container identified by doc.
func NewTerminalDialer(client kukeonv1.Client, doc v1beta1.ContainerDoc) *TerminalDialer {
	return &TerminalDialer{client: client, doc: doc}
}

// Preflight connects once and parks the connection for the next Dial. A
// connection parked by an earlier Preflight is replaced.
func (d *TerminalDialer) Preflight(ctx context.Context) error {
	conn, err := d.client.ConnectContainerTerminal(ctx, d.doc)
	if err != nil {
		return err
	}
	d.mu.Lock()
	prev := d.parked
	d.parked = conn
	d.mu.Unlock()
	if prev != nil {
		_ = prev.Close()
	}
	return nil
}

// Prepare preflights and points opts at this dialer (opts.Dial). Against a
// kukeond that predates ConnectContainerTerminal (ErrDaemonMethodUnsupported)
// it falls back to AttachContainer's HostSocketPath (opts.SocketPath), so a
// newer kuke on the host keeps attaching until the daemon is redeployed;
// that fallback still needs filesystem access to the socket, as before.
func (d *TerminalDialer) Prepare(ctx context.Context, opts *attach.Options) error {
	socketPath, err := d.route(ctx)
	if err != nil {
		return err
	}
	if socketPath == "" {
		opts.Dial = d.Dial
		opts.SocketPath = ""
		return nil
	}
	opts.Dial = nil
	opts.SocketPath = socketPath
	return nil
}

// TerminalClient preflights and returns an sbsh terminal RPC client for
// one-shot control calls (`kuke screenshot`, `kuke write`): built on this
// dialer (terminal.NewWithDialer), or, against a kukeond that predates
// ConnectContainerTerminal, on AttachContainer's HostSocketPath — the same
// fallback as Prepare.
func (d *TerminalDialer) TerminalClient(ctx context.Context, logger *slog.Logger) (rpcterminal.Client, error) {
	socketPath, err := d.route(ctx)
	if err != nil {
		return nil, err
	}
	if socketPath == "" {
		return rpcterminal.NewWithDialer(d.Dial, logger), nil
	}
	return rpcterminal.NewUnix(socketPath, logger), nil
}

// route preflights. An empty socketPath means "dial through this dialer";
// a non-empty one is the version-skew fallback path.
func (d *TerminalDialer) route(ctx context.Context) (string, error) {
	err := d.Preflight(ctx)
	if err == nil {
		return "", nil
	}
	if !errors.Is(err, errdefs.ErrDaemonMethodUnsupported) {
		return "", err
	}
	res, err := d.client.AttachContainer(ctx, d.doc)
	if err != nil {
		return "", err
	}
	if res.HostSocketPath == "" {
		return "", fmt.Errorf("daemon returned empty HostSocketPath for container %q", d.doc.Metadata.Name)
	}
	return res.HostSocketPath, nil
}

// Dial satisfies sbsh's terminal.Dialer: the parked preflight connection
// first, then a fresh ConnectContainerTerminal per call. Safe for
// concurrent use (sbsh can race a resize against a detach).
func (d *TerminalDialer) Dial(ctx context.Context) (net.Conn, error) {
	d.mu.Lock()
	parked := d.parked
	d.parked = nil
	d.mu.Unlock()
	if parked != nil {
		return parked, nil
	}
	conn, err := d.client.ConnectContainerTerminal(ctx, d.doc)
	if err != nil {
		// Return an untyped nil: a typed-nil *net.UnixConn inside the
		// net.Conn interface would read as a live connection.
		return nil, err
	}
	return conn, nil
}

// Close releases a parked connection sbsh never consumed.
func (d *TerminalDialer) Close() {
	d.mu.Lock()
	parked := d.parked
	d.parked = nil
	d.mu.Unlock()
	if parked != nil {
		_ = parked.Close()
	}
}
