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

package daemon_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/daemon"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	sbshapi "github.com/eminwux/sbsh/pkg/api"
	"github.com/eminwux/sbsh/pkg/rpcclient/terminal"
)

// pingTerminal is the slice of sbsh's terminal control service the test
// needs: it answers Ping over plain JSON-RPC, as the real terminal does.
type pingTerminal struct{}

func (pingTerminal) Ping(in *sbshapi.PingMessage, out *sbshapi.PingMessage) error {
	if in.Message != "PING" {
		return fmt.Errorf("unexpected ping %q", in.Message)
	}
	out.Message = "PONG"
	return nil
}

// startPingTerminal serves pingTerminal on a unix socket under dir and
// returns the socket path.
func startPingTerminal(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "term.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen terminal: %v", err)
	}
	srv := rpc.NewServer()
	if err = srv.RegisterName(sbshapi.TerminalService, pingTerminal{}); err != nil {
		t.Fatalf("register terminal: %v", err)
	}
	serveUnix(t, ln, func(c net.Conn) { srv.ServeCodec(jsonrpc.NewServerCodec(c)) })
	return path
}

// serveUnix runs an accept loop until the test ends.
func serveUnix(t *testing.T, ln net.Listener, serve func(net.Conn)) {
	t.Helper()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go serve(c)
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		wg.Wait()
	})
}

// terminalCore is the daemon core the service delegates to: it dials the
// test terminal the way local.Client dials the container's sbsh socket, or
// refuses with err.
type terminalCore struct {
	kukeonv1.FakeClient

	socketPath string
	err        error

	mu      sync.Mutex
	handed  []*net.UnixConn
	lastDoc v1beta1.ContainerDoc
}

func (c *terminalCore) ConnectContainerTerminal(
	ctx context.Context,
	doc v1beta1.ContainerDoc,
) (*net.UnixConn, error) {
	c.mu.Lock()
	c.lastDoc = doc
	c.mu.Unlock()
	if c.err != nil {
		return nil, c.err
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.socketPath)
	if err != nil {
		return nil, err
	}
	uc := conn.(*net.UnixConn)
	c.mu.Lock()
	c.handed = append(c.handed, uc)
	c.mu.Unlock()
	return uc, nil
}

func (c *terminalCore) AttachContainer(context.Context, v1beta1.ContainerDoc) (kukeonv1.AttachContainerResult, error) {
	return kukeonv1.AttachContainerResult{HostSocketPath: c.socketPath}, nil
}

// startKukeond serves the KukeonV1 service backed by core through the
// daemon's fd-aware codec, and returns its socket path.
func startKukeond(t *testing.T, dir string, core kukeonv1.Client) string {
	t.Helper()
	path := filepath.Join(dir, "kukeond.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen kukeond: %v", err)
	}
	srv := rpc.NewServer()
	svc := daemon.NewKukeonV1Service(context.Background(), discardLogger(), core)
	if err = srv.RegisterName(kukeonv1.ServiceName, svc); err != nil {
		t.Fatalf("register kukeond: %v", err)
	}
	serveUnix(t, ln, func(c net.Conn) { srv.ServeCodec(kukeonv1.NewServerCodec(c)) })
	return path
}

// shortTempDir returns a temp dir short enough for unix socket paths
// (sun_path is 108 bytes; t.TempDir()'s per-test path can overflow it).
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "kc") //nolint:usetesting // sun_path limit
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// TestConnectContainerTerminal_PassedConnCompletesSbshPing is the end-to-end
// contract over real unix sockets: kukeond dials the terminal, passes the
// connected socket back over SCM_RIGHTS, and sbsh's own terminal client
// completes a Ping over the passed connection.
func TestConnectContainerTerminal_PassedConnCompletesSbshPing(t *testing.T) {
	dir := shortTempDir(t)
	core := &terminalCore{socketPath: startPingTerminal(t, dir)}
	client := kukeonv1.NewUnixClient(startKukeond(t, dir, core))
	t.Cleanup(func() { _ = client.Close() })

	doc := v1beta1.ContainerDoc{Metadata: v1beta1.ContainerMetadata{Name: "work"}}
	dial := func(ctx context.Context) (net.Conn, error) {
		conn, err := client.ConnectContainerTerminal(ctx, doc)
		if err != nil {
			return nil, err
		}
		return conn, nil
	}
	tc := terminal.NewWithDialer(dial, discardLogger())

	// Two pings: each sbsh call dials (and gets) its own passed connection.
	for i := range 2 {
		var pong sbshapi.PingMessage
		if err := tc.Ping(testCtx(t), &sbshapi.PingMessage{Message: "PING"}, &pong); err != nil {
			t.Fatalf("ping %d over passed connection: %v", i, err)
		}
		if pong.Message != "PONG" {
			t.Fatalf("ping %d reply = %q, want PONG", i, pong.Message)
		}
	}
	core.mu.Lock()
	defer core.mu.Unlock()
	if core.lastDoc.Metadata.Name != "work" {
		t.Errorf("core saw doc %q, want work", core.lastDoc.Metadata.Name)
	}
	if len(core.handed) != 2 {
		t.Fatalf("daemon dialed %d times, want 2", len(core.handed))
	}
	// The daemon must not keep its copy of a passed socket open. The codec
	// closes it only after the reply is on the wire, so the client can get
	// here first: wait for the close instead of checking once.
	deadline := time.Now().Add(2 * time.Second)
	for i, c := range core.handed {
		for {
			_, err := c.Write([]byte("x"))
			if errors.Is(err, net.ErrClosed) {
				break
			}
			if time.Now().After(deadline) {
				t.Errorf("daemon copy %d still open after the reply (write err %v)", i, err)
				break
			}
			time.Sleep(time.Millisecond)
		}
	}
}

