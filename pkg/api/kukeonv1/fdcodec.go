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

package kukeonv1

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// The kukeond socket speaks plain net/rpc JSON-RPC. A reply that must hand
// the caller a connected socket (ConnectContainerTerminal) carries that
// socket's descriptor as SCM_RIGHTS ancillary data on the reply bytes, so
// the wire stays byte-compatible with jsonrpc for every other method: the
// shared, multiplexed UnixClient connection is unaffected, and an older
// client that never calls ConnectContainerTerminal never sees ancillary data.

// passedConnCarrier is implemented by reply types whose descriptor the
// server codec sends with the reply. takePassedConn returns nil when there
// is nothing to pass (the call failed).
type passedConnCarrier interface {
	takePassedConn() *net.UnixConn
}

// maxPassedFDs bounds the ancillary buffer the client allocates per read.
// A reply passes exactly one descriptor; the extra room lets the client
// notice (and close) surplus descriptors instead of the kernel truncating
// them with MSG_CTRUNC.
const maxPassedFDs = 4

// errNoPassedConn is returned when a successful reply arrived without the
// connected socket it should carry.
var errNoPassedConn = errors.New("kukeond reply carried no connection descriptor")

// NewServerCodec returns the kukeond server codec for conn. On a unix
// socket it is the standard jsonrpc server codec plus SCM_RIGHTS on replies
// that carry a connection; on any other transport it is the plain jsonrpc
// codec (no descriptor can be passed there).
func NewServerCodec(conn io.ReadWriteCloser) rpc.ServerCodec {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return jsonrpc.NewServerCodec(conn)
	}
	w := &rightsWriter{UnixConn: uc}
	return &fdServerCodec{ServerCodec: jsonrpc.NewServerCodec(w), w: w}
}

// fdServerCodec wraps the jsonrpc server codec. WriteResponse arms the
// connection writer with the passed descriptor so the single Write the
// jsonrpc encoder issues for the reply goes out with it attached.
type fdServerCodec struct {
	rpc.ServerCodec

	w *rightsWriter
	// mu serializes replies so an armed descriptor can only attach to the
	// reply it belongs to. net/rpc already serializes WriteResponse; this
	// keeps the codec correct on its own.
	mu sync.Mutex
}

func (c *fdServerCodec) WriteResponse(r *rpc.Response, body any) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	carrier, ok := body.(passedConnCarrier)
	if !ok {
		return c.ServerCodec.WriteResponse(r, body)
	}
	pass := carrier.takePassedConn()
	if pass == nil {
		return c.ServerCodec.WriteResponse(r, body)
	}
	// SCM_RIGHTS duplicates the descriptor into the receiver; the daemon's
	// copy is closed whether or not the reply could be written.
	defer func() { _ = pass.Close() }()

	raw, err := pass.SyscallConn()
	if err != nil {
		// The client reports a reply without a descriptor as errNoPassedConn.
		return c.ServerCodec.WriteResponse(r, body)
	}
	var writeErr error
	ctrlErr := raw.Control(func(fd uintptr) {
		c.w.arm(unix.UnixRights(int(fd)))
		defer c.w.disarm()
		writeErr = c.ServerCodec.WriteResponse(r, body)
	})
	if ctrlErr != nil {
		return ctrlErr
	}
	return writeErr
}

// rightsWriter is the server side of the connection. When armed, its next
// Write goes out through sendmsg with the ancillary data attached.
type rightsWriter struct {
	*net.UnixConn

	mu  sync.Mutex
	oob []byte
}

func (w *rightsWriter) arm(oob []byte) {
	w.mu.Lock()
	w.oob = oob
	w.mu.Unlock()
}

func (w *rightsWriter) disarm() { w.arm(nil) }

func (w *rightsWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	oob := w.oob
	w.oob = nil
	w.mu.Unlock()
	if oob == nil {
		return w.UnixConn.Write(p)
	}
	n, _, err := w.UnixConn.WriteMsgUnix(p, oob, nil)
	if n < 0 {
		n = 0 // sendmsg reports -1 on error; io.Writer requires n >= 0
	}
	if err != nil || n == len(p) {
		return n, err
	}
	// A stream socket may accept fewer bytes than offered; the ancillary
	// data travelled with the first chunk, so the rest goes out plain.
	m, err := w.UnixConn.Write(p[n:])
	return n + m, err
}

