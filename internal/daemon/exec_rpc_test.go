// Copyright 2026 Emiliano Spinella (eminwux)
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

package daemon_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/execstream"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"golang.org/x/sys/unix"
)

// execCore is the daemon core the service delegates ExecContainer to. It
// answers the way local.Client does: one end of a socket pair goes back to
// the caller, and a tiny session on the other end echoes stdin to stdout
// and reports exit 0 on stdin EOF. Or it refuses with err.
type execCore struct {
	kukeonv1.FakeClient

	err error

	mu      sync.Mutex
	doc     v1beta1.ContainerDoc
	req     kukeonv1.ExecRequest
	peerUID *int
	handed  []*net.UnixConn
}

func (c *execCore) ExecContainer(
	ctx context.Context,
	doc v1beta1.ContainerDoc,
	req kukeonv1.ExecRequest,
) (*net.UnixConn, error) {
	c.mu.Lock()
	c.doc, c.req = doc, req
	if uid, ok := kukeonv1.PeerUIDFrom(ctx); ok {
		c.peerUID = &uid
	}
	c.mu.Unlock()
	if c.err != nil {
		return nil, c.err
	}
	callerEnd, daemonEnd, err := unixPair()
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.handed = append(c.handed, callerEnd)
	c.mu.Unlock()
	go func() {
		defer func() { _ = daemonEnd.Close() }()
		w := execstream.NewWriter(daemonEnd)
		for {
			typ, payload, readErr := execstream.ReadFrame(daemonEnd)
			if readErr != nil {
				return
			}
			switch typ {
			case execstream.TypeStdin:
				_ = w.WriteData(execstream.TypeStdout, payload)
			case execstream.TypeStdinEOF:
				_ = w.Exit(execstream.Exit{Code: 0})
				return
			}
		}
	}()
	return callerEnd, nil
}

func unixPair() (*net.UnixConn, *net.UnixConn, error) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	var conns [2]*net.UnixConn
	for i, fd := range fds {
		f := os.NewFile(uintptr(fd), "pair")
		c, connErr := net.FileConn(f)
		_ = f.Close()
		if connErr != nil {
			return nil, nil, connErr
		}
		conns[i] = c.(*net.UnixConn)
	}
	return conns[0], conns[1], nil
}