// TestConnectContainerTerminal_SharedConnUnaffected pins wire compatibility:
// the multiplexed connection the other methods use keeps working against the
// fd-aware codec, interleaved with dedicated ConnectContainerTerminal calls.
func TestConnectContainerTerminal_SharedConnUnaffected(t *testing.T) {
	dir := shortTempDir(t)
	core := &terminalCore{socketPath: startPingTerminal(t, dir)}
	client := kukeonv1.NewUnixClient(startKukeond(t, dir, core))
	t.Cleanup(func() { _ = client.Close() })

	for range 2 {
		res, err := client.AttachContainer(testCtx(t), v1beta1.ContainerDoc{})
		if err != nil || res.HostSocketPath != core.socketPath {
			t.Fatalf("AttachContainer = %+v, %v; want %q", res, err, core.socketPath)
		}
		conn, err := client.ConnectContainerTerminal(testCtx(t), v1beta1.ContainerDoc{})
		if err != nil {
			t.Fatalf("ConnectContainerTerminal: %v", err)
		}
		_ = conn.Close()
	}
}

// TestConnectContainerTerminal_TypedErrorsCrossTheWire pins that gate
// refusals and kukeond-side dial failures reach the client as errdefs
// sentinels it can errors.Is, with no descriptor attached.
func TestConnectContainerTerminal_TypedErrorsCrossTheWire(t *testing.T) {
	cases := []error{
		errdefs.ErrAttachNotSupported,
		fmt.Errorf("%w: container \"work\" has state Stopped", errdefs.ErrAttachTaskNotRunning),
		fmt.Errorf("%w: dial /x: no such file or directory", errdefs.ErrAttachStaleSocket),
		fmt.Errorf("%w: dial /x: permission denied", errdefs.ErrAttachPermissionDenied),
		errdefs.ErrContainerNotFound,
	}
	for _, want := range cases {
		t.Run(want.Error(), func(t *testing.T) {
			dir := shortTempDir(t)
			core := &terminalCore{err: want}
			client := kukeonv1.NewUnixClient(startKukeond(t, dir, core))
			t.Cleanup(func() { _ = client.Close() })

			conn, err := client.ConnectContainerTerminal(testCtx(t), v1beta1.ContainerDoc{})
			if conn != nil {
				_ = conn.Close()
				t.Fatalf("got a connection, want refusal")
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

// legacyService stands in for a kukeond that predates
// ConnectContainerTerminal.
type legacyService struct{}

func (legacyService) Ping(*struct{}, *struct{}) error { return nil }

// TestConnectContainerTerminal_OlderDaemon_MethodUnsupported pins the
// version-skew signal the CLI falls back on.
func TestConnectContainerTerminal_OlderDaemon_MethodUnsupported(t *testing.T) {
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
	_, err = client.ConnectContainerTerminal(testCtx(t), v1beta1.ContainerDoc{})
	if !errors.Is(err, errdefs.ErrDaemonMethodUnsupported) {
		t.Fatalf("err = %v, want ErrDaemonMethodUnsupported", err)
	}
}

// TestConnectContainerTerminal_CtxCancelled pins that a cancelled caller
// returns promptly even when the daemon never answers.
func TestConnectContainerTerminal_CtxCancelled(t *testing.T) {
	dir := shortTempDir(t)
	path := filepath.Join(dir, "mute.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	release := make(chan struct{})
	serveUnix(t, ln, func(c net.Conn) {
		<-release
		_ = c.Close()
	})
	t.Cleanup(func() { close(release) })

	client := kukeonv1.NewUnixClient(path)
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = client.ConnectContainerTerminal(ctx, v1beta1.ContainerDoc{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("returned after %s, want prompt return on ctx expiry", elapsed)
	}
}