// rightsReader is the client side of a dedicated connection. Every Read
// collects descriptors that arrive as SCM_RIGHTS so the caller can claim
// them once the reply is decoded.
type rightsReader struct {
	*net.UnixConn

	mu        sync.Mutex
	fds       []int
	truncated bool
}

func (r *rightsReader) Read(p []byte) (int, error) {
	oob := make([]byte, unix.CmsgSpace(maxPassedFDs*4)) //nolint:mnd // 4 bytes per int32 descriptor
	n, oobn, flags, _, err := r.UnixConn.ReadMsgUnix(p, oob)
	if n < 0 {
		// recvmsg reports -1 on error; io.Reader callers must see 0.
		n = 0
	}
	if oobn > 0 || flags&unix.MSG_CTRUNC != 0 {
		fds := parseRights(oob[:oobn])
		r.mu.Lock()
		r.fds = append(r.fds, fds...)
		if flags&unix.MSG_CTRUNC != 0 {
			r.truncated = true
		}
		r.mu.Unlock()
	}
	return n, err
}

// take returns the collected descriptors and forgets them, so whoever
// takes them owns closing them.
func (r *rightsReader) take() ([]int, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fds, truncated := r.fds, r.truncated
	r.fds = nil
	return fds, truncated
}

func parseRights(oob []byte) []int {
	msgs, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return nil
	}
	var fds []int
	for i := range msgs {
		got, rightsErr := unix.ParseUnixRights(&msgs[i])
		if rightsErr != nil {
			continue
		}
		fds = append(fds, got...)
	}
	return fds
}

func closeFDs(fds []int) {
	for _, fd := range fds {
		_ = unix.Close(fd)
	}
}

// callReceivingConn performs a single RPC on the dedicated connection conn
// and returns the connection descriptor its reply passed back. conn is
// closed before returning. reply must carry an *APIError via replyErr.
func callReceivingConn(
	ctx context.Context,
	conn *net.UnixConn,
	method string,
	args any,
	reply any,
	replyErr func() *APIError,
) (*net.UnixConn, error) {
	rx := &rightsReader{UnixConn: conn}
	rpcc := rpc.NewClientWithCodec(jsonrpc.NewClientCodec(rx))

	done := make(chan error, 1)
	go func() { done <- rpcc.Call(method, args, reply) }()

	var callErr error
	select {
	case <-ctx.Done():
		//nolint:mnd // short deadline to unblock the pending call
		_ = conn.SetDeadline(time.Now().Add(10 * time.Millisecond))
		_ = rpcc.Close()
		<-done
		fds, _ := rx.take()
		closeFDs(fds)
		return nil, ctx.Err()
	case callErr = <-done:
	}
	_ = rpcc.Close()
	fds, truncated := rx.take()

	if callErr != nil {
		closeFDs(fds)
		return nil, translateCallError(method, callErr)
	}
	if apiErr := replyErr(); apiErr != nil {
		closeFDs(fds)
		return nil, FromAPIError(apiErr)
	}
	switch {
	case truncated:
		closeFDs(fds)
		return nil, fmt.Errorf("%s: ancillary data truncated: %w", method, errNoPassedConn)
	case len(fds) == 0:
		return nil, fmt.Errorf("%s: %w", method, errNoPassedConn)
	case len(fds) > 1:
		closeFDs(fds[1:])
	}
	return unixConnFromFD(fds[0])
}

// unixConnFromFD wraps a received descriptor as a *net.UnixConn, taking
// ownership of fd.
func unixConnFromFD(fd int) (*net.UnixConn, error) {
	f := os.NewFile(uintptr(fd), "kukeond-passed-conn")
	defer func() { _ = f.Close() }() // FileConn dups; drop the original
	c, err := net.FileConn(f)
	if err != nil {
		return nil, fmt.Errorf("wrap passed connection: %w", err)
	}
	uc, ok := c.(*net.UnixConn)
	if !ok {
		_ = c.Close()
		return nil, fmt.Errorf("passed descriptor is a %T, not a unix socket", c)
	}
	return uc, nil
}