// TestExecContainer_PassedConnCarriesTheSession is the contract over real
// unix sockets: the request reaches the core intact, the caller uid comes
// from the socket, and the passed descriptor is a working exec connection.
func TestExecContainer_PassedConnCarriesTheSession(t *testing.T) {
	dir := shortTempDir(t)
	core := &execCore{}
	client := kukeonv1.NewUnixClient(startKukeond(t, dir, core))
	t.Cleanup(func() { _ = client.Close() })

	doc := v1beta1.ContainerDoc{Metadata: v1beta1.ContainerMetadata{Name: "work"}}
	req := kukeonv1.ExecRequest{
		Command: []string{"sh", "-c", "cat"}, User: "1234:5678", Env: []string{"A=b"}, Workdir: "/tmp",
		Stdin: true, TTY: true, Cols: 132, Rows: 43,
	}
	conn, err := client.ExecContainer(testCtx(t), doc, req)
	if err != nil {
		t.Fatalf("ExecContainer: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	w := execstream.NewWriter(conn)
	if err = w.WriteData(execstream.TypeStdin, []byte("hello")); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	typ, payload, err := execstream.ReadFrame(conn)
	if err != nil || typ != execstream.TypeStdout || string(payload) != "hello" {
		t.Fatalf("frame = type %d %q (%v), want stdout hello", typ, payload, err)
	}
	if err = w.StdinEOF(); err != nil {
		t.Fatalf("write stdin EOF: %v", err)
	}
	typ, payload, err = execstream.ReadFrame(conn)
	if err != nil || typ != execstream.TypeExit {
		t.Fatalf("frame = type %d (%v), want exit", typ, err)
	}
	if exit, decodeErr := execstream.DecodeExit(payload); decodeErr != nil || exit.Code != 0 || exit.Error != "" {
		t.Fatalf("exit = %+v (%v), want code 0", exit, decodeErr)
	}
	if _, _, err = execstream.ReadFrame(conn); !errors.Is(err, io.EOF) {
		t.Fatalf("read after exit = %v, want EOF", err)
	}

	core.mu.Lock()
	defer core.mu.Unlock()
	if core.doc.Metadata.Name != "work" || !reflect.DeepEqual(core.req, req) {
		t.Errorf("core saw doc %q and request %+v, want work and %+v", core.doc.Metadata.Name, core.req, req)
	}
	if core.peerUID == nil || *core.peerUID != os.Getuid() {
		t.Errorf("caller uid = %v, want %d from SO_PEERCRED", core.peerUID, os.Getuid())
	}
	// The daemon must not keep its copy of the caller's end open.
	if len(core.handed) != 1 {
		t.Fatalf("core handed %d connections, want 1", len(core.handed))
	}
	if _, err = core.handed[0].Write([]byte("x")); !errors.Is(err, net.ErrClosed) {
		t.Errorf("daemon copy of the caller's end still open after the reply (write err %v)", err)
	}
}

// TestExecContainer_ErrorReplyPassesNoDescriptor pins that a refusal reaches
// the caller as its errdefs sentinel, with no connection.
func TestExecContainer_ErrorReplyPassesNoDescriptor(t *testing.T) {
	cases := []error{
		fmt.Errorf("%w: a command is required", errdefs.ErrExecInvalidRequest),
		fmt.Errorf("%w: container \"work\" in cell \"c\" has state Stopped", errdefs.ErrExecTaskNotRunning),
		errdefs.ErrContainerNotFound,
	}
	for _, want := range cases {
		t.Run(want.Error(), func(t *testing.T) {
			dir := shortTempDir(t)
			core := &execCore{err: want}
			client := kukeonv1.NewUnixClient(startKukeond(t, dir, core))
			t.Cleanup(func() { _ = client.Close() })

			conn, err := client.ExecContainer(testCtx(t), v1beta1.ContainerDoc{}, kukeonv1.ExecRequest{})
			if conn != nil {
				_ = conn.Close()
				t.Fatal("got a connection, want refusal")
			}
			if !errors.Is(err, errors.Unwrap(want)) && !errors.Is(err, want) {
				t.Fatalf("err = %v, want it to unwrap to the sentinel of %v", err, want)
			}
			if err.Error() != want.Error() {
				t.Errorf("message = %q, want %q verbatim", err, want)
			}
		})
	}
}

// TestExecContainer_SharedConnUnaffected pins that the multiplexed
// connection keeps working interleaved with dedicated ExecContainer calls.
func TestExecContainer_SharedConnUnaffected(t *testing.T) {
	dir := shortTempDir(t)
	core := &execCore{}
	client := kukeonv1.NewUnixClient(startKukeond(t, dir, core))
	t.Cleanup(func() { _ = client.Close() })

	for range 2 {
		conn, err := client.ExecContainer(testCtx(t), v1beta1.ContainerDoc{},
			kukeonv1.ExecRequest{Command: []string{"true"}})
		if err != nil {
			t.Fatalf("ExecContainer: %v", err)
		}
		_ = conn.Close()
		// FakeClient answers Ping-less calls with ErrUnexpectedCall through
		// the shared connection: a decoded reply, not a broken stream.
		if _, err = client.AttachContainer(testCtx(t), v1beta1.ContainerDoc{}); err == nil ||
			errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("AttachContainer on the shared connection = %v, want the core's refusal", err)
		}
	}
}

// TestExecContainer_OlderDaemon_MethodUnsupported pins the version-skew
// signal `kuke exec` reports against a kukeond without the RPC.
func TestExecContainer_OlderDaemon_MethodUnsupported(t *testing.T) {
	dir := shortTempDir(t)
	path := filepath.Join(dir, "old.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := rpc.NewServer()
	if err = srv.RegisterName(kukeonv1.ServiceName, legacyService{}); err != nil {
		t.Fatalf("register: %v", err)
	}
	serveUnix(t, ln, func(c net.Conn) { srv.ServeCodec(jsonrpc.NewServerCodec(c)) })

	client := kukeonv1.NewUnixClient(path)
	t.Cleanup(func() { _ = client.Close() })
	_, err = client.ExecContainer(testCtx(t), v1beta1.ContainerDoc{}, kukeonv1.ExecRequest{Command: []string{"id"}})
	if !errors.Is(err, errdefs.ErrDaemonMethodUnsupported) {
		t.Fatalf("err = %v, want ErrDaemonMethodUnsupported", err)
	}
}
