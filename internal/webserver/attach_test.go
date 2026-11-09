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

package webserver

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/sbsh/pkg/api"
	"github.com/eminwux/sbsh/pkg/attach"
	terminalserver "github.com/eminwux/sbsh/pkg/terminal/server"
	"github.com/gorilla/websocket"
	"golang.org/x/term"
)

type attachClient struct {
	kukeonv1.FakeClient

	cell   kukeonv1.GetCellResult
	err    error
	target chan v1.ContainerDoc
	socket string
}

func (c *attachClient) GetCell(context.Context, v1.CellDoc) (kukeonv1.GetCellResult, error) {
	return c.cell, nil
}

func (c *attachClient) AttachContainer(_ context.Context, doc v1.ContainerDoc) (kukeonv1.AttachContainerResult, error) {
	if c.target != nil {
		c.target <- doc
	}
	socket := c.socket
	if socket == "" {
		socket = "/daemon/resolved"
	}
	return kukeonv1.AttachContainerResult{HostSocketPath: socket}, c.err
}
func (c *attachClient) Close() error { return nil }

func TestForwardAttachOutputCancelsSessionOnWriteFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	closeConnection := false
	forwardAttachOutput(strings.NewReader("terminal output"), func(int, []byte) error {
		return errors.New("websocket write failed")
	}, func() {
		cancel()
		closeConnection = true
	})

	if ctx.Err() == nil || !closeConnection {
		t.Fatalf("write failure cleanup: context error %v, connection closed %t", ctx.Err(), closeConnection)
	}
}

