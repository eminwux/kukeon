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

// Package terminaltest is a test double for the commands that reach a
// container's sbsh terminal through kukeond (`kuke screenshot`, `kuke
// write`). Client fakes kukeonv1.Client's target resolution and
// ConnectContainerTerminal; each connect hands back one end of a real unix
// socketpair whose other end is served by Terminal, a JSON-RPC server
// speaking sbsh's TerminalController protocol. Tests therefore exercise the
// real sbsh terminal client (terminal.NewWithDialer) end to end.
package terminaltest

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"os"
	"sync"

	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/sbsh/pkg/api"
	"golang.org/x/sys/unix"
)

// Terminal is the fake sbsh terminal behind every connection Client hands
// out. Screen is returned by Screenshot; Write appends to the record.
type Terminal struct {
	mu     sync.Mutex
	Screen api.ScreenshotResult
	writes [][]byte
}

// Writes returns the payloads received by Write, in order.
func (t *Terminal) Writes() [][]byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([][]byte, len(t.writes))
	copy(out, t.writes)
	return out
}

// controller is the RPC receiver registered as sbsh's TerminalController.
type controller struct{ t *Terminal }

func (c *controller) Screenshot(_ *api.ScreenshotArgs, reply *api.ScreenshotResult) error {
	c.t.mu.Lock()
	defer c.t.mu.Unlock()
	*reply = c.t.Screen
	return nil
}

func (c *controller) Write(req *api.WriteRequest, _ *api.Empty) error {
	c.t.mu.Lock()
	defer c.t.mu.Unlock()
	c.t.writes = append(c.t.writes, append([]byte(nil), req.Data...))
	return nil
}

// Serve answers sbsh JSON-RPC calls on conn until the client closes it.
func (t *Terminal) Serve(conn net.Conn) {
	srv := rpc.NewServer()
	if err := srv.RegisterName(api.TerminalService, &controller{t: t}); err != nil {
		panic(fmt.Sprintf("register %s: %v", api.TerminalService, err))
	}
	go func() {
		srv.ServeCodec(jsonrpc.NewServerCodec(conn))
	}()
}

// Client fakes the kukeonv1.Client calls the terminal commands make.
//
//   - GetCell reports a Ready cell with a live root task unless GetCellFn
//     is set.
//   - ListContainers calls ListContainersFn (an error if unset).
//   - ConnectContainerTerminal calls ConnectFn (nil means accept); on
//     accept it returns a socketpair end served by Terminal.
//   - AttachContainer calls AttachContainerFn (an error if unset).
//   - ExecContainer calls ExecContainerFn (an error if unset).
type Client struct {
	kukeonv1.FakeClient

	Terminal *Terminal

	GetCellFn         func(doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error)
	ListContainersFn  func(realm, space, stack, cell string) ([]v1beta1.ContainerSpec, error)
	ConnectFn         func(doc v1beta1.ContainerDoc) error
	AttachContainerFn func(doc v1beta1.ContainerDoc) (kukeonv1.AttachContainerResult, error)
	// ExecContainerFn backs ExecContainer (an error if unset). It returns
	// the caller's end of the session; SocketPair makes one.
	ExecContainerFn func(doc v1beta1.ContainerDoc, req kukeonv1.ExecRequest) (*net.UnixConn, error)

	mu       sync.Mutex
	connects []v1beta1.ContainerDoc
	peers    []*net.UnixConn
}

// NewClient returns a Client whose cell has one root and one Attachable
// "work" container.
func NewClient() *Client {
	return &Client{
		Terminal: &Terminal{},
		ListContainersFn: func(_, _, _, _ string) ([]v1beta1.ContainerSpec, error) {
			return []v1beta1.ContainerSpec{
				{ID: "root", Root: true},
				{ID: "work", Attachable: true},
			}, nil
		},
	}
}

// Connects returns the docs ConnectContainerTerminal was called with.
func (c *Client) Connects() []v1beta1.ContainerDoc {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]v1beta1.ContainerDoc(nil), c.connects...)
}

// GetCell implements kukeonv1.Client.
func (c *Client) GetCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
	if c.GetCellFn != nil {
		return c.GetCellFn(doc)
	}
	return kukeonv1.GetCellResult{
		Cell:                     v1beta1.CellDoc{Status: v1beta1.CellStatus{State: v1beta1.CellStateReady}},
		MetadataExists:           true,
		RootContainerExists:      true,
		RootContainerTaskRunning: true,
	}, nil
}

// ListContainers implements kukeonv1.Client.
func (c *Client) ListContainers(
	_ context.Context,
	realm, space, stack, cell string,
) ([]v1beta1.ContainerSpec, error) {
	if c.ListContainersFn == nil {
		return nil, errors.New("unexpected ListContainers call")
	}
	return c.ListContainersFn(realm, space, stack, cell)
}

// ConnectContainerTerminal implements kukeonv1.Client.
func (c *Client) ConnectContainerTerminal(_ context.Context, doc v1beta1.ContainerDoc) (*net.UnixConn, error) {
	c.mu.Lock()
	c.connects = append(c.connects, doc)
	c.mu.Unlock()
	if c.ConnectFn != nil {
		if err := c.ConnectFn(doc); err != nil {
			return nil, err
		}
	}
	conn, peer, err := SocketPair()
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.peers = append(c.peers, peer)
	c.mu.Unlock()
	c.Terminal.Serve(peer)
	return conn, nil
}

// AttachContainer implements kukeonv1.Client.
func (c *Client) AttachContainer(
	_ context.Context,
	doc v1beta1.ContainerDoc,
) (kukeonv1.AttachContainerResult, error) {
	if c.AttachContainerFn == nil {
		return kukeonv1.AttachContainerResult{}, errors.New("unexpected AttachContainer call")
	}
	return c.AttachContainerFn(doc)
}

// ExecContainer implements kukeonv1.Client.
func (c *Client) ExecContainer(
	_ context.Context,
	doc v1beta1.ContainerDoc,
	req kukeonv1.ExecRequest,
) (*net.UnixConn, error) {
	if c.ExecContainerFn == nil {
		return nil, errors.New("unexpected ExecContainer call")
	}
	return c.ExecContainerFn(doc, req)
}

// Close implements kukeonv1.Client and releases the served peer ends.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, p := range c.peers {
		_ = p.Close()
	}
	c.peers = nil
	return nil
}

// SocketPair returns both ends of a connected unix stream socketpair, the
// shape of connection ConnectContainerTerminal and ExecContainer hand back.
func SocketPair() (*net.UnixConn, *net.UnixConn, error) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("socketpair: %w", err)
	}
	a, err := fileConn(fds[0])
	if err != nil {
		_ = unix.Close(fds[1])
		return nil, nil, err
	}
	b, err := fileConn(fds[1])
	if err != nil {
		_ = a.Close()
		return nil, nil, err
	}
	return a, b, nil
}

func fileConn(fd int) (*net.UnixConn, error) {
	f := os.NewFile(uintptr(fd), "socketpair")
	defer func() { _ = f.Close() }()
	c, err := net.FileConn(f)
	if err != nil {
		return nil, fmt.Errorf("FileConn: %w", err)
	}
	uc, ok := c.(*net.UnixConn)
	if !ok {
		_ = c.Close()
		return nil, fmt.Errorf("FileConn: got %T, want *net.UnixConn", c)
	}
	return uc, nil
}
