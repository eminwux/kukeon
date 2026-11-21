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

package local

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"

	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// terminalDialTimeout bounds the connect to a container's sbsh control
// socket, matching sbsh's own default for path dials. A unix connect either
// lands or fails immediately in practice; the bound only guards a wedged
// peer.
const terminalDialTimeout = 5 * time.Second

// ConnectContainerTerminal applies AttachContainer's gates and returns a
// connection to the container's sbsh control socket. In-process
// (KUKEON_NO_DAEMON) it dials the host socket path as the calling user;
// inside kukeond the RPC service calls it and passes the connection back to
// the client over the kukeond socket. Dial failures come back typed (see
// classifyTerminalDialError) because a remote caller cannot stat the path.
func (c *Client) ConnectContainerTerminal(ctx context.Context, doc v1beta1.ContainerDoc) (*net.UnixConn, error) {
	socketPath, err := c.gateAttachableSocket(doc)
	if err != nil {
		return nil, err
	}
	return dialTerminalSocket(ctx, socketPath)
}

// dialTerminalSocket connects to the sbsh control socket at socketPath.
func dialTerminalSocket(ctx context.Context, socketPath string) (*net.UnixConn, error) {
	d := net.Dialer{Timeout: terminalDialTimeout}
	conn, err := d.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return nil, classifyTerminalDialError(socketPath, err)
	}
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		_ = conn.Close()
		return nil, fmt.Errorf("dial terminal socket %s: got %T, want a unix socket", socketPath, conn)
	}
	return uc, nil
}

// classifyTerminalDialError maps a failed control-socket dial onto the
// errdefs classes `kuke run`'s boot-race retry branches on (#933, #1170),
// so the classifier keeps working when it can no longer stat the path:
//
//   - ENOENT: the socket is absent (kuketty between Remove and Listen) —
//     ErrAttachStaleSocket, retryable.
//   - EACCES with the socket absent on stat: the same transient window —
//     ErrAttachStaleSocket.
//   - EACCES with the socket present: a wrong-mode inode retrying cannot
//     fix — ErrAttachPermissionDenied.
//
// The errno is kept in the message only (%v), not the chain: over the wire
// the sentinel alone must pick the APIError Kind, and an EACCES in the chain
// would also match fs.ErrPermission. Any other failure (ECONNREFUSED,
// timeout) is wrapped as-is.
func classifyTerminalDialError(socketPath string, err error) error {
	var sentinel error
	switch {
	case errors.Is(err, syscall.ENOENT):
		sentinel = errdefs.ErrAttachStaleSocket
	case errors.Is(err, syscall.EACCES):
		sentinel = errdefs.ErrAttachStaleSocket
		if _, statErr := os.Stat(socketPath); statErr == nil {
			sentinel = errdefs.ErrAttachPermissionDenied
		}
	default:
		return fmt.Errorf("dial terminal socket %s: %w", socketPath, err)
	}
	return fmt.Errorf("%w: dial %s: %v", sentinel, socketPath, err) //nolint:errorlint // sentinel-only chain
}