func TestAttachBridge(t *testing.T) {
	client := &attachClient{
		cell: kukeonv1.GetCellResult{
			MetadataExists:           true,
			RootContainerTaskRunning: true,
			Cell:                     v1.CellDoc{Status: v1.CellStatus{State: v1.CellStateReady}},
		},
		target: make(chan v1.ContainerDoc, 1),
	}
	detached := make(chan []byte, 1)
	resized := make(chan [2]int, 1)
	mux := http.NewServeMux()
	registerAttachRouteWith(mux, func(context.Context) (kukeonv1.Client, error) { return client, nil },
		func(_ context.Context, opts attach.Options) error {
			if opts.SocketPath != "/daemon/resolved" {
				return errors.New("wrong socket")
			}
			state, err := term.MakeRaw(int(opts.Stdin.Fd()))
			if err != nil {
				return err
			}
			defer term.Restore(int(opts.Stdin.Fd()), state)
			_, _ = opts.Stdout.Write([]byte("\x1b[31mhello\x1b[0m"))
			buf := make([]byte, 2)
			if _, err = io.ReadFull(opts.Stdin, buf); err != nil {
				return err
			}
			_, _ = opts.Stdout.Write(buf)
			if _, err = io.ReadFull(opts.Stdin, buf); err != nil {
				return err
			}
			detached <- buf
			return nil
		}, func(_ context.Context, socket string, cols, rows int) error {
			if socket != "/daemon/resolved" {
				return errors.New("wrong socket")
			}
			resized <- [2]int{cols, rows}
			return nil
		})
	server := httptest.NewServer(mux)
	defer server.Close()
	path := "/api/realms/r/spaces/s/stacks/t/cells/c/containers/work/attach"
	url := "ws" + strings.TrimPrefix(server.URL, "http") + path
	_, response, err := websocket.DefaultDialer.Dial(url, nil)
	if err == nil || response.StatusCode != http.StatusForbidden {
		t.Fatal("missing origin accepted")
	}
	ws, _, err := websocket.DefaultDialer.Dial(url, http.Header{"Origin": []string{server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	var output []byte
	var gotReady bool
	for !gotReady || output == nil {
		kind, data, readErr := ws.ReadMessage()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if kind == websocket.TextMessage {
			if string(data) != `{"type":"ready"}` {
				t.Fatalf("unexpected status %q", data)
			}
			gotReady = true
		} else {
			if !gotReady {
				t.Fatal("terminal output arrived before attach readiness")
			}
			output = data
		}
	}
	if string(output) != "\x1b[31mhello\x1b[0m" {
		t.Fatalf("ANSI output %q", output)
	}
	if err = ws.WriteMessage(websocket.BinaryMessage, []byte("hi")); err != nil {
		t.Fatal(err)
	}
	_, output, err = ws.ReadMessage()
	if err != nil || string(output) != "hi" {
		t.Fatalf("echo %q: %v", output, err)
	}
	if err = ws.WriteJSON(map[string]any{"type": "resize", "cols": 101, "rows": 32}); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-resized:
		if got != [2]int{101, 32} {
			t.Fatal(got)
		}
	case <-time.After(time.Second):
		t.Fatal("resize not forwarded")
	}
	_ = ws.Close()
	select {
	case got := <-detached:
		if string(got) != "\x1d\x1d" {
			t.Fatalf("detach %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("detach not forwarded")
	}
	doc := <-client.target
	if doc.Metadata.Name != "work" || doc.Spec.RealmID != "r" || doc.Spec.SpaceID != "s" || doc.Spec.StackID != "t" ||
		doc.Spec.CellID != "c" {
		t.Fatalf("target: %+v", doc)
	}
}

func TestAttachUnsupported(t *testing.T) {
	client := &attachClient{
		cell: kukeonv1.GetCellResult{
			MetadataExists:           true,
			RootContainerTaskRunning: true,
			Cell:                     v1.CellDoc{Status: v1.CellStatus{State: v1.CellStateReady}},
		},
		err: errdefs.ErrAttachNotSupported,
	}
	mux := http.NewServeMux()
	registerAttachRoute(mux, func(context.Context) (kukeonv1.Client, error) { return client, nil })
	server := httptest.NewServer(mux)
	defer server.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/realms/r/spaces/s/stacks/t/cells/c/containers/x/attach"
	ws, _, err := websocket.DefaultDialer.Dial(url, http.Header{"Origin": []string{server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	_, data, err := ws.ReadMessage()
	if err != nil || !strings.Contains(string(data), errdefs.ErrAttachNotSupported.Error()) {
		t.Fatalf("error message %s: %v", data, err)
	}
}

func TestAttachCleanDetach(t *testing.T) {
	ready := make(chan struct{})
	client := &attachClient{
		cell: kukeonv1.GetCellResult{
			MetadataExists:           true,
			RootContainerTaskRunning: true,
			Cell:                     v1.CellDoc{Status: v1.CellStatus{State: v1.CellStateReady}},
		},
	}
	mux := http.NewServeMux()
	registerAttachRouteWith(mux, func(context.Context) (kukeonv1.Client, error) { return client, nil },
		func(_ context.Context, opts attach.Options) error {
			state, err := term.MakeRaw(int(opts.Stdin.Fd()))
			if err != nil {
				return err
			}
			defer term.Restore(int(opts.Stdin.Fd()), state)
			close(ready)

			got := make([]byte, 2)
			if _, err = io.ReadFull(opts.Stdin, got); err != nil {
				return err
			}
			if string(got) != "\x1d\x1d" {
				return errors.New("detach sequence was not forwarded")
			}
			return attach.ErrDetached
		}, func(context.Context, string, int, int) error { return nil })
	server := httptest.NewServer(mux)
	defer server.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http") +
		"/api/realms/r/spaces/s/stacks/t/cells/c/containers/work/attach"
	ws, _, err := websocket.DefaultDialer.Dial(url, http.Header{"Origin": []string{server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("terminal did not enter raw mode")
	}
	if err = ws.WriteMessage(websocket.BinaryMessage, []byte("\x1d\x1d")); err != nil {
		t.Fatal(err)
	}
	kind, message, readErr := ws.ReadMessage()
	if readErr == nil && kind == websocket.TextMessage && string(message) == `{"type":"ready"}` {
		kind, message, readErr = ws.ReadMessage()
	}
	if readErr == nil {
		t.Fatalf("clean detach sent websocket message type %d: %s", kind, message)
	}
	var netErr net.Error
	if errors.As(readErr, &netErr) && netErr.Timeout() {
		t.Fatal("websocket stayed open after clean detach")
	}
}

func TestAttachRealTerminal(t *testing.T) {
	root, err := os.MkdirTemp("", "webattach-") //nolint:usetesting // Unix socket paths have a short length limit.
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	socketPath := filepath.Join(root, "terminal.sock")
	spec := &api.TerminalSpec{
		ID:            "web-test",
		Name:          "web-test",
		Labels:        map[string]string{},
		Command:       "/bin/sh",
		CommandArgs:   []string{},
		EnvInherit:    true,
		RunPath:       root,
		SocketFile:    socketPath,
		CaptureFile:   filepath.Join(root, "capture"),
		ShutdownGrace: 500 * time.Millisecond,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := terminalserver.New(spec, logger)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serverDone := make(chan error, 1)
	go func() { serverDone <- srv.Serve(ctx, listener) }()
	defer func() { _ = srv.Stop(errors.New("test cleanup")); <-serverDone }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		md, e := srv.Metadata()
		if e == nil && md != nil && md.Status.State == api.Ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("terminal server did not become ready")
		}
		time.Sleep(20 * time.Millisecond)
	}
	client := &attachClient{
		cell: kukeonv1.GetCellResult{
			MetadataExists:           true,
			RootContainerTaskRunning: true,
			Cell:                     v1.CellDoc{Status: v1.CellStatus{State: v1.CellStateReady}},
		},
		socket: socketPath,
	}
	mux := http.NewServeMux()
	registerAttachRoute(mux, func(context.Context) (kukeonv1.Client, error) { return client, nil })
	httpServer := httptest.NewServer(mux)
	defer httpServer.Close()
	url := "ws" + strings.TrimPrefix(
		httpServer.URL,
		"http",
	) + "/api/realms/r/spaces/s/stacks/t/cells/c/containers/work/attach"
	ws, _, err := websocket.DefaultDialer.Dial(url, http.Header{"Origin": []string{httpServer.URL}})
	if err != nil {
		t.Fatal(err)
	}
	_ = ws.SetReadDeadline(time.Now().Add(10 * time.Second))
	// Wait for the real sbsh attach handshake before sending terminal input.
	for {
		md, _ := srv.Metadata()
		if md != nil && len(md.Status.Attachers) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("sbsh did not register the attach")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err = ws.WriteJSON(map[string]any{"type": "resize", "cols": 101, "rows": 32}); err != nil {
		t.Fatal(err)
	}
	if err = ws.WriteMessage(websocket.BinaryMessage, []byte("stty size; printf 'WEB_ATTACH_OK\\n'\n")); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	for !strings.Contains(output.String(), "32 101") || !strings.Contains(output.String(), "WEB_ATTACH_OK") {
		kind, data, readErr := ws.ReadMessage()
		if readErr != nil {
			t.Fatalf("terminal output %q: %v", output.String(), readErr)
		}
		if kind == websocket.TextMessage {
			if string(data) == `{"type":"ready"}` {
				continue
			}
			t.Fatalf("terminal error: %s", data)
		}
		output.Write(data)
	}
	_ = ws.Close()
	deadline = time.Now().Add(5 * time.Second)
	for {
		md, e := srv.Metadata()
		if e == nil && md != nil && len(md.Status.Attachers) == 0 && md.Status.State == api.Ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("detach did not release the session while leaving the terminal running")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
